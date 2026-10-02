package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// A general research answer keeps only a record: what the user asked, in a
// sentence. The research itself is not kept, and the keeper runs once the
// answer is out.
func TestResearchAnswerAndPhasesAreSeparateAndGeneralResearchKeepsOnlyARecord(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	var phases []string
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		// The answer waits for the schedule and action phases.
		if len(remarks(t, s)) != 0 {
			t.Error("answer published before phases completed")
		}
		switch {
		case offered["finish_read"]:
			phases = append(phases, "read")
			if strings.Contains(system, "keep_memory") || offered["keep_memory"] {
				t.Error("research was asked to write memory")
			}
			return "read_done"
		case offered["submit_answer"]:
			phases = append(phases, "answer")
			if offered["read"] || offered["keep_memory"] {
				t.Error("answer phase mixes responsibilities")
			}
			return `{"text":"一般的な調査の詳しい回答。保存は不要。","sources":[]}`
		case offered["submit_schedule"]:
			phases = append(phases, "schedule")
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		t.Fatalf("unexpected phase: %v", offered)
		return ""
	}
	f.keep = func(system, user string) string {
		phases = append(phases, "keep")
		if len(remarks(t, s)) != 1 {
			t.Error("the memory was kept before the answer was out")
		}
		if !strings.Contains(user, "一般的な調査の詳しい回答") || !strings.Contains(system, "調べた事実・候補一覧・解説は事実にしない") {
			t.Error("the keeper lacks the finished answer or its selection policy")
		}
		return `{"summary":"一般的な食文化について調べてと聞いた","tags":["食文化"],"facts":[]}`
	}
	r.react(post(t, s, "一般的な食文化について調べて").ID)
	if strings.Join(phases, ",") != "read,answer,schedule,keep" {
		t.Fatal(phases)
	}
	notes := s.Memory("gourmet").Notes
	if len(notes) != 1 || !isRecordsPath(notes[0].Path) || strings.Contains(notes[0].Text, "一般的な調査の詳しい回答") {
		t.Fatalf("notes = %+v", notes)
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

func createSchedule(at string) string {
	raw, _ := json.Marshal(map[string]any{"consistent": true, "reason": "約束した時刻に置く", "operations": []any{map[string]any{
		"type": "create", "at": at, "daily": false, "user_requested": true, "note": "通知する", "id": "",
	}}})
	return string(raw)
}

func TestAnswerWaitsForReviewedScheduleAndExecution(t *testing.T) {
	for _, mode := range []string{"success", "review-fail", "invalid-time"} {
		t.Run(mode, func(t *testing.T) {
			r, s, f := setup(t, func(system, user string) string { return `{"deliver":["routine"]}` }, "routine")
			at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
			if mode == "invalid-time" {
				at = "2時間後"
			}
			script, calls := scheduleTurn(func(int) string { return createSchedule(at) })
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				if len(remarks(t, s)) != 0 {
					t.Fatal("answer published before the plan ran")
				}
				return script(system, user, offered)
			}
			f.review = func(user string) string {
				if !strings.Contains(user, "指定の時刻に知らせるね") || !strings.Contains(user, `"schedules"`) || !strings.Contains(user, at) || !strings.Contains(user, `"user_requested": true`) {
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
				if len(schedules) != 1 || !schedules[0].UserRequested || len(said) != 1 || said[0].Text != "指定の時刻に知らせるね。" {
					t.Fatalf("schedules=%v, remarks=%v", schedules, said)
				}
			case "review-fail":
				// Only the blamed phase is made again; the answer stays as it was.
				if calls["schedule"] != maxDrafts || calls["answer"] != 1 {
					t.Fatalf("regenerated phases: %v", calls)
				}
				fallthrough
			default:
				if len(said) != 0 || len(schedules) != 0 {
					t.Fatalf("an unrun plan leaked its promise or schedule: %v / %v", said, schedules)
				}
			}
			if mode == "invalid-time" && len(f.reviews) != 0 {
				t.Fatal("a plan with an unreadable time reached the review")
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
			return createSchedule(wrong)
		}
		return createSchedule(right)
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
	if calls["answer"] != 1 || calls["schedule"] != 2 || len(f.reviews) != 2 || len(f.keeps) != 1 {
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
		return ""
	}
	f.keep = func(string, string) string {
		return `{"summary":"A店に行って良かったと言った","tags":["A店"],"facts":[{"op":"add","field":"Preference","id":"","ids":[],"text":"本人がA店に行き、気に入った。"}]}`
	}
	r.react(post(t, s, "A店に行ってみた。かなり良かった。").ID)
	note, ok := s.Note("gourmet", "Preference.md")
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
	skill, _ := r.catalogue.Find(context.Background(), "routine")
	set := r.registry(s.Settings()).For([]string{"schedule"}).As("routine")
	result, _ := r.readAll(context.Background(), skill, []ReadStep{{Type: "tool", Tool: "schedule", Args: `{"action":"set","at":"2099-01-01T22:00:00Z"}`}}, set, timeline.Entry{}, time.UTC, map[string]string{}, newLookups(10, 2), map[string]string{}, nil)
	if !strings.Contains(result, "閲覧専用の schedules_list") {
		t.Fatal(result)
	}
	if items, _ := s.Schedules("routine"); len(items) != 0 {
		t.Fatal(items)
	}
	for _, phase := range []string{"schedule", "action"} {
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

// A failing review that names no phase still regenerates the changes, so the
// second review does not see the same workflow again.
func TestAFailingReviewWithoutIssuesRegenerates(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["routine"]}` }, "routine")
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	script, calls := scheduleTurn(func(int) string { return createSchedule(at) })
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

// A field holding the answer's own sentences is a cache of what was
// researched. It is sent back, and the keeper keeps what the user said instead.
func TestAFieldCopyingTheAnswerIsSentBack(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	answer := "トランプ大統領の発言は、米中対立を続けながらも協調の余地を示したものと考えられます。輸出規制の緩和を示す合図だという見方もありますが、現時点では不確実です。"
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"` + answer + `","sources":[]}`
		}
		return `{"operations":[],"consistent":true,"reason":"約束なし"}`
	}
	keeps := 0
	f.keep = func(system, user string) string {
		keeps++
		if keeps == 1 {
			return `{"summary":"発言の意図を聞いた","tags":["政治"],"facts":[{"op":"add","field":"Preference","id":"","ids":[],"text":"- ` + answer + `"}]}`
		}
		if !strings.Contains(user, "Answer の文章をそのまま写しています") {
			t.Error("the keeper was not told why it was sent back")
		}
		return `{"summary":"中国同盟国発言の意図を聞いた","tags":["政治"],"facts":[{"op":"add","field":"Preference","id":"","ids":[],"text":"- 政治の発言の意図をよく聞く。"}]}`
	}
	r.react(post(t, s, "トランプの中国同盟国発言の意図は？").ID)
	if keeps != 2 {
		t.Fatalf("keeper calls = %d", keeps)
	}
	note, ok := s.Note("gourmet", "Preference.md")
	if !ok || strings.Contains(note.Text, "輸出規制の緩和を示す合図") {
		t.Fatalf("the field is missing or holds the answer: %+v", note)
	}
	if said := remarks(t, s); len(said) != 1 {
		t.Fatalf("remarks: %v", said)
	}
}

func TestCopiesAnswerSparesWhatTheUserSaid(t *testing.T) {
	answer := "A店は豚骨が濃厚で、夜は行列ができることが多い店です。近くのB店は魚介系で、昼の定食が安いと評判です。"
	if copiesAnswer(answer, "本人は豚骨が好きで、A店に行きたいと言った。") {
		t.Fatal("a field of what the user said was refused")
	}
	if !copiesAnswer(answer, "# A店\n\n* A店は豚骨が濃厚で、夜は行列ができることが多い店です\n* 近くのB店は魚介系で、昼の定食が安いと評判です") {
		t.Fatal("a reflowed copy of the answer was accepted")
	}
}

// A phase that has spent its reads and asks to read again is no longer
// offered read, so it hands its proposal in and the answer is published.
// Before, the refusals used up the phase's rounds: a live Ultra turn read
// eleven times without ever submitting, and the whole turn - answer included -
// was dropped as "retry limit reached", which the user saw as the persona
// having stayed quiet.
func TestAPhaseOutOfReadsIsMadeToSubmit(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	reads := 0
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"駅から5分の店です。","sources":[]}`
		case offered["submit_schedule"] && offered["read"]:
			reads++
			return `read:{"steps":[{"type":"records","tool":"","args":"{}"}]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		t.Fatalf("unexpected phase: %v", offered)
		return ""
	}
	r.react(post(t, s, "ラーメン屋を教えて").ID)

	if reads != maxReads {
		t.Fatalf("the schedule phase read %d times, want %d before being made to submit", reads, maxReads)
	}
	if got := remarks(t, s); len(got) != 1 || got[0].Text != "駅から5分の店です。" {
		t.Fatalf("the answer was dropped with the phase: %+v", got)
	}
}
