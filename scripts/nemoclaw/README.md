# NemoClaw / OpenShell / Hermes Agent 運用・管理スクリプト群

kakeratta の自律エージェント運用基盤（NVIDIA NemoClaw + OpenShell + Hermes Agent）をホストマシン上で安定稼働させるためのスクリプトおよび systemd サービス定義集です。

---

## 1. ディレクトリ構成

```text
scripts/nemoclaw/
├── README.md                              # 本ドキュメント
├── setup-hermes.sh                        # NemoClaw の導入から kakeratta 接続までを一括構築（再実行可）
├── install.sh                             # ~/.local/bin および ~/.config/systemd/user への配備/シンボリックリンク作成
├── profiles/
│   └── tokenfactory.yaml                  # Provider v2 用の Nebius Token Factory プロファイル
├── bin/                                   # 実行スクリプト群
│   ├── nemoclaw-wait-docker.sh            # Docker デーモン起動待機
│   ├── nemoclaw-reconcile-sandbox.py      # Phase: Error デッドロックの自律修復・強制解除
│   ├── nemoclaw-hermes-autostart.sh       # 自己修復型自動起動ラッパー
│   ├── nemoclaw-hermes-autostop.sh        # 排他ロック付き先行安全停止ラッパー
│   └── nemoclaw-hermes-shutdown-monitor.sh # systemd-inhibit 事前シャットダウン監視
└── systemd/                               # systemd ユーザーサービス定義
    ├── nemoclaw-openshell-gateway.service # Gateway デーモンサービス
    ├── nemoclaw-hermes-start.service      # Hermes サンドボックス自動起動・安全停止サービス
    ├── nemoclaw-hermes-shutdown-watch.service # 事前シャットダウン遅延監視サービス
    └── google-bridge.service              # (任意) Google Bridge コンパニオン自動起動サービス
```

---

## 2. Hermes の構築（`setup-hermes.sh`）

NemoClaw の導入、サンドボックス作成、Provider v2、通信許可、Hermes の設定、kakeratta の Skill・Wake Gate・cron までを一度に行います。途中で止まっても、同じコマンドをもう一度実行すれば続きから同じ状態に揃います。

| 実行主体 | モデル | 経路 |
| :--- | :--- | :--- |
| 親（担当の回答、persona ask） | `nvidia/Nemotron-3.5-Super-VL` | オンボーディングが作る `inference.local` |
| 子（`@agent` の直接依頼、delegation） | `nvidia/Nemotron-3-Ultra-550b-a55b` | Provider v2 で Token Factory へ直接（モデル名を維持） |

事前に次の 2 ファイルを作ります（どちらも `chmod 600`。値はシェル履歴に残さない）。

```bash
# ~/.config/nemoclaw/tokenfactory.env
TOKENFACTORY_API_KEY=...

# ~/.config/nemoclaw/kakeratta-mcp.env（kakeratta の「設定 → 上級オプション → 外部連携」で発行）
KAKERATTA_MCP_URL=https://kakeratta.net/api/mcp/users/<workspace>
KAKERATTA_MCP_TOKEN=...
```

```bash
scripts/nemoclaw/setup-hermes.sh
```

スクリプトが行うこと:

1. **前提の確認**: Docker への接続と、`127.0.0.1` の未使用ポートが接続を拒否すること。WSL の mirrored ネットワークでは拒否されずに固まり、NemoClaw のポート確認が `Cannot allocate dashboard port: The OpenShell forward operation timed out` で止まるため、`.wslconfig` を `networkingMode=nat` にします（`wsl --update` や `firewall=false` では直りませんでした）。
2. **NemoClaw の導入とサンドボックス作成**: `NEMOCLAW_INSTALL_TAG`（既定 `v0.0.135`）の公式インストーラーを非対話で実行します。推論は `custom`（Token Factory）で、親モデルは Super-VL です。
3. **Provider v2**: `profiles/tokenfactory.yaml` を取り込み、Provider `tokenfactory-v2` を作ってサンドボックスにつなぎます。**`providers_v2_enabled` はゲートウェイ全体（`--global`）で有効にします**。サンドボックス単位の設定では `_provider_tokenfactory_v2` がポリシーに入りません。資格情報はサンドボックスの起動時に入るので、必要なら再起動します。
4. **通信許可**: Hermes が鍵なしで使う Web 検索（`mcp.exa.ai`、`search.parallel.ai`、`api.firecrawl.dev`、`api.keenable.ai`）を `hermes` と Python にだけ許可します。サンドボックスにはプロキシを迂回する経路も名前解決もないため、`no_proxy` では通りません。kakeratta は、`https://` なら NemoClaw の管理 MCP（`mcp add`、トークンは OpenShell が差し込む）、`http://`（ローカル開発）なら管理 MCP が HTTPS 必須のため宛先だけをポリシーで許可し、トークンを `config.yaml` に書きます。
5. **Hermes の設定**: `/sandbox/.hermes/config.yaml` を直接編集します（ハッシュファイルには触れません）。`tool_search: "off"`、`agent.max_turns: 15`、cron のツールセット、`delegation`（Ultra、Token Factory 直、`reasoning_effort: high`）を設定します。
6. **Skill・Wake Gate・ワーカー**: リポジトリの `frontend/public/integrations/hermes/kakeratta/` から配置し、1 分ごとの cron `kakeratta` を作ります。
7. **確認**: サンドボックス内から Ultra を名前どおりに呼べること、Wake Gate の `list_asks` が通ることを確かめます。

ローカル開発の kakeratta に接続する場合は、`KAKERATTA_ADDR=:8787 task dev` で起動し、URL にはサンドボックスから届くホストの IP（WSL なら `ip -4 addr show eth0` のアドレス）を書きます。WSL の IP が変わったら、URL を直してもう一度実行します。kakeratta の「使用する接続先」がこのキーの接続になっていないと `list_asks` は 401 になります。

スクリプトは 2026-10-21 に、NemoClaw v0.0.135 / OpenShell 0.0.116、WSL2（ARM64、NAT）と Docker Desktop の環境で、ローカルの kakeratta（`http://`）に対して確認しました。`https://` の管理 MCP の経路はこの日の確認に含まれていません。

---

## 3. 各コンポーネントの役割

### 実行スクリプト (`bin/`)

| ファイル名 | 種別 | 役割・概要 |
| :--- | :--- | :--- |
| `nemoclaw-wait-docker.sh` | Bash | Docker デーモンが応答可能になるまで最大 60 秒待機します。Gateway サービス起動前の健全性担保に使用します。 |
| `nemoclaw-reconcile-sandbox.py` | Python 3 | OpenShell Gateway の内部 SQLite DB（`openshell.db`）を監査します。万一の強制終了や停電等で `Phase: Error` (3) や異常終了コードが残存してデッドロックした場合、Protobuf ペイロードを自動で `Phase: Ready` (2) に書き換え、Gateway を再起動して強制解除します。 |
| `nemoclaw-hermes-autostart.sh` | Bash | ホスト起動時のメイン自動起動スクリプト。Docker 待機、古い stale ロック除去、各試行ごとの `reconcile` 実行、コンテナ起動、Web Dashboard（ポート 18789）疎通確認を自律実行します。 |
| `nemoclaw-hermes-autostop.sh` | Bash | サンドボックスの安全停止ラッパー。多重実行防止ロック（`flock`）を内包し、コンテナが既に停止していれば即座にスキップ。Docker が生存している間に安全に `nemoclaw hermes-sandbox stop` を行います。 |
| `nemoclaw-hermes-shutdown-monitor.sh` | Bash | `systemd-inhibit --what=shutdown --mode=delay` を取得して常駐し、D-Bus のシャットダウン予告シグナル（`PrepareForShutdown(true)`）を監視します。シャットダウン要求時に電源断を最大 15 秒遅延させ、**Docker が落ちる前に Hermes を先行クリーン停止（Phase: Stopped / exit 0）** させます。 |

### systemd サービスユニット (`systemd/`)

| ユニット名 | 動作モード | 役割・概要 |
| :--- | :--- | :--- |
| `nemoclaw-openshell-gateway.service` | `Type=simple` | OpenShell Gateway デーモンを常駐実行します。 |
| `nemoclaw-hermes-start.service` | `Type=oneshot`<br>`RemainAfterExit=yes` | ホスト起動時に `nemoclaw-hermes-autostart.sh` を実行し、サービス停止時に `nemoclaw-hermes-autostop.sh` を実行します。 |
| `nemoclaw-hermes-shutdown-watch.service` | `Type=simple` | `nemoclaw-hermes-shutdown-monitor.sh` を常駐させ、OS シャットダウン時に Docker 停止順序競合によるエラー終了を防止します。 |
| `google-bridge.service` | `Type=simple` | Google Bridge コンパニオンを常駐実行します。`ConditionPathExists=%h/.config/google-bridge/config.json` により、Google Bridge を初期化・設定済みの環境でのみ安全に自動起動します（未設定環境ではエラーなくスキップ）。 |

---

## 4. 多重防御アーキテクチャ (sudo 完全不要)

1. **平時（通常の再起動・シャットダウン）**:
   * `nemoclaw-hermes-shutdown-watch.service` が OS のシャットダウン予告を受信。
   * logind に電源断を最大 15 秒遅延させ、Docker デーモンが停止処理に入る前に Hermes を先行クリーン停止。
   * エラー終了（exit 143）そのものを根本防止します。
2. **非常時（突発的な電源断・クラッシュ）**:
   * 万一 `Phase: Error` で停止した場合でも、次回起動時に `nemoclaw-hermes-autostart.sh` 経由で `nemoclaw-reconcile-sandbox.py` が自動検知。
   * DB 内部のステータスを即座に `Phase: Ready` に強制解除・同期し、100% 確実に自動復旧します。

---

## 5. 自動起動のインストール・セットアップ手順

本リポジトリのルートまたは本ディレクトリから以下を実行します：

```bash
# 1. ユーザー環境 (~/.local/bin, ~/.config/systemd/user) へシンボリックリンク配備
cd scripts/nemoclaw
./install.sh

# 2. systemd ユーザーサービスの有効化・起動
systemctl --user enable --now nemoclaw-openshell-gateway.service
systemctl --user enable --now nemoclaw-hermes-shutdown-watch.service
systemctl --user enable --now nemoclaw-hermes-start.service

# (Google Bridge を利用する場合のみ)
systemctl --user enable --now google-bridge.service

# 3. 稼働状態の確認
systemctl --user status nemoclaw-openshell-gateway.service
systemctl --user status nemoclaw-hermes-start.service
systemctl --user status nemoclaw-hermes-shutdown-watch.service
# (Google Bridge 利用時)
systemctl --user status google-bridge.service
```

---

## 6. 運用確認コマンド

```bash
# サンドボックス状態の確認
nemoclaw hermes-sandbox status

# OpenShell 側からのステータス確認
openshell sandbox list

# Web Dashboard (ポート 18789) への疎通確認
curl -I http://127.0.0.1:18789/

# 手動での自律修復・強制解除テスト
python3 ~/.local/bin/nemoclaw-reconcile-sandbox.py
```

---

## 7. Hermes 推奨設定 (`/sandbox/.hermes/config.yaml`)

直接依頼（Direct Request）でファイル操作や自律タスクを安定遂行させるための推奨設定です。`setup-hermes.sh` がこの内容を適用します（親の `model` はオンボーディングが `inference.local` 経由で設定します）：

```yaml
model:
  provider: custom
  base_url: https://api.tokenfactory.nebius.com/v1
  api_key: ${TOKENFACTORY_API_KEY}
  default: nvidia/Nemotron-3.5-Super-VL

delegation:
  provider: custom
  base_url: https://api.tokenfactory.nebius.com/v1
  api_key: ${TOKENFACTORY_API_KEY}
  model: nvidia/Nemotron-3-Ultra-550b-a55b
  reasoning_effort: high

tools:
  tool_search:
    enabled: "off" # Direct disclosure（JSONエスケープ破損防止）

agent:
  max_turns: 15 # persona ask用。直接依頼は別WorkerがUltraで実行

platform_toolsets:
  cron: # 直接依頼でファイル操作や自律作業、委譲をさせる場合に許可
    - web
    - file
    - terminal
    - code_execution
    - delegation
```
