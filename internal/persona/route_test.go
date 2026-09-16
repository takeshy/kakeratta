package persona

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
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
			got := Route(context.Background(), llm.New(server.URL, ""), "route", []Skill{{ID: "food"}, {ID: "tech"}}, nil, Entryish{})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Who decides the routing depends on how much there is to read: a one-line post
// is the hard case, and a post that is long because a picture was read names
// its own subject plainly enough for the light model.
func TestRouteModelFollowsTheSizeOfThePost(t *testing.T) {
	settings := store.Settings{LowModel: "light", MiddleModel: "super"}
	short := timeline.Entry{Text: "17:00になったら夕飯の買い物に行く。"}
	if got := routeModelFor(settings, []timeline.Entry{short}, short); got != "super" {
		t.Fatalf("a short post was routed by %q", got)
	}
	long := timeline.Entry{Text: strings.Repeat("あ", 2000)}
	if got := routeModelFor(settings, []timeline.Entry{long}, long); got != "light" {
		t.Fatalf("a long post was routed by %q", got)
	}
	// A capture's OCR is what makes such a post long, so it counts.
	read := timeline.Entry{Text: "これ", Media: &timeline.Media{Text: strings.Repeat("字", 2000)}}
	if got := routeModelFor(settings, []timeline.Entry{read}, read); got != "light" {
		t.Fatalf("a capture was routed by %q", got)
	}
	// Without a middle tier configured, the light model still decides.
	if got := routeModelFor(store.Settings{LowModel: "light"}, []timeline.Entry{short}, short); got != "light" {
		t.Fatalf("fallback routed by %q", got)
	}
}
