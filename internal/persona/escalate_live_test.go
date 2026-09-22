package persona

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// How long the escalation judgement adds to a reply, and how often it is right,
// on Lightning (what it runs on) and on Super (the alternative). Explicit
// opt-in, like the routing comparison: ordinary tests never spend API credits.
// Only synthetic text is sent.
func TestEscalateLiveLatency(t *testing.T) {
	if os.Getenv("KAKERATTA_ESCALATE_LIVE") != "1" {
		t.Skip("set KAKERATTA_ESCALATE_LIVE=1 and NEBIUS_API_KEY to run paid escalation measurements")
	}
	key := liveKey(t)

	answer := "神谷町なら駅前の「とん平」が豚骨で、11時から開いています。"
	cases := []struct {
		reply string
		want  bool
	}{
		{"違う、そこはもう閉店してる", true},
		{"本当？", true},
		{"もう一度考えて", true},
		{"もっと詳しく", true},
		{"深掘りして", true},
		{"ちゃんと調べて", true},
		{"ほかの店と比較して詳しく", true},
		{"根拠は？", true},
		{"ありがとう", false},
		{"なるほど", false},
		{"駅から近い店は？", false},
		{"1時間後。", false},
		{"辛いのもある？", false},
		{"そういえば明日雨らしい", false},
		{"行ってきた。うまかった", false},
	}
	// USD per million tokens, as in the 2026-09-19 routing comparison.
	models := []struct {
		id      string
		in, out float64
	}{
		{store.NemotronLightning, 0.06, 0.24},
		{store.NemotronSuper, 0.30, 0.90},
	}
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("JST", 9*3600))
	remark := timeline.Entry{ID: "answer", Kind: timeline.KindRemark, Speaker: "gourmet", Text: answer, At: at}
	for _, model := range models {
		t.Run(model.id, func(t *testing.T) {
			var input, output, correct int
			var elapsed, slowest time.Duration
			for _, tc := range cases {
				reply := timeline.Entry{ID: "reply", Kind: timeline.KindReply, Speaker: timeline.User, ReplyTo: remark.ID, Text: tc.reply, At: at.Add(time.Minute)}
				meter := &usageMeter{}
				client := llm.New(store.NebiusBaseURL, key)
				client.HTTP.Transport = meter
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				start := time.Now()
				got := Escalate(ctx, client, model.id, remark, reply)
				duration := time.Since(start)
				cancel()
				if meter.err != nil || meter.input == 0 {
					t.Fatalf("%q: API/usage failure: %v", tc.reply, meter.err)
				}
				if got == tc.want {
					correct++
				} else {
					t.Errorf("%q: got=%t want=%t", tc.reply, got, tc.want)
				}
				t.Logf("CASE %q escalate=%t want=%t input=%d output=%d seconds=%.2f", tc.reply, got, tc.want, meter.input, meter.output, duration.Seconds())
				input += meter.input
				output += meter.output
				elapsed += duration
				slowest = max(slowest, duration)
			}
			cost := (float64(input)*model.in + float64(output)*model.out) / 1e6
			t.Logf("SUMMARY model=%s correct=%d/%d mean_seconds=%.2f max_seconds=%.2f JPY/call@150=%.4f",
				model.id, correct, len(cases), elapsed.Seconds()/float64(len(cases)), slowest.Seconds(), cost*150/float64(len(cases)))
		})
	}
}

// usageMeter records token usage and an API error, without judging the reply:
// a wrong judgement is what the test counts, not a failure of the call.
type usageMeter struct {
	input, output int
	err           error
}

func (m *usageMeter) RoundTrip(req *http.Request) (*http.Response, error) {
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
	}
	m.err = json.Unmarshal(raw, &body)
	m.input, m.output = body.Usage.Input, body.Usage.Output
	return res, nil
}
