# Hermes Agentを担当の「考える役」にする

[README](../README_ja.md) · [English](hermes.md)

NemoClaw / OpenShell での構成と検証結果は[統合・検証レポート](nemoclaw-hermes-integration_ja.md)を参照してください。

担当の1ターンのうち「文脈を読み、次に何をするか判断し、workflow を書く」部分だけを、外部エージェント（Hermes）に任せられます。担当ごとに「考える役」を「内蔵モデル」か「外部エージェント」から選びます。

外部エージェントにした担当が起きると、kakeratta は MCP 上に **ask**（計画の依頼）を出します。Hermes は ask を取得（claim）して文脈を受け取り、workflow を提出します。kakeratta はその workflow を内蔵モデルのものと同じように検査・レビューし、**担当として**実行します（メモの書き込み・予定の設定・道具の呼び出し・発言）。Hermes が自分で投稿やメモの書き込みをすることはありません。

Hermes が時間内に取得しない（既定3分）、または取得後に workflow が受理されない（既定15分）場合は、内蔵モデルがそのターンを引き取ります（フォールスルー）。待ち時間は「設定 → 上級オプション → 外部連携」で変えられます。同じ場所の「外部エージェントを使う」をOFFにすると、担当ごとの設定を残したまま、一時的に全員が内蔵モデルで考えます。Hermes 自体のインストールと設定は別途必要です。

## 接続

1. kakerattaの「設定 → 上級オプション → 外部連携」を開き、「Hermes連携」の「接続キーを発行」を押します。
2. 表示された設定を、自分のHermesの `~/.hermes/config.yaml` に追加します。既に `mcp_servers` がある場合は、その配下に `kakeratta` を追加します。設定には接続URLと接続キーがそのまま入っています。

   ```yaml
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

   スクリプトを配置すると、Skill とスクリプト自身が、すでに呼んでいる MCP の経路で自動更新されます。毎分の `list_asks` の応答に配布版の Skill と Wake Gate の版（`integration: {skill, gate}`）が入っていて、どちらかが入っているものと違えば、スクリプトが `get_integration` を呼んで `~/.hermes/skills/kakeratta/SKILL.md` と自分自身を置き換えます。ほかのホストやパスには取りに行かないので、MCP のエンドポイントしか許可していないサンドボックスでも更新できます。更新したことと失敗は stderr に出ます。Gate の版2より前の Wake Gate はこの仕組みを持たないので、一度だけ手で入れ替えてください。
6. いつもどおり投稿します。振り分けで選ばれた担当のうち「外部エージェントを優先する」がONの担当について ask が出され、Hermes が workflow を考えます。返事はその担当の名前で届き、「外部エージェント」の印が付きます。

URLは**Hermesから到達できるkakerattaのURL**です。HermesのAPIサーバーを公開する必要はありません。kakerattaからHermesのプロセスを起動することもありません。

設定形式は[Hermes公式MCPガイド](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp)と[MCP設定リファレンス](https://hermes-agent.nousresearch.com/docs/reference/mcp-config-reference)、および[Hermes Cronガイド](https://hermes-agent.nousresearch.com/docs/user-guide/features/cron)を参照してください。

### NemoClaw / OpenShellサンドボックスの場合

NemoClaw / OpenShell によるガバナンス下で Hermes を運用する場合、設定管理やネットワーク境界の扱いが通常の手順と異なります（障害の詳細や運用知見は[統合レポート](nemoclaw-hermes-integration_ja.md)を参照）。

以下の手順でセットアップします：

#### 前提条件
- サンドボックスのベースイメージに、Hermes Gateway の再起動要求（exit コード `75`）を捕捉してプロセス再起動を行う修正（`start.sh`）が含まれていること（「[サンドボックス再作成後もGatewayの修正を残す](nemoclaw-hermes-integration_ja.md#サンドボックス再作成後も-gateway-の修正を残す)」参照）。未修正のイメージでは MCP 登録時にサンドボックス全体が停止します。

#### 手順1: 管理対象 MCP プロバイダーの登録（ホスト側作業）
kakeratta の画面で発行したキーは、直接 `config.yaml` に書き込まず、OpenShell の管理対象 MCP プロバイダーとして登録します。OpenShell が認証情報を安全に保管し、Hermes 側には解決用の環境変数プレースホルダーを自動注入します。また、MCP 接続に必要な `kakeratta.net:443` の Egress ポリシー（`mcp_bridge_kakeratta`）も自動合成されます。

```bash
# kakeratta の設定画面に表示される NemoClaw 用コマンドを実行
# 例:
openshell provider add -g <gateway> <sandbox> kakeratta \
  --type mcp \
  --url "https://kakeratta.net/api/mcp/users/<user_id>" \
  --token "<ISSUED_KEY>"
```

#### 手順2: コンテナ内への Skill と Wake Gate スクリプトの配置（サンドボックス内）
Skill・スクリプト・Cron はコンテナ内（Hermes ホーム: `/sandbox/.hermes`）に配置します。

```bash
# 1. Skill のインストール
nemoclaw <sandbox> exec -- hermes skills install https://kakeratta.net/integrations/hermes/kakeratta/SKILL.md

# 2. Wake Gate スクリプトの配置（ホストからコンテナへコピー）
docker cp check_kakeratta_gate.py <container_id>:/sandbox/.hermes/scripts/check_kakeratta_gate.py
nemoclaw <sandbox> exec -- chmod +x /sandbox/.hermes/scripts/check_kakeratta_gate.py
```

#### 手順3: Wake Gate 付き Cron ジョブの作成
未取得の ask がない場合の無駄な推論（毎分のトークン消費）を防ぐため、Wake Gate を紐付けた Cron ジョブを作成します。

```bash
nemoclaw <sandbox> exec -- hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
```

#### 手順4: 疎通と動作の確認
依存する前に、以下のコマンドで正常稼働を確認します：

```bash
# サンドボックスが Ready であることを確認
openshell sandbox list -g <gateway>

# kakeratta MCP が認識されていることを確認
nemoclaw <sandbox> exec -- hermes mcp list

# ask の一覧ツールが正常に応答することを確認
nemoclaw <sandbox> exec -- hermes -z "call mcp__kakeratta__list_asks"
```

> [!TIP]
> **Skill の自動更新について**:
> スクリプトを配置すると、Skill とスクリプト自身が、すでに呼んでいる MCP の経路で自動更新されます。毎分の `list_asks` の応答に配布版の Skill と Wake Gate の版（`integration: {skill, gate}`）が入っていて、どちらかが入っているものと違えば、スクリプトが `get_integration` を呼んで `~/.hermes/skills/kakeratta/SKILL.md` と自分自身を置き換えます。ほかのホストやパスには取りに行かないので、MCP のエンドポイントしか許可していないサンドボックスでも更新できます。更新したことと失敗は stderr に出ます。Gate の版2より前の Wake Gate はこの仕組みを持たないので、一度だけ手で入れ替えてください。MCP の道具が増える更新のあとは、道具の一覧がキャッシュされているため Hermes Gateway を再起動してください。

## キーとアクセス範囲

キーは発行直後の画面でだけ表示し、サーバーにはハッシュを保存します。再発行すると以前のキーは無効になり、「連携を解除」で失効できます。ブラウザのログインCookieはMCPの認証には使えません。

このキーで、発行したユーザーのタイムライン、担当一覧、各担当のメモを読み取れます。書き込みは ask への workflow 提出だけで、提出された workflow は kakeratta が検査・レビューしてから担当として実行します。モデル設定や他のユーザーのデータにはアクセスできません。

「連携を解除」は保存したキーのハッシュを消去し、接続の認証自体を無効にします。再接続にはキーの再発行とHermes側の設定の更新が必要です。キー失効は、既にHermesが取得したデータの消去は行いません。外部エージェントに任せるのをやめるには、担当の「考える役」を「内蔵モデル」に戻します。

## ask の処理

Hermesは次のMCPツールで ask を処理します。

1. `list_asks` で開いている ask を確認します。`status` が `pending` のものが未取得です。`claimBy` までに取得しないと内蔵モデルが引き取ります。
2. `claim_ask` に `askId` と実行ごとに固有の `claimId` を渡します。応答が失われた場合の再試行には同じ `claimId` を使います。別の `claimId` では取得できません。応答には `planBy`（workflow の受理期限）と `context` が含まれます。
3. 必要なら `read_for_ask` で担当自身のメモと道具を使って調べます（`notes_list`・`notes_read`・読み取り用の道具）。回数の上限は内蔵モデルと同じです。ここで `timeline_search` が見つけた投稿だけが、`speak` の `sources` に引用できます。
4. `submit_workflow` に `askId`、`claimId`、`workflow`（`{summary, steps}`）を渡します。kakeratta はすぐに検査し、必要ならレビューして結果を返します。`accepted: true` なら kakeratta が実行します。差し戻されたら `problems` を直して再提出します。差し戻しの上限を超えると内蔵モデルに戻ります（`closed: true`）。
5. 考えられない ask は `release_ask` で返します。期限を待たずに内蔵モデルが引き取ります。

### claim で渡す Context

`claim_ask` の `context` には、内蔵モデルが読むものと同じ内容が入っています。

| フィールド | 内容 |
| --- | --- |
| `persona` | 担当の ID・名前・口調・担当領域・メモの分類（使える `type`） |
| `instructions` | 担当自身への指示 |
| `skill` | メモの書き方（Open Knowledge Format） |
| `wake` / `wakeInstructions` | 起きた理由（`post`・`daily`・`schedule`・`review` など）と、その起床の指示（チェックインの指示など） |
| `trigger` | 今回の対象発言 |
| `thread` / `threadText` | 対象発言を含むスレッド全体（古い順） |
| `memoryIndex` | 担当の `index.md` |
| `schedules` | 担当が置いている予定 |
| `workflow.spec` / `workflow.schema` | workflow の書き方と JSON Schema |
| `tools` | 担当が使える道具。`use: "read"` は判断前の調べもの、`use: "workflow"` は workflow のステップで使う |
| `limits` | 読み取り・Web検索・ステップ・差し戻しの上限 |
| `briefing` | 内蔵モデルに送る system / user メッセージ。ただし外部エージェントには「発言は短く」の決まりを課さない |

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

### 定期実行と Wake Gate

Hermes Cron は毎分 ask を確認します。[配布スクリプト](../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py)が `list_asks` を呼び、未取得の ask があるときだけ Agent を起動します。取得済みの ask は起動中の Agent が処理しているので、新たには起こしません。未取得の ask がなければ `{"wakeAgent": false}` を返し、LLM を起動しません。事前確認で通信エラーが起きた場合は `{"wakeAgent": true}` を返します。既存ジョブへ追加する場合は `hermes cron edit <job_id> --script check_kakeratta_gate.py` を使います。

以前の Wake Gate スクリプト（`list_tasks` を呼ぶもの）も動くよう、`list_tasks` は未取得の ask だけを返す互換用ツールとして残しています。Skill は自動更新されますが、スクリプトは自動更新されないため、新しいスクリプトへの置き換えをおすすめします。

## チェックイン・予定・メモの見直し

毎日のチェックイン、予定による起床、メモの見直し、削除の通知も、担当の「考える役」が外部エージェントなら ask になります。`wake` と `wakeInstructions` で起床の理由と指示が分かります。伝えることがなければ、空の `speak` で終わる workflow を出します。

## 以前の「Hermesに聞く」から

以前の Hermes は独立した担当で、依頼（`list_tasks` / `claim_task` / `complete_task`）を受けて Hermes 名義で返信し、`post_report` で自発的に投稿していました。この仕組みは廃止しました。Hermes は担当ではなくなり、`@Hermes` での呼び出し、Hermes 名義の投稿、Hermes 用のメモ領域への書き込み（`write_note`）はできません。

Hermes が `claim_task` などの廃止したツールを呼んでいる場合は、ask より前の Skill と Wake Gate のまま動いています。任された一手はすべて `claim timeout` で内蔵モデルに戻ります。サーバーはその呼び出しに、最新の `SKILL.md` の取得先を返します。Wake Gate を版2以降に一度だけ手で入れ替え、Hermes Gateway を再起動してください。以後は Wake Gate が MCP 経由で Skill と自分自身を最新に保ちます。
