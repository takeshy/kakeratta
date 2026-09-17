package persona

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Explicit opt-in: ordinary tests never spend API credits. Only synthetic
// posts are sent; no runner, workspace writes, or persona tools are involved.
func TestRouteLiveComparison(t *testing.T) {
	if os.Getenv("KAKERATTA_ROUTE_LIVE") != "1" {
		t.Skip("set KAKERATTA_ROUTE_LIVE=1 and NEBIUS_API_KEY to run paid routing comparisons")
	}
	key := os.Getenv("NEBIUS_API_KEY")
	if path := os.Getenv("KAKERATTA_ROUTE_KEY_FILE"); key == "" && path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		key = strings.TrimSpace(string(raw))
	}
	if path := os.Getenv("KAKERATTA_ROUTE_SETTINGS"); key == "" && path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var settings store.Settings
		if err := json.Unmarshal(raw, &settings); err != nil {
			t.Fatal("cannot decode settings")
		}
		if strings.TrimRight(settings.BaseURL, "/") != store.NebiusBaseURL {
			t.Fatal("settings must use the Nebius endpoint")
		}
		key = settings.APIKey
	}
	if key == "" {
		t.Fatal("NEBIUS_API_KEY, KAKERATTA_ROUTE_KEY_FILE, or KAKERATTA_ROUTE_SETTINGS is required")
	}

	cases := []struct {
		name, original, text string
		want                 []string
	}{
		{"travel", "", "渋谷まで歩いて行く経路を知りたい", []string{"move"}},
		{"food", "", "お腹すいた。おすすめのラーメン屋ある？", []string{"gourmet"}},
		{"book", "", "夏目漱石のこころを読み終えた。先生の孤独が印象に残った", []string{"book"}},
		{"routine", "", "今日の20時30分からドラマを見る", []string{"routine"}},
		{"no-domain", "", "Goのコンパイラの仕組みを調べている", nil},
		{"keyword-only", "", "渋谷という変数名を使ったプログラムを書いた", nil},
		{"vague-destination", "", "17時に買い物に行く", []string{"routine"}},
		{"multiple", "", "渋谷まで歩く経路と、渋谷のおすすめのラーメン屋を教えて", []string{"move", "gourmet"}},
		{"followup", "渋谷まで歩いて行く経路を知りたい", "やっぱり自転車で", []string{"move"}},
		{"correction", "今日の20時30分からドラマを見る", "21時に変更する", []string{"routine"}},
		{"domain-change", "渋谷まで歩いて行く経路を知りたい", "移動の相談は取り消し。代わりに読書の話。こころを読み終えた", []string{"book"}},
		{"wine-word-only", "", "「ワイン」という名前の変数でビルドが失敗した", nil},
	}
	// USD per million tokens, supplied by the user on 2026-09-17.
	models := []struct {
		id      string
		in, out float64
	}{
		{store.NemotronLightning, 0.06, 0.24},
		{store.NemotronUltra, 1.00, 3.00},
	}
	for _, model := range models {
		t.Run(model.id, func(t *testing.T) {
			var input, output, correct int
			var elapsed time.Duration
			for _, tc := range cases {
				at := time.Date(2026, 9, 17, 9, 0, 0, 0, time.FixedZone("JST", 9*3600))
				entry := timeline.Entry{ID: tc.name, Kind: timeline.KindPost, Speaker: timeline.User, Text: tc.text, At: at}
				thread := []Entryish{entry}
				if tc.original != "" {
					original := entry
					original.ID, original.Text = tc.name+"-root", tc.original
					entry.Kind, entry.Root, entry.ReplyTo = timeline.KindReply, original.ID, original.ID
					entry.At = at.Add(time.Minute)
					thread = []Entryish{original, entry}
				}
				meter := &routeMeter{}
				client := llm.New(store.NebiusBaseURL, key)
				client.HTTP.Transport = meter
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				start := time.Now()
				got := Route(ctx, client, model.id, Builtins, thread, entry)
				duration := time.Since(start)
				cancel()
				if meter.err != nil || meter.input == 0 {
					// Stop on API errors rather than paying for the rest of a broken run.
					t.Fatalf("%s: API/usage failure: %v", tc.name, meter.err)
				}
				want := slices.Clone(tc.want)
				slices.Sort(got)
				slices.Sort(want)
				match := slices.Equal(got, want)
				if match {
					correct++
				} else {
					t.Errorf("%s: got=%v want=%v", tc.name, got, want)
				}
				cost := (float64(meter.input)*model.in + float64(meter.output)*model.out) / 1e6
				t.Logf("CASE %s match=%t got=%v input=%d output=%d USD=%.6f seconds=%.2f",
					tc.name, match, got, meter.input, meter.output, cost, duration.Seconds())
				input += meter.input
				output += meter.output
				elapsed += duration
			}
			cost := (float64(input)*model.in + float64(output)*model.out) / 1e6
			t.Logf("SUMMARY model=%s exact=%d/%d input=%d output=%d USD=%.6f USD/post=%.6f JPY/post@150=%.4f mean_seconds=%.2f",
				model.id, correct, len(cases), input, output, cost, cost/float64(len(cases)), cost*150/float64(len(cases)), elapsed.Seconds()/float64(len(cases)))
		})
	}
}

// Capture API usage without changing the production request or logging keys.
type routeMeter struct {
	input, output int
	err           error
}

func (m *routeMeter) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		m.err = err
		return nil, err
	}
	raw, err := io.ReadAll(res.Body)
	res.Body.Close()
	res.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		m.err = err
		return res, err
	}
	if res.StatusCode != http.StatusOK {
		m.err = fmt.Errorf("HTTP %d", res.StatusCode)
		return res, nil
	}
	var body struct {
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	m.err = json.Unmarshal(raw, &body)
	if len(body.Choices) == 0 && m.err == nil {
		m.err = fmt.Errorf("empty choices")
	}
	if m.err == nil {
		message := body.Choices[0].Message
		chosen := firstJSON(message.Content)
		for _, call := range message.ToolCalls {
			if call.Function.Name == deliverTool.Name {
				chosen = call.Function.Arguments
			}
		}
		var decision struct {
			Deliver []string `json:"deliver"`
		}
		if json.Unmarshal([]byte(chosen), &decision) != nil || decision.Deliver == nil {
			m.err = fmt.Errorf("invalid routing response")
		}
	}
	m.input, m.output = body.Usage.Input, body.Usage.Output
	return res, nil
}
