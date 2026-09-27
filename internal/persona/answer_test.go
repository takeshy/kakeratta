package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestResearchAnswerAndPhasesAreSeparateAndGeneralResearchCreatesNoNotes(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	var phases []string
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		if len(remarks(t, s)) != 0 {
			t.Error("answer published before phases completed")
		}
		switch {
		case offered["finish_read"]:
			phases = append(phases, "read")
			if strings.Contains(system, "Open Knowledge Format") || offered["submit_note"] {
				t.Error("research was asked to write memory")
			}
			return "read_done"
		case offered["submit_answer"]:
			phases = append(phases, "answer")
			if offered["read"] || offered["submit_note"] || strings.Contains(system, "Open Knowledge Format") {
				t.Error("answer phase mixes responsibilities")
			}
			return `{"text":"一般的な調査の詳しい回答。保存は不要。","sources":[]}`
		case offered["submit_schedule"]:
			phases = append(phases, "schedule")
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		case offered["submit_note"]:
			phases = append(phases, "note")
			if !strings.Contains(user, "一般的な調査の詳しい回答") || !strings.Contains(system, "回答で使っただけの一般知識") {
				t.Error("note phase lacks the finished answer or its selection policy")
			}
			return `{"operations":[]}`
		}
		t.Fatalf("unexpected phase: %v", offered)
		return ""
	}
	r.react(post(t, s, "一般的な食文化について調べて").ID)
	if strings.Join(phases, ",") != "read,answer,schedule,note" {
		t.Fatal(phases)
	}
	if len(s.Memory("gourmet").Notes) != 0 {
		t.Fatal("research answer created a note")
	}
	if len(f.reviews) != 0 {
		t.Fatalf("a plain answer was reviewed %d times", len(f.reviews))
	}
	if got := remarks(t, s); len(got) != 1 || got[0].Text != "一般的な調査の詳しい回答。保存は不要。" {
		t.Fatal(got)
	}
}

// scheduleTurn scripts a turn that promises a reminder, placing a schedule for
// it: the operations the schedule phase hands in come from ops.
func scheduleTurn(ops func(call int) string) (func(system, user string, offered map[string]bool) string, map[string]int) {
	calls := map[string]int{}
	return func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			calls["read"]++
			return "read_done"
		case offered["submit_answer"]:
			calls["answer"]++
			return `{"text":"指定の時刻に知らせるね。","sources":[]}`
		case offered["submit_schedule"]:
			calls["schedule"]++
			return ops(calls["schedule"])
		case offered["submit_action"]:
			calls["action"]++
			return `{"operations":[],"consistent":true,"reason":"なし"}`
		}
		calls["note"]++
		return `{"operations":[]}`
	}, calls
}

func createSchedule(at, path string) string {
	raw, _ := json.Marshal(map[string]any{"consistent": true, "reason": "約束した時刻に置く", "operations": []any{map[string]any{
		"type": "create", "at": at, "daily": false, "path": path, "text": "---\ntype: Errand\ntitle: 通知\n---\n通知の依頼", "note": "通知する", "id": "",
	}}})
	return string(raw)
}

func TestAnswerWaitsForReviewedScheduleAndExecution(t *testing.T) {
	for _, mode := range []string{"success", "review-fail", "invalid-path"} {
		t.Run(mode, func(t *testing.T) {
			r, s, f := setup(t, func(system, user string) string { return `{"deliver":["routine"]}` }, "routine")
			at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
			path := "errands/reminder.md"
			if mode == "invalid-path" {
				path = "../outside.md"
			}
			script, calls := scheduleTurn(func(int) string { return createSchedule(at, path) })
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				if len(remarks(t, s)) != 0 {
					t.Fatal("answer published before the plan ran")
				}
				return script(system, user, offered)
			}
			f.review = func(user string) string {
				if !strings.Contains(user, "指定の時刻に知らせるね") || !strings.Contains(user, `"schedules"`) || !strings.Contains(user, at) {
					t.Error("review did not see the answer and the schedule together")
				}
				if mode == "review-fail" {
					return `{"verdict":"fail","summary":"時刻が不一致","issues":[{"phase":"schedule","severity":"high","description":"指定時刻と予定が違う"}]}`
				}
				return `{"verdict":"pass","summary":"ok","issues":[]}`
			}
			r.react(post(t, s, "2時間後に知らせて").ID)
			schedules, _ := s.Schedules("routine")
			said := remarks(t, s)
			switch mode {
			case "success":
				if len(schedules) != 1 || len(said) != 1 || said[0].Text != "指定の時刻に知らせるね。" {
					t.Fatalf("schedules=%v, remarks=%v", schedules, said)
				}
				if _, ok := s.Note("routine", path); !ok {
					t.Fatal("schedule has no backing note")
				}
			case "review-fail":
				// Only the blamed phase is made again; the answer and the notes
				// stay as they were.
				if calls["schedule"] != maxDrafts || calls["answer"] != 1 || calls["note"] != 1 {
					t.Fatalf("regenerated phases: %v", calls)
				}
				fallthrough
			default:
				if len(said) != 0 || len(schedules) != 0 {
					t.Fatalf("an unrun plan leaked its promise or schedule: %v / %v", said, schedules)
				}
			}
			if mode == "invalid-path" && len(f.reviews) != 0 {
				t.Fatal("a plan with an unsafe path reached the review")
			}
		})
	}
}

func TestPromiseWithoutScheduleIsSentBackAndNeverPublished(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["routine"]}` }, "routine")
	script, calls := scheduleTurn(func(int) string {
		return `{"operations":[],"consistent":false,"reason":"知らせると言ったが予定がない"}`
	})
	f.phaseReply = script
	r.react(post(t, s, "2時間後に知らせて").ID)
	if calls["schedule"] < 2 {
		t.Fatalf("an inconsistent schedule phase was not asked again: %v", calls)
	}
	if said := remarks(t, s); len(said) != 0 {
		t.Fatalf("a promise with nothing behind it was published: %v", said)
	}
	if len(f.reviews) != 0 {
		t.Fatal("an empty plan was sent to the review")
	}
}

func TestScheduleFixedAfterReviewKeepsAnswerAndNotes(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["routine"]}` }, "routine")
	wrong := time.Now().Add(5 * time.Hour).Format(time.RFC3339)
	right := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	script, calls := scheduleTurn(func(call int) string {
		if call == 1 {
			return createSchedule(wrong, "errands/reminder.md")
		}
		return createSchedule(right, "errands/reminder.md")
	})
	f.phaseReply = script
	f.review = func(user string) string {
		if strings.Contains(user, wrong) {
			return `{"verdict":"fail","summary":"時刻が違う","issues":[{"phase":"schedule","severity":"high","description":"2時間後ではない"}]}`
		}
		return `{"verdict":"pass","summary":"ok","issues":[]}`
	}
	r.react(post(t, s, "2時間後に知らせて").ID)
	schedules, _ := s.Schedules("routine")
	want, _ := time.Parse(time.RFC3339, right)
	if len(schedules) != 1 || !schedules[0].At.Equal(want) {
		t.Fatalf("schedules: %v", schedules)
	}
	if calls["answer"] != 1 || calls["note"] != 1 || calls["schedule"] != 2 || len(f.reviews) != 2 {
		t.Fatalf("calls=%v reviews=%d", calls, len(f.reviews))
	}
	if said := remarks(t, s); len(said) != 1 {
		t.Fatalf("remarks: %v", said)
	}
}

func TestUserFactsCanBeRememberedWithoutReviewOrCopyingTheAnswer(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"A店に行けたんだね。詳しい返信は記憶へ複写しません。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		case offered["submit_action"]:
			return `{"operations":[],"consistent":true,"reason":"なし"}`
		}
		return `{"operations":[{"type":"write","path":"tastes/a.md","text":"---\ntype: Preference\ntitle: A店が好き\n---\n本人がA店に行き、気に入った。"}]}`
	}
	r.react(post(t, s, "A店に行ってみた。かなり良かった。").ID)
	note, ok := s.Note("gourmet", "tastes/a.md")
	if !ok || strings.Contains(note.Text, "詳しい返信") {
		t.Fatal(note)
	}
	if len(f.reviews) != 0 {
		t.Fatal("a note update was reviewed")
	}
	if len(remarks(t, s)) != 1 {
		t.Fatal("answer missing")
	}
}

func TestReadCannotExecuteActions(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	skill, _ := r.catalogue.Find("routine")
	set := r.registry(s.Settings()).For([]string{"schedule"}).As("routine")
	result, _ := r.readAll(context.Background(), skill, []ReadStep{{Type: "tool", Tool: "schedule", Args: `{"action":"set","at":"2099-01-01T22:00:00Z"}`}}, set, timeline.Entry{}, newProvenance(), map[string]string{}, newLookups(10, 2), map[string]string{})
	if !strings.Contains(result, "予定は読むものではありません") {
		t.Fatal(result)
	}
	if items, _ := s.Schedules("routine"); len(items) != 0 {
		t.Fatal(items)
	}
	for _, phase := range []string{"schedule", "action", "note"} {
		raw, _ := json.Marshal(phaseTool(phase, set).Parameters)
		if strings.Contains(string(raw), "speak") || strings.Contains(string(raw), "sources") {
			t.Fatalf("the answer leaked into the %s phase schema", phase)
		}
	}
	raw, _ := json.Marshal(answerTool.Parameters)
	if strings.Contains(string(raw), "operations") || strings.Contains(string(raw), "steps") {
		t.Fatal("state changes leaked into the answer schema")
	}
}

// A note on a schedule's own path shows only when the phases are put
// together. The note phase that brought it is sent back to fix it, rather
// than the whole turn failing with nothing run.
func TestACrossPhaseConflictIsSentBackToThePhaseThatMadeIt(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["routine"]}` }, "routine")
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	script, calls := scheduleTurn(func(int) string { return createSchedule(at, "errands/reminder.md") })
	notes := 0
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		if !offered["submit_note"] {
			return script(system, user, offered)
		}
		notes++
		if notes == 1 {
			return `{"operations":[{"type":"write","path":"errands/reminder.md","text":"---\ntype: Errand\ntitle: 通知\n---\n重複"}]}`
		}
		if !strings.Contains(user, "背景メモ") {
			t.Error("the note phase was not told what conflicted")
		}
		return `{"operations":[]}`
	}
	r.react(post(t, s, "2時間後に知らせて").ID)
	if notes != 2 || calls["schedule"] != 1 {
		t.Fatalf("notes=%d calls=%v", notes, calls)
	}
	if said := remarks(t, s); len(said) != 1 {
		t.Fatalf("remarks: %v", said)
	}
	if schedules, _ := s.Schedules("routine"); len(schedules) != 1 {
		t.Fatalf("schedules: %v", schedules)
	}
}

// Research may spend every read. A phase that has to rewrite an existing
// note still reads it on its own count, and the turn goes through.
func TestPhasesReadApartFromResearch(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{"tastes/a.md": "---\ntype: Preference\ntitle: A店\n---\n前から好き"})
	researched, notes := 0, 0
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			researched++
			return `{"steps":[{"type":"notes_list","paths":[],"tool":"","args":"{}"}]}`
		case offered["submit_answer"]:
			return `{"text":"A店、また行ったんだね。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		notes++
		if notes == 1 {
			return `read:{"steps":[{"type":"notes_read","paths":["tastes/a.md"],"tool":"","args":"{}"}]}`
		}
		return `{"operations":[{"type":"write","path":"tastes/a.md","text":"---\ntype: Preference\ntitle: A店\n---\n前から好き。また行った。"}]}`
	}
	r.react(post(t, s, "A店にまた行った").ID)
	if researched != maxReads {
		t.Fatalf("research reads = %d", researched)
	}
	if note, ok := s.Note("gourmet", "tastes/a.md"); !ok || !strings.Contains(note.Text, "また行った") {
		t.Fatalf("note = %+v", note)
	}
	if said := remarks(t, s); len(said) != 1 {
		t.Fatalf("remarks: %v", said)
	}
}

// A failing review that names no phase still regenerates the changes, so the
// second review does not see the same workflow again.
func TestAFailingReviewWithoutIssuesRegenerates(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["routine"]}` }, "routine")
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	script, calls := scheduleTurn(func(int) string { return createSchedule(at, "errands/reminder.md") })
	f.phaseReply = script
	reviews := 0
	f.review = func(string) string {
		reviews++
		if reviews == 1 {
			return `{"verdict":"fail","summary":"どこか違う","issues":[]}`
		}
		return `{"verdict":"pass","summary":"ok","issues":[]}`
	}
	r.react(post(t, s, "2時間後に知らせて").ID)
	if calls["schedule"] != 2 || reviews != 2 {
		t.Fatalf("calls=%v reviews=%d", calls, reviews)
	}
	if said := remarks(t, s); len(said) != 1 {
		t.Fatalf("remarks: %v", said)
	}
}
