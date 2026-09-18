package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// A preview is the persona's turn done over on the same post, and nothing it
// does is kept: the remark it would make comes back, but no note is written,
// nothing lands on the timeline, and no wake is counted.
func TestPreviewReplyKeepsNothing(t *testing.T) {
	workflow := map[string]any{"summary": "test", "steps": []any{
		map[string]any{"type": "write_note", "path": "places/shibuya.md", "text": "type: Place\n\n渋谷。", "tool": "", "args": "{}", "at": "", "daily": false, "note": "", "id": ""},
		map[string]any{"type": "speak", "path": "", "text": "渋谷なら20時までに着けます。", "tool": "", "args": "{}", "at": "", "daily": false, "note": "", "id": ""},
	}}
	scripted, _ := json.Marshal(workflow)
	var systems []string
	runner, s, _ := setup(t, func(system, user string) string {
		systems = append(systems, system)
		return string(scripted)
	}, "move", "gourmet")
	post := post(t, s, "20時に渋谷に着きたい")
	remark := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "move", Root: post.ID, ReplyTo: post.ID,
		Text: "渋谷ですね。", At: post.At.Add(time.Minute),
	}
	if err := s.Append(remark); err != nil {
		t.Fatal(err)
	}
	// Something said after the remark is not part of what the persona saw.
	later := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindReply, Speaker: timeline.User, Root: post.ID, ReplyTo: remark.ID, Text: "やっぱり新宿", At: post.At.Add(2 * time.Minute)}
	if err := s.Append(later); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Timeline()
	wakes := s.Memory("move").Wakes

	preview, err := runner.PreviewReply(context.Background(), remark.ID, "move")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Quiet || preview.Spoke != "渋谷なら20時までに着けます。" || preview.Error != "" {
		t.Fatalf("preview = %+v", preview)
	}
	if len(systems) == 0 || !strings.Contains(systems[0], "michi") {
		t.Fatalf("the persona was not run: %v", systems)
	}
	if _, ok := s.Note("move", "places/shibuya.md"); ok {
		t.Fatal("a preview wrote a note")
	}
	if after, _ := s.Timeline(); len(after) != len(before) {
		t.Fatalf("a preview changed the timeline: %d -> %d", len(before), len(after))
	}
	if s.Memory("move").Wakes != wakes {
		t.Fatal("a preview counted a wake")
	}
	if woke, _ := s.Woke(post.ID); len(woke) != 0 {
		t.Fatalf("a preview recorded a wake: %v", woke)
	}

	// A remark by a persona no longer in the room cannot be tried again.
	idle := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "book", Root: post.ID, ReplyTo: post.ID, Text: "本ですか", At: post.At.Add(3 * time.Minute)}
	if err := s.Append(idle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ entry, skill, want string }{
		{post.ID, "move", "担当の発言"},
		{idle.ID, "book", "有効ではありません"},
		{remark.ID, "gourmet", "その担当の発言ではありません"},
		{"missing", "move", "担当の発言"},
	} {
		if _, err := runner.PreviewReply(context.Background(), tc.entry, tc.skill); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s/%s: %v", tc.entry, tc.skill, err)
		}
	}
}
