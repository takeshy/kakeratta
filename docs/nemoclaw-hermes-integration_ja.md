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
      C[Blueprint Cron / every 1m]
      A[Hermes Agent + kakeratta Skill]
      G --> C --> A
    end
    A -->|list / claim / read / complete| M -->|MCP over HTTPS| K
    A -->|推論| P --> N[Nebius Token Factory / Nemotron 3.5 Lightning]
```

| コンポーネント | 役割 |
| --- | --- |
| kakeratta | ユーザーの会話、タスク、MCP の認証とアクセス範囲を管理する |
| Hermes Agent | Skill に従ってタスクを解釈し、自身のツールで作業して結果を返す |
| NemoClaw / OpenShell | サンドボックス、Gateway のライフサイクル、ネットワークと推論経路を管理する |
| Nebius Token Factory | モデル推論を提供する |

kakeratta は Hermes のプロセスを起動せず、Hermes も kakeratta のデータベースへ直接アクセスしません。両者の境界は MCP です。推論用の認証情報は Hermes の設定へ直接配布せず、環境が管理する推論経路を使いました。

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

リポジトリで配布する [kakeratta Skill](../frontend/public/integrations/hermes/kakeratta/SKILL.md) は、`every 1m` の Blueprint と以下の手順を定義しています。

```text
list_tasks → claim_task → get_thread → 推論・ツール利用 → complete_task
```

依頼がなければ `[SILENT]` を返します。検証環境では、このアイドル実行は約 3 秒でした。初期構成で試した独自ポーリングスクリプトや出力差分監視は、最終構成から削除しました。タスク ID が前回と同じ場合にエージェントを再起動しない差分監視では、処理失敗後に依頼が残り続ける問題があったためです。

この検証で重視した原則は、**未完了のタスクは正常完了するまで再実行可能であるべき**ということです。初期の出力差分監視では、Task A の処理が失敗した後もポーリング結果が Task A のままだと「変更なし」と判定され、Hermes が二度と起動しませんでした。Blueprint Cron では毎回タスクを確認するため、この独自の差分状態管理を削除できました。

## 構築中に解決した問題

| 観測した問題 | 原因と対応 |
| --- | --- |
| MCP 登録時にサンドボックス全体が停止 | Hermes Gateway が再起動要求として exit code `75` を返した際、`start.sh` の `set -e` で全体が終了していた。起動スクリプトで `75` を捕捉し、Gateway だけを再起動するループへ変更した。 |
| 非対話オンボーディングで別の推論プロバイダが選ばれる | `NEMOCLAW_PROVIDER="custom"`、`NEMOCLAW_MODEL="nvidia/Nemotron-3_5-Lightning"`、`NEMOCLAW_ENDPOINT_URL="https://api.tokenfactory.nebius.com/v1"` を明示し、Nebius の OpenAI 互換エンドポイントを指定した。 |
| 小型モデルが不要なツール探索やシェル操作に進む | `--tool-disclosure direct` でツール定義を直接開示し、Skill に正式な `mcp__kakeratta__*` 名と手順を記した。 |
| モデル変更後に Cron が推論を実行しない | Hermes が `global inference config drifted since this job was created` として実行を保留した。ジョブのモデルを明示的に固定した。これは意図しないモデル変更や費用増加を防ぐ安全機構だった。 |

開発中には、決定的なタスク管理だけを Python ワーカーへ移す方式も試しました。この方式は安定したものの、独自コードの保守が必要でした。最終的には Hermes 標準の **Skill + Blueprint Cron + MCP** に統一し、独自ワーカーを不要にしました。高速モデルでは、モデル選択に加えてツールの開示方法と Skill の明確さが動作を左右しました。

初期の Nemotron-3.5-Lightning 検証では、複雑な多段 MCP 操作で引数の誤り、JSON 構造の破損、同じツールの再試行、不要なシェル操作や設定調査が発生しました。`progressive` なツール開示から `direct` へ切り替え、Skill に正式なツール名と処理順序を明示することで、ツール探索に使う推論を減らしました。

## エンドツーエンド検証

本番の `https://kakeratta.net` に `@Hermes こんにちは！自己紹介して` と投稿し、以下を確認しました。

```text
投稿 → Cron → list_tasks → claim_task → get_thread
     → Nemotron-3.5-Lightning による応答生成
     → complete_task → 元の会話スレッドへ返信
```

投稿は **09:08:53 UTC**、返信は **09:09:13 UTC** で、この実行では 1 分周期のスケジューリングを含めて約 **20 秒** でした。Hermes の CLI や管理画面を人が操作せずに、元スレッドへの返信まで完了しました。この数値は単一の検証例であり、一般的な応答時間の保証値ではありません。

開発時には `NousResearch/Hermes-4-405B` と `nvidia/Nemotron-3_5-Lightning` を試しました。この環境の単純なプロンプトでは前者が約 45 秒かかる場合があり、後者は約 2～3 秒で応答しました。頻繁に起動し、1 回の仕事が比較的小さいパーソナル AI 用途では後者の応答速度を重視しました。これにより Hermes を、時折起動するバッチ処理ではなく、常に呼び出せる外部 AI ペルソナとして扱えます。

## 検証範囲と今後の用途

OpenShell 内での Hermes 起動、Nebius への推論接続、Managed MCP 経由での本番 kakeratta 接続、全 11 ツールの検出、Skill と 1 分周期 Cron、アイドル時の `[SILENT]`、依頼の claim、会話取得、自律推論、元スレッドへの自動投稿を確認しました。

最終構成では、kakeratta が提供するのは **MCP Server + Hermes Skill + Blueprint のスケジュール情報**です。Hermes が自律的に作業し、NemoClaw / OpenShell が実行境界を制御し、Nebius Token Factory が推論を提供します。独自の Daemon やオーケストレーションサービスは使いません。

同じ MCP 境界を使えば、将来的には外部情報の調査、ファイルやコードの処理、データ分析、他の MCP サービスとの連携結果を kakeratta へ返せます。外部サービスへの操作については Hermes 側の承認方針が適用され、kakeratta の通常の workflow レビューは通りません。
