package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func app(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(Options{
		Store:     s,
		Catalogue: persona.NewCatalogue(s),
		Version:   "test",
	})
	// A post queues a reaction that writes as it goes. Letting it finish before
	// the temporary workspace is removed is the same thing the binary does on
	// shutdown, so the test exercises it too.
	t.Cleanup(server.Close)
	return server, s
}

// shared builds the request a share sheet makes: a screenshot as multipart,
// from outside the page. Everything else in the app is downstream of this
// working.
// posted is the composer's request for a post of this text.
func posted(t *testing.T, text string) *http.Request {
	t.Helper()
	body, err := json.Marshal(postBody{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewRequest(http.MethodPost, "/api/post", bytes.NewReader(body))
}

// People put the URL inside the sentence.
func TestAURLInsideTheTextIsReadAsTheLink(t *testing.T) {
	server, _ := app(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, posted(t, "この記事 http://127.0.0.1/a。よかった"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var entry timeline.Entry
	_ = json.Unmarshal(recorder.Body.Bytes(), &entry)
	if entry.Link != "http://127.0.0.1/a" || entry.Text != "この記事 http://127.0.0.1/a。よかった" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Media == nil || entry.Media.Error == "" || entry.Media.ReadAt == nil {
		t.Fatal("a URL in the text must go through the public-page fetcher")
	}
}

// A reply is where a link is often dropped in mid-conversation, and it must
// be read the same way a top-level post's link is.
func TestAURLInsideAReplyIsReadAsTheLink(t *testing.T) {
	server, _ := app(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, posted(t, "ラーメン"))
	var post timeline.Entry
	_ = json.Unmarshal(recorder.Body.Bytes(), &post)

	body, _ := json.Marshal(map[string]string{"replyTo": post.ID, "text": "これ見て http://127.0.0.1/a"})
	replyRecorder := httptest.NewRecorder()
	server.ServeHTTP(replyRecorder, httptest.NewRequest(http.MethodPost, "/api/reply", bytes.NewReader(body)))
	if replyRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", replyRecorder.Code, replyRecorder.Body.String())
	}
	var reply timeline.Entry
	_ = json.Unmarshal(replyRecorder.Body.Bytes(), &reply)
	if reply.Link != "http://127.0.0.1/a" {
		t.Fatalf("reply = %+v", reply)
	}
	if reply.Media == nil || reply.Media.Error == "" || reply.Media.ReadAt == nil {
		t.Fatal("a URL inside a reply must go through the public-page fetcher")
	}
}

func TestLinkInTrimsSentencePunctuation(t *testing.T) {
	for text, want := range map[string]string{
		"see https://example.com/a.":              "https://example.com/a",
		"(https://example.com/a)":                 "https://example.com/a",
		"https://en.wikipedia.org/wiki/Go_(game)": "https://en.wikipedia.org/wiki/Go_(game)",
		"読んだ https://example.com/b?x=1&y=2 ←":     "https://example.com/b?x=1&y=2",
		"https://example.com/記事":                  "https://example.com/",
		"no link here":                            "",
		"https://.":                               "",
	} {
		if got := linkIn(text); got != want {
			t.Errorf("linkIn(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestAnEmptyPostIsRefused(t *testing.T) {
	server, _ := app(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, posted(t, ""))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestSettingsRoundTripPreservesModelFallback(t *testing.T) {
	server, s := app(t)
	configured := s.ConfiguredSettings()
	configured.Provider = store.ProviderOpenAI
	configured.BaseURL = "https://custom.example/v1"
	configured.HighModel = ""
	if err := s.SaveSettings(configured); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	server.ServeHTTP(get, httptest.NewRequest("GET", "/api/settings", nil))
	var settings store.Settings
	if err := json.Unmarshal(get.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.HighModel != "" {
		t.Fatalf("fallback was resolved in editable settings: %q", settings.HighModel)
	}
	settings.MiddleModel = "new-middle"
	body, _ := json.Marshal(settings)
	put := httptest.NewRecorder()
	server.ServeHTTP(put, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(body)))
	if put.Code != http.StatusOK || s.Settings().HighModel != "new-middle" {
		t.Fatalf("status = %d, high = %q", put.Code, s.Settings().HighModel)
	}
}

func TestPastedImageCanExceedOldJSONLimit(t *testing.T) {
	server, s := app(t)
	settings := s.ConfiguredSettings()
	settings.LowModel, settings.VisionModel = "", ""
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	image := bytes.Repeat([]byte{1}, 7<<20)
	body, _ := json.Marshal(postBody{Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/post", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var entry timeline.Entry
	if err := json.Unmarshal(recorder.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	stored, _, err := s.Blob(entry.Media.BlobID)
	if err != nil || !bytes.Equal(stored, image) {
		t.Fatal("pasted image did not survive intact")
	}
}

// A reply is not a command: it goes into the timeline under the post, and the
// personas see it the way they see anything else.
func TestAReplyHangsOffThePost(t *testing.T) {
	server, s := app(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, posted(t, "ラーメン"))
	var post timeline.Entry
	_ = json.Unmarshal(recorder.Body.Bytes(), &post)

	body, _ := json.Marshal(map[string]string{"replyTo": post.ID, "text": "@tabe どう思う"})
	replyRecorder := httptest.NewRecorder()
	server.ServeHTTP(replyRecorder, httptest.NewRequest(http.MethodPost, "/api/reply", bytes.NewReader(body)))
	if replyRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", replyRecorder.Code, replyRecorder.Body.String())
	}
	var reply timeline.Entry
	_ = json.Unmarshal(replyRecorder.Body.Bytes(), &reply)
	if reply.Root != post.ID || reply.ReplyTo != post.ID || reply.Speaker != timeline.User {
		t.Fatalf("reply = %+v", reply)
	}
	if len(timeline.Thread(mustTimeline(t, s), post.ID)) != 2 {
		t.Fatal("the reply did not join the thread")
	}
}

func TestAFragmentCanMentionOnlyOnePerson(t *testing.T) {
	server, _ := app(t)
	for _, text := range []string{
		"@gourmet @tabe 同じ担当のIDと名前",
		"@alice @bob 登録されていないハンドル",
		"@tabe @alice 担当と登録されていないハンドル",
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, posted(t, text))
		if recorder.Code != http.StatusOK {
			t.Fatalf("post %q status = %d: %s", text, recorder.Code, recorder.Body.String())
		}
	}

	postRecorder := httptest.NewRecorder()
	server.ServeHTTP(postRecorder, posted(t, "@tabe @support どっちに聞こう"))
	if postRecorder.Code != http.StatusBadRequest || !strings.Contains(postRecorder.Body.String(), "1人だけ") {
		t.Fatalf("post status = %d: %s", postRecorder.Code, postRecorder.Body.String())
	}

	valid := httptest.NewRecorder()
	server.ServeHTTP(valid, posted(t, "@tabe 質問"))
	var post timeline.Entry
	_ = json.Unmarshal(valid.Body.Bytes(), &post)
	body, _ := json.Marshal(map[string]string{"replyTo": post.ID, "text": "@tabe @support どっちも"})
	replyRecorder := httptest.NewRecorder()
	server.ServeHTTP(replyRecorder, httptest.NewRequest(http.MethodPost, "/api/reply", bytes.NewReader(body)))
	if replyRecorder.Code != http.StatusBadRequest || !strings.Contains(replyRecorder.Body.String(), "1人だけ") {
		t.Fatalf("reply status = %d: %s", replyRecorder.Code, replyRecorder.Body.String())
	}
}

func TestDeletingRemovesStoredRecord(t *testing.T) {
	server, s := app(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, posted(t, "消す"))
	var post timeline.Entry
	_ = json.Unmarshal(recorder.Body.Bytes(), &post)

	deleted := httptest.NewRecorder()
	server.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/entry/"+post.ID, nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("status = %d", deleted.Code)
	}
	entries := mustTimeline(t, s)
	if len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
	if len(timeline.Thread(entries, post.ID)) != 0 {
		t.Fatal("a deleted post is still handed out")
	}
}

// The page never holds a key. It is told whether one is set, and sending that
// back must not wipe the real one.
func TestKeysNeverReachThePage(t *testing.T) {
	server, s := app(t)
	settings := s.Settings()
	settings.APIKey = "real-key"
	settings.OpenRouterAPIKey = "openrouter-key"
	settings.JevEnabled = true
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	shown := httptest.NewRecorder()
	server.ServeHTTP(shown, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var out store.Settings
	if err := json.Unmarshal(shown.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.APIKey == "real-key" {
		t.Fatal("the key was sent to the page")
	}
	if out.APIKey == "" {
		t.Fatal("the page cannot tell whether a key is set")
	}
	if out.OpenRouterAPIKey == "openrouter-key" || out.OpenRouterAPIKey == "" {
		t.Fatalf("OpenRouter key was exposed or lost: %q", out.OpenRouterAPIKey)
	}

	out.LowModel = "something/else"
	body, _ := json.Marshal(out)
	saved := httptest.NewRecorder()
	server.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if saved.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", saved.Code, saved.Body.String())
	}
	after := s.Settings()
	if after.APIKey != "real-key" {
		t.Fatalf("the key was overwritten with the mask: %q", after.APIKey)
	}
	if after.OpenRouterAPIKey != "openrouter-key" || !after.JevEnabled {
		t.Fatalf("OpenRouter settings were not preserved: %+v", after)
	}
	if after.LowModel != "something/else" {
		t.Fatal("the change that was made did not save")
	}
}

// A URL is user input, and the relay sits on a network of its own.
func TestFetchRefusesToReachTheServersOwnNetwork(t *testing.T) {
	server, _ := app(t)
	for _, target := range []string{"http://127.0.0.1:8787/", "http://localhost/", "file:///etc/passwd"} {
		body, _ := json.Marshal(map[string]string{"url": target})
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/fetch", bytes.NewReader(body)))
		if recorder.Code == http.StatusOK {
			t.Fatalf("%s was fetched", target)
		}
	}
}

func mustTimeline(t *testing.T, s *store.Store) []timeline.Entry {
	t.Helper()
	entries, err := s.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestEditPostPreservesThreadAndMedia(t *testing.T) {
	server, workspace := app(t)
	post := timeline.Entry{ID: "editable", Kind: timeline.KindPost, Speaker: timeline.User, Text: "before", At: time.Now().UTC(), Media: &timeline.Media{BlobID: "image"}, Link: "https://example.com"}
	if err := workspace.Append(post); err != nil {
		t.Fatal(err)
	}
	reply := timeline.Entry{ID: "reply", Kind: timeline.KindReply, Speaker: timeline.User, Root: post.ID, Text: "reply"}
	if err := workspace.Append(reply); err != nil {
		t.Fatal(err)
	}
	edit := func(id, body string, want int) {
		t.Helper()
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/entry/"+id, strings.NewReader(body)))
		if response.Code != want {
			t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body.String())
		}
	}
	edit(post.ID, `{"text":" after "}`, http.StatusOK)
	updated, _ := workspace.Entry(post.ID)
	if updated.Text != "after" || !updated.At.Equal(post.At) || updated.Media.BlobID != "image" || updated.Link != post.Link {
		t.Fatalf("updated = %+v", updated)
	}
	if len(mustTimeline(t, workspace)) != 2 {
		t.Fatal("edit changed the thread size")
	}
	edit(post.ID, `{"text":""}`, http.StatusOK)
	edit(post.ID, `{}`, http.StatusBadRequest)
	edit(reply.ID, `{"text":"changed"}`, http.StatusNotFound)
	edit("missing", `{"text":"changed"}`, http.StatusNotFound)
	post.ID = "persona"
	post.Speaker = "tabe"
	if err := workspace.Append(post); err != nil {
		t.Fatal(err)
	}
	edit(post.ID, `{"text":"changed"}`, http.StatusNotFound)
	post.ID = "plain"
	post.Speaker = timeline.User
	post.Media = nil
	post.Link = ""
	if err := workspace.Append(post); err != nil {
		t.Fatal(err)
	}
	edit(post.ID, `{"text":"  "}`, http.StatusBadRequest)
	post.Deleted = true
	if err := workspace.Append(post); err != nil {
		t.Fatal(err)
	}
	edit(post.ID, `{"text":"changed"}`, http.StatusNotFound)
}
