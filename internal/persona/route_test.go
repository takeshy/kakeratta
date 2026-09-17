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
		{`{"deliver":["food（tabe）","tech (gijutsu)"]}`, []string{"food", "tech"}},
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

// Routing matches short conditions with Lightning; the review looks for the
// obviously wrong with Super. There is no model setting to follow any more.
func TestRoutingUsesLightningAndReviewUsesSuper(t *testing.T) {
	if got := routingModel(); got != store.NemotronLightning {
		t.Fatalf("routing model = %q", got)
	}
	if got := reviewModel(); got != store.NemotronSuper {
		t.Fatalf("review model = %q", got)
	}
}

// A post goes where it belongs and nowhere else. "明日9:10に神谷町に着きたい"
// reached the wine persona under the old rule of delivering when in doubt.
func TestTheRoutingPromptDeliversOnlyWhereAPostBelongs(t *testing.T) {
	for _, rule := range []string{
		"単語だけで判定せず",
		"条件にない前提を足しません",
		"本人と担当の発言を含むスレッド全体",
		"補足・訂正",
		"一致なしなら",
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
