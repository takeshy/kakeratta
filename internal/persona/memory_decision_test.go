package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The keeper decides what to retain, even when an old client submits a false
// remember flag. Related silence and speech both reach the keeper.
func TestRelatedAnswersRunKeeperRegardlessOfLegacyRemember(t *testing.T) {
	for _, remember := range []bool{false, true} {
		for _, text := range []string{"", "了解です"} {
			t.Run(fmt.Sprintf("remember=%t spoken=%t", remember, text != ""), func(t *testing.T) {
				r, s, f := setup(t, func(string, string) string { return `{"deliver":["routine"]}` }, "routine")
				calls := 0
				f.phaseReply = func(_, _ string, tools map[string]bool) string {
					calls++
					if !tools[submitAnswer] {
						t.Fatalf("unnecessary planning call: %v", tools)
					}
					raw, _ := json.Marshal(map[string]any{"text": text, "sources": []string{}, "remember": remember, "schedule": false, "unrelated": false})
					return string(raw)
				}
				entry := post(t, s, "前は朝の散歩。最近は夕方に歩いています。")
				r.react(entry.ID)
				want := 1
				if calls != 1 || len(f.keeps) != want || len(f.reviews) != 0 {
					t.Fatalf("answer=%d keeper=%d (want %d) review=%d", calls, len(f.keeps), want, len(f.reviews))
				}
			})
		}
	}
}

func TestMemoryKeeperUsesRelatednessInsteadOfAnAnswerDecision(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		unrelated  bool
		want       int
	}{
		{"related silence", "", false, 1},
		{"unrelated silence", "", true, 0},
		{"spoken answer", "今回は行ってみては？", false, 1},
		{"spoken answer cannot be unrelated", "今回は行ってみては？", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, f := setup(t, func(string, string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
			f.phaseReply = func(_, _ string, offered map[string]bool) string {
				if !offered[submitAnswer] {
					t.Fatalf("unexpected phase: %v", offered)
				}
				raw, _ := json.Marshal(map[string]any{"text": tc.text, "sources": []string{}, "unrelated": tc.unrelated, "schedule": false})
				return string(raw)
			}
			post := post(t, s, "静岡のA店に行きたかったが時間がなく寄れなかった")
			r.react(post.ID)
			if len(f.keeps) != tc.want {
				t.Fatalf("keeper calls=%d want=%d", len(f.keeps), tc.want)
			}
		})
	}
}

func TestRememberIsNotAdvertisedAndLegacyFalseIsIgnored(t *testing.T) {
	raw, _ := json.Marshal(answerTool.Parameters)
	if strings.Contains(string(raw), `"remember"`) {
		t.Fatal("answer schema still asks for remember")
	}
	answer, err := ParseAnswer([]byte(`{"text":"回答","sources":[],"remember":false,"schedule":false}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(answer)
	if strings.Contains(string(raw), `"remember"`) {
		t.Fatal("legacy decision survived into the execution answer")
	}
}

// A check-in handed records due for review keeps them through the keeper even
// when an old client says not to remember it, or the same review comes back.
func TestCheckInReviewRunsKeeperWithLegacyRememberFalse(t *testing.T) {
	r, _, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	f.phaseReply = func(_, _ string, offered map[string]bool) string {
		if !offered[submitAnswer] {
			t.Fatalf("unexpected phase: %v", offered)
		}
		return `{"text":"行きたい店の記憶は、まだ残しておきますか？","sources":[],"remember":false,"schedule":false,"unrelated":false}`
	}
	client, settings := r.clientOf()
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	trigger := timeline.Entry{Speaker: "system", At: time.Now(), Text: "Review due"}
	r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, false, false, dailyShareInstructions+reviewInstructions, false, client, settings, r.registry(settings))
	if len(f.keeps) != 1 {
		t.Fatalf("keeper ran %d times for a review check-in, want 1", len(f.keeps))
	}
}

// Edits use the durable keeper to correct the prior memory, including when
// the edited text is now outside the persona's domain.
func TestUnrelatedEditRunsKeeperToCorrectPriorFacts(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	unrelated := false
	f.phaseReply = func(_, _ string, offered map[string]bool) string {
		if offered[submitAnswer] {
			raw, _ := json.Marshal(map[string]any{"text": "", "sources": []string{}, "remember": false, "schedule": false, "unrelated": unrelated})
			return string(raw)
		}
		return `{"operations":[],"consistent":true,"reason":"なし"}`
	}
	f.keep = func(string, string) string {
		return `{"remember":true,"summary":"A店が気に入った","tags":["A店"],"expires":"","review":""}`
	}
	entry := post(t, s, "A店が気に入った")
	r.react(entry.ID)
	if ids := recordIDs(r, "gourmet"); len(ids) != 1 || len(f.keeps) != 1 {
		t.Fatalf("records=%v keeps=%d", ids, len(f.keeps))
	}
	if note, ok := s.Note("gourmet", MemoPath); !ok || !strings.Contains(note.Text, entry.ID) {
		t.Fatalf("record not kept from the post: %+v", note)
	}

	// A fact the daily pass made from the post must still be corrected.
	ff := r.loadMemo("gourmet", s.Settings().Location())
	ff.Facts = []fact{{ID: "f_legacy", Text: "本人はA店が気に入った。", From: []string{entry.ID}}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(ff, s.Settings().Location())); err != nil {
		t.Fatal(err)
	}
	unrelated = true
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	prior := r.loadMemo("gourmet", s.Settings().Location()).Facts[0]
	f.keep = func(string, string) string {
		raw, _ := json.Marshal(map[string]any{"summary": "A店の感想を取り消した", "tags": []string{"訂正"}, "facts": []map[string]any{{"op": "invalidate", "id": prior.ID}}})
		return string(raw)
	}
	entry.Text = "テスト"
	if err := s.UpdateEntry(entry); err != nil {
		t.Fatal(err)
	}
	client, settings := r.clientOf()
	r.run(context.Background(), skill, []timeline.Entry{entry}, entry, false, false, editedInstructions, false, client, settings, r.registry(settings))
	if len(f.keeps) != 2 {
		t.Fatalf("keeper did not process the edit: %d", len(f.keeps))
	}
	if ids := recordIDs(r, "gourmet"); len(ids) != 1 {
		t.Fatalf("edit did not replace the prior record: %v", ids)
	}
	if facts := r.loadMemo("gourmet", s.Settings().Location()); len(facts.Facts) != 0 {
		t.Fatalf("keeper did not invalidate the old fact: %+v", facts)
	}
}

func TestCheckInTidiesNewRecords(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", MemoPath, memoText(memo{Records: []record{{At: time.Now().Add(-time.Hour), ID: "e_a", Summary: "散歩が好き"}}}, time.UTC)); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Now().UTC(), "gourmet")
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	if len(f.summaries) != 1 {
		t.Fatalf("check-in rewrote summaries %d times, want 1", len(f.summaries))
	}
}
