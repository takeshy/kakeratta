package persona

import (
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestThreadContextKeepsReplyAndAttachmentsWithoutRepeatingTrigger(t *testing.T) {
	parent := timeline.Entry{ID: "parent", Speaker: "infographic", Text: strings.Repeat("図解の対象となる長文", 200)}
	trigger := timeline.Entry{ID: "trigger", Speaker: timeline.User, ReplyTo: parent.ID, Text: "訂正:予算は2000円", Media: &timeline.Media{Text: "添付資料の全文"}}
	stale := trigger
	stale.Text = "更新前の本文"
	hidden := timeline.Entry{ID: "deleted", Deleted: true, Text: "削除された本文"}
	for _, entries := range [][]timeline.Entry{{parent, stale, hidden}, {parent, hidden}} {
		got := threadContext(entries, trigger, nil, time.UTC)
		for _, text := range []string{parent.Text, trigger.Media.Text, parent.ID + " への返事"} {
			if strings.Count(got, text) != 1 {
				t.Errorf("content must occur once: %q", text)
			}
		}
		if len(entries) == 3 && !strings.Contains(got, "あなた（ユーザー）「"+trigger.Text+"」") {
			t.Errorf("target does not show whose words it is: %s", got)
		}
		if strings.Contains(got, stale.Text) || strings.Contains(got, hidden.Text) {
			t.Fatal("stale/deleted content rendered")
		}
	}
	if stale.Text != "更新前の本文" {
		t.Fatal("input mutated")
	}
}

func TestThreadContextHandlesSystemWakeWithoutID(t *testing.T) {
	trigger := timeline.Entry{Speaker: "system", Text: "Daily check-in"}
	for _, entries := range [][]timeline.Entry{nil, {trigger}, {{ID: "gone", Deleted: true}, trigger}} {
		got := threadContext(entries, trigger, nil, time.UTC)
		if strings.Count(got+"\n", "\n"+trigger.Text+"\n") != 1 {
			t.Fatalf("system wake missing or duplicated: %s", got)
		}
		if len(entries) > 0 && !strings.Contains(got, "1番目の発言") {
			t.Fatalf("system wake target missing: %s", got)
		}
	}
}

func TestThreadContextTargetIsAnExcerptOfALongEntry(t *testing.T) {
	trigger := timeline.Entry{ID: "trigger", Speaker: timeline.User, Text: strings.Repeat("長い本文", 100)}
	got := threadContext([]timeline.Entry{trigger}, trigger, nil, time.UTC)
	if strings.Count(got, trigger.Text) != 1 || !strings.Contains(got, "「"+string([]rune(trigger.Text)[:targetExcerptRunes])+"…」") {
		t.Fatalf("long target not shortened: %s", got)
	}
}

func TestThreadContextLeavesAmbiguousEntriesWithoutIDAlone(t *testing.T) {
	trigger := timeline.Entry{Speaker: "system", Text: "Daily check-in"}
	got := threadContext([]timeline.Entry{trigger, trigger}, trigger, nil, time.UTC)
	if strings.Contains(got, "番目の発言") || strings.Count(got, trigger.Text) != 3 {
		t.Fatalf("an entry without id was guessed among look-alikes: %s", got)
	}
}
