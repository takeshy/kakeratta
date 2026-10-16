package persona

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestMemorySelectionReadsOneFieldAfterChoosingAndKeepsStablePrefix(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	skill.Instructions = "ANSWER_WRITING_INSTRUCTIONS"
	skill.Tone = "ANSWER_TONE"
	for field, body := range map[string]string{
		"Preference": factsText("Preference", fieldFacts{Summary: "PREFERENCE_SUMMARY", Current: []fact{{ID: "f_p", Text: "辛いものが好き"}}}, time.UTC),
		"Shop":       factsText("Shop", fieldFacts{Summary: "SHOP_SUMMARY", Current: []fact{{ID: "f_s", Text: "UNRELATED_SHOP_FACT"}}}, time.UTC),
	} {
		if err := s.WriteNote(skill.ID, fieldPath(field), body); err != nil {
			t.Fatal(err)
		}
	}
	old := post(t, s, "RECENT_RECORD_ONLY")
	writeRecord(t, s, skill.ID, old, old.ID)
	f.selectMemory = func(system, user string) string {
		for _, excluded := range []string{"PREFERENCE_SUMMARY", "SHOP_SUMMARY", "UNRELATED_SHOP_FACT", "f_p", "f_s", "RECENT_RECORD_ONLY", "PUBLISHED_ANSWER", "ANSWER_WRITING_INSTRUCTIONS", "ANSWER_TONE"} {
			if strings.Contains(system+user, excluded) {
				t.Errorf("initial selection includes %s", excluded)
			}
		}
		return "Preference"
	}
	f.keep = func(system, user string) string {
		for _, required := range []string{"PREFERENCE_SUMMARY", "f_p", "辛いものが好き"} {
			if !strings.Contains(user, required) {
				t.Errorf("selected memory missing %s", required)
			}
		}
		for _, excluded := range []string{"SHOP_SUMMARY", "UNRELATED_SHOP_FACT", "RECENT_RECORD_ONLY", "PUBLISHED_ANSWER"} {
			if strings.Contains(user, excluded) {
				t.Errorf("update includes %s", excluded)
			}
		}
		return `{"summary":"辛さの好みを伝えた","tags":["好み"],"facts":[]}`
	}
	client, settings := r.clientOf()
	var usage []llm.CallRecord
	client.Observe = func(record llm.CallRecord) { usage = append(usage, record) }
	for _, text := range []string{"辛いものが好き", "辛いものは好きなままです"} {
		entry := post(t, s, text)
		_, err := r.keepNotes(context.Background(), client, settings, keeperTurn{skill: skill, thread: []timeline.Entry{entry}, trigger: entry, answer: "PUBLISHED_ANSWER", here: time.UTC})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(f.selections) != 2 || len(f.keeps) != 2 || len(usage) != 4 {
		t.Fatalf("selection=%d keeper=%d usage=%d", len(f.selections), len(f.keeps), len(usage))
	}
	a, b := strings.Split(f.selections[0], "\n----\n")[0], strings.Split(f.selections[1], "\n----\n")[0]
	if a != b {
		t.Fatal("selection prefix changes with the post")
	}
	for i, record := range usage {
		want := "memory_select"
		if i%2 == 1 {
			want = "memory_update"
		}
		if record.Purpose != want || record.RetryCount != 0 {
			t.Fatalf("usage=%+v", usage)
		}
	}
}

func TestMemorySelectionNoneFinishesWithoutUpdateOrRetryJob(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = answeringReply("どういたしまして")
	f.selectMemory = func(string, string) string { return "" }
	r.react(post(t, s, "ありがとう").ID)
	if len(f.selections) != 1 || len(f.keeps) != 0 {
		t.Fatalf("selection=%d updates=%d", len(f.selections), len(f.keeps))
	}
	if jobs, _ := s.NoteJobs(); len(jobs) != 0 {
		t.Fatalf("skipped memory has a retry job: %+v", jobs)
	}
	if paths, _ := s.NotePaths("gourmet"); len(paths) != 0 {
		t.Fatalf("skipped memory wrote notes: %v", paths)
	}
}

func TestMemorySelectionFailureRetriesWithoutExpandingContext(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = answeringReply("いいね")
	f.selectMemory = func(string, string) string { return "UnknownField" }
	if err := s.WriteNote("gourmet", "Shop.md", factsFile("Shop", [2]string{"SECRET_SHOP_CONTEXT", ""})); err != nil {
		t.Fatal(err)
	}
	r.react(post(t, s, "A店に行きたい").ID)
	if len(f.selections) != maxKeeperAttempts || len(f.keeps) != 0 {
		t.Fatalf("selection=%d updates=%d", len(f.selections), len(f.keeps))
	}
	for _, prompt := range f.selections {
		if strings.Contains(prompt, "SECRET_SHOP_CONTEXT") {
			t.Fatal("selection retry expanded into the old full-memory prompt")
		}
	}
	if jobs, _ := s.NoteJobs(); len(jobs) != 1 {
		t.Fatalf("failed selection lost durable retry: %+v", jobs)
	}
}

func TestSelectedMemoryRejectsCrossFieldWrites(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = answeringReply("いいね")
	f.selectMemory = func(string, string) string { return "Preference" }
	shop := factsText("Shop", fieldFacts{Current: []fact{{ID: "f_shop", Text: "A店に行きたい"}}}, time.UTC)
	if err := s.WriteNote("gourmet", "Shop.md", shop); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.keep = func(_, user string) string {
		calls++
		if calls == 1 {
			return `{"summary":"辛いものが好き","tags":["好み"],"facts":[{"op":"invalidate","id":"f_shop"},{"op":"add","field":"Shop","text":"B店に行きたい"}]}`
		}
		if !strings.Contains(user, "f_shop はいまの事実にありません") || !strings.Contains(user, "分野「Shop」はありません") {
			t.Errorf("cross-field write was not rejected: %s", user)
		}
		return `{"summary":"辛いものが好き","tags":["好み"],"facts":[{"op":"add","field":"Preference","text":"辛いものが好き"}]}`
	}
	r.react(post(t, s, "辛いものが好き").ID)
	if calls != 2 || len(f.selections) != 1 {
		t.Fatalf("updates=%d selection=%d", calls, len(f.selections))
	}
	if note, _ := s.Note("gourmet", "Shop.md"); note.Text != shop {
		t.Fatal("unselected field was changed")
	}
	if note, _ := s.Note("gourmet", "Preference.md"); !strings.Contains(note.Text, "辛いものが好き") {
		t.Fatal("selected field was not written")
	}
}

func TestMemorySelectionContextIsBoundedAndIncludesAttachments(t *testing.T) {
	k := keeperTurn{here: time.UTC}
	for i := 0; i < 10; i++ {
		k.thread = append(k.thread, timeline.Entry{ID: string(rune('a' + i)), Speaker: "user", Text: strings.Repeat("前", 1000)})
	}
	k.thread[0].Text = "OLDER_CONTEXT_MUST_NOT_APPEAR"
	k.trigger = timeline.Entry{ID: "current", Speaker: "user", Text: "HEAD" + strings.Repeat("中", 10000) + "TAIL"}
	k.thread = append(k.thread, k.trigger)
	text := memorySelectionContext(k)
	if len([]rune(text)) > 3100 || strings.Contains(text, "OLDER_CONTEXT_MUST_NOT_APPEAR") || !strings.Contains(text, "HEAD") || !strings.Contains(text, "TAIL") {
		t.Fatalf("classification excerpt is not bounded: %d runes", len([]rune(text)))
	}
	k.trigger = timeline.Entry{ID: "image", Speaker: "user", Text: "これ覚えて", Media: &timeline.Media{Describe: "白いワインのラベル"}}
	if text = memorySelectionContext(k); !strings.Contains(text, "白いワインのラベル") {
		t.Fatalf("attachment interpretation missing: %s", text)
	}
}
