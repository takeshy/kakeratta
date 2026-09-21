# NemoClaw × Hermes Agent × Nebius Token Factory × kakeratta

## 自律型パーソナルAIエージェント統合・検証レポート

[README](../README_ja.md) · [Hermes の設定と再試行](hermes_ja.md)

本書は、NVIDIA NemoClaw / OpenShell 内で動く Hermes Agent が、Nebius Token Factory で推論し、kakeratta の MCP を介して依頼を取得・処理・返信する構成の検証記録です。目的は、単に LLM をサンドボックス内で実行することではなく、本番稼働中のパーソナル AI アプリケーションから自律エージェントへ安全に仕事を委譲し、結果を元の会話へ返すところまで自動化することです。認証情報、ワークスペース ID、アクセストークン、内部タスク ID は掲載しません。

## 構成と責務

```mermaid
flowchart TD
    U[ユーザー] -->|@Hermes 投稿| K[kakeratta 本番 Web アプリ / MCP]
    subgraph H[NemoClaw / OpenShell ホスト]
      M[Managed MCP / ネットワークポリシー]
      P[Secure Inference Proxy]
    end
    subgraph S[OpenShell サンドボックス]
      G[Hermes Gateway]
      C[Hermes Cron + Wake Gate / every 1m]
      A[Hermes Agent + kakeratta Skill]
      G --> C --> A
    end
    A -->|list / claim / read / complete| M -->|MCP over HTTPS| K
    A -->|推論| P --> N[Nebius Token Factory / Nemotron Super 120B]
```

| コンポーネント | 役割 |
| --- | --- |
| kakeratta | ユーザーの会話、タスク、MCP の認証とアクセス範囲を管理する |
| Hermes Agent | Skill に従ってタスクを解釈し、自身のツールで作業して結果を返す |
| NemoClaw / OpenShell | サンドボックス、Gateway のライフサイクル、ネットワークと推論経路を管理する |
| Nebius Token Factory | モデル推論を提供する |

kakeratta は Hermes のプロセスを起動せず、Hermes も kakeratta のデータベースへ直接アクセスしません。両者の境界は MCP です。モデル推論は OpenShell のプロキシを通します。MCP 接続には別の認証キーが必要です。

## MCP と自律実行

kakeratta の MCP は、検証時に次の 11 ツールを公開しました。

| ツール | 用途 |
| --- | --- |
| `list_personas` | 担当一覧 |
| `get_timeline` | タイムラインの取得 |
| `get_thread` | 会話スレッドの取得 |
| `list_notes` / `read_note` | 担当のメモ一覧・内容の取得 |
| `write_note` | Hermes 自身のメモの保存 |
| `list_tasks` | 未処理・取得済み依頼の確認 |
| `claim_task` | 依頼の排他的な取得 |
| `cancel_task` | 依頼の取り消し |
| `complete_task` | 結果の保存と元スレッドへの投稿 |
| `post_report` | Hermes からの自発的な報告 |

Hermes からの接続テストでは、接続成功と全 11 ツールの検出を確認しました。実際の呼び出し名は `mcp__kakeratta__list_tasks` のように `mcp__kakeratta__` が付きます。

リポジトリで配布する [kakeratta Skill](../frontend/public/integrations/hermes/kakeratta/SKILL.md) は、以下の手順を定義しています。1 分周期の Hermes Cron には Wake Gate スクリプトを指定します。

```text
list_tasks → claim_task → get_thread → 推論・ツール利用 → complete_task
```

初期構成では依頼がなければ `[SILENT]` を返し、アイドル実行は約 3 秒でした。ただし Super 120B へ切り替えた後、この方式でも毎分推論が発生することが分かりました。現在は Cron の事前実行スクリプトによる **Wake Gate** を使い、タスクがなければ `{"wakeAgent": false}` を返してエージェントの起動自体を省略します。検証したアイドル tick は約 0.06 秒で、LLM 呼び出しは 0 回でした。タスクがある場合、または事前確認で通信エラーが起きた場合は `{"wakeAgent": true}` としてエージェントを起動します。[配布スクリプト](../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py)を Cron ジョブに指定します。

初期構成で試した独自ポーリングスクリプトや出力差分監視は、最終構成から削除しました。タスク ID が前回と同じ場合にエージェントを再起動しない差分監視では、処理失敗後に依頼が残り続ける問題があったためです。Wake Gate は毎回 `list_tasks` を確認する方式です。

この検証で重視した原則は、**未完了のタスクは正常完了するまで再確認できるべき**ということです。初期の出力差分監視では、Task A の処理が失敗した後もポーリング結果が Task A のままだと「変更なし」と判定され、Hermes が二度と起動しませんでした。現在は毎回確認します。取得済みタスクで外部操作の成否が不明な場合の再実行は、[再試行ガイド](hermes_ja.md#claim後にクラッシュした場合)に従います。

## 構築中に解決した問題

| 観測した問題 | 原因と対応 |
| --- | --- |
| MCP 登録時にサンドボックス全体が停止 | Hermes Gateway が再起動要求として exit code `75` を返した際、`start.sh` の `set -e` で全体が終了していた。起動スクリプトで `75` を捕捉し、Gateway だけを再起動するループへ変更した。 |
| 非対話オンボーディングで別の推論プロバイダが選ばれる | `NEMOCLAW_PROVIDER="custom"`、`NEMOCLAW_MODEL="nvidia/Nemotron-3_5-Lightning"`、`NEMOCLAW_ENDPOINT_URL="https://api.tokenfactory.nebius.com/v1"` を明示し、Nebius の OpenAI 互換エンドポイントを指定した。 |
| 小型モデルが不要なツール探索やシェル操作に進む | `--tool-disclosure direct` でツール定義を直接開示し、Skill に正式な `mcp__kakeratta__*` 名と手順を記した。 |
| モデル変更後に Cron が推論を実行しない | Hermes が `global inference config drifted since this job was created` として実行を保留した。ジョブのモデルを明示的に固定した。これは意図しないモデル変更や費用増加を防ぐ安全機構だった。 |
| MCP が `401 Unauthorized` を返す | OpenShell の L7 プロキシは世代付きプレースホルダー（例: `openshell:resolve:env:v<REVISION>_KAKERATTA_MCP_TOKEN`）を要求する。`config.yaml` 側のリビジョンがプロバイダの最新リビジョンと一致しない場合、OpenShell がトークン置換をスキップし未解決のプレースホルダーをそのまま送信する。プロバイダの環境リビジョンを確認し、`config.yaml` 側のプレースホルダーを同期して復旧した。 |
| NemoClaw CLI コマンドが無応答でハングする | サンドボックス再作成の中断時などに、古い PID のロックファイル（`~/.nemoclaw-portable-host.lock` や `~/.nemoclaw/state/mcp-lifecycle-locks/` 配下）が残存し、後続コマンドが排他ロック待ちでブロックされていた。所有 PID の生存確認を行い、孤立した stale ロックを安全に削除して即時応答を回復した。 |
| サンドボックスが `Phase: Error` でデッドロックする | Gateway 停止に伴いコンテナが停止した際、OpenShell Gateway が `Phase: Error` を記録した。Docker 側でコンテナを再起動しても、`openshell sandbox stop` は Ready を要求し `start` は Stopped を要求するため CLI 操作が拒絶される。Gateway の内部 DB（`openshell.db`）の sandbox 状態を `Ready` に再同期して CLI 制御を復旧した。 |
| ホスト再起動時に NemoClaw が自動起動しない | Gateway が参照する DB パスの不一致と、NemoClaw の権限保護フェイルセーフ（`GatewayAuthorityError`）が原因。Gateway を systemd サービス化したことでオンボーディング記録（`standalone`）と権限主体が食い違い、フォワード復旧プローブが拒絶されていた。公式設計に準拠した設定ファイル（`~/.config/openshell/gateway.env`）と正規サービス（`nemoclaw-openshell-gateway.service`）を導入し、セッション記録（`onboard-session.json`）の Authority を `packaged-service` に整合させて復旧した。 |

### サンドボックス再作成後も Gateway の修正を残す

管理付き MCP の登録後に `exec relay closed` が出る、またはサンドボックスが停止する場合は、Hermes Gateway の終了コード `75` を確認してください。これは Gateway の再起動要求です。コンテナ内の `/usr/local/bin/nemoclaw-start` を直接修正しただけでは、サンドボックス再作成時に消えます。

修正済みの `agents/hermes/start.sh` を含む NemoClaw イメージを使うか、テスト済みのベースイメージを固定したカスタムイメージを作り、修正済みスクリプトを `/usr/local/bin/nemoclaw-start` にコピーします。`nemoclaw onboard --from /path/to/Dockerfile` で使う Dockerfile の最終ステージには、次の宣言も必要です。
ビルドコンテキスト内の `start.sh` に実行権限を付けてください（`chmod 755 start.sh`）。

```dockerfile
ARG NEMOCLAW_TOOL_DISCLOSURE=progressive
ENV NEMOCLAW_TOOL_DISCLOSURE=${NEMOCLAW_TOOL_DISCLOSURE}
COPY --chown=root:root start.sh /usr/local/bin/nemoclaw-start
```

再作成前にサンドボックスの状態をバックアップしてください。再作成後は `Ready`、`hermes mcp list` での kakeratta の有効化、skill・Wake Gate スクリプト・cron ジョブの存在を確認します。MCP の認証情報は Dockerfile や skill、cron ではなく OpenShell に保存します。

### ホスト再起動時の自動起動とデッドロック回避

ホストマシン（Linux）の再起動後も Hermes と kakeratta を常駐運用するには、systemd ユーザーサービスを設定します。ただし、以下の 2 つの重大な落とし穴に注意してください。

#### 1. ブート時 Docker 待ち合わせ（`Phase: Error` デッドロックの防止）
systemd ユーザーサービス（`systemctl --user`）は、root 権限で動くシステムの `docker.service` を直接 `Requires=` で待機することができません。
そのため、PC ブート時に Docker デーモンが立ち上がるより数秒早く NemoClaw が起動を試みてしまい、`Failure layer: docker_unreachable` で失敗します。
さらに、この起動失敗を検知した OpenShell Gateway はサンドボックスを **`Phase: Error`** にマークしてしまい、以降は「`sandbox must be Stopped to start (current phase: Error)`」として一切の起動・停止操作を拒絶するデッドロックに陥ります。

**対策**: サービス実行前（`ExecStartPre`）に、Docker デーモンが応答可能になるまで待機するループを必ず組み込んでください。

```bash
# ExecStartPre 用スクリプト例 (nemoclaw-wait-docker.sh)
until docker info >/dev/null 2>&1; do
  sleep 1
done
```

#### 2. NemoClaw 権限整合性（`GatewayAuthorityError` の回避）
CLI（手動実行）で `nemoclaw onboard` を行った場合、オンボーディング台帳（`~/.nemoclaw/onboard-session.json`）には Gateway 管理主体が `standalone` として記録されます。
これを後から systemd サービス（`packaged-service`）で動かそうとすると、NemoClaw の権限保護フェイルセーフが働き、ポートフォワード復旧が「`host port ... is held by a listener that NemoClaw cannot attribute to this sandbox's OpenShell forward`」として拒絶されます（ポート競合と誤認されやすいエラーですが、実際には権限不一致による拒絶です）。

**対策**:
- NemoClaw 公式管理サービス **`nemoclaw-openshell-gateway.service`**（先頭に `# NEMOCLAW_MANAGED_OPENSHELL_GATEWAY=1` を付与）を使用する。
- Gateway 設定は `~/.config/openshell/gateway.env` (`0600`) に記述し、正しい DB パス（`OPENSHELL_DATA_DIR`）を指定する。
- `~/.nemoclaw/onboard-session.json` 内の `checkpoint.gatewayAuthority.value.source` を `"packaged-service"` に更新して整合させる。


開発中には、決定的なタスク管理だけを Python ワーカーへ移す方式も試しました。この方式は安定したものの、独自コードの保守が必要でした。最終的には Hermes 標準の **Skill + Cron + MCP** に統一し、独自ワーカーを不要にしました。高速モデルでは、モデル選択に加えてツールの開示方法と Skill の明確さが動作を左右しました。

初期の Nemotron-3.5-Lightning 検証では、複雑な多段 MCP 操作で引数の誤り、JSON 構造の破損、同じツールの再試行、不要なシェル操作や設定調査が発生しました。`progressive` なツール開示から `direct` へ切り替え、Skill に正式なツール名と処理順序を明示することで、ツール探索に使う推論を減らしました。

## エンドツーエンド検証

本番の `https://kakeratta.net` に `@Hermes こんにちは！自己紹介して` と投稿し、以下を確認しました。

```text
投稿 → Cron → list_tasks → claim_task → get_thread
     → Nemotron-3.5-Lightning による応答生成
     → complete_task → 元の会話スレッドへ返信
```

投稿は **09:08:53 UTC**、返信は **09:09:13 UTC** で、この実行では 1 分周期のスケジューリングを含めて約 **20 秒** でした。Hermes の CLI や管理画面を人が操作せずに、元スレッドへの返信まで完了しました。この数値は単一の検証例であり、一般的な応答時間の保証値ではありません。

開発時には `NousResearch/Hermes-4-405B` と `nvidia/Nemotron-3_5-Lightning` を試しました。この環境の単純なプロンプトでは前者が約 45 秒かかる場合があり、後者は約 2～3 秒で応答しました。その後、Lightning がツールエラーやネットワーク不達時に不要な設定変更を試みて長い実行に陥るケースがあったため、より複雑な文脈理解とツール判断が必要な運用を **`nvidia/nemotron-3-super-120b-a12b`** に切り替えました。

Super 120B での追加検証では、Hermes がユーザーのモデルに関する質問を `list_tasks` で検出し、`claim_task` の後に Web 検索を行い、収集した情報を使って `complete_task` で元スレッドへ回答しました。これにより、単純な応答に加えて外部調査を含む自律処理を確認しました。回答文中の個別のモデル仕様やベンチマーク値は、この連携の検証結果としては扱いません。

Skill のエラー処理も改訂しました。タスク取得後にツールや外部 API が失敗した場合は、失敗した操作と理由をユーザーへ説明する結果を `complete_task` で返す手順です。`complete_task` 自体に到達できない障害では返信を保証できないため、接続復旧後の確認が必要です。

## 検証範囲と今後の用途

OpenShell 内での Hermes 起動、Nebius への推論接続、本番 kakeratta MCP への接続、全 11 ツールの検出、Skill と 1 分周期 Cron、Wake Gate によるアイドル時の LLM 呼び出し省略、依頼の claim、会話取得、自律推論、Web 検索、元スレッドへの自動投稿を確認しました。

最終構成では、kakeratta が提供するのは **MCP Server + Hermes Skill + Wake Gate スクリプト**です。Hermes が自律的に作業し、NemoClaw / OpenShell が実行境界を制御し、Nebius Token Factory が推論を提供します。独自の Daemon やオーケストレーションサービスは使いません。

同じ MCP 境界を使えば、将来的には外部情報の調査、ファイルやコードの処理、データ分析、他の MCP サービスとの連携結果を kakeratta へ返せます。外部サービスへの操作については Hermes 側の承認方針が適用され、kakeratta の通常の workflow レビューは通りません。
