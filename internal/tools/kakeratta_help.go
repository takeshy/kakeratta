package tools

import (
	"context"
	"fmt"

	"github.com/takeshy/kakeratta/docs/system"
	"github.com/takeshy/kakeratta/internal/timeline"
)

var kakerattaHelp = Tool{
	Name: "kakeratta_help", ReadOnly: true,
	Usage: `kakeratta_help — このバージョンのkakerattaに同梱したOKF形式の仕様を読む。pathを省略するとindex.md（目次）。目次から質問に関連する章を選び、pathにそのファイル名を指定して読む。英語の仕様を根拠にユーザーの言語で答える。リポジトリへのリンクは開発用の参照であり、ユーザーの設定や稼働状況を示すものではない。`,
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string", "description": "読む章のファイル名。省略時index.md",
			"enum": []string{"index.md", "architecture.md", "backend.md", "personas.md", "frontend.md", "integrations.md", "operations.md"}},
	}, "additionalProperties": false},
	run: func(_ context.Context, _ *Registry, args Args, _ *timeline.Place) (Result, error) {
		path := "index.md"
		if value, exists := args["path"]; exists {
			name, ok := value.(string)
			if !ok || name == "" {
				return Result{}, fmt.Errorf("path は仕様のファイル名を指定してください")
			}
			path = name
		}
		// Only root Markdown chapters can be read; no filesystem or network access.
		switch path {
		case "index.md", "architecture.md", "backend.md", "personas.md", "frontend.md", "integrations.md", "operations.md":
		default:
			return Result{}, fmt.Errorf("仕様の章が見つかりません。path を省略して目次を確認してください")
		}
		raw, err := system.Bundle.ReadFile(path)
		if err != nil {
			return Result{}, err
		}
		return Result{Text: "kakeratta specification: " + path + "\n\n" + string(raw)}, nil
	},
}
