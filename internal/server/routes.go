package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/capture"
	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
	"github.com/takeshy/kakeratta/internal/websearch"
)

// maxCapture is the biggest image the app takes. A phone screenshot is well
// under it; anything far over is not a capture.
const maxCapture = 20 << 20

func (s *Server) hasMultipleMentionedPeople(text string) bool {
	// Resolve mention spellings before counting: a persona may be called by
	// either its id or its name, while an unknown @handle calls nobody here.
	return len(persona.Named(s.catalogue.All(), text)) > 1
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/mcp/connection", s.guard(s.mcpConnection))
	s.mux.HandleFunc("POST /api/mcp/connection", s.guard(s.issueMCPKey))
	s.mux.HandleFunc("DELETE /api/mcp/connection", s.guard(s.revokeMCPKey))
	s.mux.HandleFunc("/api/mcp/users/{workspace}", s.serveMCP)
	s.mux.HandleFunc("GET /api/auth/identity", s.guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(s.store.Root()+"\x00")))
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "enabled": false})
	}))
	s.mux.HandleFunc("POST /api/social/preview", s.guard(s.previewSocial))
	s.mux.HandleFunc("POST /api/social/publish", s.guard(s.publishSocial))
	s.mux.HandleFunc("GET /api/social/bluesky/client-metadata.json", blueskyMetadata)
	s.mux.HandleFunc("GET /api/social/bluesky/connection", s.guard(s.blueskyConnection))
	s.mux.HandleFunc("POST /api/social/bluesky/connect", s.guard(s.connectBluesky))
	s.mux.HandleFunc("GET /api/social/bluesky/callback", s.guard(s.blueskyCallback))
	s.mux.HandleFunc("POST /api/social/bluesky/finish", s.guard(s.finishBluesky))
	s.mux.HandleFunc("DELETE /api/social/bluesky/connection", s.guard(s.disconnectBluesky))
	s.mux.HandleFunc("GET /api/auth/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
	})
	s.mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	s.mux.HandleFunc("GET /api/timeline", s.guard(s.getTimeline))
	s.mux.HandleFunc("POST /api/timeline/search", s.guard(s.searchTimeline))
	s.mux.HandleFunc("GET /api/thread/{id}", s.guard(s.getThread))
	s.mux.HandleFunc("POST /api/post", s.guard(s.postEntry))
	s.mux.HandleFunc("POST /api/reply", s.guard(s.postReply))
	s.mux.HandleFunc("DELETE /api/entry/{id}", s.guard(s.deleteEntry))
	s.mux.HandleFunc("PATCH /api/entry/{id}", s.guard(s.editEntry))
	s.mux.HandleFunc("GET /api/entry/{id}/routing", s.guard(s.getRouting))
	s.mux.HandleFunc("POST /api/entry/{id}/routing/check", s.guard(s.checkRouting))
	s.mux.HandleFunc("POST /api/entry/{id}/routing/apply", s.guard(s.applyRouting))
	s.mux.HandleFunc("POST /api/entry/{id}/reply/preview", s.guard(s.previewReply))
	s.mux.HandleFunc("POST /api/entry/{id}/reply/apply", s.guard(s.applyReply))
	s.mux.HandleFunc("GET /api/blob/{id}", s.guard(s.getBlob))
	s.mux.HandleFunc("GET /api/events", s.guard(s.events2))
	s.mux.HandleFunc("POST /api/fetch", s.guard(s.postFetch))

	s.mux.HandleFunc("GET /api/personas", s.guard(s.getPersonas))
	s.mux.HandleFunc("PUT /api/personas/{id}/daily", s.guard(s.putDailyCheckIn))
	s.mux.HandleFunc("DELETE /api/personas/{id}/schedules/{schedule}", s.guard(s.deleteSchedule))
	s.mux.HandleFunc("PUT /api/personas", s.guard(s.putPersonas))
	s.mux.HandleFunc("POST /api/personas/build", s.guard(s.buildPersona))
	s.mux.HandleFunc("POST /api/personas/draft", s.guard(s.savePersonaDraft))
	s.mux.HandleFunc("PUT /api/personas/{id}", s.guard(s.savePersonaDraft))
	s.mux.HandleFunc("POST /api/personas/install", s.guard(s.installPersona))
	s.mux.HandleFunc("DELETE /api/personas/{id}", s.guard(s.uninstallPersona))
	s.mux.HandleFunc("GET /api/personas/{id}/memory", s.guard(s.getMemory))
	s.mux.HandleFunc("DELETE /api/personas/{id}/memory", s.guard(s.forgetMemory))
	s.mux.HandleFunc("GET /api/personas/{id}/memory/note", s.guard(s.getNote))
	s.mux.HandleFunc("DELETE /api/personas/{id}/memory/note", s.guard(s.forgetNote))
	s.mux.HandleFunc("PUT /api/personas/{id}/memory/note", s.guard(s.editNote))

	s.mux.HandleFunc("GET /api/settings", s.guard(s.getSettings))
	s.mux.HandleFunc("PUT /api/settings", s.guard(s.putSettings))
	s.mux.HandleFunc("GET /api/export", s.guard(s.exportWorkspace))
	s.mux.HandleFunc("POST /api/export", s.guard(s.startExport))
	s.mux.HandleFunc("DELETE /api/workspace", s.guard(s.destroyWorkspace))
	s.mux.HandleFunc("POST /api/search/check", s.guard(s.checkSearch))
	s.mux.HandleFunc("GET /api/models", s.guard(s.getModels))
	s.mux.HandleFunc("GET /api/openrouter/models", s.guard(s.getOpenRouterModels))
	s.mux.HandleFunc("GET /api/about", s.guard(s.getAbout))

	// The dictation stack is reached through the relay so credentials stay out
	// of the browser.

	s.mux.HandleFunc("/", notFound)
}

// notFound is every path that is not the API. The page lives on its own origin
// or dev server; a request for it landing here is a misconfiguration, and a
// JSON error says so faster than a blank file listing would.
func notFound(w http.ResponseWriter, r *http.Request) {
	fail(w, http.StatusNotFound, "not found: "+r.URL.Path)
}

// ---- timeline --------------------------------------------------------------

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
	if s.hasMultipleMentionedPeople(body.Text) {
		fail(w, http.StatusBadRequest, "@で指名できる担当は1人だけです")
		return
	}
	entry := timeline.Entry{
		ID: id, RequestHash: hash, Kind: timeline.KindPost, Speaker: timeline.User,
		Text: trimmed(body.Text), At: at, Link: trimmed(body.Link),
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
	s.land(w, entry, body.Place)
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
func (s *Server) land(w http.ResponseWriter, entry timeline.Entry, reactionPlace *timeline.Place) {
	if err := s.store.Append(entry); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.publishEvent("entry", entry)
	s.runner.React(entry.ID, reactionPlace)
	writeJSON(w, http.StatusOK, entry)
}

type replyBody struct {
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
	if s.hasMultipleMentionedPeople(body.Text) {
		fail(w, http.StatusBadRequest, "@で指名できる担当は1人だけです")
		return
	}
	reply := timeline.Entry{
		ID: id, RequestHash: hash, Kind: timeline.KindReply, Speaker: timeline.User,
		Text: trimmed(body.Text), At: at,
		Root: timeline.RootOf(parent), ReplyTo: parent.ID,
	}
	readLink(r.Context(), &reply)
	s.land(w, reply, nil)
}

func (s *Server) editEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text *string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || body.Text == nil {
		fail(w, http.StatusBadRequest, "本文を入力してください")
		return
	}
	if s.hasMultipleMentionedPeople(*body.Text) {
		fail(w, http.StatusBadRequest, "@で指名できる担当は1人だけです")
		return
	}
	entry, ok := s.store.Entry(r.PathValue("id"))
	if !ok || entry.Deleted || entry.Speaker != timeline.User || entry.Kind != timeline.KindPost {
		fail(w, http.StatusNotFound, store.ErrNotEditable.Error())
		return
	}
	entry, err := s.store.UpdateText(entry.ID, *body.Text)
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
	// New records also carry the routed text, so a late result or failed
	// deletion cannot make the old decision apply to the edited post.
	_ = s.store.ForgetRouting(entry.ID)
	s.publishEvent("entry", entry)
	s.runner.EntryEdited(entry.ID)
	writeJSON(w, http.StatusOK, entry)
}

// deleteEntry removes an entry. A post takes the conversation under it along
// - the remarks and the user's own lines - since a reply without its post is
// nothing, and a note that cited such a reply would otherwise keep pointing at
// a thread that no longer opens.
func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.store.Entry(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, "見つかりません")
		return
	}
	root := timeline.RootOf(entry)
	gone := []timeline.Entry{entry}
	if entry.Kind == timeline.KindPost {
		if entries, err := s.store.Timeline(); err == nil {
			var thread []timeline.Entry
			for _, other := range timeline.Thread(entries, root) {
				if !other.Deleted {
					thread = append(thread, other)
				}
			}
			if len(thread) > 0 {
				gone = thread
			}
		}
	}
	for i := range gone {
		gone[i].Deleted = true
		if err := s.store.Append(gone[i]); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.publishEvent("entry", gone[i])
		// What the router decided about a line goes with it. Failing to drop
		// it is not a reason to keep the line.
		_ = s.store.ForgetRouting(gone[i].ID)
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
	w.Header().Set("Content-Type", mime)
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

// ---- personas --------------------------------------------------------------

func (s *Server) getPersonas(w http.ResponseWriter, r *http.Request) {
	settings := s.store.Settings()
	skills := s.catalogue.All()
	type row struct {
		persona.Skill
		DailyCheckIn store.DailyCheckIn `json:"dailyCheckIn"`
		// Schedules are the wakes this persona asked the app for, so the card can
		// show what it is waiting for.
		Schedules     []store.Schedule `json:"schedules,omitempty"`
		Active        bool             `json:"active"`
		Wakes         int              `json:"wakes"`
		Notes         string           `json:"notes,omitempty"`
		PendingTasks  int              `json:"pendingTasks,omitempty"`
		MCPConfigured bool             `json:"mcpConfigured,omitempty"`
	}
	active := map[string]bool{}
	for _, id := range settings.Active {
		active[id] = true
	}
	out := make([]row, 0, len(skills))
	tasks, _ := store.AgentTasks(r.Context(), s.store)
	mcp, _ := s.store.SocialValue(r.Context(), mcpCredential)
	pendingTasks := 0
	for _, task := range tasks {
		if task.State != "completed" {
			pendingTasks++
		}
	}
	for _, skill := range skills {
		if skill.ID == "hermes" && len(mcp.Data) == 0 {
			continue
		}
		memory := s.store.MemoryMeta(skill.ID)
		schedules, err := s.store.Schedules(skill.ID)
		if err != nil {
			// A listing failure must not hide the room; the card shows none.
			schedules = nil
		}
		pending := 0
		if skill.ID == "hermes" {
			pending = pendingTasks
		}
		out = append(out, row{Skill: skill, DailyCheckIn: settings.DailyCheckIns[skill.ID], Schedules: schedules, Active: active[skill.ID], Wakes: memory.Wakes, PendingTasks: pending, MCPConfigured: skill.ID == "hermes" && len(mcp.Data) > 0})
	}
	writeJSON(w, http.StatusOK, map[string]any{"personas": out, "tools": tools.New(s.tools).Names()})
}

func (s *Server) putPersonas(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active []string `json:"active"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	settings := s.store.ConfiguredSettings()
	previouslyActive := make(map[string]bool, len(settings.Active))
	for _, id := range settings.Active {
		previouslyActive[id] = true
	}
	var kept []string
	stillActive := make(map[string]bool, len(body.Active))
	for _, id := range body.Active {
		if _, ok := s.catalogue.Find(id); ok {
			kept = append(kept, id)
			stillActive[id] = true
		}
	}
	settings.Active = kept
	if err := s.store.SaveSettings(settings); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Wakes belong to an active persona. Remove them at the moment it is
	// paused, including future schedules that the minute loop has not reached.
	for id := range previouslyActive {
		if !stillActive[id] {
			if err := s.store.DeleteSchedules(id); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	s.getPersonas(w, r)
}

func (s *Server) installPersona(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	skill, err := s.catalogue.Install(raw, s.knownTool)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	encoded, err := json.Marshal(skill)
	if err != nil || s.store.PersistSkill(skill.ID, encoded) != nil {
		_ = s.catalogue.Uninstall(skill.ID)
		fail(w, http.StatusInternalServerError, "担当を保存できませんでした")
		return
	}
	writeJSON(w, http.StatusOK, added{Skill: skill, Active: s.activatePersona(skill.ID) == nil})
}

// added is a new persona as the one who made it sees it: whether it is already
// in the room decides what the confirmation says.
type added struct {
	persona.Skill
	Active bool `json:"active"`
}

// activatePersona puts a newly added persona in the room. Adding one means
// wanting to use it, and a separate switch nobody expects left new personas
// silent; pausing one stays a switch on its card.
func (s *Server) activatePersona(id string) error {
	settings := s.store.ConfiguredSettings()
	if slices.Contains(settings.Active, id) {
		return nil
	}
	settings.Active = append(settings.Active, id)
	return s.store.SaveSettings(settings)
}

func (s *Server) uninstallPersona(w http.ResponseWriter, r *http.Request) {
	if err := s.catalogue.Uninstall(r.PathValue("id")); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.DeleteSkill(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A removed persona has no panel left to show or forget its notes from, so
	// its memory goes with it rather than lingering where nobody can reach it.
	if err := s.store.ForgetMemory(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.DeleteSchedules(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// deleteSchedule takes one wake a persona asked for off its card. The persona
// sets them, but the person it would wake is the one who says when it stops;
// the persona is not woken about it.
func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteSchedule(r.PathValue("id"), r.PathValue("schedule"))
	if errors.Is(err, store.ErrScheduleNotFound) {
		fail(w, http.StatusNotFound, "予定が見つかりません")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.getPersonas(w, r)
}

// getMemory shows what a persona has kept. It is the persona's own text, and
// seeing it is the only way to tell what it decided was worth keeping.
//
// The notes only ever grow, so they come a page at a time, the most recently
// written first: ?after= is the next cursor of the page already shown, ?tag=
// keeps only the notes that carry that tag, and ?type= only the notes of that
// kind.
func (s *Server) getMemory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 || limit > maxMemoryPage {
		limit = defaultMemoryPage
	}
	var match func(store.Note) bool
	tag, kind := strings.TrimSpace(query.Get("tag")), strings.TrimSpace(query.Get("type"))
	if tag != "" || kind != "" {
		match = func(note store.Note) bool {
			return (tag == "" || slices.ContainsFunc(persona.NoteTags(note.Text), func(have string) bool { return strings.EqualFold(have, tag) })) &&
				(kind == "" || strings.EqualFold(persona.NoteType(note.Text), kind))
		}
	}
	page, err := s.store.MemoryPage(r.PathValue("id"), query.Get("after"), limit, match)
	if errors.Is(err, store.ErrMemoryCursor) {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

const (
	defaultMemoryPage = 50
	maxMemoryPage     = 200
)

// getNote is one note by name: a link in a note can point at one on a page
// not read yet.
func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {
	note, ok := s.store.Note(r.PathValue("id"), r.URL.Query().Get("path"))
	if !ok {
		fail(w, http.StatusNotFound, "メモが見つかりません")
		return
	}
	writeJSON(w, http.StatusOK, note)
}

// A separate route ensures a missing note path can never erase all memory.
func (s *Server) forgetNote(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		fail(w, http.StatusBadRequest, "メモを指定してください")
		return
	}
	note, ok := s.store.Note(r.PathValue("id"), path)
	if !ok || note.Path != path {
		fail(w, http.StatusNotFound, "メモが見つかりません")
		return
	}
	if err := s.store.DeleteNote(r.PathValue("id"), path); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The app does not edit notes or the schedules that went with them: the
	// persona is woken and puts its own memory back in order.
	s.runner.NoteForgotten(r.PathValue("id"), path)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// editNote replaces the body of one note with what the user typed. A persona's
// notes are its own, but a misread label is a misread label: when the text a
// persona was given was wrong, the person who knows can put the note right
// without waiting for the persona to be told. The links the app keeps from the
// note to its posts are kept as they were; the persona is not woken.
func (s *Server) editNote(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		fail(w, http.StatusBadRequest, "メモを指定してください")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Text) == "" {
		fail(w, http.StatusBadRequest, "メモの本文を入力してください")
		return
	}
	id := r.PathValue("id")
	note, ok := s.store.Note(id, path)
	if !ok || note.Path != path {
		fail(w, http.StatusNotFound, "メモが見つかりません")
		return
	}
	if err := s.store.WriteNote(id, path, body.Text); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, _ := s.store.Note(id, path)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) forgetMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ForgetMemory(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Nothing is left to reconcile, so the persona is not woken. The schedules
	// are the app's own bookkeeping and go with the memory they served.
	if err := s.store.DeleteSchedules(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- settings --------------------------------------------------------------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings := s.store.ConfiguredSettings()
	// Keys go out to the page only as "set or not". The page never needs them,
	// and a page that does not have them cannot leak them.
	settings.APIKey = masked(settings.APIKey)
	settings.OpenRouterAPIKey = masked(settings.OpenRouterAPIKey)
	settings.Search.APIKey = masked(settings.Search.APIKey)
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	current := s.store.ConfiguredSettings()
	next := current
	if err := readJSON(r, &next); err != nil {
		fail(w, http.StatusBadRequest, "設定を読めませんでした")
		return
	}
	// Persona schedules have their own endpoint; a stale settings form must not undo them.
	next.DailyCheckIns = current.DailyCheckIns
	// A masked key coming back means "unchanged", not "set it to asterisks".
	next.APIKey = unmask(next.APIKey, current.APIKey)
	next.OpenRouterAPIKey = unmask(next.OpenRouterAPIKey, current.OpenRouterAPIKey)
	next.Search.APIKey = unmask(next.Search.APIKey, current.Search.APIKey)
	if err := s.store.SaveSettings(next); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.scheduleIndex()
	s.getSettings(w, r)
}

func (s *Server) getModels(w http.ResponseWriter, r *http.Request) {
	client, _ := s.client()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	models, err := client.Models(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) getOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	settings := s.store.Settings()
	if strings.TrimSpace(settings.OpenRouterAPIKey) == "" {
		fail(w, http.StatusBadRequest, "OpenRouter APIキーを入力してください")
		return
	}
	client := llm.NewOpenRouter(store.OpenRouterBaseURL, settings.OpenRouterAPIKey)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	check, err := http.NewRequestWithContext(ctx, http.MethodGet, store.OpenRouterBaseURL+"/key", nil)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	check.Header.Set("Authorization", "Bearer "+settings.OpenRouterAPIKey)
	response, err := client.HTTP.Do(check)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		fail(w, http.StatusBadGateway, "OpenRouter APIキーを確認できませんでした")
		return
	}
	models, err := client.Models(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) checkSearch(w http.ResponseWriter, r *http.Request) {
	client := websearch.New(s.store.Settings().Search.APIKey)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := client.Search(ctx, "kakeratta", 1); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) getAbout(w http.ResponseWriter, r *http.Request) {
	about := map[string]any{
		"version": s.version,
		"providers": []map[string]string{
			{"id": string(store.ProviderNebius), "label": "Nebius Token Factory", "endpoint": store.NebiusBaseURL},
		},
		"nemotron": map[string]string{"low": store.NemotronNano, "middle": store.NemotronSuper, "high": store.NemotronUltra},
		// What the operator wired up, so the page can say what personas can reach.
		"tools":      tools.New(s.tools).Names(),
		"mcpServers": s.tools.MCP.Servers(),
	}
	// Where the files are is worth saying only on the machine that holds them.
	// A hosted workspace lives in Firestore and object storage, and the
	// directory left behind is a scratch one nobody can open.
	if s.workspaceKey == "" {
		about["workspace"] = s.store.Root()
	}
	writeJSON(w, http.StatusOK, about)
}

func masked(key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	return "********"
}

func unmask(next, current string) string {
	if next == "********" {
		return current
	}
	return next
}

// ---- dictation -------------------------------------------------------------

// ---- helpers ---------------------------------------------------------------

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
			mime = header
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

func (s *Server) putDailyCheckIn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.catalogue.Find(id); !ok {
		fail(w, http.StatusNotFound, "担当が見つかりません")
		return
	}
	var schedule store.DailyCheckIn
	if err := readJSON(r, &schedule); err != nil {
		fail(w, http.StatusBadRequest, "設定を読めませんでした")
		return
	}
	if err := schedule.Validate(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	settings := s.store.ConfiguredSettings()
	if settings.DailyCheckIns == nil {
		settings.DailyCheckIns = map[string]store.DailyCheckIn{}
	}
	settings.DailyCheckIns[id] = schedule
	if err := s.store.SaveSettings(settings); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.getPersonas(w, r)
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
