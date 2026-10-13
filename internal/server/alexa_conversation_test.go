package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/alexa"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestAlexaConfirmationCorrectionAndReply(t *testing.T) {
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
	e.Request.Timestamp = time.Now()
	e.Request.Locale = "ja-JP"
	var last struct {
		SessionAttributes map[string]string
		Response          struct {
			OutputSpeech struct{ Text string }
			Reprompt     struct{ OutputSpeech struct{ Text string } }
			Directives   []struct {
				Type, SlotToElicit string
				UpdatedIntent      struct{ Name string }
			}
		}
	}
	n := 0
	invoke := func(intent, text string) string {
		n++
		e.Request.ID = strings.Repeat("r", n)
		e.Request.Intent.Name = intent
		e.Request.Intent.Slots = map[string]struct {
			Value string `json:"value"`
		}{"text": {Value: text}}
		w := httptest.NewRecorder()
		s.handleAlexa(w, httptest.NewRequest("POST", "/api/alexa", nil), e)
		last.SessionAttributes = nil
		last.Response.Directives = nil
		if err := json.Unmarshal(w.Body.Bytes(), &last); err != nil {
			t.Fatal(err)
		}
		e.Session.Attributes = last.SessionAttributes
		return last.Response.OutputSpeech.Text
	}
	count := func(want int) {
		t.Helper()
		entries, err := st.Timeline()
		if err != nil || len(entries) != want {
			t.Fatalf("entries=%d, want=%d, err=%v", len(entries), want, err)
		}
	}
	if got := invoke("AMAZON.FallbackIntent", ""); strings.Contains(got, "かけらったメモです") {
		t.Fatal(got)
	}
	if got := invoke("AMAZON.YesIntent", ""); !strings.Contains(got, "ありません") {
		t.Fatal(got)
	}
	if got := invoke("ThrowKakeraIntent", ""); got != "メモの内容をどうぞ。" {
		t.Fatal(got)
	}
	if len(last.Response.Directives) != 1 || last.Response.Directives[0].Type != "Dialog.ElicitSlot" {
		t.Fatal("no slot elicitation")
	}
	invoke("ThrowKakeraIntent", "明日は山歩き")
	count(0)
	if got := invoke("AMAZON.NoIntent", ""); got != "メモの内容をどうぞ。" {
		t.Fatal(got)
	}
	if e.Session.Attributes["draftText"] != "" {
		t.Fatal("rejected draft retained")
	}
	if got := invoke("AMAZON.YesIntent", ""); !strings.Contains(got, "ありません") {
		t.Fatal(got)
	}
	count(0)
	invoke("ThrowKakeraIntent", "明日は散歩")
	if got := invoke("AMAZON.FallbackIntent", ""); !strings.Contains(got, "明日は散歩") {
		t.Fatal("lost draft", got)
	}
	if got := invoke("AMAZON.YesIntent", ""); got != "投稿しました。" {
		t.Fatal(got)
	}
	count(1)
	root := e.Session.Attributes["postId"]
	for i, text := range []string{"古い返事", "最後の返事"} {
		if err := st.Append(timeline.Entry{ID: []string{"old", "latest"}[i], Root: root, Kind: timeline.KindRemark, Speaker: "support", Text: text, At: time.Now().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := invoke("ReadRepliesIntent", ""); !strings.Contains(got, "最後の返事") || strings.Contains(got, "古い返事") {
		t.Fatal(got)
	}
	if got := invoke("ReplyKakeraIntent", ""); got != "返信の内容をどうぞ。" {
		t.Fatal(got)
	}
	invoke("ReplyKakeraIntent", "ありがとう")
	count(3)
	invoke("ReviseKakeraIntent", "")
	if last.Response.Directives[0].UpdatedIntent.Name != "ReplyKakeraIntent" {
		t.Fatal("correction lost reply target")
	}
	invoke("ReplyKakeraIntent", "夕方に歩きます")
	draftID := e.Session.Attributes["draftId"]
	invoke("AMAZON.YesIntent", "")
	count(4)
	entry, ok := st.Entry(draftID)
	if !ok || entry.Kind != timeline.KindReply || entry.Root != root || entry.ReplyTo != "latest" || entry.Text != "夕方に歩きます" {
		t.Fatalf("bad reply: %+v", entry)
	}
	// A stale/deleted or foreign-workspace target cannot become a new root post.
	e.Session.Attributes = map[string]string{"replyId": "foreign"}
	if got := invoke("ReplyKakeraIntent", "hello"); !strings.Contains(got, "先に") {
		t.Fatal(got)
	}
	count(4)
	e.Session.Attributes = map[string]string{"replyId": "latest"}
	invoke("ReplyKakeraIntent", "削除後の返信")
	if _, err := st.DeleteEntry(context.Background(), "latest"); err != nil {
		t.Fatal(err)
	}
	if got := invoke("AMAZON.YesIntent", ""); !strings.Contains(got, "返信先が見つかりません") {
		t.Fatal(got)
	}
}
