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
			got := Route(context.Background(), llm.New(server.URL, ""), "route", []Skill{{ID: "food"}, {ID: "tech"}}, nil, Entryish{}).Deliver
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The reasons come back under whatever name the router used for a persona and
// are kept under its id; a name that is nobody's is dropped. A reply that made
// no sense is not an answer, and neither is a router that is not configured.
func TestRoutingKeepsExclusionReasonsByID(t *testing.T) {
	skills := []Skill{{ID: "food", Name: "tabe"}, {ID: "tech"}}
	f := &fake{reply: func(string, string) string {
		return `{"deliver":[],"excluded_reasons":{"food（tabe）":"x","unknown":"y","tech":"z"}}`
	}}
	server := f.start(t)
	got := Route(context.Background(), llm.New(server.URL, ""), "route", skills, nil, Entryish{})
	if !got.Answered || got.Error != "" {
		t.Fatalf("expected an answer, got %+v", got)
	}
	if want := map[string]string{"food": "x", "tech": "z"}; !reflect.DeepEqual(got.Excluded, want) {
		t.Fatalf("excluded %v, want %v", got.Excluded, want)
	}

	f.reply = func(string, string) string { return `{}` }
	if got := Route(context.Background(), llm.New(server.URL, ""), "route", skills, nil, Entryish{}); got.Answered || got.Error == "" {
		t.Fatalf("a reply without deliver is not an answer: %+v", got)
	}
	if got := Route(context.Background(), nil, "route", skills, nil, Entryish{}); got.Answered || got.Error == "" {
		t.Fatalf("no client is not an answer: %+v", got)
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
		// The reasons are read by a person, so they come in the post's language,
		// and in the conditions' language when the post is a bare image.
		"同じ言語で書き",
		"担当条件が書かれている言語",
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
