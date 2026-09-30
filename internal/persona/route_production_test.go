package persona

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

type productionRouteMeasurement struct {
	Error         string
	Model         string
	Provider      string
	Targets       []string
	Input, Output int
	USD, Seconds  float64
}

type productionRouteResult struct {
	ID, Text     string
	Measurements []productionRouteMeasurement
}

// Read-only replay: never instantiate Runner or invoke persona tools.
// Current saved content/domains are used; historical edits are not recoverable.
func TestRouteProductionComparison(t *testing.T) {
	if os.Getenv("KAKERATTA_ROUTE_PRODUCTION") != "1" {
		t.Skip("explicit production replay opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	token, err := exec.CommandContext(ctx, "gcloud", "auth", "print-access-token").Output()
	if err != nil {
		t.Fatal("cannot obtain gcloud access token")
	}
	client, err := firestore.NewClient(ctx, "kakeratta", option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: strings.TrimSpace(string(token))})))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	userID := os.Getenv("KAKERATTA_ROUTE_USER")
	if userID == "" {
		users, err := client.Collection("users").Limit(2).Documents(ctx).GetAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 1 {
			t.Fatalf("found %d users (query limited to 2); specify KAKERATTA_ROUTE_USER to avoid selecting another user's data", len(users))
		}
		userID = users[0].Ref.ID
	}
	workspace, err := store.NewFirestoreWorkspace(ctx, client, userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace.Root())
	config, err := client.Collection("users").Doc(userID).Collection("config").Doc("settings").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Read active IDs only; production credentials are never used.
	var settings struct{ Active []string }
	if err := config.DataTo(&settings); err != nil {
		t.Fatal(err)
	}
	skills := NewCatalogue(workspace).Active(context.Background(), settings.Active)
	if len(skills) == 0 {
		t.Fatal("no active personas")
	}
	entries, err := workspace.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	var triggers []timeline.Entry
	for _, entry := range entries {
		if entry.Speaker != timeline.User || entry.Deleted {
			continue
		}
		thread := timeline.Thread(entries, timeline.RootOf(entry))
		if len(thread) == 0 || thread[0].Deleted {
			continue
		}
		triggers = append(triggers, entry)
	}
	if len(triggers) > 20 {
		triggers = triggers[len(triggers)-20:]
	}
	t.Logf("DATASET active_personas=%d saved_entries=%d replay_triggers=%d", len(skills), len(entries), len(triggers))
	if os.Getenv("KAKERATTA_ROUTE_INSPECT_ONLY") == "1" {
		// Private local snapshot for manual review, including capture text and
		// thread context; no settings or credentials are included.
		raw, err := json.MarshalIndent(struct {
			Skills  []Skill
			Entries []timeline.Entry
		}{skills, entries}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("/tmp/kakeratta-production-routing-context.json", raw, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if len(triggers) == 0 {
		t.Fatal("no replayable posts")
	}
	var results []productionRouteResult
	if os.Getenv("KAKERATTA_ROUTE_RESUME") == "1" {
		raw, err := os.ReadFile("/tmp/kakeratta-production-routing.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &results); err != nil {
			t.Fatal(err)
		}
		for i, row := range results {
			if i >= len(triggers) || row.ID != triggers[i].ID {
				t.Fatal("dataset changed; cannot resume")
			}
		}
	}
	remeasureFrom := 1
	if value, err := strconv.Atoi(os.Getenv("KAKERATTA_ROUTE_REMEASURE_FROM")); err == nil && value > 0 {
		remeasureFrom = value
	}
	for n, trigger := range triggers {
		thread := timeline.Thread(entries, timeline.RootOf(trigger))
		// Never let a model see answers or follow-ups after its trigger.
		for i, entry := range thread {
			if entry.ID == trigger.ID {
				thread = thread[:i+1]
				break
			}
		}
		row := productionRouteResult{ID: trigger.ID, Text: trigger.Text}
		if n < len(results) {
			row = results[n]
		}
		models := []struct {
			provider, baseURL, keyFile, id string
			in, out                        float64
			fast                           bool
		}{
			{"nebius", store.NebiusBaseURL, os.Getenv("KAKERATTA_ROUTE_KEY_FILE"), store.NemotronLightning, .06, .24, true},
			{"nebius", store.NebiusBaseURL, os.Getenv("KAKERATTA_ROUTE_KEY_FILE"), store.NemotronNano, .06, .24, true},
			{"nebius", store.NebiusBaseURL, os.Getenv("KAKERATTA_ROUTE_KEY_FILE"), store.NemotronSuper, .30, .90, false},
			{"nebius", store.NebiusBaseURL, os.Getenv("KAKERATTA_ROUTE_KEY_FILE"), store.NemotronUltra, 1, 3, false},
			{"openrouter-decisions", store.OpenRouterBaseURL, os.Getenv("KAKERATTA_ROUTE_OPENROUTER_KEY_FILE"), "~typesafe/jev-latest", .042, 0, false},
			{"openrouter-chat", store.OpenRouterBaseURL, os.Getenv("KAKERATTA_ROUTE_OPENROUTER_KEY_FILE"), "~openai/gpt-luna-latest", .20, 1.20, false},
			{"openrouter-chat", store.OpenRouterBaseURL, os.Getenv("KAKERATTA_ROUTE_OPENROUTER_KEY_FILE"), "~openai/gpt-luna-latest#thinking-off", .20, 1.20, false},
		}
		only := strings.TrimSpace(os.Getenv("KAKERATTA_ROUTE_ONLY"))
		// Rotate order to reduce systematic warm-up/order effects.
		if shift := n % len(models); shift != 0 {
			models = append(models[shift:], models[:shift]...)
		}
		for _, model := range models {
			if only != "" && model.id != only {
				continue
			}
			if model.fast && os.Getenv("KAKERATTA_ROUTE_REMEASURE_FAST") == "1" && n+1 >= remeasureFrom {
				row.Measurements = slices.DeleteFunc(row.Measurements, func(measurement productionRouteMeasurement) bool {
					return measurement.Model == model.id
				})
			}
			alreadyMeasured := false
			for _, measurement := range row.Measurements {
				if measurement.Model == model.id {
					alreadyMeasured = true
					break
				}
			}
			if alreadyMeasured {
				continue
			}
			modelKey, err := os.ReadFile(model.keyFile)
			if err != nil {
				t.Fatal(err)
			}
			callCtx, stop := context.WithTimeout(ctx, 90*time.Second)
			start := time.Now()
			var targets []string
			var input, output int
			var actualCost float64
			var callErr error
			if model.provider == "openrouter-decisions" {
				targets, input, output, actualCost, callErr = routeWithJev(callCtx, strings.TrimSpace(string(modelKey)), model.id, skills, thread, trigger)
			} else {
				meter := &routeMeter{}
				api := llm.New(model.baseURL, strings.TrimSpace(string(modelKey)))
				if model.provider == "openrouter-chat" {
					api = llm.NewOpenRouter(model.baseURL, strings.TrimSpace(string(modelKey)))
				}
				thinkingOff := strings.HasSuffix(model.id, "#thinking-off")
				api.HTTP.Transport = &routeBenchmarkTransport{meter: meter, fast: model.fast, thinkingOff: thinkingOff}
				targets = Route(callCtx, api, strings.TrimSuffix(model.id, "#thinking-off"), skills, thread, trigger).Deliver
				input, output, callErr = meter.input, meter.output, meter.err
			}
			elapsed := time.Since(start)
			stop()
			if (callErr != nil && callErr.Error() != "invalid routing response") || input == 0 {
				t.Fatalf("case %d API/usage failure: %v", n+1, callErr)
			}
			failure := ""
			if callErr != nil {
				failure = callErr.Error()
			}
			cost := (float64(input)*model.in + float64(output)*model.out) / 1e6
			if actualCost > 0 {
				cost = actualCost
			}
			slices.Sort(targets)
			row.Measurements = append(row.Measurements, productionRouteMeasurement{
				Error: failure, Model: model.id, Provider: model.provider, Targets: targets, Input: input, Output: output,
				USD:     cost,
				Seconds: elapsed.Seconds(),
			})
		}
		if n < len(results) {
			results[n] = row
		} else {
			results = append(results, row)
		}
		same := measurementsAgree(row.Measurements)
		t.Logf("CASE %d agreement=%t", n+1, same)
		// Private checkpoint survives a later API failure; do not commit post text.
		raw, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("/tmp/kakeratta-production-routing.json", raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	agreement := 0
	for _, row := range results {
		if measurementsAgree(row.Measurements) {
			agreement++
		}
	}
	t.Logf("AGREEMENT %d/%d (agreement is not ground-truth accuracy)", agreement, len(results))
	for _, model := range []string{store.NemotronLightning, store.NemotronNano, store.NemotronSuper, store.NemotronUltra, "~typesafe/jev-latest", "~openai/gpt-luna-latest", "~openai/gpt-luna-latest#thinking-off"} {
		var input, output int
		var usd, seconds float64
		for _, row := range results {
			for _, m := range row.Measurements {
				if m.Model == model {
					input += m.Input
					output += m.Output
					usd += m.USD
					seconds += m.Seconds
				}
			}
		}
		cases := 0
		for _, row := range results {
			for _, measurement := range row.Measurements {
				if measurement.Model == model {
					cases++
					break
				}
			}
		}
		if cases > 0 {
			t.Logf("SUMMARY model=%s cases=%d input=%d output=%d USD=%.6f JPY/route@150=%.4f mean_seconds=%.2f",
				model, cases, input, output, usd, usd*150/float64(cases), seconds/float64(cases))
		}
	}
}

type routeBenchmarkTransport struct {
	meter       *routeMeter
	fast        bool
	thinkingOff bool
}

func (t *routeBenchmarkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.fast && !t.thinkingOff {
		return t.meter.RoundTrip(req)
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if t.fast {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
		body["max_tokens"] = 1024
		body["temperature"] = 0.1
	}
	if t.thinkingOff {
		body["reasoning"] = map[string]any{"enabled": false}
	}
	raw, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	return t.meter.RoundTrip(req)
}

func routeWithJev(ctx context.Context, key, model string, skills []Skill, thread []Entryish, trigger Entryish) ([]string, int, int, float64, error) {
	questions := map[string]any{}
	for _, skill := range skills {
		questions[skill.ID] = map[string]any{
			"type":         "choice",
			"instructions": routePrompt + "\nこの担当条件に今回の対象発言が一致するか判定してください。担当条件: " + skill.Domain,
			"criteria": map[string]any{
				"deliver": "担当条件に一致する",
				"exclude": "担当条件に一致しない",
			},
		}
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"state": strings.Join([]string{
			"スレッド全体（古い順）:", renderAll(thread), "",
			"今回の対象発言（この発言をスレッド全体の文脈で解釈）:", trigger.Render(nil, nil),
		}, "\n"),
		"questions": questions,
	})
	if err != nil {
		return nil, 0, 0, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/alpha/decisions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, 0, 0, 0, fmt.Errorf("HTTP %d: %s", res.StatusCode, raw)
	}
	var parsed struct {
		Answers map[string]struct {
			Choice string `json:"choice"`
		} `json:"answers"`
		Usage struct {
			Input  int     `json:"input_tokens"`
			Output int     `json:"output_tokens"`
			Cost   float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, 0, 0, err
	}
	var targets []string
	for _, skill := range skills {
		answer, ok := parsed.Answers[skill.ID]
		if !ok {
			return nil, parsed.Usage.Input, parsed.Usage.Output, parsed.Usage.Cost, fmt.Errorf("missing answer for %s", skill.ID)
		}
		if answer.Choice == "deliver" {
			targets = append(targets, skill.ID)
		}
	}
	return targets, parsed.Usage.Input, parsed.Usage.Output, parsed.Usage.Cost, nil
}

func measurementsAgree(measurements []productionRouteMeasurement) bool {
	if len(measurements) < 2 || measurements[0].Error != "" {
		return false
	}
	for _, measurement := range measurements[1:] {
		if measurement.Error != "" || !slices.Equal(measurements[0].Targets, measurement.Targets) {
			return false
		}
	}
	return true
}
