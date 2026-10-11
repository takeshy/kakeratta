package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/alexa"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestAlexaOAuthLifecycle(t *testing.T) {
	g := testGateway(t)
	g.options.Alexa = &alexa.Config{SkillID: "skill", ClientID: "client", ClientSecret: "secret", RedirectURLs: []string{"https://alexa.amazon.co.jp/callback"}}
	cookie := sessionCookie(t, g, "alice")
	body := map[string]string{"client_id": "client", "redirect_uri": "https://alexa.amazon.co.jp/callback", "state": "state & preserved", "response_type": "code", "scope": "alexa"}
	authorize := func(body map[string]string, origin string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/alexa/authorize", bytes.NewReader(data))
		r.AddCookie(cookie)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	bad := map[string]string{}
	for k, v := range body {
		bad[k] = v
	}
	bad["redirect_uri"] += ".evil"
	if w := authorize(bad, ""); w.Code != 400 {
		t.Fatalf("unsafe redirect: %d %s", w.Code, w.Body)
	}
	if w := authorize(body, "https://evil.example"); w.Code != 403 {
		t.Fatalf("cross-origin: %d", w.Code)
	}
	w := authorize(body, "")
	if w.Code != 200 {
		t.Fatalf("authorize: %d %s", w.Code, w.Body)
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	redirect, _ := url.Parse(result["redirect"])
	code := redirect.Query().Get("code")
	if code == "" || redirect.Query().Get("state") != body["state"] {
		t.Fatalf("redirect: %s", result["redirect"])
	}
	exchange := func(form url.Values, secret string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/alexa/token", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetBasicAuth("client", secret)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {body["redirect_uri"]}}
	if w := exchange(form, "wrong"); w.Code != 401 {
		t.Fatal("bad client accepted")
	}
	form.Set("redirect_uri", "https://evil.example")
	if w := exchange(form, "secret"); w.Code != 400 {
		t.Fatal("wrong redirect accepted")
	}
	form.Set("redirect_uri", body["redirect_uri"])
	w = exchange(form, "secret")
	if w.Code != 200 {
		t.Fatalf("exchange: %d %s", w.Code, w.Body)
	}
	var tokens struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &tokens)
	if tokens.Access == "" || tokens.Refresh == "" {
		t.Fatal("missing tokens")
	}
	if w := exchange(form, "secret"); w.Code != 400 {
		t.Fatal("authorization code reused")
	}
	tenant, _ := g.tenant("alice")
	v, _ := tenant.store.SocialValue(context.Background(), alexaCredential)
	for _, token := range []string{code, tokens.Access, tokens.Refresh} {
		if bytes.Contains(v.Data, []byte(token)) {
			t.Fatal("plaintext credential persisted")
		}
	}
	// Access tokens never substitute for a Firebase browser session.
	r := httptest.NewRequest("GET", "/api/timeline", nil)
	r.Header.Set("Authorization", "Bearer "+tokens.Access)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("Alexa token authorized browser API")
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.Refresh}}
	if w := exchange(refresh, "secret"); w.Code != 200 {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("DELETE", "/api/alexa/connection", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if w := exchange(refresh, "secret"); w.Code != 400 {
		t.Fatal("revoked refresh token accepted")
	}
}

func TestAlexaPostsRepliesSchedulesAndIsolation(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: st, Catalogue: persona.NewCatalogue(st), Passive: true})
	t.Cleanup(s.Halt)
	token := s.newAlexaToken()
	data, _ := json.Marshal(alexaCredentials{Access: alexaHash(token), AccessUntil: time.Now().Add(time.Hour)})
	if _, err := st.SwapSocialValue(context.Background(), alexaCredential, "", store.SocialValue{Data: data}); err != nil {
		t.Fatal(err)
	}
	e := alexa.Envelope{}
	e.Context.System.User.AccessToken = token
	e.Request.Type = "IntentRequest"
	e.Request.ID = "test-request"
	e.Request.Timestamp = time.Now()
	e.Request.Intent.Name = "ThrowKakeraIntent"
	e.Request.Intent.Slots = map[string]struct {
		Value string `json:"value"`
	}{"text": {Value: "明日は散歩に行きたい"}}
	r := httptest.NewRequest("POST", "/api/alexa", nil)
	invoke := func() *httptest.ResponseRecorder { w := httptest.NewRecorder(); s.handleAlexa(w, r, e); return w }
	for range 2 {
		w := invoke()
		if !strings.Contains(w.Body.String(), "受け付けました") {
			t.Fatalf("post: %s", w.Body)
		}
	}
	entries, err := st.Timeline()
	if err != nil || len(entries) != 1 {
		t.Fatalf("duplicate entries: %v %v", entries, err)
	}
	root := entries[0].ID
	for _, entry := range []timeline.Entry{
		{ID: "reply-1", Root: root, Kind: timeline.KindRemark, Speaker: "support", Text: "**散歩**を楽しんでください。", At: time.Now()},
		{ID: "other-root", Root: "other-root", Kind: timeline.KindPost, Speaker: timeline.User, Text: "別の話", At: time.Now()},
		{ID: "reply-2", Root: "other-root", Kind: timeline.KindRemark, Speaker: "support", Text: "別の返事です", At: time.Now()},
	} {
		if err := st.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	e.Request.Intent.Name = "ReadRepliesIntent"
	e.Session.Attributes = map[string]string{"postId": root}
	w := invoke()
	if !strings.Contains(w.Body.String(), "散歩を楽しんで") || strings.Contains(w.Body.String(), "別の返事") || strings.Contains(w.Body.String(), "**") {
		t.Fatalf("replies: %s", w.Body)
	}
	settings := st.Settings()
	settings.TimeZone = "Asia/Tokyo"
	if err := st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Minute)
	if _, err := st.SaveSchedule(store.Schedule{Skill: "support", At: at, Note: "散歩に行く"}); err != nil {
		t.Fatal(err)
	}
	e.Request.Intent.Name = "ReadSchedulesIntent"
	w = invoke()
	loc, _ := time.LoadLocation("Asia/Tokyo")
	if !strings.Contains(w.Body.String(), at.In(loc).Format("1月2日15時04分")) || !strings.Contains(w.Body.String(), "散歩に行く") {
		t.Fatalf("schedules: %s", w.Body)
	}
	// Neither a modified token nor a token with another workspace prefix grants access.
	for _, invalid := range []string{s.newAlexaToken(), strings.Repeat("a", 64) + "." + strings.Split(token, ".")[1]} {
		e.Context.System.User.AccessToken = invalid
		w = invoke()
		if !strings.Contains(w.Body.String(), "LinkAccount") || strings.Contains(w.Body.String(), "散歩に行く") {
			t.Fatal("invalid token exposed schedule")
		}
	}
	if text := alexaVoice("ja-JP").speechText("<html><body>secret<script>script</script></body></html>", 700); strings.Contains(text, "secret") || strings.Contains(text, "script") {
		t.Fatal("HTML read as speech")
	}
}
