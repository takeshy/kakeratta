package server

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Explicit opt-in: "作る" on a suggestion drafts the persona on the real
// builder model in a temporary workspace.
func TestSuggestedPersonaCreateLive(t *testing.T) {
	if os.Getenv("KAKERATTA_SUGGESTION_LIVE") != "1" {
		t.Skip("set KAKERATTA_SUGGESTION_LIVE=1 and KAKERATTA_ROUTE_KEY_FILE to run a paid draft")
	}
	key, err := os.ReadFile(os.Getenv("KAKERATTA_ROUTE_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	server, workspace := app(t)
	settings := workspace.ConfiguredSettings()
	settings.BaseURL, settings.APIKey = store.NebiusBaseURL, strings.TrimSpace(string(key))
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	memo := "# Memo\n\n## いまの事実\n\n- [f_ramen] 豚骨ラーメンが好き from:e_r0\n- [f_nuka] ぬか床を毎朝かき混ぜている from:e_n0\n- [f_miso] 毎年冬に味噌を仕込む from:e_n1\n\n## かけらの記録\n\n" +
		"- 2026-10-01 09:00 [e_n0](/?entry=e_n0) ぬか床を仕込んだ #発酵 #漬物\n" +
		"- 2026-10-03 09:00 [e_n1](/?entry=e_n1) 自家製味噌の天地返しをした #発酵 #味噌\n" +
		"- 2026-10-05 09:00 [e_n2](/?entry=e_n2) 塩麹を仕込んだ #発酵\n" +
		"- 2026-10-06 12:00 [e_r1](/?entry=e_r1) 三田の二郎に並んだ #ラーメン\n"
	if err := workspace.WriteNote("gourmet", persona.MemoPath, memo); err != nil {
		t.Fatal(err)
	}
	ask := timeline.Entry{ID: "ask", Kind: timeline.KindPost, Speaker: "gourmet", Root: "ask", Text: "発酵食づくりの担当を作って分けませんか？", At: time.Now(),
		Suggestion: &timeline.PersonaSuggestion{Topic: "発酵食づくり", Reason: "ぬか床や味噌の仕込みの記録が続いている", Status: timeline.SuggestionAsked}}
	if err := workspace.Append(ask); err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/entry/ask/suggestion", strings.NewReader(`{"choice":"create","language":"ja"}`)).WithContext(context.Background())
	request.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(out, request)
	t.Logf("%d %s", out.Code, out.Body.String())
	if out.Code != 200 {
		t.Fatal("create failed")
	}
	entry, _ := workspace.Entry("ask")
	made, ok := server.catalogue.Find(context.Background(), entry.Suggestion.Persona)
	if !ok {
		t.Fatalf("no persona %q", entry.Suggestion.Persona)
	}
	t.Logf("PERSONA %s %s %s\ndomain: %s\ntone: %s\nmemory: %s\ninstructions: %s\ntools: %v",
		made.ID, made.Icon, made.Name, made.Domain, made.Tone, made.Memory, made.Instructions, made.Tools)
	note, _ := workspace.Note(made.ID, persona.MemoPath)
	t.Logf("MEMO\n%s", note.Text)
	if !strings.Contains(note.Text, "ぬか床") || strings.Contains(note.Text, "二郎") {
		t.Fatal("unexpected memory seeded")
	}
	narrowed, _ := server.catalogue.Find(context.Background(), "gourmet")
	t.Logf("SOURCE DOMAIN %s", narrowed.Domain)
	kept, _ := workspace.Note("gourmet", persona.MemoPath)
	t.Logf("SOURCE MEMO\n%s", kept.Text)
	if strings.Contains(kept.Text, "ぬか床") || !strings.Contains(kept.Text, "二郎") {
		t.Fatal("the move left the source wrong")
	}
}
