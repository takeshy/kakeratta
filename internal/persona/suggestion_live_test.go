package persona

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Explicit opt-in: a real daily pass and check-in on a temporary workspace,
// to see that a gathered subject reaches the user as a post with buttons.
func TestPersonaSuggestionLive(t *testing.T) {
	if os.Getenv("KAKERATTA_SUGGESTION_LIVE") != "1" {
		t.Skip("set KAKERATTA_SUGGESTION_LIVE=1 and KAKERATTA_ROUTE_KEY_FILE to run a paid check-in")
	}
	key := liveKey(t)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.BaseURL, settings.APIKey, settings.Active, settings.TimeZone = store.NebiusBaseURL, key, []string{"gourmet"}, "Asia/Tokyo"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	r := New(s, NewCatalogue(s), func() (*llm.Client, store.Settings) { return llm.New(store.NebiusBaseURL, key), s.Settings() })
	zone := s.Settings().Location()
	day := checkInDay(s, time.Now().In(zone).Truncate(24*time.Hour), "gourmet")
	start := day.Add(-20 * 24 * time.Hour)
	m := memo{CheckedAt: start, Facts: []fact{{ID: "f_ramen", Text: "豚骨ラーメンが好き", From: []string{"e_r0"}}}}
	nuka := []string{"ぬか床を仕込んだ", "ぬか床に昆布と唐辛子を足した", "きゅうりのぬか漬けが酸っぱくなりすぎた", "ぬか床の水抜きをした",
		"白菜の浅漬けを試した", "自家製味噌を仕込んだ", "ヨーグルトメーカーで甘酒を作った", "味噌の天地返しをした",
		"ぬか床に足しぬかをした", "キムチを漬けた", "塩麹を仕込んだ", "甘酒を豆乳で割った"}
	for i, text := range nuka {
		m.Records = append(m.Records, record{At: start.Add(time.Duration(i+1) * 36 * time.Hour), ID: fmt.Sprintf("e_n%d", i), Summary: text, Tags: []string{"発酵", "漬物"}})
	}
	m.Records = append(m.Records, record{At: start.Add(time.Hour), ID: "e_r1", Summary: "三田の二郎に並んだ", Tags: []string{"ラーメン"}})
	if err := s.WriteNote("gourmet", MemoPath, memoText(m, zone)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r.dailyAt(ctx, store.CheckInCloses(day).Add(-time.Minute))
	got := r.loadMemo("gourmet", zone)
	t.Logf("SuggestedAt=%v facts=%d records=%d", got.SuggestedAt, len(got.Facts), len(got.Records))
	said, _ := s.SpeakerEntries(ctx, "gourmet")
	for _, e := range said {
		t.Logf("POST %s suggestion=%+v\n%s", e.ID, e.Suggestion, e.Text)
	}
	if got.SuggestedAt.IsZero() {
		t.Fatal("the daily pass suggested no new persona")
	}
	if len(said) == 0 || said[len(said)-1].Suggestion == nil {
		t.Fatal("the check-in post carries no suggestion")
	}
}

// Explicit opt-in: once the suggesting persona's condition leaves the subject
// out, a post on it is routed to the new persona alone.
func TestSuggestedPersonaRoutingLive(t *testing.T) {
	if os.Getenv("KAKERATTA_SUGGESTION_LIVE") != "1" {
		t.Skip("set KAKERATTA_SUGGESTION_LIVE=1 and KAKERATTA_ROUTE_KEY_FILE to run paid routing")
	}
	key := liveKey(t)
	var gourmet Skill
	for _, skill := range Builtins {
		if skill.ID == "gourmet" {
			gourmet = skill
		}
	}
	gourmet.Domain += "ただし、発酵食品を自分で仕込む話は除く。"
	hakko := Skill{ID: "hakko", Name: "発酵食", Icon: "🫙", Domain: "ぬか床・味噌・麹・漬物など、発酵食品を自分で仕込む話や写真。"}
	cases := []struct {
		text string
		want []string
	}{
		{"ぬか床に昆布と唐辛子を足した", []string{"hakko"}},
		{"今年も味噌を仕込んだ。天地返しは来月", []string{"hakko"}},
		{"三田の二郎に並んだ。豚骨が濃かった", []string{"gourmet"}},
		{"今夜は何を食べよう", []string{"gourmet"}},
	}
	model := store.NemotronSuper
	client := llm.New(store.NebiusBaseURL, key)
	for i, tc := range cases {
		entry := timeline.Entry{ID: fmt.Sprintf("r%d", i), Kind: timeline.KindPost, Speaker: timeline.User, Text: tc.text, At: time.Now()}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		got := Route(ctx, client, model, []Skill{gourmet, hakko}, []Entryish{entry}, entry)
		cancel()
		t.Logf("%s → %v %v", tc.text, got.Deliver, got.Excluded)
		if !slices.Equal(got.Deliver, tc.want) {
			t.Errorf("%s: delivered to %v, want %v", tc.text, got.Deliver, tc.want)
		}
	}
}
