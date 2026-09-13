package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/capture"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/speech"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
	"github.com/takeshy/kakeratta/internal/websearch"
)

var errHTTPOnly = errors.New("この形態では使えません")

// maxCapture is the biggest image the app takes. A phone screenshot is well
// under it; anything far over is not a capture.
const maxCapture = 20 << 20

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/auth/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
	})
	s.mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	// The share target posts here, and so does the iOS shortcut. It is the one
	// endpoint that has to work from outside the page, so it takes multipart
	// and accepts its token in the form as well as the header.
	s.mux.HandleFunc("POST /api/capture", s.guard(s.postCapture))

	s.mux.HandleFunc("GET /api/timeline", s.guard(s.getTimeline))
	s.mux.HandleFunc("POST /api/post", s.guard(s.postEntry))
	s.mux.HandleFunc("POST /api/reply", s.guard(s.postReply))
	s.mux.HandleFunc("DELETE /api/entry/{id}", s.guard(s.deleteEntry))
	s.mux.HandleFunc("GET /api/blob/{id}", s.guard(s.getBlob))
	s.mux.HandleFunc("GET /api/events", s.guard(s.events2))
	s.mux.HandleFunc("POST /api/fetch", s.guard(s.postFetch))

	s.mux.HandleFunc("GET /api/personas", s.guard(s.getPersonas))
	s.mux.HandleFunc("PUT /api/personas", s.guard(s.putPersonas))
	s.mux.HandleFunc("POST /api/personas/install", s.guard(s.installPersona))
	s.mux.HandleFunc("DELETE /api/personas/{id}", s.guard(s.uninstallPersona))
	s.mux.HandleFunc("GET /api/personas/{id}/memory", s.guard(s.getMemory))
	s.mux.HandleFunc("DELETE /api/personas/{id}/memory", s.guard(s.forgetMemory))

	s.mux.HandleFunc("GET /api/settings", s.guard(s.getSettings))
	s.mux.HandleFunc("PUT /api/settings", s.guard(s.putSettings))
	s.mux.HandleFunc("GET /api/models", s.guard(s.getModels))
	s.mux.HandleFunc("GET /api/about", s.guard(s.getAbout))

	// The dictation stack, carried over whole and reached over HTTP instead of
	// through a desktop bridge. The keys stay here, which is also why the
	// browser can talk to a local whisper.cpp without tripping over CORS.
	s.mux.HandleFunc("POST /api/speech/request", s.guard(s.speechRequest))
	s.mux.HandleFunc("POST /api/speech/vertex", s.guard(s.speechVertex))
	s.mux.HandleFunc("POST /api/speech/live/start", s.guard(s.liveStart))
	s.mux.HandleFunc("POST /api/speech/live/audio", s.guard(s.liveAudio))
	s.mux.HandleFunc("POST /api/speech/live/finish", s.guard(s.liveFinish))
	s.mux.HandleFunc("POST /api/speech/live/stop", s.guard(s.liveStop))

	s.mux.HandleFunc("/", s.page)
}

// page serves the app. Every path that is not a file is the app itself, so a
// share target landing on /?shared=1 or a deep link still opens it.
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("token") {
		s.guard(func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query()
			query.Del("token")
			target := *r.URL
			target.RawQuery = query.Encode()
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			http.Redirect(w, r, target.RequestURI(), http.StatusSeeOther)
		})(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || !strings.Contains(path, ".") {
		r = r.Clone(r.Context())
		r.URL.Path = "/"
	}
	switch {
	case strings.HasSuffix(path, ".webmanifest"):
		// Go does not know this one, and a browser will not read a share target
		// out of a manifest served as plain text.
		w.Header().Set("Content-Type", "application/manifest+json")
	case path == "sw.js":
		// A worker that gets cached is a worker that cannot be replaced.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
	}
	s.assets.ServeHTTP(w, r)
}

// ---- timeline --------------------------------------------------------------

func (s *Server) getTimeline(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.Timeline()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type postBody struct {
	Text  string          `json:"text"`
	Link  string          `json:"link"`
	Image string          `json:"image"` // data: URL or bare base64
	Place *timeline.Place `json:"place"`
}

// postEntry is the way in from the page: a paste, a typed line, a URL, a
// transcript.
func (s *Server) postEntry(w http.ResponseWriter, r *http.Request) {
	var body postBody
	if err := readJSONLimit(r, &body, int64(base64.StdEncoding.EncodedLen(maxCapture))+(1<<20)); err != nil {
		fail(w, http.StatusBadRequest, "投稿を読めませんでした")
		return
	}
	entry := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User,
		Text: trimmed(body.Text), At: time.Now(), Link: trimmed(body.Link),
	}
	entry.Root = entry.ID
	if s.store.Settings().KeepPlace {
		entry.Place = body.Place
	}
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

// postCapture is the share sheet's way in: multipart, from outside the page.
func (s *Server) postCapture(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxCapture); err != nil {
		fail(w, http.StatusBadRequest, "受け取れませんでした")
		return
	}
	entry := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User,
		At: time.Now(),
	}
	entry.Root = entry.ID
	// Android's share target sends title/text/url under those names; the iOS
	// shortcut sends whatever it was told to. Both are accepted.
	entry.Text = trimmed(first(r, "text", "note", "title"))
	entry.Link = trimmed(first(r, "url", "link"))
	var reactionPlace *timeline.Place
	if lat, lon := r.FormValue("lat"), r.FormValue("lon"); lat != "" && lon != "" {
		latitude, latErr := strconv.ParseFloat(lat, 64)
		longitude, lonErr := strconv.ParseFloat(lon, 64)
		if latErr == nil && lonErr == nil {
			reactionPlace = &timeline.Place{Lat: latitude, Lon: longitude, Label: r.FormValue("place")}
			if s.store.Settings().KeepPlace {
				entry.Place = reactionPlace
			}
		}
	}

	file, header, err := firstFile(r, "image", "file", "files", "photo")
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxCapture+1))
		if err != nil {
			fail(w, http.StatusBadRequest, "画像を読めませんでした")
			return
		}
		if len(data) > maxCapture {
			fail(w, http.StatusRequestEntityTooLarge, "画像が大きすぎます")
			return
		}
		mime := header.Header.Get("Content-Type")
		if mime == "" || mime == "application/octet-stream" {
			mime = http.DetectContentType(data)
		}
		id, err := s.store.PutBlob(data, mime)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		entry.Media = &timeline.Media{BlobID: id, MIME: mime}
	}
	if entry.Media == nil && entry.Text == "" && entry.Link == "" {
		fail(w, http.StatusBadRequest, "画像も文章もありません")
		return
	}
	readLink(r.Context(), &entry)
	s.land(w, entry, reactionPlace)
}

// Both the composer and share sheet hand every persona the same page text.
func readLink(ctx context.Context, entry *timeline.Entry) {
	if entry.Link == "" || entry.Media != nil {
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
	s.events.send("entry", entry)
	s.runner.React(entry.ID, reactionPlace)
	writeJSON(w, http.StatusOK, entry)
}

type replyBody struct {
	ReplyTo string `json:"replyTo"`
	Text    string `json:"text"`
}

func (s *Server) postReply(w http.ResponseWriter, r *http.Request) {
	var body replyBody
	if err := readJSON(r, &body); err != nil || trimmed(body.Text) == "" {
		fail(w, http.StatusBadRequest, "返事が空です")
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
	reply := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindReply, Speaker: timeline.User,
		Text: trimmed(body.Text), At: time.Now(),
		Root: timeline.RootOf(parent), ReplyTo: parent.ID,
	}
	if err := s.store.Append(reply); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.send("entry", reply)
	s.runner.React(reply.ID, nil)
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.store.Entry(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, "見つかりません")
		return
	}
	entry.Deleted = true
	if err := s.store.Append(entry); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.send("entry", entry)
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
		Active bool   `json:"active"`
		Wakes  int    `json:"wakes"`
		Notes  string `json:"notes,omitempty"`
	}
	active := map[string]bool{}
	for _, id := range settings.Active {
		active[id] = true
	}
	out := make([]row, 0, len(skills))
	for _, skill := range skills {
		memory := s.store.Memory(skill.ID)
		out = append(out, row{Skill: skill, Active: active[skill.ID], Wakes: memory.Wakes})
	}
	writeJSON(w, http.StatusOK, map[string]any{"personas": out, "tools": tools.New(settings).Names()})
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
	var kept []string
	for _, id := range body.Active {
		if _, ok := s.catalogue.Find(id); ok {
			kept = append(kept, id)
		}
	}
	settings.Active = kept
	if err := s.store.SaveSettings(settings); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
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
	writeJSON(w, http.StatusOK, skill)
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
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// getMemory shows what a persona has kept. It is the persona's own text, and
// seeing it is the only way to tell what it decided was worth keeping.
func (s *Server) getMemory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Memory(r.PathValue("id")))
}

func (s *Server) forgetMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ForgetMemory(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- settings --------------------------------------------------------------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings := s.store.ConfiguredSettings()
	// Keys go out to the page only as "set or not". The page never needs them,
	// and a page that does not have them cannot leak them.
	settings.APIKey = masked(settings.APIKey)
	settings.Search.APIKey = masked(settings.Search.APIKey)
	settings.Speech.APIKey = masked(settings.Speech.APIKey)
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	current := s.store.ConfiguredSettings()
	next := current
	if err := readJSON(r, &next); err != nil {
		fail(w, http.StatusBadRequest, "設定を読めませんでした")
		return
	}
	// A masked key coming back means "unchanged", not "set it to asterisks".
	next.APIKey = unmask(next.APIKey, current.APIKey)
	next.Search.APIKey = unmask(next.Search.APIKey, current.Search.APIKey)
	next.Speech.APIKey = unmask(next.Speech.APIKey, current.Speech.APIKey)
	if err := s.store.SaveSettings(next); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
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

func (s *Server) getAbout(w http.ResponseWriter, r *http.Request) {
	frameworks := map[string]any{}
	for id, entry := range store.LocalFrameworks {
		frameworks[string(id)] = map[string]string{"label": entry.Label, "endpoint": entry.Endpoint}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   s.version,
		"workspace": s.store.Root(),
		"providers": []map[string]string{
			{"id": string(store.ProviderNebius), "label": "Nebius Token Factory", "endpoint": store.NebiusBaseURL},
			{"id": string(store.ProviderOpenAI), "label": "OpenAI 互換", "endpoint": "https://api.openai.com/v1"},
			{"id": string(store.ProviderLocal), "label": "ローカルLLM", "endpoint": store.LocalFrameworks["ollama"].Endpoint},
		},
		"frameworks": frameworks,
		"search":     websearch.Providers,
		"nemotron":   map[string]string{"low": store.NemotronNano, "middle": store.NemotronSuper},
		"tiers":      []string{string(persona.TierLow), string(persona.TierMiddle), string(persona.TierHigh)},
		"tools":      tools.New(s.store.Settings()).Names(),
		"needsToken": s.token != "",
	})
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

func (s *Server) speechRequest(w http.ResponseWriter, r *http.Request) {
	var request speech.ExternalHTTPRequest
	if err := readJSONLimit(r, &request, 24<<20); err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	response, err := s.speech.SpeechHTTPRequest(request)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) speechVertex(w http.ResponseWriter, r *http.Request) {
	var request speech.ExternalHTTPRequest
	if err := readJSON(r, &request); err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	response, err := s.speech.VertexSpeechHTTPRequest(request)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) liveStart(w http.ResponseWriter, r *http.Request) {
	id, err := s.speech.StartLiveSpeech()
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sessionId": id})
}

func (s *Server) liveAudio(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
		Audio     string `json:"audio"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	if err := s.speech.SendLiveSpeechAudio(body.SessionID, body.Audio); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) liveFinish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	if err := s.speech.FinishLiveSpeech(body.SessionID); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) liveStop(w http.ResponseWriter, r *http.Request) {
	s.speech.StopLiveSpeech()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- helpers ---------------------------------------------------------------

func first(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := r.FormValue(name); value != "" {
			return value
		}
	}
	return ""
}

func firstFile(r *http.Request, names ...string) (multipartFile, *multipartHeader, error) {
	for _, name := range names {
		file, header, err := r.FormFile(name)
		if err == nil {
			return file, header, nil
		}
	}
	return nil, nil, http.ErrMissingFile
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
