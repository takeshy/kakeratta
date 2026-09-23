package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Routing uses Super unless Jev is enabled. Workflow review uses the middle
// slot, so it is Super by default and follows the Middle model on OpenRouter.
func TestRoutingAndReviewUseSuper(t *testing.T) {
	if got := routingModel(); got != store.NemotronSuper {
		t.Fatalf("routing model = %q", got)
	}
	if got := reviewModel(store.Settings{MiddleModel: store.NemotronSuper, LowModel: store.NemotronLightning}); got != store.NemotronSuper {
		t.Fatalf("review model = %q", got)
	}
	if got := reviewModel(store.Settings{MiddleModel: "openai/gpt-oss-120b", LowModel: store.OpenRouterLow}); got != "openai/gpt-oss-120b" {
		t.Fatalf("OpenRouter review model = %q", got)
	}
}

func TestJevRoutesEveryPersonaThroughOneDecisionsRequest(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		var body struct {
			Model     string                    `json:"model"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != jevModel || len(body.Questions) != 2 {
			t.Fatalf("request = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"typesafe/jev-1.13","answers":{"food":{"type":"choice","choice":"deliver"},"book":{"type":"choice","choice":"exclude"}},"usage":{"input_tokens":10,"output_tokens":2}}`))
	}))
	defer server.Close()

	got := RouteJev(context.Background(), server.URL, "openrouter-secret", []Skill{
		{ID: "food", Domain: "食べ物の話"}, {ID: "book", Domain: "本の話"},
	}, nil, Entryish{Text: "ラーメン"})
	if !got.Answered || !reflect.DeepEqual(got.Deliver, []string{"food"}) {
		t.Fatalf("routing = %+v", got)
	}
	if reason, ok := got.Excluded["book"]; !ok || reason != "" {
		t.Fatalf("Jev should retain the exclusion without inventing a reason: %+v", got.Excluded)
	}
	if authorization != "Bearer openrouter-secret" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestJevNeedsBothTheAdvancedOptionAndItsKey(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		key     string
		want    bool
	}{
		{false, "", false},
		{true, "", false},
		{false, "key", false},
		{true, "key", true},
	} {
		got := jevConfigured(store.Settings{JevEnabled: tc.enabled, OpenRouterAPIKey: tc.key})
		if got != tc.want {
			t.Fatalf("enabled=%t key=%q: got %t", tc.enabled, tc.key, got)
		}
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
