package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/usage"
)

func TestUsageAPIIncludesCommonCostsAndFiltersPersona(t *testing.T) {
	s, w := app(t)
	cost := 0.0123
	client, _ := s.client()
	record := llm.NewCallRecord(llm.WithTrace(context.Background(), llm.Trace{Persona: "gourmet", Wake: "post"}), "https://openrouter.ai/api/v1", "some-model", "answer", 1, 1)
	record.Outcome = "success"
	record.Usage = &llm.Usage{Cost: &cost}
	client.Observe(*record)
	if err := usage.Record(w, llm.CallRecord{Purpose: "embedding", Trace: llm.Trace{Persona: "gourmet"}, Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	request := func(query string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		s.ServeHTTP(out, httptest.NewRequest("GET", "/api/usage"+query, nil))
		return out
	}
	month := time.Now().In(w.Settings().Location()).Format("2006-01")
	for _, tc := range []struct {
		query string
		rows  int
	}{{"?month=" + month, 2}, {"?month=" + month + "&persona=gourmet", 1}, {"?month=2000-01", 0}} {
		out := request(tc.query)
		var result struct {
			Usage usage.Month `json:"usage"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || len(result.Usage.Rows) != tc.rows {
			t.Fatalf("%s: %d %s", tc.query, out.Code, out.Body.String())
		}
		if tc.rows == 1 && result.Usage.Rows[0].ReportedUSD != cost {
			t.Fatal("reported OpenRouter cost lost")
		}
		if out.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("usage response must not be cached")
		}
	}
	if out := request("?month=invalid"); out.Code != 400 {
		t.Fatalf("invalid month: %d", out.Code)
	}
	guarded := httptest.NewRecorder()
	guardedRequest := httptest.NewRequest("GET", "/api/usage", nil)
	guardedRequest.Header.Set("X-Kakeratta-Workspace", "another-account")
	s.ServeHTTP(guarded, guardedRequest)
	if out := guarded; out.Code != 401 {
		t.Fatalf("workspace guard: %d", out.Code)
	}
}
