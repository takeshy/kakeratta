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

func TestRoutingRequiresBooleanAndReason(t *testing.T) {
	for _, tc := range []struct {
		reply           string
		answered, match bool
	}{
		{`{}`, false, false}, {`null`, false, false}, {`{"match":"yes","reason":"x"}`, false, false},
		{`{"match":true}`, false, false}, {`{"match":true,"reason":" "}`, false, false},
		{`{"match":true,"reason":"一致"}`, true, true}, {`{"match":false,"reason":"対象外"}`, true, false},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			f := &fake{reply: func(string, string) string { return tc.reply }}
			server := f.start(t)
			got := Route(context.Background(), llm.New(server.URL, ""), "route", []Skill{{ID: "food"}}, nil, Entryish{})
			if got.Answered != tc.answered || (len(got.Deliver) > 0) != tc.match {
				t.Fatalf("routing = %+v", got)
			}
			if tc.answered && !tc.match && got.Excluded["food"] != "対象外" {
				t.Fatalf("reason = %+v", got)
			}
		})
	}
	if got := Route(context.Background(), nil, "route", []Skill{{ID: "food"}}, nil, Entryish{}); got.Answered || got.Error == "" {
		t.Fatalf("routing = %+v", got)
	}
}

// Routing defaults to Lightning unless a Router model is selected. Workflow review uses the middle
// slot, so it is Super by default and follows the Middle model on OpenRouter.
func TestRoutingUsesLightningAndReviewUsesSuper(t *testing.T) {
	if got := routingModel(); got != store.NemotronLightning {
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

func TestJevNeedsOpenRouterSelectionAndItsKey(t *testing.T) {
	if jevConfigured(store.Settings{OpenRouterMode: true, OpenRouterRouterModel: store.OpenRouterRouter, OpenRouterAPIKey: "key", JevEnabled: true}) {
		t.Fatal("a legacy toggle must not override the selected router")
	}
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
		got := jevConfigured(store.Settings{OpenRouterMode: tc.enabled, OpenRouterRouterModel: jevModel, OpenRouterAPIKey: tc.key})
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
