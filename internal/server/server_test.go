package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func app(t *testing.T, token string) (*Server, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(Options{
		Store:     s,
		Catalogue: persona.NewCatalogue(s.SkillDir()),
		Assets:    fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
		Version:   "test",
		Token:     token,
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
func shared(t *testing.T, fields map[string]string, image []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	for name, value := range fields {
		_ = form.WriteField(name, value)
	}
	if image != nil {
		part, err := form.CreateFormFile("image", "screenshot.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(image)
	}
	_ = form.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/capture", body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	return request
}

func TestAScreenshotFromTheShareSheetLands(t *testing.T) {
	server, s := app(t, "")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, shared(t, map[string]string{"text": "この一節がよかった"}, []byte{0x89, 'P', 'N', 'G'}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var entry timeline.Entry
	if err := json.Unmarshal(recorder.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != timeline.KindPost || entry.Text != "この一節がよかった" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Media == nil || entry.Media.BlobID == "" {
		t.Fatal("the screenshot did not make it")
	}
	// The capture has not been read yet, and the post is already in. The
	// reaction happens afterwards; the user does not wait for it.
	if entry.Media.Ready() {
		t.Fatal("the response waited for the image to be read")
	}
	data, _, err := s.Blob(entry.Media.BlobID)
	if err != nil || len(data) != 4 {
		t.Fatalf("blob = %v (%v)", data, err)
	}

	entries, _ := s.Timeline()
	if len(entries) != 1 {
		t.Fatalf("timeline = %+v", entries)
	}
}

// Android's share target sends title/text/url; a shortcut sends whatever it was
// told to. Both are the same post.
func TestTheShareSheetsFieldNamesAreAllAccepted(t *testing.T) {
	server, _ := app(t, "")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, shared(t, map[string]string{
		"title": "記事のタイトル", "url": "https://example.com/a",
	}, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var entry timeline.Entry
	_ = json.Unmarshal(recorder.Body.Bytes(), &entry)
	if entry.Text != "記事のタイトル" || entry.Link != "https://example.com/a" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestAnEmptyShareIsRefused(t *testing.T) {
	server, _ := app(t, "")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, shared(t, nil, nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

// The relay holds the inference keys, so with a token set nothing gets in
// without it - and the shortcut's way of presenting one has to work.
func TestTheTokenGuardsEveryWayIn(t *testing.T) {
	server, _ := app(t, "sesame")

	bare := httptest.NewRecorder()
	server.ServeHTTP(bare, httptest.NewRequest(http.MethodGet, "/api/timeline", nil))
	if bare.Code != http.StatusUnauthorized {
		t.Fatalf("without a token: %d", bare.Code)
	}

	// A shortcut sets a header.
	header := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
	request.Header.Set("Authorization", "Bearer sesame")
	server.ServeHTTP(header, request)
	if header.Code != http.StatusOK {
		t.Fatalf("with a header: %d", header.Code)
	}
	// Having presented it once, the browser carries it, which is what makes the
	// share target work: it posts with no chance to set a header.
	cookies := header.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != cookieName {
		t.Fatalf("no cookie was set: %+v", cookies)
	}
	withCookie := httptest.NewRecorder()
	shareRequest := shared(t, map[string]string{"text": "共有"}, nil)
	shareRequest.AddCookie(cookies[0])
	server.ServeHTTP(withCookie, shareRequest)
	if withCookie.Code != http.StatusOK {
		t.Fatalf("share target with the cookie: %d %s", withCookie.Code, withCookie.Body.String())
	}

	// And the form field, for anything that can send neither.
	inForm := httptest.NewRecorder()
	server.ServeHTTP(inForm, shared(t, map[string]string{"text": "共有", "token": "sesame"}, nil))
	if inForm.Code != http.StatusOK {
		t.Fatalf("share target with the token in the form: %d", inForm.Code)
	}

	wrong := httptest.NewRecorder()
	server.ServeHTTP(wrong, shared(t, map[string]string{"text": "共有", "token": "wrong"}, nil))
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("with a wrong token: %d", wrong.Code)
	}
}

// A reply is not a command: it goes into the timeline under the post, and the
// personas see it the way they see anything else.
func TestAReplyHangsOffThePost(t *testing.T) {
	server, s := app(t, "")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, shared(t, map[string]string{"text": "ラーメン"}, nil))
	var post timeline.Entry
	_ = json.Unmarshal(recorder.Body.Bytes(), &post)

	body, _ := json.Marshal(map[string]string{"replyTo": post.ID, "text": "@たべ どう思う"})
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

func TestDeletingLeavesATombstoneRatherThanRewritingHistory(t *testing.T) {
	server, s := app(t, "")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, shared(t, map[string]string{"text": "消す"}, nil))
	var post timeline.Entry
	_ = json.Unmarshal(recorder.Body.Bytes(), &post)

	deleted := httptest.NewRecorder()
	server.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/entry/"+post.ID, nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("status = %d", deleted.Code)
	}
	entries := mustTimeline(t, s)
	if len(entries) != 1 || !entries[0].Deleted {
		t.Fatalf("entries = %+v", entries)
	}
	if len(timeline.Thread(entries, post.ID)) != 0 {
		t.Fatal("a deleted post is still handed out")
	}
}

// The page never holds a key. It is told whether one is set, and sending that
// back must not wipe the real one.
func TestKeysNeverReachThePage(t *testing.T) {
	server, s := app(t, "")
	settings := s.Settings()
	settings.APIKey = "real-key"
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

	out.LightModel = "something/else"
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
	if after.LightModel != "something/else" {
		t.Fatal("the change that was made did not save")
	}
}

// A URL is user input, and the relay sits on a network of its own.
func TestFetchRefusesToReachTheServersOwnNetwork(t *testing.T) {
	server, _ := app(t, "")
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
