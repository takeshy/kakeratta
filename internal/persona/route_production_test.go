package persona

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
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
	skills := NewCatalogue(workspace.SkillDir()).Active(settings.Active)
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
	key, err := os.ReadFile(os.Getenv("KAKERATTA_ROUTE_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	type measurement struct {
		Error         string
		Model         string
		Targets       []string
		Input, Output int
		USD, Seconds  float64
	}
	type result struct {
		ID, Text     string
		Measurements []measurement
	}
	var results []result
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
	agreement := 0
	for _, row := range results {
		if row.Measurements[0].Error == "" && row.Measurements[1].Error == "" && slices.Equal(row.Measurements[0].Targets, row.Measurements[1].Targets) {
			agreement++
		}
	}
	for n, trigger := range triggers {
		if n < len(results) {
			continue
		}
		thread := timeline.Thread(entries, timeline.RootOf(trigger))
		// Never let a model see answers or follow-ups after its trigger.
		for i, entry := range thread {
			if entry.ID == trigger.ID {
				thread = thread[:i+1]
				break
			}
		}
		row := result{ID: trigger.ID, Text: trigger.Text}
		models := []struct {
			id      string
			in, out float64
		}{
			{store.NemotronLightning, .06, .24},
			{store.NemotronUltra, 1, 3},
		}
		// Alternate order to reduce systematic warm-up/order effects.
		if n%2 == 1 {
			slices.Reverse(models)
		}
		for _, model := range models {
			meter := &routeMeter{}
			api := llm.New(store.NebiusBaseURL, strings.TrimSpace(string(key)))
			api.HTTP.Transport = meter
			callCtx, stop := context.WithTimeout(ctx, 90*time.Second)
			start := time.Now()
			targets := Route(callCtx, api, model.id, skills, thread, trigger).Deliver
			elapsed := time.Since(start)
			stop()
			if (meter.err != nil && meter.err.Error() != "invalid routing response") || meter.input == 0 {
				t.Fatalf("case %d API/usage failure: %v", n+1, meter.err)
			}
			failure := ""
			if meter.err != nil {
				failure = meter.err.Error()
			}
			slices.Sort(targets)
			row.Measurements = append(row.Measurements, measurement{
				Error: failure, Model: model.id, Targets: targets, Input: meter.input, Output: meter.output,
				USD:     (float64(meter.input)*model.in + float64(meter.output)*model.out) / 1e6,
				Seconds: elapsed.Seconds(),
			})
		}
		same := row.Measurements[0].Error == "" && row.Measurements[1].Error == "" && slices.Equal(row.Measurements[0].Targets, row.Measurements[1].Targets)
		if same {
			agreement++
		}
		results = append(results, row)
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
	t.Logf("AGREEMENT %d/%d (agreement is not ground-truth accuracy)", agreement, len(results))
	for _, model := range []string{store.NemotronLightning, store.NemotronUltra} {
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
		t.Logf("SUMMARY model=%s cases=%d input=%d output=%d USD=%.6f JPY/route@150=%.4f mean_seconds=%.2f",
			model, len(results), input, output, usd, usd*150/float64(len(results)), seconds/float64(len(results)))
	}
}
