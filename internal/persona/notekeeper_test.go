package persona

import (
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func note(path, title, description string) store.Note {
	return store.Note{Path: path, Text: "---\ntype: Question\ntitle: " + title + "\ndescription: " + description + "\ntags: [a]\n---\n本文"}
}

// A note the keeper cannot write costs the note, never the answer. This is the
// failure that started it: the note phase of a turn could not finish, and the
// user saw a persona that had stayed quiet although its answer was good.
func TestAKeeperThatFailsCostsOnlyTheNotes(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"駅から5分の店です。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		// The keeper only ever hands in something the app cannot run.
		return `{"operations":[{"type":"write","path":"../outside.md","text":"x"}]}`
	}
	r.react(post(t, s, "ラーメン屋を教えて").ID)

	if got := remarks(t, s); len(got) != 1 || got[0].Text != "駅から5分の店です。" {
		t.Fatalf("the answer was lost with the notes: %+v", got)
	}
	for _, kept := range s.Memory("gourmet").Notes {
		if kept.Path != "index.md" {
			t.Fatalf("a note the app refused was written: %+v", kept)
		}
	}
}

// The keeper is handed the notes the turn had already opened, without having
// to choose them, and cannot read on its own.
func TestTheKeeperIsHandedWhatTheTurnRead(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{"tastes/a.md": "---\ntype: Preference\ntitle: A店\n---\n前から好き"})
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return `{"steps":[{"type":"notes_read","paths":["tastes/a.md"],"tool":"","args":"{}"}]}`
		case offered["submit_answer"]:
			return `{"text":"A店、また行ったんだね。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		if !strings.Contains(user, "=== tastes/a.md ===") || !strings.Contains(user, "前から好き") {
			t.Errorf("the keeper was not handed the note the turn read:\n%s", user)
		}
		return `{"operations":[{"type":"write","path":"tastes/a.md","text":"---\ntype: Preference\ntitle: A店\n---\n前から好き。また行った。"},{"type":"write","path":"index.md","text":"# 好み\n\n* [A店](/tastes/a.md) - 前から好き"}]}`
	}
	r.react(post(t, s, "A店にまた行った").ID)
	if got, ok := s.Note("gourmet", "tastes/a.md"); !ok || !strings.Contains(got.Text, "また行った") {
		t.Fatalf("note = %+v", got)
	}
}

// Two notes carrying one title under different directories are the same mistake
// as two in one directory; the keeper is shown them, and may merge them.
func TestTheKeeperMergesNotesOfOneTitleAcrossDirectories(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{
		"question/deposit.md":  "---\ntype: Preference\ntitle: デポジットの必要性\n---\n一つ目",
		"questions/deposit.md": "---\ntype: Preference\ntitle: デポジットの必要性\n---\n二つ目",
	})
	f.choose = func(user string) []string {
		if !strings.Contains(user, "同じ・よく似た題名") {
			t.Error("the keeper was not shown the duplicate")
		}
		return []string{"question/deposit.md", "questions/deposit.md"}
	}
	calls := 0
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"了解です。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		calls++
		if calls == 1 {
			// Writing a third path for the same title is refused.
			return `{"operations":[{"type":"write","path":"misc/deposit.md","text":"---\ntype: Preference\ntitle: デポジットの必要性\n---\n三つ目"},{"type":"write","path":"index.md","text":"# 質問\n\n* [デポジットの必要性](/misc/deposit.md) - x"}]}`
		}
		return `{"operations":[{"type":"write","path":"questions/deposit.md","text":"---\ntype: Preference\ntitle: デポジットの必要性\n---\n一つ目と二つ目をまとめた"},{"type":"forget","path":"question/deposit.md","text":""},{"type":"write","path":"index.md","text":"# 質問\n\n* [デポジットの必要性](/questions/deposit.md) - まとめた"}]}`
	}
	r.react(post(t, s, "デポジットは要らない気がする").ID)

	if calls != 2 {
		t.Fatalf("keeper proposals = %d, want the first refused and the merge accepted", calls)
	}
	if _, ok := s.Note("gourmet", "question/deposit.md"); ok {
		t.Fatal("the merged note was left behind")
	}
	if merged, ok := s.Note("gourmet", "questions/deposit.md"); !ok || !strings.Contains(merged.Text, "まとめた") {
		t.Fatalf("merged = %+v", merged)
	}
	if _, ok := s.Note("gourmet", "misc/deposit.md"); ok {
		t.Fatal("a third note for the same title was written")
	}
}

// A silent turn still gets its notes kept, and a schedule wake does not run the
// keeper at all.
func TestOnlyTurnsThatLeaveSomethingToKeepRunTheKeeper(t *testing.T) {
	post := timeline.Entry{ID: "p"}
	cases := []struct {
		name         string
		trigger      bool
		instructions string
		want         bool
	}{
		{"a post of the user's", true, "", true},
		{"a deletion tidy-up", false, deletedInstructions, true},
		{"a note that expired", false, expiredInstructions, true},
		{"the daily check-in", false, dailyInstructions, true},
		{"a review", false, reviewInstructions, true},
		{"a schedule waking", false, scheduleInstructions, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := post
			if !tc.trigger {
				entry.ID = ""
			}
			if got := keepsNotes(entry, tc.instructions); got != tc.want {
				t.Fatalf("keepsNotes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRepairedIndexKeepsTheGroupingAndMakesItComplete(t *testing.T) {
	notes := []store.Note{
		note("questions/a.md", "Aについて", "Aの話"),
		note("questions/b.md", "Bについて", "Bの話"),
		note("tastes/c.md", "C", ""),
	}
	index := "# 質問\n\n* [Aについて](/questions/a.md) - Aの話\n* [消えたもの](/questions/gone.md) - もうない\n\n# 好み\n\n"
	fixed := repairedIndex(index, notes)

	if !strings.Contains(fixed, "# 質問\n\n* [Aについて](/questions/a.md) - Aの話") || !strings.Contains(fixed, "# 好み") {
		t.Fatalf("the persona's grouping was not kept:\n%s", fixed)
	}
	if strings.Contains(fixed, "gone.md") {
		t.Fatalf("a link to a note that does not exist was kept:\n%s", fixed)
	}
	for _, path := range []string{"/questions/b.md", "/tastes/c.md"} {
		if !strings.Contains(fixed, path) {
			t.Fatalf("%s is not linked:\n%s", path, fixed)
		}
	}
	if !strings.Contains(fixed, "* [Bについて](/questions/b.md) - Bの話") {
		t.Fatalf("a note was added without its title and description:\n%s", fixed)
	}
	if strings.Count(fixed, unsortedHeading) != 1 {
		t.Fatalf("the unsorted heading should appear once:\n%s", fixed)
	}
	if again := repairedIndex(fixed, notes); again != fixed {
		t.Fatalf("repairing is not stable:\n%s\n---\n%s", fixed, again)
	}
}

func TestRepairedIndexOfAnEmptyMemoryStaysEmpty(t *testing.T) {
	if got := repairedIndex("", nil); got != "" {
		t.Fatalf("index = %q", got)
	}
}

func TestRepairedIndexRemovesDuplicateNoteLines(t *testing.T) {
	notes := []store.Note{note("questions/a.md", "A", "説明")}
	index := "# 質問\n\n* [A](/questions/a.md) - 説明\n\n# 未整理\n\n* [A](/questions/a.md)\n"
	fixed := repairedIndex(index, notes)
	if strings.Count(fixed, "](/questions/a.md)") != 1 {
		t.Fatalf("note is listed more than once:\n%s", fixed)
	}
}

func TestTidyHintsFindUnlinkedNotesAndTitlesSharedAcrossDirectories(t *testing.T) {
	notes := []store.Note{
		note("question/x.md", "デポジットの必要性", "a"),
		note("questions/x.md", "デポジットの必要性", "b"),
		note("tastes/c.md", "全く別の話", "c"),
	}
	unlinked, groups := tidyHints(notes, "# 質問\n\n* [別の話](/tastes/c.md)\n")
	if len(unlinked) != 2 || len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("unlinked=%v groups=%v", unlinked, groups)
	}
}

// A note written with a section is listed under that heading, moved there from
// any other; a new heading is made; without a section a listed note's line is
// brought up to date where it is. The persona's other lines stay as they were.
func TestPlaceLineListsANoteUnderItsSection(t *testing.T) {
	index := "# 質問\n\n* [古い](/questions/old.md) - d\n\n# 好み\n\n* [A](/tastes/a.md) - a\n"
	moved := note("tastes/a.md", "A店", "よく行く")

	got := placeNoteInIndex(index, "質問", moved)
	want := "# 質問\n\n* [古い](/questions/old.md) - d\n* [A店](/tastes/a.md) - よく行く\n\n# 好み\n"
	if got != want {
		t.Fatalf("moved:\n%s\nwant:\n%s", got, want)
	}
	got = placeNoteInIndex(index, "店", note("shops/b.md", "B店", "駅前"))
	if !strings.HasSuffix(got, "# 好み\n\n* [A](/tastes/a.md) - a\n\n# 店\n\n* [B店](/shops/b.md) - 駅前\n") {
		t.Fatalf("new heading:\n%s", got)
	}
	got = placeNoteInIndex("# 好み\n", "好み", note("tastes/c.md", "C", "c"))
	if got != "# 好み\n\n* [C](/tastes/c.md) - c\n" {
		t.Fatalf("under an empty heading:\n%q", got)
	}
	got = placeNoteInIndex(index, "", moved)
	if !strings.Contains(got, "# 好み\n\n* [A店](/tastes/a.md) - よく行く") {
		t.Fatalf("refreshed in place:\n%s", got)
	}
	if got := placeNoteInIndex(index, "", note("new/x.md", "X", "x")); got != index {
		t.Fatalf("an unlisted note without a section was placed:\n%s", got)
	}
	if got := placeNoteInIndex("", "質問", note("q/x.md", "X", "")); got != "# 質問\n\n* [X](/q/x.md)\n" {
		t.Fatalf("into an empty index:\n%q", got)
	}
}

func TestEmptyFrontMatterTitleAndDescriptionStayEmpty(t *testing.T) {
	note := "---\ntitle:\ndescription:\ntags: [ramen]\n---\n本文"
	if got := noteTitle(note); got != "" {
		t.Fatalf("title = %q", got)
	}
	if got := noteDescription(note); got != "" {
		t.Fatalf("description = %q", got)
	}
}

// The keeper lists a note by naming its heading; it does not have to rewrite
// index.md, and what it writes is placed there by the app.
func TestTheKeeperNamesTheHeadingAndTheAppPlacesTheNote(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{
		"tastes/a.md": "---\ntype: Preference\ntitle: A店\n---\n前から好き",
		"index.md":    "# 好み\n\n* [A店](/tastes/a.md)\n",
	})
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"いいね。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		return `{"operations":[{"type":"write","path":"shops/b.md","text":"---\ntype: Shop\ntitle: B店\ndescription: 駅前の店\n---\n行きたい","section":"店"}]}`
	}
	r.react(post(t, s, "B店に行きたい").ID)
	index, _ := s.Note("gourmet", "index.md")
	if !strings.Contains(index.Text, "# 好み\n\n* [A店](/tastes/a.md)") || !strings.Contains(index.Text, "# 店\n\n* [B店](/shops/b.md) - 駅前の店") {
		t.Fatalf("index.md:\n%s", index.Text)
	}
	if strings.Contains(index.Text, unsortedHeading) {
		t.Fatalf("a placed note went to unsorted:\n%s", index.Text)
	}
}

// A tidy-up hands the keeper the notes whose text still links to one that was
// taken away, found by the app: the index does not say which they are.
func TestATidyUpIsHandedTheNotesLinkingToOneGone(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"deliver":[]}` }, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{
		"tastes/a.md": "---\ntype: Preference\ntitle: A\n---\n[B店](/shops/b.md) と比べて好き",
		"tastes/c.md": "---\ntype: Preference\ntitle: C\n---\n関係ない",
		"shops/d.md":  "---\ntype: Shop\ntitle: D\n---\n残った",
	})
	trigger := timeline.Entry{Speaker: "system", Text: "Notes that still cite other posts: shops/d.md. Notes the app deleted because they rested only on it: shops/b.md."}
	got := r.handedNotes("gourmet", trigger, deletedInstructions)
	if strings.Join(got, ",") != "shops/d.md,tastes/a.md" {
		t.Fatalf("handed = %v", got)
	}
}

// The user's reply to a post the persona started on its own - a review
// question - hands the keeper the notes that post was about.
func TestAReplyToThePersonasOwnPostHandsItsNotes(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"deliver":[]}` }, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{"shops/soka.md": "---\ntype: Shop\ntitle: 草加\n---\n草加の店"})
	asked := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: "gourmet", Text: "そろそろ忘れていい？", Notes: []string{"shops/soka.md"}}
	asked.Root = asked.ID
	if err := s.Append(asked); err != nil {
		t.Fatal(err)
	}
	reply := replyTo(t, s, asked, "もういい")
	if got := r.handedNotes("gourmet", reply, ""); len(got) != 1 || got[0] != "shops/soka.md" {
		t.Fatalf("handed = %v", got)
	}
}

// What a proposal is held to in index.md is what it touches: a note it writes
// must name its heading or already be listed. A mess it did not make is not
// held against it.
func TestIndexProblemsAreOnlyAboutWhatTheProposalTouches(t *testing.T) {
	notes := []store.Note{note("questions/old.md", "古い", "d"), note("questions/other.md", "別", "d")}
	index := "# 質問\n\n* [古い](/questions/old.md) - d\n"

	if got := indexProblems(notes, index, nil); len(got) != 0 {
		t.Fatalf("a proposal that changes nothing was asked for an index: %v", got)
	}
	unrelated := []NoteOperation{{Type: "write", Path: "questions/old.md", Text: "---\ntitle: 古い\n---\n更新"}}
	if got := indexProblems(notes, index, unrelated); len(got) != 0 {
		t.Fatalf("an unlinked note it did not touch was held against it: %v", got)
	}
	added := []NoteOperation{{Type: "write", Path: "questions/new.md", Text: "---\ntitle: 新しい\n---\n本文"}}
	if got := indexProblems(notes, index, added); len(got) != 1 || !strings.Contains(got[0], "questions/new.md") {
		t.Fatalf("a note written without a heading was accepted: %v", got)
	}
	placed := []NoteOperation{{Type: "write", Path: "questions/new.md", Text: "---\ntitle: 新しい\n---\n本文", Section: "質問"}}
	if got := indexProblems(notes, index, placed); len(got) != 0 {
		t.Fatalf("problems with a note that names its heading: %v", got)
	}
	linked := append(added, NoteOperation{Type: "write", Path: "index.md", Text: index + "* [新しい](/questions/new.md) - 新\n"})
	if got := indexProblems(notes, index, linked); len(got) != 0 {
		t.Fatalf("problems with a linked note: %v", got)
	}
	// The app takes a forgotten note's line out; the proposal need not.
	forgotten := []NoteOperation{{Type: "forget", Path: "questions/old.md"}}
	if got := indexProblems(notes, index, forgotten); len(got) != 0 {
		t.Fatalf("a forget was asked to rewrite the index: %v", got)
	}
}

// A persona that stays quiet because the post is not its business keeps no
// notes, and the routing sheet records why; one that stays quiet with nothing
// to say still has its notes kept.
func TestAnUnrelatedSilenceKeepsNothingAndIsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		unrelated bool
	}{{"unrelated", true}, {"related", false}} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
			kept := 0
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				switch {
				case offered["finish_read"]:
					return "read_done"
				case offered["submit_answer"]:
					if tc.unrelated {
						return `{"text":"","sources":[],"unrelated":true}`
					}
					return `{"text":"","sources":[],"unrelated":false}`
				case offered["submit_schedule"]:
					return `{"operations":[],"consistent":true,"reason":"約束なし"}`
				}
				kept++
				return `{"operations":[]}`
			}
			entry := post(t, s, "明日は雨らしい")
			r.react(entry.ID)
			record, _ := s.Routing(entry.ID)
			if record == nil {
				t.Fatal("no routing record")
			}
			if tc.unrelated {
				if kept != 0 || !contains(record.Unrelated, "gourmet") {
					t.Fatalf("keeper runs = %d, unrelated = %v", kept, record.Unrelated)
				}
				return
			}
			if kept == 0 || len(record.Unrelated) != 0 {
				t.Fatalf("keeper runs = %d, unrelated = %v", kept, record.Unrelated)
			}
		})
	}
}

// Only a silence can be unrelated: a persona that speaks is taken as having
// something to say about it.
func TestOnlyASilenceIsUnrelated(t *testing.T) {
	answer, err := ParseAnswer([]byte(`{"text":"雨ですね","sources":[],"unrelated":true}`))
	if err != nil || answer.Unrelated {
		t.Fatalf("answer = %+v, %v", answer, err)
	}
	answer, err = ParseAnswer([]byte(`{"text":"","sources":[],"unrelated":true}`))
	if err != nil || !answer.Unrelated {
		t.Fatalf("answer = %+v, %v", answer, err)
	}
	// An agent that does not know the field is still understood.
	if _, err := ParseAnswer([]byte(`{"text":"","sources":[]}`)); err != nil {
		t.Fatal(err)
	}
}

// A model that keeps calling read after it was withdrawn is stopped: the phase
// gives up within a bounded number of rounds instead of calling for ever with
// the keeper's lock held, and the answer is already out.
func TestAKeeperThatKeepsReadingIsStopped(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	rounds := 0
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"駅から5分の店です。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		rounds++
		if rounds > 50 {
			t.Fatal("the keeper did not stop")
		}
		return `force-read:{"steps":[{"type":"notes_list","paths":[],"tool":"","args":"{}"}]}`
	}
	r.react(post(t, s, "ラーメン屋を教えて").ID)
	if rounds > maxPhaseRounds {
		t.Fatalf("rounds = %d, want at most %d", rounds, maxPhaseRounds)
	}
	if got := remarks(t, s); len(got) != 1 {
		t.Fatalf("remarks = %+v", got)
	}
}
