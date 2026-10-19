# Google Bridge

各ユーザーが自分の端末・サーバーで起動する、Hermes向けGoogle連携Webアプリです。Google CloudプロジェクトとOAuthクライアントも本人のものを使います。kakeratta本体の起動は不要です。

リポジトリのルートから実行します（Go 1.26、既存のGo moduleを共用）。

```sh
mise exec -- go run ./tools/google-bridge/cmd/google-bridge -init
mise exec -- go run ./tools/google-bridge/cmd/google-bridge
```

既定URLは `http://127.0.0.1:8094`。初回に表示された設定ファイルの管理キーでログインし、Google接続とHermes用MCPキーを設定します。

- `cmd/google-bridge/`: 起動・設定初期化・プロセスロック
- `internal/google/`: OAuth・暗号化保存・Google読み取り・承認付き書き込み
- `internal/persistence/`: 実行前の承認保存を確定するfsyncチェックポイント
- `internal/server/`: 管理API・MCP・埋め込みWeb画面
- `hermes/`: 判定待ちがある回だけLLMを起動する既存cron用の単発同期スクリプト
- `tests/`: ブラウザー側のフローと承認画面のテスト

Gmail検索・本文・スレッド、Calendar一覧・予定取得、承認付きGmail送信/返信/下書き保存・Calendar作成/変更/削除に対応します。MCPから承認は発行できません。実Google/Hermes接続は未検証です。重要メールの差分同期と、kakeratta Timelineへの取り込み待ち表示にも対応します。本人がTimelineで「取り込む」を選んで初めて通常投稿として処理し、「取り込まない」で削除します。定期実行は既存Hermes cronから行います。

[設定・操作・復旧ガイド](../../docs/guides/google_ja.md) / [設計・調査](../../docs/design/google-hermes-integration-spec_ja.md)

```sh
mise exec -- go test ./tools/google-bridge/... -count=1
mise exec -- node --test tools/google-bridge/tests/*.test.js
python3 -m unittest discover -s tools/google-bridge/tests -p 'test_*.py'
```

テストは偽のGoogle APIを使い、実アカウントへの送信や予定変更を行いません。

取り込み待ちはカードからモーダルで確認します。破棄時は「この種を除外」「このFromを除外」を個別に選択できます。FromはLLMより前に完全一致で除外し、LLM依頼文・種類・Fromのルールを個人用アプリから編集・解除できます。

Google Cloudのセットアップ案内では、プロジェクト/API別のCLI手順とConsoleリンクを表示します。Web用OAuth JSONは専用フォームで読み込み、種類とリダイレクトURIを検証します。Cloudの操作・ログイン・同意は本人が行い、接続失敗時は復旧案内を表示します。

「05 今日・明日の予定」ではCalendarを日次に読み取り、予定ごとにTimelineで取り込む/取り込まないを選択できます。チェック付き破棄は単発の予定IDまたは定期予定のシリーズIDでLLMより先に除外し、判定文と除外一覧を専用アプリから編集・解除できます。日次ジョブは既存Hermes cronとcheck_calendar_bridge.pyを使用し、この取り込みでGoogle Calendarへ書き込みません。
