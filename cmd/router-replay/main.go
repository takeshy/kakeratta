// router-replay compares answer-stage transcripts without writing workspace state.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

type replayCase struct {
	ID               string        `json:"id"`
	Messages         []llm.Message `json:"messages"`
	PreviousAnswer   string        `json:"previousAnswer"`
	Reply            string        `json:"reply"`
	AlwaysUltra      bool          `json:"alwaysUltra"`
	ExpectedContains []string      `json:"expectedContains"`
}
type price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}
type call struct {
	Model         string `json:"model"`
	Input         int    `json:"inputTokens"`
	Output        int    `json:"outputTokens"`
	UsageReported bool   `json:"usageReported"`
	Status        int    `json:"status"`
}
type meter struct{ calls []call }

func (m *meter) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(res.Body)
	res.Body.Close()
	res.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Model string `json:"model"`
		Usage *struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &parsed)
	item := call{Model: parsed.Model, Status: res.StatusCode, UsageReported: parsed.Usage != nil}
	if item.Model == "" && req.GetBody != nil {
		body, bodyErr := req.GetBody()
		if bodyErr == nil {
			var request struct{ Model string }
			_ = json.NewDecoder(body).Decode(&request)
			body.Close()
			item.Model = request.Model
		}
	}
	if parsed.Usage != nil {
		item.Input, item.Output = parsed.Usage.Input, parsed.Usage.Output
	}
	m.calls = append(m.calls, item)
	return res, nil
}
func measured(original *llm.Client, m *meter) *llm.Client {
	clone := *original
	client := *original.HTTP
	client.Transport = m
	clone.HTTP = &client
	return &clone
}

type result struct {
	ID           string          `json:"id"`
	Mode         string          `json:"mode"`
	Seconds      float64         `json:"seconds"`
	Answer       *persona.Answer `json:"answer,omitempty"`
	Error        string          `json:"error,omitempty"`
	Calls        []call          `json:"calls"`
	Missing      []string        `json:"missingExpectedText,omitempty"`
	EstimatedUSD *float64        `json:"estimatedVisibleUSD"`
	CostScope    string          `json:"costScope"`
}

func evaluate(ctx context.Context, tc replayCase, mode string, router persona.AnswerRouter, provider *llm.Client, settings store.Settings, prices map[string]price) result {
	m := &meter{}
	provider = measured(provider, m)
	if router.Client != nil {
		router.Client = measured(router.Client, m)
	}
	out := result{ID: tc.ID, Mode: mode, CostScope: "visible completion usage only; Switchyard internal judge calls excluded"}
	start := time.Now()
	answer, err := persona.ReplayAnswer(ctx, router, provider, settings, tc.Messages, tc.PreviousAnswer, tc.Reply, tc.AlwaysUltra)
	out.Seconds, out.Answer, out.Calls = time.Since(start).Seconds(), answer, m.calls
	if err != nil {
		out.Error = err.Error()
	}
	if answer != nil {
		for _, expected := range tc.ExpectedContains {
			if !strings.Contains(answer.Text, expected) {
				out.Missing = append(out.Missing, expected)
			}
		}
	}
	known := err == nil && len(m.calls) > 0
	cost := 0.0
	for _, item := range m.calls {
		p, ok := prices[item.Model]
		if !ok || !item.UsageReported || item.Status >= 300 {
			known = false
		}
		cost += (float64(item.Input)*p.Input + float64(item.Output)*p.Output) / 1e6
	}
	if known {
		out.EstimatedUSD = &cost
	}
	return out
}

func run() error {
	input := flag.String("input", "", "JSONL answer-stage cases (required)")
	pricesPath := flag.String("prices", "", "JSON model -> input/output USD per million tokens; optional")
	mode := flag.String("mode", "both", "lightning, switchyard, or both")
	flag.Parse()
	if *input == "" {
		return fmt.Errorf("-input is required")
	}
	if *mode != "both" && *mode != "lightning" && *mode != "switchyard" {
		return fmt.Errorf("invalid -mode")
	}
	prices := map[string]price{}
	if *pricesPath != "" {
		raw, err := os.ReadFile(*pricesPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &prices); err != nil {
			return err
		}
		for model, p := range prices {
			if p.Input < 0 || p.Output < 0 {
				return fmt.Errorf("negative price for %s", model)
			}
		}
	}
	f, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer f.Close()
	// Validate the entire dataset before making any paid requests.
	var cases []replayCase
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	ids := map[string]bool{}
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var tc replayCase
		if err := json.Unmarshal(scanner.Bytes(), &tc); err != nil {
			return fmt.Errorf("case %d: %w", len(cases)+1, err)
		}
		if strings.TrimSpace(tc.ID) == "" || len(tc.Messages) == 0 || ids[tc.ID] {
			return fmt.Errorf("cases require unique ids and nonempty messages")
		}
		ids[tc.ID] = true
		cases = append(cases, tc)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(cases) == 0 {
		return fmt.Errorf("no cases")
	}
	var router persona.AnswerRouter
	if *mode != "lightning" {
		// The command chooses both modes independently of the app's ROUTER_MODE.
		if err := os.Setenv("ROUTER_MODE", "switchyard"); err != nil {
			return err
		}
		router, err = persona.AnswerRouterFromEnv()
		if err != nil {
			return err
		}
	}
	settings := store.Settings{LowModel: store.NemotronLightning, MiddleModel: store.NemotronSuper, HighModel: store.NemotronUltra}
	provider := llm.New(first(os.Getenv("REPLAY_BASE_URL"), store.NebiusBaseURL), os.Getenv("REPLAY_API_KEY"))
	settings.LowModel = first(os.Getenv("REPLAY_LIGHTNING_MODEL"), settings.LowModel)
	settings.MiddleModel = first(os.Getenv("REPLAY_SUPER_MODEL"), settings.MiddleModel)
	settings.HighModel = first(os.Getenv("REPLAY_ULTRA_MODEL"), settings.HighModel)
	encoder := json.NewEncoder(os.Stdout)
	failed := false
	for i, tc := range cases {
		modes := []string{*mode}
		if *mode == "both" {
			modes = []string{"lightning", "switchyard"}
			if i%2 == 1 {
				modes = []string{"switchyard", "lightning"}
			}
		}
		for _, current := range modes {
			selected := persona.AnswerRouter{}
			if current == "switchyard" {
				selected = router
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			out := evaluate(ctx, tc, current, selected, provider, settings, prices)
			cancel()
			if err := encoder.Encode(out); err != nil {
				return err
			}
			if out.Error != "" {
				failed = true
			}
		}
	}
	if failed {
		return fmt.Errorf("some completions failed; inspect JSONL errors")
	}
	return nil
}
func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
