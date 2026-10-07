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

// The answer decides whether the post is worth remembering. Without it the
// keeper's model is not called; with it the keeper keeps facts as before.
func TestAnswerDecisionsSkipUnneededFollowupCalls(t *testing.T) {
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
				want := 0
				if remember {
					want = 1
				}
				if calls != 1 || len(f.keeps) != want || len(f.reviews) != 0 {
					t.Fatalf("answer=%d keeper=%d (want %d) review=%d", calls, len(f.keeps), want, len(f.reviews))
				}
			})
		}
	}
}

// A check-in handed records due for review keeps them through the keeper even
// when the answer says the check-in itself is not worth remembering, or the
// same review comes back the next day.
func TestCheckInReviewRunsKeeperWhateverRememberSays(t *testing.T) {
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

// An edited post that is no longer worth remembering leaves nothing of what
// was kept of it before, without asking the keeper's model.
func TestEditNotWorthRememberingDropsWhatWasKept(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	remember := true
	f.phaseReply = func(_, _ string, offered map[string]bool) string {
		if offered[submitAnswer] {
			raw, _ := json.Marshal(map[string]any{"text": "", "sources": []string{}, "remember": remember, "schedule": false, "unrelated": false})
			return string(raw)
		}
		return `{"operations":[],"consistent":true,"reason":"なし"}`
	}
	f.keep = func(string, string) string {
		return `{"summary":"A店が気に入った","tags":["A店"],"expires":"","review":"","facts":[{"op":"add","field":"Preference","id":"","ids":[],"text":"本人はA店が気に入った。"}]}`
	}
	entry := post(t, s, "A店が気に入った")
	r.react(entry.ID)
	if ids := recordIDs(r, "gourmet"); len(ids) != 1 || len(f.keeps) != 1 {
		t.Fatalf("records=%v keeps=%d", ids, len(f.keeps))
	}
	if note, ok := s.Note("gourmet", "Preference.md"); !ok || !strings.Contains(note.Text, entry.ID) {
		t.Fatalf("fact not kept from the post: %+v", note)
	}

	remember = false
	entry.Text = "テスト"
	if err := s.UpdateEntry(entry); err != nil {
		t.Fatal(err)
	}
	client, settings := r.clientOf()
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	r.run(context.Background(), skill, []timeline.Entry{entry}, entry, false, false, editedInstructions, false, client, settings, r.registry(settings))
	if len(f.keeps) != 1 {
		t.Fatalf("keeper model called for an edit not worth remembering: %d", len(f.keeps))
	}
	if ids := recordIDs(r, "gourmet"); len(ids) != 0 {
		t.Fatalf("record of the earlier text survived: %v", ids)
	}
	if note, ok := s.Note("gourmet", "Preference.md"); ok && strings.Contains(note.Text, entry.ID) {
		t.Fatalf("fact resting on the earlier text survived:\n%s", note.Text)
	}
}

func TestCheckInRewritesFieldSummaries(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "Preference.md", factsText("Preference", fieldFacts{Current: []fact{{ID: "f_a", Text: "散歩が好き", Since: time.Now()}}}, time.UTC)); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Now().UTC(), "gourmet")
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	if len(f.summaries) != 1 {
		t.Fatalf("check-in rewrote summaries %d times, want 1", len(f.summaries))
	}
}
