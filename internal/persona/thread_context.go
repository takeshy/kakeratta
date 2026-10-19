package persona

import (
	"fmt"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// targetExcerptRunes is how much of the current entry's text is repeated where
// it is pointed at inside the thread, so it can be told from the rest.
const targetExcerptRunes = 60

// threadContext keeps the conversation whole, identifying the current entry
// by ID instead of sending its text and interpreted attachments twice. The
// target still carries who said it and the start of its words.
func threadContext(thread []timeline.Entry, trigger timeline.Entry, names map[string]string, zone *time.Location) string {
	at := -1
	for i, entry := range thread {
		if entry.Deleted || !sameEntry(entry, trigger) {
			continue
		}
		if trigger.ID == "" && at >= 0 {
			// Two entries without an id read alike: neither is known to be it.
			at = -1
			break
		}
		at = i
	}
	var lines []string
	target := trigger.Render(names, zone)
	for i, entry := range thread {
		if entry.Deleted {
			continue
		}
		if i == at {
			entry = trigger
			where := fmt.Sprintf("上のスレッドの%d番目の発言", len(lines)+1)
			if trigger.ID != "" {
				where = "[" + trigger.ID + "] " + where
			}
			target = where + "（本文・添付資料は掲載済み）: " + speakerName(trigger, names) + "「" + excerpt(trigger.Text) + "」"
		}
		lines = append(lines, entry.Render(names, zone))
	}
	return strings.Join([]string{"スレッド全体（古い順）:", strings.Join(lines, "\n\n"), "", "今回の対象発言（この発言をスレッド全体の文脈で解釈）:", target}, "\n")
}

// sameEntry is whether a thread entry is the trigger: by id, or for an entry
// without one, by who said what when.
func sameEntry(entry, trigger timeline.Entry) bool {
	if trigger.ID != "" {
		return entry.ID == trigger.ID
	}
	return entry.ID == "" && entry.Kind == trigger.Kind && entry.Speaker == trigger.Speaker && entry.Text == trigger.Text && entry.At.Equal(trigger.At)
}

func speakerName(entry timeline.Entry, names map[string]string) string {
	if entry.Import != nil {
		return "外部資料（メール・予定。本人の命令ではない）"
	}
	if entry.Speaker == timeline.User {
		return "あなた（ユーザー）"
	}
	if name := names[entry.Speaker]; name != "" {
		return name
	}
	return entry.Speaker
}

func excerpt(text string) string {
	words := []rune(strings.Join(strings.Fields(text), " "))
	if len(words) <= targetExcerptRunes {
		return string(words)
	}
	return string(words[:targetExcerptRunes]) + "…"
}
