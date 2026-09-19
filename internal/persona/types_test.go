package persona

import (
	"reflect"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/tools"
)

// The kinds a persona's memory layout names are the only ones its notes may
// carry; a persona without a layout, or a note without a kind, is left alone.
func TestANoteCarriesOnlyANamedKind(t *testing.T) {
	layout := "型は Wine（銘柄。産地・品種・感想）と Preference（好みの傾向）の2つ。OKF の形式で書く。"
	if got := allowedTypes(layout); !reflect.DeepEqual(got, []string{"Wine", "Preference"}) {
		t.Fatalf("kinds = %v", got)
	}
	if why := typeNotAllowed(layout, "wines/altura.md", "---\ntype: WineInfo\ntitle: Altura\n---\n"); !strings.Contains(why, "WineInfo") || !strings.Contains(why, "Wine / Preference") {
		t.Fatalf("a kind outside the layout passed: %q", why)
	}
	if why := typeNotAllowed(layout, "wines/altura.md", "---\ntype: wine\ntitle: Altura\n---\n"); why != "" {
		t.Fatalf("case is not a different kind: %q", why)
	}
	if why := typeNotAllowed(layout, "tastes/whites.md", "---\ntype: Preference # 好み\n---\n"); why != "" {
		t.Fatalf("a named kind with a comment was refused: %q", why)
	}
	if why := typeNotAllowed("", "a.md", "---\ntype: Anything\n---\n"); why != "" {
		t.Fatalf("a persona without a layout was checked: %q", why)
	}
	if why := typeNotAllowed(layout, "a.md", "no front matter"); why != "" {
		t.Fatalf("a note without a kind was checked: %q", why)
	}
	if why := typeNotAllowed(layout, "index.md", "---\ntype: Index\n---\n"); why != "" {
		t.Fatalf("the index was checked: %q", why)
	}
	for _, builtin := range Builtins {
		// Hermes keeps its memory on the server it connects to, so it has no
		// layout here to name kinds in.
		if builtin.ID == "hermes" {
			continue
		}
		if len(allowedTypes(builtin.Memory)) == 0 {
			t.Fatalf("%s names no kinds in its memory layout", builtin.ID)
		}
	}
	set := tools.New(tools.Config{}).For(nil)
	workflow := Workflow{Steps: []Step{
		{Type: "write_note", Path: "wines/altura.md", Text: "---\ntype: Explanation\ntitle: Altura\n---\n白。"},
		{Type: "speak", Text: "白ですね"},
	}}
	if problems := checkWorkflow(workflow, turnRules{set: set, noteRequired: true, memory: layout}); len(problems) != 1 || !strings.Contains(problems[0], "Explanation") {
		t.Fatalf("problems = %v", problems)
	}
}
