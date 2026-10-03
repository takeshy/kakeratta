package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/capture"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// maxCapture is the biggest image the app takes. A phone screenshot is well
// under it; anything far over is not a capture.
const maxCapture = 20 << 20

func (s *Server) hasMultipleMentionedPeople(ctx context.Context, text string) bool {
	// Resolve mention spellings before counting: a persona may be called by
	// either its id or its name, while an unknown @handle calls nobody here.
	return len(persona.Named(s.catalogue.All(ctx), text)) > 1
}

func (s *Server) getTimeline(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := s.store.TimelinePage(r.Context(), limit, r.URL.Query().Get("before"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrTimelineCursor) {
			status = http.StatusBadRequest
		}
		fail(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

type postBody struct {
	Language       string          `json:"language"`
	ClientID       string          `json:"clientId"`
	ClientAt       time.Time       `json:"clientAt"`
	SpeechAutoSend bool            `json:"speechAutoSend"`
	Text           string          `json:"text"`
	Link           string          `json:"link"`
	Image          string          `json:"image"` // data: URL or bare base64
	Place          *timeline.Place `json:"place"`
}

// postEntry is the way in from the page: a paste, a typed line, a URL, a
// transcript.
func (s *Server) postEntry(w http.ResponseWriter, r *http.Request) {
	var body postBody
	if err := readJSONLimit(r, &body, int64(base64.StdEncoding.EncodedLen(maxCapture))+(1<<20)); err != nil {
		fail(w, http.StatusBadRequest, "投稿を読めませんでした")
		return
	}
	id, hash, at, done := s.clientEntry(w, body.ClientID, body.ClientAt, body)
	if done {
		return
	}
	text, err := s.formatDictation(r.Context(), body.Text, body.SpeechAutoSend)
	if err != nil {
		fail(w, http.StatusBadGateway, "文章を整えられませんでした。入力は残っています。再送信してください。")
		return
	}
	body.Text = text
	if s.hasMultipleMentionedPeople(r.Context(), body.Text) {
		fail(w, http.StatusBadRequest, "@で指名できる担当は1人だけです")
		return
	}
	entry := timeline.Entry{
		ID: id, RequestHash: hash, Kind: timeline.KindPost, Speaker: timeline.User,
		Text: trimmed(body.Text), At: at, Link: trimmed(body.Link), Language: displayLanguage(body.Language),
	}
	entry.Root = entry.ID
	entry.Place = body.Place
	if body.Image != "" {
		data, mime, err := decodeImage(body.Image)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := s.store.PutBlob(data, mime)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		entry.Media = &timeline.Media{BlobID: id, MIME: mime}
	}
	readLink(r.Context(), &entry)
	if entry.Text == "" && entry.Media == nil && entry.Link == "" {
		fail(w, http.StatusBadRequest, "空の投稿です")
		return
	}
	entry.Place = s.resolvePlace(r.Context(), entry.Place)
	s.land(w, entry, entry.Place)
}

// dropCapture removes the image a request saved for an entry that was not
// stored after all.
func (s *Server) dropCapture(entry timeline.Entry) {
	if entry.Media != nil && entry.Media.BlobID != "" {
		_ = s.store.DeleteBlob(entry.Media.BlobID)
	}
}

// Every post hands each persona the same page text.
func readLink(ctx context.Context, entry *timeline.Entry) {
	if entry.Media != nil {
		return
	}
	if entry.Link == "" {
		entry.Link = linkIn(entry.Text)
	}
	if entry.Link == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	fetched, err := capture.Fetch(ctx, entry.Link)
	now := time.Now()
	entry.Media = &timeline.Media{ReadAt: &now}
	if err != nil {
		entry.Media.Error = err.Error()
		return
	}
	entry.Media.Describe = fetched.Title
	entry.Media.Text = fetched.Text
	entry.Media.Origin = fetched.URL
}

// land stores a post and hands it to the runner.
//
// The response goes back before anyone has reacted. That is the whole shape of
// the app: the capture is in, and whatever anyone has to say about it turns up
// later.
//
// Only the request that creates the entry reacts to it. Two tabs sending the
// same queued post, or a retry racing the first attempt, both pass the early
// clientEntry check; here the second one finds the first and answers with it
// instead of waking every persona again.
func (s *Server) land(w http.ResponseWriter, entry timeline.Entry, reactionPlace *timeline.Place) {
	reaction := store.EntryReaction(entry)
	reaction.Place = reactionPlace
	stored, created, err := s.store.CreateEntry(entry, reaction)
	if err != nil {
		s.dropCapture(entry)
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrEntryMissing) {
			status = http.StatusNotFound
		}
		fail(w, status, err.Error())
		return
	}
	if !created {
		s.dropCapture(entry)
		if stored.RequestHash != entry.RequestHash {
			fail(w, http.StatusConflict, "投稿IDが別の内容に使われています")
			return
		}
		writeJSON(w, http.StatusOK, stored)
		return
	}
	s.publishEvent("entry", entry)
	s.runner.RouteAccepted(reaction.ID)
	writeJSON(w, http.StatusOK, entry)
}

type replyBody struct {
	Language       string    `json:"language"`
	ClientID       string    `json:"clientId"`
	ClientAt       time.Time `json:"clientAt"`
	SpeechAutoSend bool      `json:"speechAutoSend"`
	ReplyTo        string    `json:"replyTo"`
	Text           string    `json:"text"`
}

func (s *Server) postReply(w http.ResponseWriter, r *http.Request) {
	var body replyBody
	if err := readJSON(r, &body); err != nil || trimmed(body.Text) == "" {
		fail(w, http.StatusBadRequest, "返事が空です")
		return
	}
	id, hash, at, done := s.clientEntry(w, body.ClientID, body.ClientAt, body)
	if done {
		return
	}
	parent, ok := s.store.Entry(body.ReplyTo)
	if !ok || parent.Deleted {
		fail(w, http.StatusNotFound, "返事の宛先が見つかりません")
		return
	}
	if root, exists := s.store.Entry(timeline.RootOf(parent)); !exists || root.Deleted {
		fail(w, http.StatusNotFound, "返事の宛先が見つかりません")
		return
	}
	text, err := s.formatDictation(r.Context(), body.Text, body.SpeechAutoSend)
	if err != nil {
		fail(w, http.StatusBadGateway, "文章を整えられませんでした。入力は残っています。再送信してください。")
		return
	}
	body.Text = text
	if s.hasMultipleMentionedPeople(r.Context(), body.Text) {
		fail(w, http.StatusBadRequest, "@で指名できる担当は1人だけです")
		return
	}
	reply := timeline.Entry{
		ID: id, RequestHash: hash, Kind: timeline.KindReply, Speaker: timeline.User,
		Text: trimmed(body.Text), At: at, Language: displayLanguage(body.Language),
		Root: timeline.RootOf(parent), ReplyTo: parent.ID,
	}
	readLink(r.Context(), &reply)
	s.land(w, reply, nil)
}

func displayLanguage(value string) string {
	if value == "ja" || value == "en" {
		return value
	}
	return ""
}

func (s *Server) editEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text *string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || body.Text == nil {
		fail(w, http.StatusBadRequest, "本文を入力してください")
		return
	}
	if s.hasMultipleMentionedPeople(r.Context(), *body.Text) {
		fail(w, http.StatusBadRequest, "@で指名できる担当は1人だけです")
		return
	}
	entry, ok := s.store.Entry(r.PathValue("id"))
	summary := entry.Speaker == timeline.App && entry.Summary != ""
	if !ok || entry.Deleted || (entry.Speaker != timeline.User && !summary) || entry.Kind != timeline.KindPost {
		fail(w, http.StatusNotFound, store.ErrNotEditable.Error())
		return
	}
	var initial []store.Reaction
	if !summary {
		updated := entry
		updated.Text = trimmed(*body.Text)
		reaction := store.EntryReaction(updated)
		reaction.Edited = true
		initial = append(initial, reaction)
	}
	entry, err := s.store.UpdateText(entry.ID, *body.Text, initial...)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotEditable) {
			status = http.StatusNotFound
		} else if errors.Is(err, store.ErrEmptyText) {
			status = http.StatusBadRequest
		}
		fail(w, status, err.Error())
		return
	}
	// A summary is not routed: setting it right is all an edit does.
	if summary {
		s.publishEvent("entry", entry)
		writeJSON(w, http.StatusOK, entry)
		return
	}
	// UpdateText cleared the old decision before releasing the routing request.
	s.publishEvent("entry", entry)
	s.runner.RouteAccepted(initial[0].ID)
	writeJSON(w, http.StatusOK, entry)
}

// deleteEntry removes an entry. A post takes the conversation under it along
// - the remarks and the user's own lines - since a reply without its post is
// nothing, and a note that cited such a reply would otherwise keep pointing at
// a thread that no longer opens. A remark or a reply takes what answered it,
// directly or further down: a wrong answer deleted alone left the replies
// that repeated it, and those were searched and read back in its place.
func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.store.Entry(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, "見つかりません")
		return
	}
	root := timeline.RootOf(entry)
	gone, err := s.store.DeleteEntry(r.Context(), entry.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, deleted := range gone {
		_ = s.store.UpdateRecord(r.Context(), store.RecordFavorites, deleted.ID, time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil })
		deleted.Deleted = true // Browser removal notification, never persisted.
		s.publishEvent("entry", deleted)
		_ = s.store.ForgetRouting(deleted.ID)
	}
	// Whoever wrote any of this down is told, and tidies its own notes. The
	// app removes the lines, never the memory someone built on them.
	s.runner.EntriesForgotten(root, gone)
	entry.Deleted = true
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) getBlob(w http.ResponseWriter, r *http.Request) {
	data, mime, err := s.store.Blob(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !imageTypes[mime] {
		// Blobs saved before types were checked are served as bytes, never
		// as a page on the app's own origin.
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

func (s *Server) postFetch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "URLを読めませんでした")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	fetched, err := capture.Fetch(ctx, body.URL)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fetched)
}

// imageTypes are the captures the app stores. The blob is served from the
// page's own origin, so anything else - HTML, SVG with script - is refused
// rather than kept under a name the browser would run.
var imageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/heic": true, "image/heif": true, "image/avif": true,
}

// decodeImage takes either a data: URL from a paste or bare base64.
func decodeImage(value string) ([]byte, string, error) {
	mime := "image/png"
	payload := value
	if strings.HasPrefix(value, "data:") {
		comma := strings.IndexByte(value, ',')
		if comma < 0 {
			return nil, "", errors.New("画像を読めませんでした")
		}
		header := value[5:comma]
		if semicolon := strings.IndexByte(header, ';'); semicolon > 0 {
			header = header[:semicolon]
		}
		if header != "" {
			mime = strings.ToLower(strings.TrimSpace(header))
		}
		if mime == "image/jpg" {
			mime = "image/jpeg"
		}
		if !imageTypes[mime] {
			return nil, "", errors.New("この形式の画像は保存できません")
		}
		payload = value[comma+1:]
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return nil, "", errors.New("画像を読めませんでした")
	}
	if len(data) > maxCapture {
		return nil, "", errors.New("画像が大きすぎます")
	}
	return data, mime, nil
}

var clientIDPattern = regexp.MustCompile(`^local-[a-f0-9-]{36}$`)

func (s *Server) clientEntry(w http.ResponseWriter, id string, at time.Time, body any) (string, string, time.Time, bool) {
	if id == "" {
		return store.NewID("e"), "", time.Now(), false
	}
	if !clientIDPattern.MatchString(id) || at.IsZero() || at.After(time.Now().Add(5*time.Minute)) || at.Year() < 2000 {
		fail(w, http.StatusBadRequest, "投稿IDまたは日時が不正です")
		return "", "", at, true
	}
	raw, _ := json.Marshal(body)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if old, ok := s.store.Entry(id); ok {
		if old.RequestHash != hash {
			fail(w, http.StatusConflict, "投稿IDが別の内容に使われています")
		} else {
			writeJSON(w, http.StatusOK, old)
		}
		return id, hash, at, true
	}
	return id, hash, at, false
}
