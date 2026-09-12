package tools

import (
	"context"

	"github.com/takeshy/dokking/internal/timeline"
	"github.com/takeshy/dokking/internal/websearch"
)

var webSearch = Tool{
	Name:  "web_search",
	Usage: `web_search — Webを検索する。{"tool":"web_search","args":{"query":"検索語"}}`,
	run: func(ctx context.Context, r *Registry, args Args, _ *timeline.Place) (Result, error) {
		query := args.str("query")
		if query == "" {
			return Result{}, errNoQuery
		}
		client := websearch.New(
			r.settings.Search.Provider, r.settings.Search.BaseURL, r.settings.Search.APIKey)
		results, err := client.Search(ctx, query, int(args.num("limit", 5)))
		if err != nil {
			return Result{}, err
		}
		if len(results) == 0 {
			return Result{Text: "検索結果はありませんでした。"}, nil
		}
		out := Result{Text: websearch.Lines(results)}
		for _, hit := range results {
			out.Sources = append(out.Sources, timeline.Source{Label: hit.Title, URL: hit.URL})
		}
		return out, nil
	},
}
