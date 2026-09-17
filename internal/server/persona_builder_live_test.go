package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

// Opt-in generation check; no workspace or production writes.
func TestCompactPersonaBuilderLive(t *testing.T) {
	if os.Getenv("KAKERATTA_BUILDER_LIVE") != "1" {
		t.Skip("paid generation test requires explicit opt-in")
	}
	key, err := os.ReadFile(os.Getenv("KAKERATTA_ROUTE_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	client := llm.New(store.NebiusBaseURL, strings.TrimSpace(string(key)))
	model := personaBuilderModel()
	var domains []string
	for _, input := range []string{"ワイン", "料理の写真からプリン体を判定してほしい", "読んでいる本の内容を一緒に考えてほしい"} {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		reply, err := client.Chat(ctx, llm.Request{
			Model: model, JSON: true, Temperature: .4,
			Messages: []llm.Message{
				{Role: "system", Content: builderPrompt + "\n設定データ:\n" + `{"existing":null,"usedIDs":[],"availableTools":["web_search"],"language":"ja"}`},
				{Role: "user", Content: input},
			},
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Message string
			Draft   *persona.Skill
		}
		if err := llm.DecodeJSON(reply, &result); err != nil {
			t.Fatal(err)
		}
		if result.Draft == nil {
			t.Fatal("expected completed draft")
		}
		if err := persona.Validate(*result.Draft, func(name string) bool { return name == "web_search" }); err != nil {
			t.Fatal(err)
		}
		domains = append(domains, result.Draft.Domain)
		t.Logf("DOMAIN chars=%d text=%s", utf8.RuneCountInString(result.Draft.Domain), result.Draft.Domain)
	}
	raw, err := json.MarshalIndent(domains, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/kakeratta-compact-builder-domains.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
}
