package persona

import (
	"context"
	"reflect"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
)

func TestInvalidRoutingFallsBackButExplicitSilenceDoesNot(t *testing.T) {
	for _, tc := range []struct {
		reply string
		want  []string
	}{
		{`{}`, []string{"food", "tech"}},
		{`null`, []string{"food", "tech"}},
		{`{"deliver":null}`, []string{"food", "tech"}},
		{`{"deliver":["unknown"]}`, []string{"food", "tech"}},
		{`{"deliver":[]}`, nil},
		{`{"deliver":["food","food"]}`, []string{"food"}},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			f := &fake{reply: func(string, string) string { return tc.reply }}
			server := f.start(t)
			got := Route(context.Background(), llm.New(server.URL, ""), "route", []Skill{{ID: "food"}, {ID: "tech"}}, nil)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
