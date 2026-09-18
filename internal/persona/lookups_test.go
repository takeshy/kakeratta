package persona

import (
	"strings"
	"testing"
)

// A turn may look things up only so many times, and never the same thing
// twice: the second identical call is answered from the first.
func TestLookupsAreBudgetedAndNotRepeated(t *testing.T) {
	budget := newLookups(2)
	if _, ok := budget.take("web_search", `{"query":"norton altura"}`); !ok {
		t.Fatal("the first call was refused")
	}
	budget.keep("web_search", `{"query":"norton altura"}`, "- Norton Altura White Blend")
	again, ok := budget.take("web_search", ` {"query":"norton altura"} `)
	if ok || !strings.HasPrefix(again, "- Norton Altura White Blend") || !strings.Contains(again, "もう一度は行いません") {
		t.Fatalf("a repeated call was not answered from the first: %v %q", ok, again)
	}
	if budget.left() != 1 {
		t.Fatalf("a repeat spent the budget: %d left", budget.left())
	}
	if _, ok := budget.take("place_search", `{"query":"ramen"}`); !ok {
		t.Fatal("a different call within budget was refused")
	}
	if out, ok := budget.take("web_search", `{"query":"something else"}`); ok || out != lookupsSpent {
		t.Fatalf("a call over budget was made: %v %q", ok, out)
	}
	if again, ok := budget.take("web_search", `{"query":"norton altura"}`); ok || !strings.HasPrefix(again, "- Norton") {
		t.Fatalf("a known answer is still given when the budget is spent: %v %q", ok, again)
	}
}
