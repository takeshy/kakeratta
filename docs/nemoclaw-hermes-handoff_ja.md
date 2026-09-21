# NemoClaw / Hermes 引き継ぎ書

更新日: 2026-09-21

## 現在の状態

- sandbox 名は `hermes-sandbox`。
- NemoClaw のレジストリは、カスタム Dockerfile を使う構成になっている。
  - `/home/takeshy/.config/nemoclaw/hermes-image/Dockerfile`
  - ベースイメージは digest 固定。
  - `start.sh` を `/usr/local/bin/nemoclaw-start` にコピーする。
  - `NEMOCLAW_TOOL_DISCLOSURE` の `ARG` と `ENV` を含む。
- 再作成は成功し、コンテナ内の `nemoclaw-start` は修正版になっている。
- 推論設定は compatible-endpoint / `nvidia/nemotron-3-super-120b-a12b`。
- `fromDockerfile` と `imageTag` は `/home/takeshy/.nemoclaw/sandboxes.json` で確認できる。

## 原因

管理付き MCP の追加時、Hermes Gateway は終了コード `75` で再起動を要求する。旧 `start.sh` は `set -e` によりこの終了を異常終了として扱い、Gateway だけでなく sandbox 全体を停止させていた。実行中のコンテナを直接修正するだけでは、sandbox 再作成時に修正が失われる。

## 実施済みの変更

- 公開 skill に、MCP を先に設定すること、`Ready` と `hermes mcp list` を確認すること、終了コード 75 の確認方法を追記した。
- 英語・日本語の統合ドキュメントに、カスタムイメージと Dockerfile の要件を追記した。
- NemoClaw 用のカスタムイメージを作成し、sandbox を再作成した。
- 再作成前のバックアップを保存した。

変更ファイル:

- `frontend/public/integrations/hermes/kakeratta/SKILL.md`
- `docs/nemoclaw-hermes-integration.md`
- `docs/nemoclaw-hermes-integration_ja.md`

## 復旧完了状況 (2026-09-21 完了)

sandbox 再作成に伴い消失していた kakeratta の全状態について、OpenShell の credential 管理境界を維持したまま復旧・動作検証を完了した。

- [x] skill: `/sandbox/.hermes/skills/kakeratta/SKILL.md` 配置完了 (`hermes skills list` で enabled)
- [x] Wake Gate: `/sandbox/.hermes/scripts/check_kakeratta_gate.py` 配置・実行権限付与完了
- [x] OpenShell ポリシー & Provider: `hermes-sandbox-mcp-kakeratta` アタッチ、`mcp_bridge_kakeratta` ポリシー (v3) 適用完了
- [x] 管理付き kakeratta MCP: `/sandbox/.hermes/config.yaml` 登録、Generation-scoped プレースホルダー経由での疎通確認完了 (`wakeAgent: false`)
- [x] cron job: `kakeratta` (新 job ID: `b9506f3083e8`, 1分間隔) 登録完了
- [x] cron 実行検証: 1分周期の tick で Wake Gate が呼び出され、アイドル時に LLM 呼び出しをスキップ (`wakeAgent=false, skipping agent run` / `last_status: ok`) することを確認

## 復元時の参照元バックアップ

`/home/takeshy/.nemoclaw/rebuild-backups/hermes-sandbox/2026-09-21T00-48-51-420Z`

認証情報は復元・表示・Dockerfile への埋め込みを一切行わず、OpenShell の provider/proxy 解決を維持。

## 復旧手順（実績）

1. コンテナ名を確認する。

   ```bash
   docker ps --format '{{.Names}} {{.Status}} {{.Image}}'
   ```

2. 現在のコンテナへ skill と Wake Gate をアップロードする。`nemoclaw upload` の宛先はファイルパスを明示し、`.py` をディレクトリとして作らないようにする。

   ```bash
   nemoclaw upload ./frontend/public/integrations/hermes/kakeratta/SKILL.md \
     hermes-sandbox:/sandbox/.hermes/skills/kakeratta/SKILL.md
   nemoclaw upload ./frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py \
     hermes-sandbox:/sandbox/.hermes/scripts/check_kakeratta_gate.py
   nemoclaw hermes-sandbox exec -- chmod +x /sandbox/.hermes/scripts/check_kakeratta_gate.py
   ```

3. `nemoclaw hermes-sandbox mcp list` と `hermes mcp list` で kakeratta が enabled か確認する。無ければ、OpenShell の既存 credential を使って managed MCP を再登録する。トークンをコマンドラインやログに出さない。

4. Wake Gate 付き cron を 1 件だけ作る。

   ```bash
   nemoclaw hermes-sandbox exec -- hermes cron create 'every 1m' \
     --name kakeratta --skill kakeratta \
     --script check_kakeratta_gate.py --deliver local \
     --model nvidia/nemotron-3-super-120b-a12b \
     --provider compatible-endpoint
   ```

5. `hermes cron list` で重複のないこと、Wake Gate の空タスク実行が `wakeAgent: false` になることを確認する。

## 注意点

- `nemoclaw onboard --from` の Dockerfile には、最終ステージに次の 2 行が必須。

  ```dockerfile
  ARG NEMOCLAW_TOOL_DISCLOSURE=progressive
  ENV NEMOCLAW_TOOL_DISCLOSURE=${NEMOCLAW_TOOL_DISCLOSURE}
  ```

- OpenShell の gateway builder は BuildKit ではないため、`COPY --chmod` は使わない。`start.sh` をビルドコンテキストで `chmod 755` にして、`COPY --chown=root:root` を使う。
- 再作成前には必ず snapshot を作る。
- `~/.nemoclaw-portable-host.lock` が残っている場合は、owner PID が存在するか確認する。PID が存在しない stale lock だけを削除する。
- host 側の `nemoclaw-hermes-start.service` は論理 sandbox 名で起動するため、再作成後も構成は維持される。ただし、skill・MCP・cron は sandbox 状態として別途復元が必要。
- OpenShell の L7 プロキシ認証プレースホルダーは generation-scoped（例: `openshell:resolve:env:v<REVISION>_KAKERATTA_MCP_TOKEN`）を要求する。靜的なプレースホルダー名では置換されず 401 となるため、`GetSandboxProviderEnvironment` の `provider_env_revision` を参照して一致させる。
- ホスト再起動時の自動起動サービス構成（NemoClaw公式設計準拠）:
  - NemoClaw は upstream の `openshell-gateway.service` が存在すると上書きを控える設計（sourceBoundary）のため、公式管理サービス **`nemoclaw-openshell-gateway.service`**（先頭に `# NEMOCLAW_MANAGED_OPENSHELL_GATEWAY=1` を含む）を使用する。
  - Gateway 設定は `~/.config/openshell/gateway.env` (0600) に記述する（`OPENSHELL_DATA_DIR=/home/takeshy/.local/share/openshell-docker-gateway`、`OPENSHELL_DRIVER=docker`、フォワードバインド設定等）。
  - Hermes 起動サービス `nemoclaw-hermes-start.service` の依存先を `nemoclaw-openshell-gateway.service` に指定する。
  - **GatewayAuthorityError（権限主体変更検知によるプローブ拒絶）の回避**:
    - オンボーディング記録（`~/.nemoclaw/onboard-session.json`）の `checkpoint.gatewayAuthority.value.source` が `standalone` のままだと、Gateway を packaged-service（systemd）に移行した際に NemoClaw の権限保護フェイルセーフが働き、ポートフォワード復旧プローブが失敗して「`host port ... is held by a listener that NemoClaw cannot attribute...`」という拒絶エラーになる。
    - systemd ユーザーサービス運用に移行する際は、`onboard-session.json` 内の `gatewayAuthority.value.source` を `"packaged-service"` に更新してオンボーディング権限と一致させる必要がある。
  - **ブート時 Docker 待ち合わせ (`docker_unreachable` 回避による根本予防)**:
    - systemd ユーザーサービスはシステム側の `docker.service` を直接 `Requires=` で待機できない仕様のため、ホストブート直後に Docker 未起動のまま起動試行して `docker_unreachable` となり、OpenShell が `Phase: Error` デッドロックに陥る問題があった。
    - この対策として、サービス起動前の `ExecStartPre` にて `nemoclaw-wait-docker.sh`（Docker デーモンの応答待ち）を実行し、Docker が完全に立ち上がるまで起動を待機させる。
    - ※ SQLite の Protobuf バイナリ直接置換による強制ステータス書き換えは DB 破損リスクがあるため常駐サービスには組み込まず、Docker 起動待機によって `Phase: Error` の発生自体を根本予防する安全・クリーンな構成にしている。



