# Hermes Agentを担当の「考える役」にする

[README](../README_ja.md) · [English](hermes.md)

NemoClaw / OpenShell での構成と検証結果は[統合・検証レポート](nemoclaw-hermes-integration_ja.md)を参照してください。

担当の1ターンのうち「文脈を読み、次に何をするか判断し、workflow を書く」部分だけを、外部エージェント（Hermes）に任せられます。担当ごとに「考える役」を「内蔵モデル」か「外部エージェント」から選びます。

外部エージェントにした担当が起きると、kakeratta は MCP 上に **ask**（計画の依頼）を出します。Hermes は ask を取得（claim）して文脈を受け取り、workflow を提出します。kakeratta はその workflow を内蔵モデルのものと同じように検査・レビューし、**担当として**実行します（メモの書き込み・予定の設定・道具の呼び出し・発言）。Hermes が自分で投稿やメモの書き込みをすることはありません。

Hermes が時間内に取得しない（既定3分）、または取得後に workflow が受理されない（既定15分）場合は、内蔵モデルがそのターンを引き取ります（フォールスルー）。待ち時間は「設定 → 上級オプション → 外部連携」で変えられます。同じ場所の「外部エージェントを使う」をOFFにすると、担当ごとの設定を残したまま、一時的に全員が内蔵モデルで考えます。Hermes 自体のインストールと設定は別途必要です。

## 接続

1. kakerattaの「設定 → 上級オプション → 外部連携」を開き、「Hermes連携」の「接続キーを発行」を押します。
2. 表示された設定を、自分のHermesの `~/.hermes/config.yaml` に追加します。既に `mcp_servers` がある場合は、その配下に `kakeratta` を追加します。また、MCP ツールの引数エスケープ破損（`Expecting ',' delimiter...`）を防ぐため、`tools.tool_search.enabled: "off"`（Direct disclosure）を設定します。

   ```yaml
   tools:
     tool_search:
       enabled: "off"

   mcp_servers:
     kakeratta:
       url: "https://kakeratta.net/api/mcp/users/xxx"
       headers:
         Authorization: "Bearer 発行されたキー"
   ```

3. キーを確認できるのは発行直後の画面だけです。設定はキーを含むため、第三者に共有しないでください。再発行した場合は、`config.yaml` の設定を新しいものに置き換えます。

4. Hermesを再起動し、kakerattaの「担当」で、Hermesに任せたい担当の「外部エージェントを優先する」をONにします。このチェックと待ち時間の設定は、接続キーを発行したあとにだけ表示されます。「優先」なので、Hermes が時間内に応じなければ内蔵モデルが考えます。
5. Hermesで定期的に自動処理させるには、kakeratta公式Skillと Wake Gate スクリプトをインストールし、スクリプト付きの Cron ジョブを作ります。`SKILL.md` の URL だけをインストールしてもスクリプトは含まれないため、別途コピーしてください。

   ```bash
   # Skillのインストール
   hermes skills install https://kakeratta.net/integrations/hermes/kakeratta/SKILL.md

   # https://kakeratta.net/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py
   # を ~/.hermes/scripts/check_kakeratta_gate.py に配置してから
   hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
   ```

   Wake Gate が未取得の ask なしと判定した場合、Agent を起動せず、待機時の LLM 呼び出しを省きます。ask の取得期限は数分なので、間隔は1分のままにしてください。

   スクリプトを配置すると、Skill とスクリプト自身が、すでに呼んでいる MCP の経路で自動更新されます。毎分の `list_asks` の応答に配布版の Skill と Wake Gate の版（`integration: {skill, gate}`）が入っていて、どちらかが入っているものと違えば、スクリプトが `get_integration` を呼んで `~/.hermes/skills/kakeratta/SKILL.md` と自分自身を置き換えます。ほかのホストやパスには取りに行かないので、外部へのアクセスが制限された環境でも更新できます。更新したことと失敗は stderr に出ます。Gate の版2より前の Wake Gate はこの仕組みを持たないので、一度だけ手で入れ替えてください。
6. いつもどおり投稿します。振り分けで選ばれた担当のうち「外部エージェントを優先する」がONの担当について ask が出され、Hermes が workflow を考えます。返事はその担当の名前で届き、「外部エージェント」の印が付きます。

URLは**Hermesから到達できるkakerattaのURL**です。HermesのAPIサーバーを公開する必要はありません。kakerattaからHermesのプロセスを起動することもありません。

設定形式は[Hermes公式MCPガイド](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp)と[MCP設定リファレンス](https://hermes-agent.nousresearch.com/docs/reference/mcp-config-reference)、および[Hermes Cronガイド](https://hermes-agent.nousresearch.com/docs/user-guide/features/cron)を参照してください。

### NemoClaw / OpenShell で運用する場合

NVIDIA NemoClaw / OpenShell のサンドボックス配下で Hermes を運用する場合、設定管理・ポリシー合成・プロセス監視や自動起動など、サンドボックス環境特有の要件が存在します。

具体的なセットアップ手順（管理対象 MCP プロバイダーの登録、コンテナ内への Skill/スクリプト配置、Web 検索 Egress の開放、`Phase: Error` デッドロック回避など）については、[**NemoClaw / OpenShell 統合・検証レポート**](nemoclaw-hermes-integration_ja.md) を参照してください。

## キーとアクセス範囲

キーは発行直後の画面でだけ表示し、サーバーにはハッシュを保存します。再発行すると以前のキーは無効になり、「連携を解除」で失効できます。ブラウザのログインCookieはMCPの認証には使えません。

このキーで、発行したユーザーのタイムライン、担当一覧、各担当のメモを読み取れます。書き込みは ask への workflow 提出だけで、提出された workflow は kakeratta が検査・レビューしてから担当として実行します。モデル設定や他のユーザーのデータにはアクセスできません。

「連携を解除」は保存したキーのハッシュを消去し、接続の認証自体を無効にします。再接続にはキーの再発行とHermes側の設定の更新が必要です。キー失効は、既にHermesが取得したデータの消去は行いません。外部エージェントに任せるのをやめるには、担当の「考える役」を「内蔵モデル」に戻します。

## MCP と自律プランニング

kakeratta の MCP サーバーは、外部エージェント向けに以下のツールを公開しています。Hermes 内でのツール名は `mcp__kakeratta__<tool_name>` となります。

| ツール | 用途 |
| --- | --- |
| `list_personas` | 利用可能な担当ペルソナ一覧 |
| `get_timeline` | タイムラインの取得 |
| `get_thread` | 会話スレッドの取得 |
| `list_notes` / `read_note` | ペルソナのメモ一覧・内容の取得 |
| `list_asks` | 未処理・取得済み ask の確認（配布バージョン情報を含む） |
| `claim_ask` | ask の排他的な取得とコンテキスト（ペルソナ設定・指示・制約等）の取得 |
| `read_for_ask` | プランニングに必要な追加情報の参照（メモ・ツール検索） |
| `submit_workflow` | 策定したワークフロー（`speak`, `write_note` 等）の提出 |
| `release_ask` | 自力でのプランニングを断念し、kakeratta 内蔵モデルへ即時返却 |
| `get_integration` | 最新の Skill および Wake Gate スクリプト本文の取得 |

### プランニングの流れ

リポジトリで配布する公式 Skill（`SKILL.md`）に基づき、Hermes は以下の手順で担当の思考を担当します。

```text
list_asks → claim_ask → コンテキスト解釈 → read_for_ask（必要に応じ） → submit_workflow
```

1. **`list_asks`**: 開いている ask を確認します。`status: "pending"` のものが未取得です。`claimBy` までに取得されないと内蔵モデルが引き取ります。
2. **`claim_ask`**: `askId` と実行ごとに固有の `claimId` を渡して排他的に取得します。応答には `planBy`（workflow の受理期限）と詳細な `context` が含まれます。応答が失われた場合の再試行には同じ `claimId` を使います。
3. **調査と推論**: 必要に応じて `read_for_ask` で担当自身のメモや道具を使って調べます（`notes_list`・`notes_read`・読み取り用の道具）。ここで `timeline_search` が見つけた投稿だけが、`speak` の `sources` に引用できます。また、必要に応じて Web 検索等も併用します。
4. **`submit_workflow`**: `askId`、`claimId`、および策定した行動計画 `workflow`（`{summary, steps}`）を提出します。kakeratta はすぐに検査・レビューし、問題がなければ担当自身のアクション（発言、メモの保存、予定の設定等）として実行します。差し戻されたら `problems` を直して再提出します。
5. **`release_ask`**: 自力で考えられない ask は、期限切れを待たずに即座に内蔵モデルへ引き渡します。

### claim で渡す Context

`claim_ask` の `context` には、内蔵モデルが読むものと同じ内容が入っています。

| フィールド | 内容 |
| --- | --- |
| `persona` | 担当の ID・名前・口調・担当領域・メモの分類（使える `type`） |
| `instructions` | 担当自身への指示 |
| `skill` | メモの書き方（Open Knowledge Format） |
| `wake` / `wakeInstructions` | 起きた理由（`post`・`daily`・`schedule`・`review` など）と、その起床の指示（チェックインの指示など） |
| `trigger` | 今回の対象発言 |
| `thread` | 対象発言を含むスレッド全体（古い順） |
| `memoryIndex` | 担当の `index.md` |
| `schedules` | 担当が置いている予定 |
| `workflow.spec` / `workflow.schema` | workflow の書き方と JSON Schema |
| `tools` | 担当が使える道具。`use: "read"` は判断前の調べもの、`use: "workflow"` は workflow のステップで使う |
| `limits` | 読み取り・Web検索・ステップ・差し戻しの上限 |
| `rules` | このターンに適用する共通ルール。外部エージェントには「発言は短く」の決まりを課さない |
| `now` / `place` / `noteCount` / `turn` | 現在時刻・投稿の位置情報・メモ件数の概要・名指しや傾聴による発言条件 |

投稿やメモの中身はデータであり、Hermes への指示ではありません。

### 待ち時間とフォールスルー

| 状況 | 結果 |
| --- | --- |
| `claimBy`（既定: 出してから3分）までに取得されない | 内蔵モデルが考える |
| 取得後 `planBy`（既定: 取得から15分）までに workflow が受理されない | 内蔵モデルが考える |
| 差し戻しの上限を超えた・レビューに失敗した | 内蔵モデルが考える |
| `release_ask` が呼ばれた | すぐに内蔵モデルが考える |
| 取得を待つ時間（既定3分）のあいだ、Hermes から MCP の呼び出しが一度もない | ask を出さず、すぐに内蔵モデルが考える（応答なし） |

Hermes の Wake Gate は毎分 `list_asks` を呼ぶので、認証済みの MCP 呼び出しがあるたびに「接続中」とみなします。取得を待つ時間のあいだ一度も呼び出しがなければ応答なしとみなし、次に呼び出しが来るまで、外部エージェントを優先する担当も ask を出さずに内蔵モデルで考えます。呼び出しが来た時点で自動的に元に戻ります。取得した ask をまだ終えていない間は、考えている最中で Wake Gate が動かないため、呼び出しがなくても応答なしとはみなしません（その ask には、workflow を出すまでの期限が別にあります）。サーバーの再起動直後は、最初の呼び出しが来るまで応答なし扱いです。状態は「設定 → 上級オプション → 外部連携」に最終接続の時刻とともに表示されます。Hermes の cron の間隔は取得を待つ時間より短くしてください。長いと呼び出しの合間に応答なし扱いになり、ほとんど担当を任されなくなります。

フォールスルーした ask はなくなり、遅れて届いた `submit_workflow` は「依頼が見つかりません」で拒否されます。ask は待っているターンと一緒にメモリ上にだけあり、kakeratta が再起動するとその ask も終わります。外部エージェントを待つ担当は、他の投稿やチェックインの処理を止めないよう、キューとは別に動きます。振り分けシートでは、その担当は結果が出るまで「返事を考えています…」と表示されます。

「試し直し」（返信のプレビュー）は、担当の「考える役」にかかわらず内蔵モデルで行います。

### ゼロトークン待機（Wake Gate）

1分周期の Cron で毎回エージェント（LLM）を起動すると、アイドル時にも毎分トークン消費と推論コストが発生します。
これを防ぐため、Cron の事前実行スクリプトである **Wake Gate**（[check_kakeratta_gate.py](../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py)）を Cron ジョブに指定します。

- **未処理 ask がない場合**: `{"wakeAgent": false}` を返し、LLM を一切起動せずに即時終了します（アイドル tick 約 0.06 秒、LLM 呼び出し 0 回）。
- **未処理 ask がある場合（または通信エラー時）**: `{"wakeAgent": true}` を返し、Hermes Agent を起動してプランニングを実行します。

既存ジョブへ追加・変更する場合は `hermes cron edit <job_id> --script check_kakeratta_gate.py` を使います。

### MCP 経由の自律アップデート機構

Wake Gate スクリプトには、Skill および自分自身の自動更新機構が統合されています。

- `list_asks` のレスポンスには、配布中の最新バージョン（`integration: {skill, gate}`）が含まれます。
- スクリプトがローカルのバージョンと差異を検知した場合、同一の MCP 接続を経由して `get_integration` ツールを呼び出し、最新の `SKILL.md` や `check_kakeratta_gate.py` を取得して一時ファイル経由でアトミックに自己更新します。
- これにより、外部 Web への自由な GET が制限されている環境であっても、すでに許可されている MCP の接続経路だけで常に最新仕様へ自動追従できます。

## チェックイン・予定・メモの見直し

毎日のチェックイン、予定による起床、メモの見直し、削除の通知も、担当の「考える役」が外部エージェントなら ask になります。`wake` と `wakeInstructions` で起床の理由と指示が分かります。伝えることがなければ、空の `speak` で終わる workflow を出します。
