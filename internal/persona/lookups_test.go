package persona

import (
	"strings"
	"testing"
)

// A turn may look things up only so many times, and never the same thing
// twice: the second identical call is answered from the first.
func TestLookupsAreBudgetedAndNotRepeated(t *testing.T) {
	budget := newLookups(2, 2)
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

// Web search spends the user's Tavily credit, so it has a smaller budget
// inside the turn's: two searches, four on Ultra. A repeat is not a search.
func TestWebSearchHasItsOwnSmallerBudget(t *testing.T) {
	for _, deep := range []bool{false, true} {
		budget := newLookups(turnReadLimit(deep), turnSearchLimit(deep))
		for i := 0; i < turnSearchLimit(deep); i++ {
			args := `{"query":"q` + string(rune('a'+i)) + `"}`
			if _, ok := budget.take("web_search", args); !ok {
				t.Fatalf("deep=%v: search %d was refused", deep, i+1)
			}
			budget.keep("web_search", args, "result")
		}
		if again, ok := budget.take("web_search", `{"query":"qa"}`); ok || !strings.HasPrefix(again, "result") {
			t.Fatalf("deep=%v: a repeat was not answered from the first: %v %q", deep, ok, again)
		}
		if out, ok := budget.take("web_search", `{"query":"more"}`); ok || out != searchesSpent {
			t.Fatalf("deep=%v: a search over its budget was made: %v %q", deep, ok, out)
		}
		if _, ok := budget.take("place_search", `{"query":"ramen"}`); !ok {
			t.Fatalf("deep=%v: another lookup was refused after searches ran out", deep)
		}
	}
	if turnSearchLimit(false) != 2 || turnSearchLimit(true) != 4 {
		t.Fatalf("limits are %d and %d", turnSearchLimit(false), turnSearchLimit(true))
	}
}
