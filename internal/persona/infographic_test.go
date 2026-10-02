package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestInfographicUsesExpandedContentAndDisplayLanguage(t *testing.T) {
	for _, image := range []bool{false, true} {
		for _, language := range []string{"ja", "en"} {
			t.Run(language+map[bool]string{true: "-image", false: "-url"}[image], func(t *testing.T) {
				r, s, f := setup(t, func(string, string) string { return `{"deliver":["infographic"]}` }, "infographic")
				original := post(t, s, "元の資料")
				readAt := time.Now()
				original.Media = &timeline.Media{ReadAt: &readAt, Describe: "資料の題名", Text: "展開・解釈された文章：売上は昨年の2倍"}
				if image {
					original.Media.BlobID = "already-read-image"
				} else {
					original.Link = "https://example.com/article"
					original.Media.Origin = original.Link
				}
				if err := s.UpdateEntry(original); err != nil {
					t.Fatal(err)
				}
				trigger := timeline.Entry{ID: "infographic-request", Kind: timeline.KindReply, Speaker: timeline.User, Text: "インフォグラフィックにして", Language: language, Root: original.ID, ReplyTo: original.ID, At: time.Now()}
				if _, _, err := s.CreateEntry(trigger); err != nil {
					t.Fatal(err)
				}
				html := `<!DOCTYPE html><html lang="` + language + `"><body><h1>図解</h1></body></html>`
				f.phaseReply = func(system, user string, offered map[string]bool) string {
					if !strings.Contains(user, original.Media.Text) || !strings.Contains(user, original.Media.Describe) {
						t.Fatalf("expanded content missing: %s", user)
					}
					if !strings.Contains(system, "("+language+")") || !strings.Contains(system, "一般規則より優先") {
						t.Fatal("display language not prioritized")
					}
					switch {
					case offered["finish_read"]:
						return "read_done"
					case offered["submit_answer"]:
						raw, _ := json.Marshal(Answer{Text: html, Sources: []string{}})
						return string(raw)
					}
					t.Fatalf("unexpected phase: %v", offered)
					return ""
				}
				skill, _ := r.catalogue.Find(context.Background(), "infographic")
				client, settings := r.clientOf()
				settings.HighModel = "ultra-test"
				entry := r.run(context.Background(), skill, []timeline.Entry{original, trigger}, trigger, true, false, "", false, client, settings, r.registry(settings))
				if entry == nil || entry.Text != html || entry.Model != "ultra" {
					t.Fatalf("HTML was not published intact: %+v", entry)
				}
				stored, ok := s.Entry(entry.ID)
				if !ok || stored.Text != html || len(f.keeps) != 0 {
					t.Fatal("HTML persistence or memory behavior failed")
				}
				for _, model := range f.models {
					if model != "ultra-test" {
						t.Fatalf("model = %q, expected Ultra", model)
					}
				}
			})
		}
	}
}

func TestInfographicLanguageFallsBackToOriginalRequest(t *testing.T) {
	thread := []timeline.Entry{{Speaker: timeline.User, Language: "en"}}
	if !strings.Contains(infographicRules(timeline.Entry{}, thread), "英語 (en)") {
		t.Fatal("missing original language")
	}
}

func TestAlwaysUltraIsOptionalForOrdinaryPersonas(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "always-ultra"}[enabled], func(t *testing.T) {
			r, s, f := setup(t, nil, "support")
			skill, _ := r.catalogue.Find(context.Background(), "support")
			skill.AlwaysUltra = enabled
			raw, _ := json.Marshal(skill)
			if _, err := r.catalogue.Update(context.Background(), skill.ID, raw, nil); err != nil {
				t.Fatal(err)
			}
			skill, _ = r.catalogue.Find(context.Background(), "support")
			trigger := post(t, s, "設定について質問")
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				if enabled && strings.Contains(system, "今回ユーザーは、前の回答より") {
					t.Fatal("ordinary request presented as a revision")
				}
				if offered["finish_read"] {
					return "read_done"
				}
				if offered["submit_answer"] {
					return `{"text":"回答","sources":[]}`
				}
				t.Fatalf("unexpected phase: %v", offered)
				return ""
			}
			client, settings := r.clientOf()
			settings.HighModel, settings.MiddleModel = "ultra-test", "super-test"
			entry := r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, true, false, "", false, client, settings, r.registry(settings))
			wantSlot, wantModel := "super", "super-test"
			if enabled {
				wantSlot, wantModel = "ultra", "ultra-test"
			}
			if entry == nil || entry.Model != wantSlot {
				t.Fatalf("entry = %+v", entry)
			}
			for _, model := range f.models {
				if model != wantModel {
					t.Fatalf("model = %s, want %s", model, wantModel)
				}
			}
		})
	}
}
