package usage

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestCostPreservesUnknownAndZeroAndUsesReportedOpenRouterCost(t *testing.T) {
	integer := func(v int) *int { return &v }
	decimal := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name            string
		record          llm.CallRecord
		amount          float64
		known, reported bool
	}{
		{"missing", llm.CallRecord{Provider: "nebius", Model: store.NemotronSuper}, 0, false, false},
		{"partial", llm.CallRecord{Provider: "nebius", Model: store.NemotronSuper, Usage: &llm.Usage{Input: integer(100)}}, 0, false, false},
		{"zero", llm.CallRecord{Provider: "nebius", Model: store.NemotronSuper, Usage: &llm.Usage{Input: integer(0), Output: integer(0)}}, 0, true, false},
		{"nebius", llm.CallRecord{Provider: "nebius", Model: store.NemotronSuper, Usage: &llm.Usage{Input: integer(1000), Output: integer(100)}}, 0.00039, true, false},
		{"embedding", llm.CallRecord{Provider: "nebius", Model: "Qwen/Qwen3-Embedding-8B", Usage: &llm.Usage{Input: integer(1000)}}, 0.00001, true, false},
		{"custom", llm.CallRecord{Provider: "custom", Model: store.NemotronSuper, Usage: &llm.Usage{Input: integer(100), Output: integer(10), Cost: decimal(1)}}, 0, false, false},
		{"openrouter", llm.CallRecord{Provider: "openrouter", Model: "auto", Usage: &llm.Usage{Cost: decimal(0.123)}}, 0.123, true, true},
		{"openrouter-free", llm.CallRecord{Provider: "openrouter", Usage: &llm.Usage{Cost: decimal(0)}}, 0, true, true},
		{"openrouter-unknown", llm.CallRecord{Provider: "openrouter", Model: store.NemotronSuper, Usage: &llm.Usage{Input: integer(100), Output: integer(10)}}, 0, false, false},
		{"invalid-cost", llm.CallRecord{Provider: "openrouter", Usage: &llm.Usage{Cost: decimal(-1)}}, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			amount, known, reported := Cost(tc.record)
			if math.Abs(amount-tc.amount) > 1e-12 || known != tc.known || reported != tc.reported {
				t.Fatalf("got %v %v %v", amount, known, reported)
			}
		})
	}
}

func TestMonthlyAccountingConcurrentUpdatesAndRestart(t *testing.T) {
	root := t.TempDir()
	w, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var pending sync.WaitGroup
	for i := 0; i < 25; i++ {
		pending.Add(1)
		go func() {
			defer pending.Done()
			if err := Record(w, llm.CallRecord{Trace: llm.Trace{Persona: "gourmet", Wake: "post"}, Outcome: "success"}); err != nil {
				t.Error(err)
			}
		}()
	}
	pending.Wait()
	if err := RecordSearch(w, llm.Trace{Persona: "gourmet", Wake: "daily"}, true); err != nil {
		t.Fatal(err)
	}
	if err := Record(w, llm.CallRecord{Trace: llm.Trace{Persona: "gourmet", Wake: "post"}, Purpose: "embedding", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := Record(w, llm.CallRecord{Purpose: "routing", Outcome: "transport_error"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Read(context.Background(), reopened, time.Now().In(w.Settings().Location()).Format("2006-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Rows) != 4 {
		t.Fatalf("rows: %+v", data.Rows)
	}
	for _, row := range data.Rows {
		switch row.Component {
		case "embedding":
			if row.Persona != "" {
				t.Fatal("embedding assigned to a persona")
			}
		case "routing":
			if row.Failures != 1 {
				t.Fatal("failure omitted")
			}
		case "persona":
			if row.Category == "reply" && (row.Calls != 25 || row.Unpriced != 25) {
				t.Fatalf("lost updates: %+v", row)
			}
			if row.Category == "scheduled" && (row.Searches != 1 || row.SearchFailures != 1) {
				t.Fatalf("search: %+v", row)
			}
		}
	}
}

func TestCalendarMonthUsesWorkspaceTimezone(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := w.Settings()
	settings.TimeZone = "Asia/Tokyo"
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	if err := add(w, at, llm.Trace{Persona: "gourmet", Wake: "schedule"}, "persona", Counts{Searches: 1}); err != nil {
		t.Fatal(err)
	}
	data, err := Read(context.Background(), w, "2026-10")
	if err != nil || len(data.Rows) != 1 {
		t.Fatalf("data=%+v err=%v", data, err)
	}
	empty, err := Read(context.Background(), w, "2026-09")
	if err != nil || len(empty.Rows) != 0 {
		t.Fatal("incorrect month")
	}
}
