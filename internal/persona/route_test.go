package persona

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
)

// A routing answer that cannot be used delivers to nobody. Waking everyone was
// the old fallback, and it is exactly the noise routing exists to prevent.
func TestRoutingThatCannotBeUsedDeliversToNobody(t *testing.T) {
	for _, tc := range []struct {
		reply string
		want  []string
	}{
		{`{}`, nil},
		{`null`, nil},
		{`{"deliver":null}`, nil},
		{`{"deliver":["unknown"]}`, nil},
		{`{"deliver":[]}`, nil},
		{`{"deliver":["food","food"]}`, []string{"food"}},
		{`{"deliver":["food","unknown"]}`, []string{"food"}},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			f := &fake{reply: func(string, string) string { return tc.reply }}
			server := f.start(t)
			got := Route(context.Background(), llm.New(server.URL, ""), "route", []Skill{{ID: "food"}, {ID: "tech"}}, nil, Entryish{})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Routing is the app's own judgement, like the review, so it is made by the
// best model configured - with a single-model setup still working.
func TestRoutingAndReviewUseTheHarnessModel(t *testing.T) {
	if got := harnessModel(store.Settings{LowModel: "light", MiddleModel: "super", HighModel: "ultra"}); got != "ultra" {
		t.Fatalf("harness = %q", got)
	}
	if got := harnessModel(store.Settings{LowModel: "light"}); got != "light" {
		t.Fatalf("single-model harness = %q", got)
	}
}

// A post goes where it belongs and nowhere else. "明日9:10に神谷町に着きたい"
// reached the wine persona under the old rule of delivering when in doubt.
func TestTheRoutingPromptDeliversOnlyWhereAPostBelongs(t *testing.T) {
	for _, rule := range []string{
		"本当に属するときだけ",
		"言葉が出てきただけでは渡しません",
		"神谷町",
		"ドラマを見る",
		"誰にも渡しません",
	} {
		if !strings.Contains(routePrompt, rule) {
			t.Fatalf("the routing prompt lost %q", rule)
		}
	}
	for _, gone := range []string{"既定は「渡す」", "迷ったら渡して"} {
		if strings.Contains(routePrompt, gone) {
			t.Fatalf("the routing prompt still leans on delivering: %q", gone)
		}
	}
}
