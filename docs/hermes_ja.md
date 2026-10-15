# Hermes Agentを担当の「考える役」にする

[README](../README_ja.md) · [English](hermes.md)

NemoClaw / OpenShell での構成と検証結果は[統合・検証レポート](nemoclaw-hermes-integration_ja.md)を参照してください。

担当の1ターンのうち「文脈を読んで調べ、回答を書く」部分を、外部エージェント（Hermes）に任せられます。担当ごとに「考える役」を「内蔵モデル」か「外部エージェント」から選びます。

外部エージェントにした担当が起きると、kakeratta は MCP 上に **ask**（計画の依頼）を出します。Hermes は ask を取得（claim）して文脈を受け取り、**回答**（`{text, sources}`）だけを提出します。kakeratta は回答を受け取ったあと、Superで予定を計画し、回答とあわせた **workflow** を組み立てます。コードで検査し、回答との整合をレビューしてから**担当として**実行し、回答を公開します。記憶は公開後にメモ係が更新します。Hermes が自分で投稿・メモの書き込み・状態の変更をすることはありません。回答の質のレビューもしません（kakeratta のモデルからは、Hermes が自分の道具で調べた内容が見えないためです）。

Hermes が時間内に取得しない（既定3分）、または取得後に回答が受理されない（既定15分）場合は、内蔵モデルがそのターンを引き取ります（フォールスルー）。待ち時間は「設定 → 上級オプション → 外部連携」の接続先ごとの「接続情報と待ち時間」で、エージェントごとに変えられます。同じ画面の「外部エージェントを使う」をOFFにすると、外部エージェントからのMCP接続を拒否し（403）、担当ごとの設定を残したまま全員が内蔵モデルで考えます。一方、取得待ち時間のあいだ接続がなく自動で応答なしになった場合は、接続を受け付け続け、次の接続で元に戻ります。Hermes 自体のインストールと設定は別途必要です。

内蔵モデルへのフォールバックでは、調査は常にSuper、最終回答は通常Super、deep返信・AlwaysUltraならUltraです。内蔵調査のread schemaは各roundの検索残数と利用可能な道具に合わせて更新します。Hermes自身の調査loopと既存の `read_for_ask` インターフェースは維持し、内蔵schemaの変更はHermes自身の道具を制限しません。詳しくは[調査budgetの仕様](./system/personas.md#research-budgets-and-available-reads)を参照してください。

## 複数のエージェントを切り替える

「設定 → 上級オプション → 外部連携」の「＋ 接続先を追加」から、名前を付けて接続先を追加できます。キーは接続先ごとに発行されます。「使用する接続先」の一覧でアプリ全体の接続先を1つ選ぶと、外部エージェントを優先する担当すべてに適用されます。追加した接続先は自動で選択されます。他のキーは保存されますが、MCPへアクセスできるのは選択中のキーだけです。切り替え時の待機中ターンは内蔵モデルが引き取ります。使用中の接続先を削除すると、残っている先頭の接続先が使用中になります。既存の接続は「Hermes」として引き継ぎ、キーは発行時のみ表示します。

Hermes以外も、MCP Streamable HTTPのエンドポイントと `Authorization: Bearer <TOKEN>` ヘッダーで接続できます。エージェント側で `list_asks` の定期取得、`claim_ask`、必要な調査、`submit_answer` の実行が必要です。接続するだけでこの処理が自動実行されるわけではありません。以下のHermes固有の設定は、他のクライアントでは不要です。

## 接続

1. kakerattaの「設定 → 上級オプション → 外部連携」を開き、「外部エージェント接続」で接続名（初回は「Hermes」が入っています）を確かめ、「追加してキーを発行」を押します。表示された「「Hermes」の接続設定」の「Hermesでの設定方法」を開きます。
2. 表示された設定を、自分のHermesの `~/.hermes/config.yaml` に追加します。既に `mcp_servers` がある場合は、その配下に `kakeratta` を追加します。また、MCP ツールの引数エスケープ破損（`Expecting ',' delimiter...`）を防ぐため、`tools.tool_search.enabled: "off"`（Direct disclosure）を設定します。

   ```yaml
   tools:
     tool_search:
       enabled: "off"

   agent:
     max_turns: 15 # ペルソナ回答は15ターン（直接依頼は直接依頼ワーカーが処理し、エージェントは起動しない）

   platform_toolsets:
     cron: # ペルソナ回答中のファイル操作・実行・委譲を許可
       - web
       - file
       - terminal
       - code_execution
       - delegation

   mcp_servers:
     kakeratta:
       url: "https://kakeratta.net/api/mcp/users/xxx"
       headers:
         Authorization: "Bearer 発行されたキー"
   ```

3. キーを確認できるのは発行直後の画面だけです。設定はキーを含むため、第三者に共有しないでください。再発行した場合は、`config.yaml` の設定を新しいものに置き換えます。

4. Hermesを再起動し、kakerattaの「担当」で、Hermesに任せたい担当の「外部エージェントを優先する」をONにします。このチェックは、接続先を設定し、設定の「外部エージェントを使う」がONのときだけ表示されます。「優先」なので、Hermes が時間内に応じなければ内蔵モデルが考えます。
5. Hermesで定期的に自動処理させるには、kakeratta公式Skillと Wake Gate スクリプトをインストールし、スクリプト付きの Cron ジョブを作ります。`SKILL.md` の URL だけをインストールしてもスクリプトは含まれないため、別途コピーしてください。

   ```bash
   # Skillのインストール
   hermes skills install https://kakeratta.net/integrations/hermes/kakeratta/SKILL.md

   # https://kakeratta.net/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py
   # を ~/.hermes/scripts/check_kakeratta_gate.py に配置してから
   hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
   ```

   Wake Gate が未取得の ask なしと判定した場合、Agent を起動せず、待機時の LLM 呼び出しを省きます。ask の取得期限は数分なので、間隔は1分のままにしてください。

   インストール後の自動更新については、[MCP 経由の自律アップデート機構](#mcp-経由の自律アップデート機構)を参照してください。

6. いつもどおり投稿します。振り分けで選ばれた担当のうち「外部エージェントを優先する」がONの担当について ask が出され、Hermes が調べて回答を書きます。返事はその担当の名前で届き、「外部エージェント」の印が付きます。

URLは**Hermesから到達できるkakerattaのURL**です。HermesのAPIサーバーを公開する必要はありません。kakerattaからHermesのプロセスを起動することもありません。

設定形式は[Hermes公式MCPガイド](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp)と[MCP設定リファレンス](https://hermes-agent.nousresearch.com/docs/reference/mcp-config-reference)、および[Hermes Cronガイド](https://hermes-agent.nousresearch.com/docs/user-guide/features/cron)を参照してください。

### NemoClaw / OpenShell で運用する場合

NVIDIA NemoClaw / OpenShell のサンドボックス配下で Hermes を運用する場合、設定管理・ポリシー合成・プロセス監視や自動起動など、サンドボックス環境特有の要件が存在します。親をNemotron 3 Super、委譲した子をNemotron 3 Ultraに分ける場合、OpenShell 0.0.116の共有 `inference.local` ではなく、モデル名を維持するProvider v2経由でToken Factoryへ接続します。親は依頼の受付と振り分けを担い、高難度の処理を子へ委譲します。

具体的なセットアップ手順（[Provider v2への移行](nemoclaw-hermes-integration_ja.md#openshell-provider-v2への移行2026-10-13)、管理対象 MCP プロバイダーの登録、コンテナ内への Skill/スクリプト配置、Web 検索 Egress の開放、`Phase: Error` デッドロック回避など）については、[**NemoClaw / OpenShell 統合・検証レポート**](nemoclaw-hermes-integration_ja.md) を参照してください。

## キーとアクセス範囲

キーは発行直後の画面でだけ表示し、サーバーにはハッシュを保存します。接続先の行の「キーを再発行」で以前のキーは無効になり、「削除」で失効できます。ブラウザのログインCookieはMCPの認証には使えません。

このキーで、発行したユーザーのタイムライン、担当一覧、各担当の記憶を読み取れます。書き込みは ask への回答の提出だけです。ターンに必要な変更は kakeratta が決め、検査してから担当として実行し、回答を公開します。モデル設定や他のユーザーのデータにはアクセスできません。

接続先の「削除」は保存したキーのハッシュを消去し、接続の認証自体を無効にします。再接続にはキーの再発行とHermes側の設定の更新が必要です。キー失効は、既にHermesが取得したデータの消去は行いません。外部エージェントに任せるのをやめるには、担当の「考える役」を「内蔵モデル」に戻します。

## MCP と自律プランニング

kakeratta の MCP サーバーは、外部エージェント向けに以下のツールを公開しています。Hermes 内でのツール名は `mcp__kakeratta__<tool_name>` となります。

| ツール | 用途 |
| --- | --- |
| `list_personas` | 利用可能な担当ペルソナ一覧 |
| `get_timeline` | タイムラインの取得 |
| `get_thread` | 会話スレッドの取得 |
| `search_timeline` | タイムライン全体を語句・意味・期間で検索 |
| `list_schedules` | 全担当の予定の一覧（閲覧のみ） |
| `list_notes` / `read_note` | 担当の記憶のファイル（分野ごとのまとめ文と事実、月ごとのかけらの記録）の一覧・内容の取得 |
| `list_asks` | 未処理・取得済み ask と、有効なときは直接依頼の確認（配布バージョン情報を含む） |
| `claim_ask` | ask の排他的な取得とコンテキスト（ペルソナ設定・指示・制約等）の取得 |
| `read_for_ask` | プランニングに必要な追加情報の参照（`records` と、`timeline_search` などの読み取り用の道具） |
| `submit_answer` | 最終回答（`{text, sources}`。任意で `schedule`・`unrelated`）だけを提出 |
| `release_ask` | 自力でのプランニングを断念し、kakeratta 内蔵モデルへ即時返却 |
| `get_integration` | 最新の Skill・Wake Gate スクリプト・直接依頼ワーカー本文の取得 |
| `claim_request` | 直接依頼の取得と会話の取得 |
| `comment_on_entry` | 直接依頼の処理中に、ほかの投稿（誤った回答など）の下へエージェントとして返信する。1依頼10件まで |
| `post_as_persona` | 計画者が外部エージェントの担当として新しい投稿を書いたとおりに投稿する。計画・レビュー・記憶・予定は通らない。1担当1日5件まで |
| `reply_request` | 取得した直接依頼へ返信をスレッドに投稿し、依頼を終える |

### プランニングの流れ

リポジトリで配布する公式 Skill（`SKILL.md`）に基づき、Hermes は以下の手順で担当の思考を担当します。

```text
list_asks → claim_ask → コンテキスト解釈 → read_for_ask・自前の調査（必要に応じ） → submit_answer
```

1. **`list_asks`**: 開いている ask を確認します。`status: "pending"` のものが未取得です。`claimBy` までに取得されないと内蔵モデルが引き取ります。
2. **`claim_ask`**: `askId` と実行ごとに固有の `claimId` を渡して排他的に取得します。応答には `planBy`（回答の受理期限）と詳細な `context` が含まれます。応答が失われた場合の再試行には同じ `claimId` を使います。
3. **調査と推論**: 必要に応じて `read_for_ask` の `records` ステップで担当の前のかけらの記録を調べます（`args` に `tag`・`query`・`before` のうち要るもの、または `ids` で記録の投稿そのもの（10件までまとめて））。`tool` ステップでは、`context.tools` で `use: "read"` とされた道具（`timeline_search` など）を呼べます。この `read_for_ask` 経由の `timeline_search` が見つけた投稿と、記録の ID で読んだ投稿だけが、`speak` の `sources` に引用できます。また、必要に応じて Web 検索等も併用します。
4. **`submit_answer`**: `askId`、`claimId`、`answer: {text, sources, schedule, unrelated}` を提出します。任意の `schedule` は予定の作成・変更・取消が必要か、`unrelated` は領域外として黙ったかを表します。`schedule` を省略すると予定処理は従来どおり動きます。kakeratta は形、ターンの規則、出典IDを確認します。`accepted: true` は受領であり、まだ公開されていません。予定のフェーズを実行してから公開します。その後、記憶する担当のメモ係が、関係のあるユーザー投稿（発言せず聞くだけの場合も）について記憶するかと内容を判断します。編集も以前の記憶を訂正するためメモ係に渡します。旧形式の `remember` は受け付けますが値は無視します。差し戻されたら `problems` を直して再提出します。
5. **`release_ask`**: 自力で考えられない ask は、期限切れを待たずに即座に内蔵モデルへ引き渡します。

### claim で渡す Context

`claim_ask` の `context` には、内蔵モデルが回答するときに読むものと同じ内容が入っています。記憶の残し方・状態を変えるスキーマは含みません。それらは kakeratta の後のフェーズが扱います。

| フィールド | 内容 |
| --- | --- |
| `persona` | 担当の ID・名前・口調・担当領域 |
| `instructions` | 担当自身への指示 |
| `capabilities` | 回答のあと kakeratta がこの担当のためにできる変更。回答はこれ以外を約束しない |
| `wake` / `wakeInstructions` | 起きた理由（`post`・`daily`・`schedule`・`review-reply` など）と、その起床の指示（チェックインの指示など） |
| `trigger` | 今回の対象発言 |
| `thread` | 対象発言を含むスレッド全体（古い順） |
| `memory` | 担当の記憶。分野ごとのまとめ文と今の事実（`- [f_id] 事実`）、最近のかけらの記録（`- 日時 [投稿ID] 1文 #タグ`） |
| `schedules` | 担当が置いている予定 |
| `answerSchema` | 回答（`{text, sources}`）の JSON Schema |
| `tools` | `read_for_ask` で使える読み取り専用の道具（`records`・`timeline_search` など） |
| `limits` | 読み取り・Web検索の上限 |
| `rules` | このターンの答え方。外部エージェントには「短く」の決まりを課さない |
| `now` / `place` / `turn` | 現在時刻・投稿の位置情報・名指しや傾聴による発言条件 |

投稿や記憶の中身はデータであり、Hermes への指示ではありません。

### 直接依頼

`@外部エージェント`・`@エージェント`・`@agent`（大文字・小文字、全角・半角は区別しません）で外部エージェント自身を名指しした投稿は、担当ではなく使用中の接続のエージェントに届きます。接続名では呼び出せませんが、エージェントの返信はユーザーにも担当にも接続名で表示されます。これらの呼び名は予約語で、担当には付けられません。振り分け・プランニング・記憶は行いません。外部エージェントの返信への返信、または名指しで始めた会話の中で自分の発言やアプリの通知に返信したものは、次の依頼になります。その返信で担当を名指しした場合は、その担当が答えます。専用のON/OFF設定はなく、名指ししなければ使われません。外部エージェントがOFFのときや接続がないときに名指しした投稿は、担当にも振り分けず、kakeratta が理由を返信します。名指しせずに以前の会話へ返信した場合は、その間は通常どおり担当に振り分けます。

`list_asks` は開いている依頼を `requests` に載せます。Wake Gate は保留中の直接依頼を配布済みの直接依頼ワーカーに渡します（ワーカーが取得したまま終えられなかった依頼も渡し、同じ `claimId` で再開します。ワーカーは同時に1つだけ動きます）。ワーカーは `claim_request`（`requestId`・安定した `claimId`）で取得して `replyBy`（取得から2時間）とスレッド全体を受け取り、ワーカーの道具（既定は読み取り専用）を持つ通常の Hermes エージェントとして Ultra を1回実行し、その最終応答を `reply_request`（`requestId`・`claimId`・`text`）で1回返信します。親モデルは直接依頼では起動しません。返信は話者 `agent` として依頼の下に表示されます。ワーカーは `get_timeline`・`get_thread`・`search_timeline`・`list_schedules`・記憶の参照ツール（いずれも読み取り専用）で kakeratta を読めます。依頼の仕組みには `comment_on_entry`（ほかの投稿の下への返信）もあります。この返信で担当は起きません。ユーザーがそれに返信すると、外部エージェントへの次の依頼になります。`claimBy`（依頼から2時間）までに取得されない、または `replyBy`（取得から2時間）までに返信がない依頼は、kakeratta の通知をスレッドに残します。内蔵モデルへのフォールスルーはありません。取得後に期限を過ぎた依頼は1日のあいだ期限切れとして残し、操作実行などのあとで遅れて届いた返信も投稿します。未取得の依頼を編集すると、その依頼が更新されます。取得済みの依頼を編集した場合は新しい依頼を作らず、「処理中のため編集は反映されません。返信で伝えてください。」と返信します。外部エージェントをOFFにするか接続を変えると、開いている依頼は通知付きで取り下げます。タイムラインでは依頼中・処理中の投稿に表示が付きます。エージェントの返信と、依頼が受け取られなかった通知はプッシュ通知します。依頼ごとに、表示の横の🔔で通知しないように切り替えられます。

#### 直接依頼ワーカーの設定

ワーカーは専用の Hermes home（`~/.hermes/kakeratta/hermes-home`）で Ultra を動かします。この home は、処理のたびに `config.yaml` から作り直されます。モデル・プロバイダー・接続先・キーの参照は `kakeratta_worker` セクションがあればそこから、なければ `delegation` から取ります。親の `model`（Super）は使いません。追加の設定がなければ、エージェントの道具は読み取り専用です（Web 検索・取得、kakeratta の読み取り専用 MCP、ホーム配下のローカルファイルの読み取り。ドットファイルや秘密情報らしい名前は読めません）。

```yaml
kakeratta_worker:            # 省略可。なければ delegation を使う
  model: nvidia/Nemotron-3-Ultra-550b-a55b
  provider: custom
  base_url: https://api.tokenfactory.nebius.com/v1
  api_key: ${TOKENFACTORY_API_KEY}
  reasoning_effort: high
  max_tokens: 16000          # 1回のモデル呼び出しの出力上限（推論込み）
  max_turns: 15              # 1回の実行でのツール呼び出しの上限
  toolsets: "web,kakeratta,files"
  mcp_servers: []            # エージェントに渡す MCP サーバー（上の mcp_servers の名前）
  max_api_calls: 40          # 1依頼あたり
  max_request_tokens: 1500000
```

エージェントに操作（メール送信など）をさせるには、その操作を提供する MCP サーバーの名前を `mcp_servers` に書くか、`toolsets` にツールセットを足します。Review の段階はなく、エージェントは実行中にその道具を使います。操作の道具を設定している場合、失敗・中断した実行はすでに操作した可能性があるため、再実行せずにその旨を返信します。依頼ごとの監査ログは `~/.hermes/kakeratta/audit/<依頼ID>.jsonl` にあります。

### 待ち時間とフォールスルー

ask を出したあとは、次の条件で内蔵モデルがそのターンを引き取ります。

| 状況 | 結果 |
| --- | --- |
| `claimBy`（既定: 出してから3分）までに取得されない | 内蔵モデルが答える |
| 取得後 `planBy`（既定: 取得から15分）までに回答が受理されない | 内蔵モデルが答える |
| 回答の差し戻しが上限を超えた | 内蔵モデルが答える |
| `release_ask` が呼ばれた | すぐに内蔵モデルが答える |

フォールスルーした ask はなくなり、遅れて届いた `submit_answer` は「依頼が見つかりません」で拒否されます。ask は待っているプロセスではなくワークスペースに保存されるので、kakeratta を再起動しても残り、サーバーインスタンスが複数あってもエージェントはどのインスタンスに接続しても構いません。ask を出した時点で担当のターンはいったん終わるため、他の投稿やチェックインの処理は止まりません。ask が閉じると、エージェントの回答を使って、または内蔵モデルでターンが再開されます。振り分けシートでは、それまで担当は「返事を考えています…」と表示されます。

受理された回答を内蔵モデルがやり直すことはありません。後のフェーズが回答の約束を裏付けられない（たとえば「知らせるね」と言ったのに予定を置けない）場合や、変更の実行に失敗した場合は、回答を公開せずに止めます。名指しされた担当は「今はうまく答えられませんでした」と返します。実行ログ（標準出力の `workflow <担当>`）には、各フェーズの提案、差し戻しの理由、レビュー、実行したステップが残ります。

「試し直し」（返信のプレビュー）は、担当の「考える役」にかかわらず内蔵モデルで行います。

### 外部エージェントが応答なし（away）の場合

ask を出す前に、外部エージェントの接続状態を確認します。判定に使う長さは取得待ち時間と同じですが、ask を出してからではなく、最後の認証済み MCP 呼び出しから測ります。応答なしなら ask を出さず、すぐに内蔵モデルが考えます。

認証済みの MCP 呼び出しがあるたびに最終接続時刻を記録します。Hermes の Wake Gate は通常、毎分 `list_asks` を呼びます。最後の呼び出しから取得待ち時間（既定3分）を超えると応答なしとみなし、次の呼び出しで自動的に元に戻ります。

取得した ask をまだ終えていない間は、考えている最中で Wake Gate が動かないため、呼び出しがなくても応答なしとはみなしません（その ask には、回答を出すまでの期限が別にあります）。サーバーの再起動直後は、最初の呼び出しが来るまで応答なし扱いです。

状態は「設定 → 上級オプション → 外部連携」に最終接続の時刻とともに表示されます。Hermes の cron の間隔は取得を待つ時間より短くしてください。長いと呼び出しの合間に応答なし扱いになり、ほとんど担当を任されなくなります。

### ゼロトークン待機（Wake Gate）

1分周期の Cron で毎回エージェント（LLM）を起動すると、アイドル時にも毎分トークン消費と推論コストが発生します。
これを防ぐため、Cron の事前実行スクリプトである **Wake Gate**（[check_kakeratta_gate.py](../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py)）を Cron ジョブに指定します。

- **未処理 ask がない場合**: `{"wakeAgent": false}` を返し、LLM を一切起動せずに即時終了します（アイドル tick 約 0.06 秒、LLM 呼び出し 0 回）。また、`agent.max_turns` は安全な 15 に維持されます。
- **通常の未処理 ask がある場合**: `{"wakeAgent": true}` を返し、15 ターンの安全境界を保ったままペルソナ回答を実行します。
- **直接依頼（direct request）がある場合**: 同じ tick で配布済みの [直接依頼ワーカー](../frontend/public/integrations/hermes/kakeratta/scripts/direct_request_worker.py) が処理します（取得 → ワーカーの道具を持つ Ultra を1回実行 → 最終応答を返信）。親モデルは起動しません。エージェントが起動するのは persona ask がある場合だけです。

既存ジョブへ追加・変更する場合は `hermes cron edit <job_id> --script check_kakeratta_gate.py` を使います。

### MCP 経由の自律アップデート機構

Wake Gate スクリプトには、Skill・自分自身・直接依頼ワーカーの自動更新機構が統合されています。

- `list_asks` のレスポンスには、配布中の最新バージョン（`integration: {skill, gate, worker}`）が含まれます。
- スクリプトがローカルのバージョンと差異を検知した場合、同一の MCP 接続を経由して `get_integration` ツールを呼び出し、最新の `SKILL.md` や `check_kakeratta_gate.py`・`direct_request_worker.py` を取得して一時ファイル経由でアトミックに自己更新します。
- これにより、外部 Web への自由な GET が制限されている環境であっても、すでに許可されている MCP の接続経路だけで常に最新仕様へ自動追従できます。
- 更新したことと失敗は stderr に出ます。

Gate の版2より前の Wake Gate は自動更新の仕組みを持たないので、一度だけ手で入れ替えてください。

## チェックイン・予定・削除

毎日のチェックイン（見直しの日が来た記録が渡されることがあり、そのうち1件について聞きます）、予定による起床、その問いへの返事も、担当の「考える役」が外部エージェントなら ask になります。`wake` と `wakeInstructions` で起床の理由と指示が分かります。伝えることがなければ、`text` を空にした回答を出します。記憶は、ユーザーのかけらごとに kakeratta のメモ係が更新します。削除の後始末は内蔵モデルが行い、ask にはなりません。
