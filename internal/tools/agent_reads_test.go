package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

func TestAgentReadsAreWorkspaceBoundAndDoNotGrantWrites(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteNote("gourmet", "Memo.md", "軽い赤ワインが好き"); err != nil {
		t.Fatal(err)
	}
	base := New(Config{}).WithSchedules(w)
	direct := base.WithAgentReads(w, map[string]string{"gourmet": "tabe"})
	names := []string{"list_personas", "list_notes", "read_note", "get_thread", "schedules_list", "web_search"}
	set := direct.For(names).As("agent")
	for _, name := range names {
		if !set.IsReadOnly(name) {
			t.Fatalf("%s is not read-only", name)
		}
	}
	for _, name := range []string{"schedule", "write_note", "notes_write", "shell", "read_file"} {
		if set.Allows(name) {
			t.Fatalf("direct agent can call %s", name)
		}
	}
	if base.For(names).Allows("read_note") {
		t.Fatal("cross-persona reads leaked to ordinary registry")
	}
	for _, tc := range []struct {
		name string
		args Args
		want string
	}{
		{"list_personas", Args{}, "tabe"},
		{"list_notes", Args{"persona": "gourmet"}, "Memo.md"},
		{"read_note", Args{"persona": "gourmet", "path": "Memo.md"}, "軽い赤ワインが好き"},
		{"schedules_list", Args{}, ""},
	} {
		result, err := set.Run(context.Background(), tc.name, tc.args, nil)
		if err != nil || !strings.Contains(result.Text, tc.want) {
			t.Fatalf("%s = %+v, %v", tc.name, result, err)
		}
	}
	for _, args := range []Args{{"persona": "../other", "path": "Memo.md"}, {"persona": "gourmet", "path": "../../config.json"}} {
		if _, err := set.Run(context.Background(), "read_note", args, nil); err == nil {
			t.Fatal("out-of-scope note was readable")
		}
	}
	if set.ReadAvailable("web_search", nil) {
		t.Fatal("unconfigured Web search was offered")
	}
	if !direct.WithTavilyKey("test").For(names).ReadAvailable("web_search", nil) {
		t.Fatal("configured Web search unavailable")
	}
	note, _ := w.Note("gourmet", "Memo.md")
	if note.Text != "軽い赤ワインが好き" {
		t.Fatal("reading changed another persona's note")
	}
}
