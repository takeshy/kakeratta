# NemoClaw / OpenShell / Hermes Agent 運用・管理スクリプト群

kakeratta の自律エージェント運用基盤（NVIDIA NemoClaw + OpenShell + Hermes Agent）をホストマシン上で安定稼働させるためのスクリプトおよび systemd サービス定義集です。

---

## 1. ディレクトリ構成

```text
scripts/nemoclaw/
├── README.md                              # 本ドキュメント
├── install.sh                             # ~/.local/bin および ~/.config/systemd/user への配備/シンボリックリンク作成
├── bin/                                   # 実行スクリプト群
│   ├── nemoclaw-wait-docker.sh            # Docker デーモン起動待機
│   ├── nemoclaw-reconcile-sandbox.py      # Phase: Error デッドロックの自律修復・強制解除
│   ├── nemoclaw-hermes-autostart.sh       # 自己修復型自動起動ラッパー
│   ├── nemoclaw-hermes-autostop.sh        # 排他ロック付き先行安全停止ラッパー
│   └── nemoclaw-hermes-shutdown-monitor.sh # systemd-inhibit 事前シャットダウン監視
└── systemd/                               # systemd ユーザーサービス定義
    ├── nemoclaw-openshell-gateway.service # Gateway デーモンサービス
    ├── nemoclaw-hermes-start.service      # Hermes サンドボックス自動起動・安全停止サービス
    └── nemoclaw-hermes-shutdown-watch.service # 事前シャットダウン遅延監視サービス
```

---

## 2. 各コンポーネントの役割

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

---

## 3. 多重防御アーキテクチャ (sudo 完全不要)

1. **平時（通常の再起動・シャットダウン）**:
   * `nemoclaw-hermes-shutdown-watch.service` が OS のシャットダウン予告を受信。
   * logind に電源断を最大 15 秒遅延させ、Docker デーモンが停止処理に入る前に Hermes を先行クリーン停止。
   * エラー終了（exit 143）そのものを根本防止します。
2. **非常時（突発的な電源断・クラッシュ）**:
   * 万一 `Phase: Error` で停止した場合でも、次回起動時に `nemoclaw-hermes-autostart.sh` 経由で `nemoclaw-reconcile-sandbox.py` が自動検知。
   * DB 内部のステータスを即座に `Phase: Ready` に強制解除・同期し、100% 確実に自動復旧します。

---

## 4. インストール・セットアップ手順

本リポジトリのルートまたは本ディレクトリから以下を実行します：

```bash
# 1. ユーザー環境 (~/.local/bin, ~/.config/systemd/user) へシンボリックリンク配備
cd scripts/nemoclaw
./install.sh

# 2. systemd ユーザーサービスの有効化・起動
systemctl --user enable --now nemoclaw-openshell-gateway.service
systemctl --user enable --now nemoclaw-hermes-shutdown-watch.service
systemctl --user enable --now nemoclaw-hermes-start.service

# 3. 稼働状態の確認
systemctl --user status nemoclaw-openshell-gateway.service
systemctl --user status nemoclaw-hermes-start.service
systemctl --user status nemoclaw-hermes-shutdown-watch.service
```

---

## 5. 運用確認コマンド

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

## 6. Hermes 推奨設定 (`/sandbox/.hermes/config.yaml`)

直接依頼（Direct Request）でファイル操作や自律タスクを安定遂行させるための推奨設定です：

```yaml
tools:
  tool_search:
    enabled: "off" # Direct disclosure（JSONエスケープ破損防止）

agent:
  max_turns: 15 # 通常のペルソナ回答は15ターン（Wake Gateが直接依頼時に自動で40に一時拡張）

platform_toolsets:
  cron: # 直接依頼でファイル操作や自律作業をさせる場合に許可
    - web
    - file
    - terminal
    - code_execution
```
