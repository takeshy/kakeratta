# OKF スキル

`okf/SKILL.md` は [OKF (Open Knowledge Format)](https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf)
のバンドルの書き方です。開発者向けのシステム文書（`docs/system/`）はこの形式で書きます。

ペルソナにはもう渡していません。ペルソナの記憶は OKF ではなく、分野ごとのまとめ
（`{分野}.md`、各2000字以内）と、かけらごとの1文の記録（`records/YYYY-MM.md`）で、
どちらも本体が書きます（`internal/persona/memory.go`、`notekeeper.go`）。

元にしたのは [llm-hub-skills の okf スキル](https://github.com/takeshy/llm-hub-skills/tree/main/skills/okf)
です。
