package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestEveryPersonaCanSearchItsUsersTimelineWithCitations(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(timeline.Entry{ID: "old", Kind: timeline.KindPost, Speaker: timeline.User, Text: "赤ワイン"}); err != nil {
		t.Fatal(err)
	}
	set := New(Config{}).WithSearch(search.New(w)).For(nil)
	if !strings.Contains(set.Describe(), "timeline_search") {
		t.Fatal("search not exposed to existing personas")
	}
	result, err := set.Run(context.Background(), "timeline_search", Args{"query": "赤ワイン", "mode": "keyword"}, nil)
	// The link is in the row for the persona to cite; the hits themselves
	// are not attached to its remark.
	if err != nil || !strings.Contains(result.Text, "赤ワイン") || !strings.Contains(result.Text, "/?entry=old") || len(result.Sources) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := set.Run(context.Background(), "web_search", Args{"query": "wine"}, nil); err == nil {
		t.Fatal("undeclared web search permitted")
	}
}

func TestSemanticContextIncludesTailAndFiltersWeakEvidence(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body struct{ Input []string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		rows := []map[string]any{}
		for i, input := range body.Input {
			v := make([]float64, search.Dimensions)
			score := 0.4
			if strings.HasPrefix(input, "Instruct:") {
				score = 1
			} else if strings.Contains(input, "ピノ") {
				score = 0.8
			}
			v[0], v[1] = score, math.Sqrt(1-score*score)
			rows = append(rows, map[string]any{"index": i, "embedding": v})
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": rows})
	}))
	defer provider.Close()
	if err := w.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []timeline.Entry{
		{ID: "wine", Kind: timeline.KindPost, Speaker: timeline.User, Text: strings.Repeat("食事の記録。", 100) + "気に入ったのはピノ・ノワール"},
		{ID: "other", Kind: timeline.KindPost, Speaker: timeline.User, Text: "Goの実装"},
	} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	s := search.New(w)
	set := New(Config{}).WithSearch(s).For(nil)
	for _, tc := range []struct {
		name string
		args Args
		want int
	}{
		{"default", Args{"query": "軽い赤のお酒", "limit": 10}, 1},
		{"lower threshold", Args{"query": "軽い赤のお酒", "min_score": 0.3, "limit": 10}, 2},
		{"no evidence", Args{"query": "軽い赤のお酒", "min_score": 0.9}, 0},
		{"keyword", Args{"query": "Go", "mode": "keyword", "min_score": 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := set.Run(context.Background(), "timeline_search", tc.args, nil)
			if err != nil || rowCount(result.Text) != tc.want {
				t.Fatalf("%+v %v", result, err)
			}
			if tc.name == "default" {
				if !strings.Contains(result.Text, "気に入ったのはピノ・ノワール") || strings.Contains(result.Text, "Goの実装") {
					t.Fatalf("missing tail or leaked weak evidence: %s", result.Text)
				}
				var rows []map[string]any
				if err := json.Unmarshal([]byte(result.Text), &rows); err != nil {
					t.Fatal(err)
				}
				if rows[0]["speaker"] != string(timeline.User) || rows[0]["score"].(float64) <= 0 {
					t.Fatal(rows)
				}
			}
			if tc.want == 0 && !strings.Contains(result.Text, "確認できたとは判断せず") {
				t.Fatal(result.Text)
			}
		})
	}
	for _, score := range []float64{-1, 1.1, math.NaN(), math.Inf(1)} {
		if _, err := set.Run(context.Background(), "timeline_search", Args{"query": "wine", "min_score": score}, nil); err == nil {
			t.Fatal("invalid threshold accepted")
		}
	}
	// The UI still offers weaker candidates and keeps its compact excerpt.
	ui, err := s.Search(context.Background(), "軽い赤のお酒", "semantic", 10, 0)
	if err != nil || ui.Total != 2 || strings.Contains(ui.Hits[0].Snippet, "ピノ") {
		t.Fatalf("%+v %v", ui, err)
	}
}

// rowCount is how many records a search answered with; none when it says so
// in prose.
func rowCount(text string) int {
	start := strings.Index(text, "[")
	if start < 0 {
		return 0
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text[start:]), &rows); err != nil {
		return 0
	}
	return len(rows)
}

func TestSearchByWindowAloneReadsAMorningInOneCall(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	at := func(hour int) time.Time { return time.Date(2026, 9, 20, hour, 0, 0, 0, tokyo) }
	for _, entry := range []timeline.Entry{
		{ID: "night", Kind: timeline.KindPost, Speaker: timeline.User, Text: "寝る前の記録", At: at(0)},
		{ID: "pill", Kind: timeline.KindPost, Speaker: timeline.User, Text: "飲んだ", At: at(7)},
		{ID: "coffee", Kind: timeline.KindPost, Speaker: timeline.User, Text: "コーヒー", At: at(9)},
		{ID: "lunch", Kind: timeline.KindPost, Speaker: timeline.User, Text: "昼", At: at(13)},
	} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	set := New(Config{}).WithSearch(search.New(w)).WithZone("Asia/Tokyo").For(nil)
	run := func(args Args) (Result, error) { return set.Run(context.Background(), "timeline_search", args, nil) }

	// No words, only a window in the user's clock: the morning, newest first,
	// and no sources to hang on the remark.
	result, err := run(Args{"from": "2026-09-20 05:00", "to": "2026-09-20 12:00"})
	if err != nil || len(result.Sources) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(result.Text), &rows); err != nil {
		t.Fatal(err, result.Text)
	}
	if len(rows) != 2 || rows[0]["id"] != "coffee" || rows[1]["id"] != "pill" || rows[0]["at"] != "2026-09-20T09:00:00+09:00" {
		t.Fatalf("%v", rows)
	}
	// What it found is offered for citing, by id, with the line a link shows.
	if len(result.Citable) != 2 || result.Citable[1].ID != "pill" || result.Citable[1].Label != "2026-09-20 飲んだ" {
		t.Fatalf("citable = %+v", result.Citable)
	}
	// A date alone is the whole day; words inside a window still rank.
	if result, err = run(Args{"from": "2026-09-20", "to": "2026-09-20"}); err != nil || rowCount(result.Text) != 4 {
		t.Fatalf("%+v %v", result, err)
	}
	if result, err = run(Args{"query": "飲んだ", "from": "2026-09-20 05:00", "to": "2026-09-20 12:00"}); err != nil || rowCount(result.Text) != 1 || !strings.Contains(result.Text, `"pill"`) {
		t.Fatalf("%+v %v", result, err)
	}
	if result, err = run(Args{"query": "飲んだ", "from": "2026-09-20 08:00"}); err != nil || rowCount(result.Text) != 0 || !strings.Contains(result.Text, "期間を変えて") {
		t.Fatalf("%+v %v", result, err)
	}
	if result, err = run(Args{"from": "2026-09-21"}); err != nil || rowCount(result.Text) != 0 || !strings.Contains(result.Text, "その期間に記録はありませんでした") {
		t.Fatalf("%+v %v", result, err)
	}
	// An offset in the text wins over the user's zone.
	if result, err = run(Args{"from": "2026-09-19T20:00:00Z", "to": "2026-09-20T03:00:00Z"}); err != nil || rowCount(result.Text) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, args := range []Args{{}, {"query": ""}, {"from": "today"}, {"from": "2026-09-20 12:00", "to": "2026-09-20 05:00"}} {
		if _, err := run(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestSearchHandsBackUpToThirtyAndSaysWhatIsLeft(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		if err := w.Append(timeline.Entry{ID: fmt.Sprintf("e%02d", i), Kind: timeline.KindPost, Speaker: timeline.User, Text: "薬 " + strconv.Itoa(i), At: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	set := New(Config{}).WithSearch(search.New(w)).For(nil)
	run := func(args Args) (Result, error) { return set.Run(context.Background(), "timeline_search", args, nil) }

	result, err := run(Args{"from": "2026-09-20"})
	if err != nil || rowCount(result.Text) != 30 || !strings.HasPrefix(result.Text, "全40件のうち30件。") {
		t.Fatalf("%d %v %.80s", rowCount(result.Text), err, result.Text)
	}
	if result, err = run(Args{"query": "薬", "mode": "keyword", "limit": 100}); err != nil || rowCount(result.Text) != 30 {
		t.Fatalf("%d %v", rowCount(result.Text), err)
	}
	if result, err = run(Args{"query": "薬", "mode": "keyword"}); err != nil || rowCount(result.Text) != 10 {
		t.Fatalf("%d %v", rowCount(result.Text), err)
	}
}
