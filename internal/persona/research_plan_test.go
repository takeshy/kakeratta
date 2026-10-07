package persona

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func writeResearchReply(w http.ResponseWriter, name string, args any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": testCall(name, args)}}})
}

func TestDepthUsesOnlyExplicitStringMatches(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"詳しく", true}, {"もっと詳しく説明して", true}, {"深掘りして", true}, {"もう一度考えて", true}, {"Explain IN DETAIL", true}, {"deep dive", true},
		{"ありがとう", false}, {"何時ごろ？", false}, {"複数の設計を比較して", false}, {"分散処理の整合性を検証して", false},
	} {
		if got := wantsDeep(tc.text); got != tc.want {
			t.Errorf("%q depth=%t want=%t", tc.text, got, tc.want)
		}
	}
	if strings.Contains(individualRoutePrompt, "deep") {
		t.Error("router should not select answer depth")
	}
}
