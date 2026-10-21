package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// "Make it" under a suggestion drafts the persona with no questions, installs
// and activates it, and starts its memory with what of the suggesting
// persona's memory was picked for it, moved out of the suggesting persona's; a
// second press is refused.
func TestASuggestedPersonaIsMadeByItsButton(t *testing.T) {
	server, workspace := app(t)
	var prompt string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		content := "{}"
		if len(request.Messages) == 2 && strings.Contains(request.Messages[0].Content, "記憶の整理から提案された新しい担当") {
			prompt = request.Messages[1].Content
			draft, _ := json.Marshal(map[string]any{"message": "作りました", "draft": persona.Skill{ID: "gourmet", Name: "ぬか床", Icon: "🥒", Domain: "発酵食づくりの話。", Memory: "仕込みと味の変化を覚える。"},
				"facts": []string{"f_nuka"}, "records": []string{"e_a"}, "sourceDomain": "料理・食事の写真や話。ただし、発酵食づくりの話は除く。"})
			content = string(draft)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	memo := "# Memo\n\n## いまの事実\n\n- [f_nuka] ぬか床を毎朝かき混ぜている from:e_a\n- [f_ramen] 豚骨ラーメンが好き from:e_b\n\n## かけらの記録\n\n" +
		"- 2026-10-01 09:00 [e_a](/?entry=e_a) ぬか床を仕込んだ #発酵\n- 2026-10-01 12:00 [e_b](/?entry=e_b) 二郎に行った #ラーメン\n"
	if err := workspace.WriteNote("gourmet", persona.MemoPath, memo); err != nil {
		t.Fatal(err)
	}
	ask := timeline.Entry{ID: "ask", Kind: timeline.KindPost, Speaker: "gourmet", Root: "ask", Text: "ぬか床の担当を作りませんか？", At: time.Now(),
		Suggestion: &timeline.PersonaSuggestion{Topic: "発酵食づくり", Reason: "ぬか床の記録が続いている", Status: timeline.SuggestionAsked}}
	if err := workspace.Append(ask); err != nil {
		t.Fatal(err)
	}
	answer := func(body string) (int, map[string]json.RawMessage) {
		out := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/entry/ask/suggestion", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(out, request)
		var got map[string]json.RawMessage
		_ = json.Unmarshal(out.Body.Bytes(), &got)
		return out.Code, got
	}
	if code, _ := answer(`{"choice":"maybe"}`); code != 400 {
		t.Fatalf("an unknown answer: %d", code)
	}
	code, got := answer(`{"choice":"create","language":"ja"}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, got)
	}
	if !strings.Contains(prompt, "発酵食づくり") || !strings.Contains(prompt, "[f_nuka]") || !strings.Contains(prompt, "[e_a]") {
		t.Fatalf("the draft was not shown the suggestion and memory:\n%s", prompt)
	}
	var entry timeline.Entry
	_ = json.Unmarshal(got["entry"], &entry)
	// The drafted id was taken by the suggesting persona, so it gets another.
	if entry.Suggestion == nil || entry.Suggestion.Status != timeline.SuggestionCreated || entry.Suggestion.Persona != "gourmet-2" {
		t.Fatalf("answered post = %+v", entry.Suggestion)
	}
	made, ok := server.catalogue.Find(context.Background(), "gourmet-2")
	if !ok || made.Name != "ぬか床" || !made.Remembers() || !slices.Contains(workspace.ConfiguredSettings().Active, "gourmet-2") {
		t.Fatalf("made = %+v, %v", made, ok)
	}
	if narrowed, _ := server.catalogue.Find(context.Background(), "gourmet"); narrowed.Domain != "料理・食事の写真や話。ただし、発酵食づくりの話は除く。" {
		t.Fatalf("the suggesting persona's condition = %q", narrowed.Domain)
	}
	note, _ := workspace.Note("gourmet-2", persona.MemoPath)
	if !strings.Contains(note.Text, "ぬか床を毎朝かき混ぜている") || !strings.Contains(note.Text, "ぬか床を仕込んだ") || strings.Contains(note.Text, "ラーメン") {
		t.Fatalf("new memory:\n%s", note.Text)
	}
	if kept, _ := workspace.Note("gourmet", persona.MemoPath); strings.Contains(kept.Text, "ぬか床") || !strings.Contains(kept.Text, "豚骨ラーメンが好き") || !strings.Contains(kept.Text, "二郎に行った") {
		t.Fatalf("the suggesting persona's memory after the move:\n%s", kept.Text)
	}
	if code, _ := answer(`{"choice":"decline"}`); code != 409 {
		t.Fatalf("a second answer: %d", code)
	}
}

// With no rewritten condition from the model, the subject is still left out
// of the suggesting persona's own, in the language it was written in.
func TestExcludedLineFollowsTheCondition(t *testing.T) {
	if got := excludedLine("料理・食事の写真。", "発酵食"); got != "ただし、発酵食の話は除く。" {
		t.Fatalf("ja = %q", got)
	}
	if got := excludedLine("Food photos.", "Recipes"); got != " Excludes Recipes." {
		t.Fatalf("en = %q", got)
	}
}
