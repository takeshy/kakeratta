# Hermes Agentを担当として使う

[README](../README_ja.md) · [English](hermes.md)

NemoClaw / OpenShell での構成と検証結果は[統合・検証レポート](nemoclaw-hermes-integration_ja.md)を参照してください。

kakerattaはMCPサーバーとして依頼を保存し、Hermesが接続して依頼を取得・実行・報告します。Hermesのモデル・ツール・スキル・長期記憶はHermes側で管理します。Hermes自体のインストールと設定は別途必要です。

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

4. Hermesを再起動し、kakerattaの「担当」でHermesを有効にします。
5. Hermesで定期的に自動処理させるには、kakeratta公式Skillと Wake Gate スクリプトをインストールし、スクリプト付きの Cron ジョブを作ります。`SKILL.md` の URL だけをインストールしてもスクリプトは含まれないため、別途コピーしてください。

   ```bash
   # Skillのインストール
   hermes skills install https://kakeratta.net/integrations/hermes/kakeratta/SKILL.md

   # https://kakeratta.net/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py
   # を ~/.hermes/scripts/check_kakeratta_gate.py に配置してから
   hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
   ```

   Wake Gate がタスクなしと判定した場合、Agent を起動せず、待機時の LLM 呼び出しを省きます。

   スクリプトを配置すると、Skill は自動更新されます。1日1回、kakeratta.net から配布版の `SKILL.md` を取得し、`metadata.version` が新しくなっていれば `~/.hermes/skills/kakeratta/SKILL.md` を上書きします。手動の再インストールは、この仕組みの外でバージョンがズレた場合やスクリプト本体を新しく入れ直した場合にだけ必要です。
6. タイムライン上部の担当バーでHermesの名前を押し、`@Hermes READMEの構成を整理して` のように依頼を投稿します。通常投稿はRouterの担当条件で振り分けられ、Hermesの返信への返事も依頼になります。Hermesが自律的にタスクを取得・処理し、完了報告を返信します。

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
未処理タスクがない場合の無駄な推論（毎分のトークン消費）を防ぐため、Wake Gate を紐付けた Cron ジョブを作成します。

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

# タスク確認ツールが正常に応答することを確認
nemoclaw <sandbox> exec -- hermes -z "call mcp__kakeratta__list_tasks"
```

> [!TIP]
> **Skill の自動更新について**:
> 一度配置された Wake Gate スクリプトは、1日1回 `kakeratta.net`（`mcp_bridge_kakeratta` により 443 Egress は許可済み）から配布版 `SKILL.md` を取得し、バージョンが更新されていれば自動で上書き更新します。そのため、日々の運用で Skill を手動再インストールする必要はありません（スクリプト本体の更新時のみ手動反映が必要です）。

## キーとアクセス範囲

キーは発行直後の画面でだけ表示し、サーバーにはハッシュを保存します。再発行すると以前のキーは無効になり、「連携を解除」で失効できます。ブラウザのログインCookieはMCPの認証には使えません。

このキーは、発行したユーザーのタイムライン、担当一覧、各担当のメモを読み取れます。また、Hermesのメモの保存、依頼の取得・取消・完了、Hermes名義の報告ができます。モデル設定や他のユーザーのデータにはアクセスできず、他の担当のメモは書き換えられません。

Hermes担当がONの間だけ、MCPツールで読み書きできます。OFFにすると、タイムライン・担当一覧・メモ・依頼を含むすべてのMCPツールへのアクセスを拒否します。キーは保持するため、ONに戻せば再利用できます。OFFの間も接続初期化やツール定義の一覧取得はできますが、ユーザーデータは取得できません。

「連携を解除」は保存したキーのハッシュを消去し、接続の認証自体を無効にします。再接続にはキーの再発行とHermes側の `config.yaml` の更新が必要です。担当のOFFやキー失効は、既にHermesが取得したデータの消去や実行中の外部操作の停止は行いません。

## 依頼の処理と再試行

Hermesは次のMCPツールで依頼を処理します。

1. `list_tasks` で未処理・取得済みの依頼を確認します。
2. `claim_task` に `taskId` と実行ごとに固有の `claimId` を渡します。応答が失われた場合の再試行には同じ `claimId` を使います。応答には対象スレッドと担当の指示が含まれます。
3. Hermes自身のツールで作業します。取得済みの依頼を、再接続したという理由だけで再実行しないでください。取得権は自動では期限切れになりません。
4. `complete_task` に `taskId`、`claimId`、結果の `text` を渡します。空の本文なら投稿せず完了します。本文は32KBまでです。

結果はタイムラインへの配信前に保存されます。配信が失敗しても作業をやり直さず、同じ完了操作を再試行するか、`list_tasks` で未配信結果を再配信します。最初に受理した結果は後から変更できません。一時的な読み取り障害では依頼や未配信結果を残し、削除・編集を確認できた依頼は無効にします。

### 定期実行と未完了タスク

Hermes Cron は毎分依頼を確認します。Wake Gate の事前実行スクリプトが `list_tasks` を呼び、タスクがあるときだけ Agent を起動します。前回と同じタスク ID が返っても「変化なし」を理由に実行を省略しません。初期に試した出力差分監視では、処理に失敗したタスクが次回も同じ ID のまま残ると Hermes が再起動せず、依頼が pending のまま止まる問題がありました。

未取得（pending）のタスクは次の起動で処理できます。取得済み（claimed）のタスクも `list_tasks` で状態を確認できますが、作業の再実行は別の判断です。特に外部操作の成否が不明な場合は、同じ作業を無条件に繰り返さないでください。

### Wake Gate による待機時の推論省略

[配布スクリプト](../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py)は上記の Cron 作成時に指定します。既存ジョブへ追加する場合は `hermes cron edit <job_id> --script check_kakeratta_gate.py` を使います。タスクがなければスクリプトが `{"wakeAgent": false}` を返し、LLM を起動しません。タスクがある場合や事前確認で通信エラーが起きた場合は `{"wakeAgent": true}` を返します。検証したアイドル tick は約 0.06 秒、LLM 呼び出し 0 回でした。これはモデル推論の待機時消費についての値です。

### claim後にクラッシュした場合

`list_tasks` と `claim_task` は、依頼に次の復旧情報を含めます。

```json
{
  "id": "task_...",
  "status": "claimed",
  "claimId": "execution-...",
  "claimedAt": "2026-09-20T03:00:00Z"
}
```

`claimedAt` は最初に取得した時刻（UTC）で、同じ `claimId` による再試行では変わりません。旧バージョンで取得済みの依頼は時刻が不明なため `null` です。既存クライアント向けの `state` と `claim` も互換用に返します。

`claim_task → GitHub操作 → クラッシュ → complete_task未実行` という場合、kakerattaは外部操作が済んだか判定できません。外部操作のexactly-onceは保証しません。claimに期限はなく、別の `claimId` による取り直しは拒否します。同じ `claimId` で状態を再取得できても、それは外部操作を再実行してよいという意味ではありません。

結果が不明な場合は自動再実行を止め、ユーザーにGitHubなどの外部状態を確認してもらいます。再実行が必要なら、ユーザーの指示で `cancel_task` を呼び、その後ユーザーが**新しい投稿として依頼**します。同じ本文でも、新しい投稿は別の依頼になります。取り消した依頼を取り直すことはできません。

`cancel_task` は不要な未処理・取得済みの依頼を取り消しますが、既にHermes側で動いている処理は停止できません。依頼ボックスは未処理128件までで、完了済みの配信記録は直近8件を保持します。

Hermesの作業はkakerattaの通常のworkflowレビューを通りません。外部操作の承認方針はHermes側で設定してください。返信の試し直しによる再実行は無効です。

## 毎日のチェックインと自発的な報告

担当画面でチェックインの希望時刻を設定すると、kakerattaサーバーが確認依頼を保存します。実際に確認するのはHermesが依頼を取得したときです。自動対応にはkakerattaの稼働とHermes側の定期実行が必要です。伝えることがない場合は `complete_task` の本文を空にして完了します。

Hermes自身のcronやスキルからの報告にはMCPツール `post_report` を使います。次はツール呼び出しの引数の例です。

```json
{"id":"release-check-2026-09-19","text":"昨日のIssueに関係するリリースがありました。"}
```

`id` は報告ごとに固有にし、同じ結果の再送には同じIDを使います。同一IDで重複投稿せず、削除済みの投稿も復活させません。`replyTo` に既存投稿のIDを指定すると返信になります。本文は32KBまでです。これは専用のREST投稿APIではなく、認証済みMCP接続で呼び出すツールです。

## 会話・記憶と動作確認

`get_timeline`、`get_thread` で投稿を参照し、`list_notes`、`read_note` で各担当の共有メモを読めます。`write_note` はHermesのメモ領域だけに保存します。kakerattaはHermesの会話セッションIDを管理しません。Hermes側の履歴・長期記憶は独立しており、kakerattaの投稿を削除してもHermesが取得済みの情報は削除されません。

接続後は、まずタイムラインの読み取りと小さな依頼の取得・完了を確認してください。Firebase認証環境でサーバー起動直後にワークスペースの復元が終わっていない場合は503となるため、後から再試行します。

回帰テストでは、一時的な読み取り障害で未処理・取得済みの依頼を取り消さないこと、保存済みの結果を配信済みにしないこと、復旧後の再試行、確認済みの編集・削除による無効化を検証しています。
