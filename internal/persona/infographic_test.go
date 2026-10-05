package persona

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestInfographicReplyToAnotherPersonasAnswer(t *testing.T) {
	for _, mention := range []string{"@infographic", "@インフォグラフィック"} {
		t.Run(mention, func(t *testing.T) {
			r, s, f := setup(t, func(system, user string) string {
				t.Errorf("unexpected routing or reply escalation: %s", system)
				return `{"deliver":[]}`
			}, "gourmet", "book", "infographic")
			original := post(t, s, "昼食の候補を比較して")
			answer := timeline.Entry{ID: "comparison-answer", Kind: timeline.KindRemark, Speaker: "gourmet", Root: original.ID, ReplyTo: original.ID, Text: "A店は駅から3分で予算1000円。B店は駅から10分で予算1500円。", At: time.Now()}
			if err := s.Append(answer); err != nil {
				t.Fatal(err)
			}
			other := timeline.Entry{ID: "other-answer", Kind: timeline.KindRemark, Speaker: "book", Root: original.ID, ReplyTo: original.ID, Text: "食後には読書もおすすめ。", At: time.Now()}
			if err := s.Append(other); err != nil {
				t.Fatal(err)
			}
			trigger := replyTo(t, s, answer, mention+" この回答をインフォグラフィックにして")
			html := `<!DOCTYPE html><html lang="ja"><body><h1>昼食の比較</h1><p>A店：3分、1000円。B店：10分、1500円。</p></body></html>`
			reads, answers := 0, 0
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				if !strings.Contains(user, answer.Text) || !strings.Contains(user, answer.ID+" への返事") || !strings.Contains(user, trigger.Text) {
					t.Errorf("replied-to answer or request missing: %s", user)
				}
				if !strings.Contains(system, "その回答の説明・比較・提案を図解する") || !strings.Contains(system, "元の投稿やスレッド内の最後の回答へ勝手に対象を置き換えず") {
					t.Error("missing reply subject instructions")
				}
				switch {
				case offered["finish_read"]:
					reads++
					return "read_done"
				case offered["submit_answer"]:
					answers++
					raw, _ := json.Marshal(Answer{Text: html, Sources: []string{}})
					return string(raw)
				default:
					t.Errorf("unexpected phase: %v", offered)
					return ""
				}
			}
			r.react(trigger.ID)
			record, err := s.Routing(trigger.ID)
			if err != nil || record == nil || !reflect.DeepEqual(record.Delivered, []string{"infographic"}) || !reflect.DeepEqual(record.Called, map[string]string{"infographic": "mention"}) {
				t.Fatalf("routing = %+v, error = %v", record, err)
			}
			said := remarks(t, s)
			if len(said) != 3 {
				t.Fatalf("expected one new answer, got %d remarks", len(said))
			}
			result := said[2]
			if result.Speaker != "infographic" || result.Text != html || result.Root != original.ID || result.ReplyTo != trigger.ID || result.Model != "ultra" {
				t.Fatalf("unexpected infographic: %+v", result)
			}
			if reads != 1 || answers != 1 || len(f.keeps) != 0 {
				t.Fatalf("reads=%d answers=%d memory writes=%d", reads, answers, len(f.keeps))
			}
		})
	}
}

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
				if model := f.lastModelFor("インフォグラフィック"); model != "ultra-test" {
					t.Fatalf("answer model = %q, expected Ultra", model)
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
			if model := f.lastModelFor("support"); model != wantModel {
				t.Fatalf("answer model = %s, want %s", model, wantModel)
			}
		})
	}
}
