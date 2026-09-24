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

func TestApplyRoutingUsesCheckedDecisionAndRunsPersona(t *testing.T) {
	routes, turns := 0, 0
	runner, workspace, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			routes++
			return `{"deliver":["gourmet"],"excluded_reasons":{}}`
		}
		turns++
		return `{"summary":"test","steps":[{"type":"write_note","path":"food/ramen.md","text":"ラーメンが好き"},{"type":"speak","text":"ラーメンですね"}]}`
	}, "gourmet")
	post := post(t, workspace, "ラーメンを食べた")
	check, err := runner.CheckRouting(context.Background(), post.ID, "gourmet")
	if err != nil || !check.Answered || !check.Delivered || check.Token == "" {
		t.Fatalf("check = %+v, %v", check, err)
	}
	if routes != 1 || turns != 0 {
		t.Fatalf("preview ran persona: routes=%d turns=%d", routes, turns)
	}
	if err := runner.ApplyRouting(context.Background(), check.Token, post.ID); err != nil {
		t.Fatal(err)
	}
	if routes != 1 || turns == 0 {
		t.Fatalf("apply rerouted or skipped persona: routes=%d turns=%d", routes, turns)
	}
	if _, ok := workspace.Note("gourmet", "food/ramen.md"); !ok {
		t.Fatal("ordinary turn did not write its note")
	}
	entries, _ := workspace.Timeline()
	if len(entries) != 2 || entries[1].Text != "ラーメンですね" {
		t.Fatalf("reply = %+v", entries)
	}
	record, _ := workspace.Routing(post.ID)
	if record == nil || len(record.Delivered) != 1 || len(record.Done) != 1 || len(record.Spoke) != 1 {
		t.Fatalf("routing = %+v", record)
	}
	if err := runner.ApplyRouting(context.Background(), check.Token, post.ID); err != nil {
		t.Fatal(err)
	}
	if routes != 1 || turns != 1 {
		t.Fatal("retry ran the router or persona again")
	}
}

func TestApplyReplyKeepsExactPreviewWithoutRunningModelAgain(t *testing.T) {
	workflow := map[string]any{"summary": "test", "steps": []any{
		map[string]any{"type": "write_note", "path": "food/ramen.md", "text": "味噌ラーメン"},
		map[string]any{"type": "speak", "text": "味噌ですね"},
	}}
	scripted, _ := json.Marshal(workflow)
	turns := 0
	runner, workspace, _ := setup(t, func(system, user string) string { turns++; return string(scripted) }, "gourmet")
	post := post(t, workspace, "ラーメン")
	old := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "gourmet", Root: post.ID, ReplyTo: post.ID, Text: "醤油ですね", At: post.At.Add(time.Minute)}
	if err := workspace.Append(old); err != nil {
		t.Fatal(err)
	}
	preview, err := runner.PreviewReply(context.Background(), old.ID, "gourmet")
	if err != nil || preview.Spoke != "味噌ですね" || preview.Token == "" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	before := turns
	if err := runner.ApplyReply(preview.Token, old.ID); err != nil {
		t.Fatal(err)
	}
	if turns != before {
		t.Fatal("acceptance ran the model again")
	}
	changed, _ := workspace.Entry(old.ID)
	if changed.Text != preview.Spoke || changed.ID != old.ID {
		t.Fatalf("changed = %+v", changed)
	}
	if note, ok := workspace.Note("gourmet", "food/ramen.md"); !ok || !strings.HasPrefix(note.Text, "味噌ラーメン") {
		t.Fatalf("note = %+v, %v", note, ok)
	}
	if err := runner.ApplyReply(preview.Token, old.ID); err != nil {
		t.Fatal(err)
	}
	if turns != before {
		t.Fatal("retry ran the model again")
	}
}

func TestApplyReplyAddsMissingRemark(t *testing.T) {
	runner, workspace, _ := setup(t, func(system, user string) string {
		return `{"summary":"test","steps":[{"type":"write_note","path":"food/ramen.md","text":"食べた"},{"type":"speak","text":"おいしそう"}]}`
	}, "gourmet")
	post := post(t, workspace, "ラーメンを食べた")
	if err := workspace.SaveRouting(post.ID, store.Routing{Delivered: []string{"gourmet"}, EntryText: &post.Text, Tracked: true, Done: []string{"gourmet"}}); err != nil {
		t.Fatal(err)
	}
	preview, err := runner.PreviewReply(context.Background(), post.ID, "gourmet")
	if err != nil || preview.Token == "" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if err := runner.ApplyReply(preview.Token, post.ID); err != nil {
		t.Fatal(err)
	}
	entries, _ := workspace.Timeline()
	if len(entries) != 2 || entries[1].ReplyTo != post.ID || entries[1].Text != preview.Spoke {
		t.Fatalf("reply = %+v", entries)
	}
	record, _ := workspace.Routing(post.ID)
	if !contains(record.Spoke, "gourmet") {
		t.Fatalf("routing = %+v", record)
	}
	if woke, _ := workspace.Woke(post.ID); !contains(woke, "gourmet") {
		t.Fatalf("wakes = %v", woke)
	}
}
