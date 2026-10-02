package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/websearch"
)

var webSearch = Tool{
	Name: "web_search",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"query":      map[string]any{"type": "string", "description": "検索する語句"},
		"topic":      map[string]any{"type": "string", "enum": []string{"general", "news"}, "description": "通常はgeneral。最新のニュース・報道を調べるときはnews"},
		"time_range": map[string]any{"type": "string", "enum": []string{"day", "week", "month", "year"}, "description": "現在から遡る検索期間。newsで期間省略時はday"},
		"start_date": map[string]any{"type": "string", "description": "この日より後に公開された情報。YYYY-MM-DD。time_rangeと併用不可"},
		"end_date":   map[string]any{"type": "string", "description": "この日より前に公開された情報。YYYY-MM-DD。time_rangeと併用不可"},
	}, "required": []any{"query"}},
	Usage: `web_search — Webを検索する。「最新のニュース」や毎朝のニュース配信ではtopic:newsとtime_range:dayを使う。ユーザーが期間を指定した場合はその期間を優先し、time_range（day/week/month/year）またはstart_date/end_date（YYYY-MM-DD）で絞る。両方式は併用しない。newsで期間省略時は直近1日。通常の調査はgeneralで期間省略可。直近の結果が不足して期間を広げる場合は、その期間と記事の公開日時を回答で区別する。公開日時が返る場合は確認し、検索順位を新しさの順とみなさない。日付不明の記事を今日のニュースと断定しない。出典リンクを示す。`,
	run: func(ctx context.Context, r *Registry, args Args, _ *timeline.Place) (Result, error) {
		query := args.str("query")
		if query == "" {
			return Result{}, errNoQuery
		}
		options := websearch.SearchOptions{}
		for name, target := range map[string]*string{"topic": &options.Topic, "time_range": &options.TimeRange, "start_date": &options.StartDate, "end_date": &options.EndDate} {
			if value, exists := args[name]; exists {
				text, ok := value.(string)
				if !ok || strings.TrimSpace(text) == "" {
					return Result{}, fmt.Errorf("%s は空でない文字列で指定してください", name)
				}
				*target = strings.TrimSpace(text)
			}
		}
		client := websearch.New(r.tavilyKey)
		results, err := client.SearchWithOptions(ctx, query, int(args.num("limit", 5)), options)
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
