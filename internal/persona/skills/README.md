# ペルソナに渡すスキル

ここにあるものは、起こされたペルソナの system prompt にそのまま入ります。
**形式を渡すだけで、守らせはしません。** 本体はペルソナのメモを読まないので、
書かれたものが形式に合っているかどうかを知る手段もありません。

| | 中身 |
|---|---|
| `okf/SKILL.md` | メモを [OKF (Open Knowledge Format)](https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf) のバンドルとして持つための書き方 |

`okf/SKILL.md` はこのアプリ用に書いたものです。形式（frontmatter、1概念1ファイル、
index.md、相互リンク）は OKF そのままですが、手順はこのアプリの道具
（`notes_read` / `notes_write` / `notes_forget`）に合わせてあります。

元にしたのは [llm-hub-skills の okf スキル](https://github.com/takeshy/llm-hub-skills/tree/main/skills/okf)
ですが、あちらは Obsidian の vault に `create_note` で書く前提なので、そのまま渡すと
「この部分は読み飛ばせ」という指示が大量に要ります。軽いモデルに渡すプロンプトとしては
害の方が大きいため、形式だけを取って書き直しました。

**書くときの注意**: ここに書いたものは毎回すべてのペルソナのプロンプトに乗ります。
長くすると、軽いモデルほど本題（投稿への反応）が薄まります。
