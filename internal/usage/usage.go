// Package usage keeps per-workspace monthly accounting without prompt contents.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
)

// Prices are USD per million tokens, reviewed 2026-10-09 against:
// https://nebius.com/services/token-factory/models/nvidia-nemotron-models-inference
// https://nebius.com/services/token-factory/models/qwen-models-inference
// https://nebius.com/services/token-factory/models/glm-models-inference
// Super VL was added at the same Super rate, verified 2026-10-16.
var nebiusRates = map[string][2]float64{
	strings.ToLower(store.NemotronLightning): {0.06, 0.24},
	strings.ToLower(store.NemotronNano):      {0.06, 0.24},
	strings.ToLower(store.NemotronSuper):     {0.30, 0.90},
	"nvidia/nemotron-3-super-120b-a12b":      {0.30, 0.90},
	strings.ToLower(store.NemotronUltra):     {1, 3},
	strings.ToLower(store.NebiusVision):      {0.15, 0.50},
	"qwen/qwen3-embedding-8b":                {0.01, 0},
}

type Counts struct {
	Calls          int     `json:"calls"`
	Failures       int     `json:"failures"`
	EstimatedUSD   float64 `json:"estimatedUsd"`
	ReportedUSD    float64 `json:"reportedUsd"`
	Unpriced       int     `json:"unpriced"`
	Searches       int     `json:"searches"`
	SearchFailures int     `json:"searchFailures"`
}

type Row struct {
	Persona   string `json:"persona"`
	Category  string `json:"category"`
	Component string `json:"component"`
	Counts
}

type Month struct {
	Month string    `json:"month"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	Rows  []Row     `json:"rows"`
}

func Category(wake string) string {
	switch wake {
	case "post", "listening", "post-edited", "review-reply":
		return "reply"
	case "daily", "schedule":
		return "scheduled"
	default:
		return "other"
	}
}

// Cost returns known billed cost or a list-price estimate. Missing counts and
// unsupported models stay unknown; reasoning/cache details are not added again.
func Cost(r llm.CallRecord) (float64, bool, bool) {
	if r.Usage == nil {
		return 0, false, false
	}
	if r.Provider == "openrouter" && r.Usage.Cost != nil {
		cost := *r.Usage.Cost
		if cost >= 0 && !math.IsNaN(cost) && !math.IsInf(cost, 0) {
			return cost, true, true
		}
		return 0, false, false
	}
	rates, ok := nebiusRates[strings.ToLower(r.Model)]
	if r.Provider != "nebius" || !ok || r.Usage.Input == nil || *r.Usage.Input < 0 {
		return 0, false, false
	}
	output := 0
	if rates[1] > 0 {
		if r.Usage.Output == nil || *r.Usage.Output < 0 {
			return 0, false, false
		}
		output = *r.Usage.Output
	}
	return (float64(*r.Usage.Input)*rates[0] + float64(output)*rates[1]) / 1e6, true, false
}

func Record(w store.Workspace, r llm.CallRecord) error {
	delta := Counts{Calls: 1}
	if r.Outcome != "success" {
		delta.Failures = 1
	}
	cost, known, reported := Cost(r)
	if !known {
		delta.Unpriced = 1
	} else if reported {
		delta.ReportedUSD = cost
	} else {
		delta.EstimatedUSD = cost
	}
	trace := r.Trace
	component := "persona"
	switch {
	case r.Purpose == "routing":
		component = "routing"
		trace.Persona = ""
	case r.Purpose == "embedding":
		component = "embedding"
		trace.Persona = ""
	case trace.Persona == "":
		component = "other"
	}
	return add(w, time.Now(), trace, component, delta)
}

func RecordSearch(w store.Workspace, trace llm.Trace, failed bool) error {
	delta := Counts{Searches: 1}
	if failed {
		delta.SearchFailures = 1
	}
	component := "persona"
	if trace.Persona == "" {
		component = "other"
	}
	return add(w, time.Now(), trace, component, delta)
}

func add(w store.Workspace, at time.Time, trace llm.Trace, component string, delta Counts) error {
	month := at.In(w.Settings().Location()).Format("2006-01")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return w.UpdateRecord(ctx, store.RecordUsage, month, time.Time{}, func(raw []byte, exists bool) ([]byte, error) {
		data := Month{Month: month, First: at}
		if exists {
			if err := json.Unmarshal(raw, &data); err != nil {
				return nil, err
			}
		}
		data.Last = at
		category := Category(trace.Wake)
		index := -1
		for i, row := range data.Rows {
			if row.Persona == trace.Persona && row.Category == category && row.Component == component {
				index = i
				break
			}
		}
		if index < 0 {
			data.Rows = append(data.Rows, Row{Persona: trace.Persona, Category: category, Component: component})
			index = len(data.Rows) - 1
		}
		c := &data.Rows[index].Counts
		c.Calls += delta.Calls
		c.Failures += delta.Failures
		c.EstimatedUSD += delta.EstimatedUSD
		c.ReportedUSD += delta.ReportedUSD
		c.Unpriced += delta.Unpriced
		c.Searches += delta.Searches
		c.SearchFailures += delta.SearchFailures
		return json.Marshal(data)
	})
}

func Read(ctx context.Context, w store.Workspace, month string) (Month, error) {
	if _, err := time.Parse("2006-01", month); err != nil {
		return Month{}, errors.New("invalid usage month")
	}
	raw, exists, err := w.Record(ctx, store.RecordUsage, month)
	data := Month{Month: month, Rows: []Row{}}
	if err == nil && exists {
		err = json.Unmarshal(raw, &data)
	}
	return data, err
}
