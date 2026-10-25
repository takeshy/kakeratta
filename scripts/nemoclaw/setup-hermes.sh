#!/bin/bash
# ==============================================================================
# NemoClaw / OpenShell 上に kakeratta 用の Hermes を構築する
# ==============================================================================
# 親（担当の回答）は Super-VL を OpenShell の inference.local で、子（@agent と
# delegation）は Ultra を Provider v2 経由の Token Factory 直結で動かします。
# 何度実行しても同じ状態に収束します。
#
# 必要なもの:
#   - Docker（Linux の dockerd、または WSL から使える Docker Desktop）
#   - ~/.config/nemoclaw/tokenfactory.env   TOKENFACTORY_API_KEY=...
#   - ~/.config/nemoclaw/kakeratta-mcp.env  KAKERATTA_MCP_URL=... / KAKERATTA_MCP_TOKEN=...
#     （kakeratta の「設定 → 外部連携」で発行。URL はサンドボックス
#       から届く宛先。https はそのまま、http（ローカル開発）はホストの IP を書く）
#
# 使用法:
#   scripts/nemoclaw/setup-hermes.sh
#
# 環境変数で変更できるもの:
#   SANDBOX (hermes-sandbox) / GATEWAY (nemoclaw) / NEMOCLAW_INSTALL_TAG (v0.0.135)
#   TOKENFACTORY_ENV / KAKERATTA_MCP_ENV（上記ファイルの場所）
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
INTEGRATION_DIR="$REPO_ROOT/frontend/public/integrations/hermes/kakeratta"
PROFILE_FILE="$SCRIPT_DIR/profiles/tokenfactory.yaml"

SANDBOX="${SANDBOX:-hermes-sandbox}"
GATEWAY="${GATEWAY:-nemoclaw}"
NEMOCLAW_INSTALL_TAG="${NEMOCLAW_INSTALL_TAG:-v0.0.135}"
TOKENFACTORY_ENV="${TOKENFACTORY_ENV:-$HOME/.config/nemoclaw/tokenfactory.env}"
KAKERATTA_MCP_ENV="${KAKERATTA_MCP_ENV:-$HOME/.config/nemoclaw/kakeratta-mcp.env}"

TOKENFACTORY_URL="https://api.tokenfactory.nebius.com/v1"
PARENT_MODEL="nvidia/Nemotron-3.5-Super-VL"
ULTRA_MODEL="nvidia/Nemotron-3-Ultra-550b-a55b"
PROVIDER_NAME="tokenfactory-v2"
# Hermes が鍵なしで使う Web 検索。OpenShell のプロキシを通すので許可が要る。
SEARCH_HOSTS=(mcp.exa.ai search.parallel.ai api.firecrawl.dev api.keenable.ai)
SANDBOX_BINARIES=(--binary /usr/local/bin/hermes --binary "/usr/bin/python3*" --binary "/opt/hermes/.venv/bin/python*")
HERMES_PY=/opt/hermes/.venv/bin/python

export PATH="$HOME/.local/bin:$PATH"

log() { echo "==> $*"; }
die() { echo "ERROR: $*" >&2; exit 1; }
strip_ansi() { sed 's/\x1b\[[0-9;]*m//g'; }
sx() { openshell sandbox exec -g "$GATEWAY" -n "$SANDBOX" -- "$@"; }

# --- 1. 前提 -----------------------------------------------------------------
log "前提を確認"
docker info >/dev/null 2>&1 || die "Docker に接続できません（WSL なら Docker Desktop を起動）"
[ -r "$TOKENFACTORY_ENV" ] || die "$TOKENFACTORY_ENV がありません"
[ -r "$KAKERATTA_MCP_ENV" ] || die "$KAKERATTA_MCP_ENV がありません"
set -a
# shellcheck source=/dev/null
. "$TOKENFACTORY_ENV"
# shellcheck source=/dev/null
. "$KAKERATTA_MCP_ENV"
set +a
[ -n "${TOKENFACTORY_API_KEY:-}" ] || die "TOKENFACTORY_API_KEY が空です"
[ -n "${KAKERATTA_MCP_URL:-}" ] && [ -n "${KAKERATTA_MCP_TOKEN:-}" ] || die "KAKERATTA_MCP_URL / KAKERATTA_MCP_TOKEN が空です"
for f in "$PROFILE_FILE" "$INTEGRATION_DIR/SKILL.md" "$INTEGRATION_DIR/scripts/check_kakeratta_gate.py" "$INTEGRATION_DIR/scripts/direct_request_worker.py"; do
    [ -r "$f" ] || die "$f がありません"
done

# NemoClaw は空きポートの判定で 127.0.0.1 へ接続し、15 秒で打ち切る。WSL の
# mirrored ネットワークでは未使用ポートへの接続が拒否されず固まり、サンドボックス
# 作成が "OpenShell forward operation timed out" で止まる。
if ! python3 -I -c '
import socket, sys
s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
try:
    socket.create_connection(("127.0.0.1", port), timeout=3).close()
except ConnectionRefusedError:
    sys.exit(0)
except OSError:
    sys.exit(1)
'; then
    die "127.0.0.1 の未使用ポートへの接続が拒否されず固まります。WSL なら .wslconfig を networkingMode=nat にして wsl --shutdown してください"
fi

# --- 2. NemoClaw とサンドボックス ---------------------------------------------
# オンボーディングの推論経路（inference.local）は親の Super-VL 用。
onboard_env=(
    NEMOCLAW_AGENT=hermes NEMOCLAW_NON_INTERACTIVE=1 NEMOCLAW_ACCEPT_THIRD_PARTY_SOFTWARE=1
    NEMOCLAW_SANDBOX_NAME="$SANDBOX" NEMOCLAW_PROVIDER=custom
    NEMOCLAW_ENDPOINT_URL="$TOKENFACTORY_URL" NEMOCLAW_MODEL="$PARENT_MODEL"
    COMPATIBLE_API_KEY="$TOKENFACTORY_API_KEY" NEMOCLAW_WEB_SEARCH_PROVIDER=none
    NEMOCLAW_POLICY_MODE=suggested NEMOCLAW_GATEWAY_PORT=8080 NEMOCLAW_DISABLE_TELEMETRY=1
)
if ! command -v nemohermes >/dev/null 2>&1; then
    log "NemoClaw $NEMOCLAW_INSTALL_TAG を導入してサンドボックス $SANDBOX を作成（5〜15分）"
    installer="$(mktemp -d)/nemoclaw.sh"
    curl -fsSL -o "$installer" https://www.nvidia.com/nemoclaw.sh
    env "${onboard_env[@]}" NEMOCLAW_INSTALL_TAG="$NEMOCLAW_INSTALL_TAG" bash "$installer" </dev/null
elif ! nemohermes "$SANDBOX" status >/dev/null 2>&1; then
    log "サンドボックス $SANDBOX を作成（5〜15分）"
    env "${onboard_env[@]}" nemohermes onboard --resume --non-interactive </dev/null
else
    log "サンドボックス $SANDBOX は作成済み"
fi
nemohermes "$SANDBOX" status >/dev/null 2>&1 || die "サンドボックス $SANDBOX が動いていません"

# --- 3. Provider v2（Ultra をモデル名のまま Token Factory へ）-----------------
log "Provider v2 を設定"
# サンドボックス単位の設定では Provider の通信許可がポリシーに入らない。
openshell settings set -g "$GATEWAY" --global --yes --key providers_v2_enabled --value true >/dev/null
# 取り込み済みのプロファイルは変えない（更新には書き出した resource_version が要る）。
if ! openshell provider profile export -g "$GATEWAY" tokenfactory >/dev/null 2>&1; then
    openshell provider profile import -g "$GATEWAY" --file "$PROFILE_FILE" >/dev/null
fi
if ! openshell provider get -g "$GATEWAY" "$PROVIDER_NAME" >/dev/null 2>&1; then
    # 値なしの --credential はこのシェルの環境から読む。
    openshell provider create -g "$GATEWAY" --name "$PROVIDER_NAME" --type tokenfactory --credential TOKENFACTORY_API_KEY >/dev/null
fi
if ! openshell sandbox provider list -g "$GATEWAY" "$SANDBOX" 2>/dev/null | strip_ansi | grep -q "^$PROVIDER_NAME "; then
    openshell sandbox provider attach -g "$GATEWAY" "$SANDBOX" "$PROVIDER_NAME" >/dev/null
fi
# Provider の資格情報はサンドボックスの起動時に環境へ入る。
if ! sx sh -c 'case "$TOKENFACTORY_API_KEY" in openshell:resolve:*) exit 0;; *) exit 1;; esac'; then
    log "Provider を読み込ませるためサンドボックスを再起動"
    nemohermes "$SANDBOX" stop >/dev/null
    nemohermes "$SANDBOX" start >/dev/null
fi

# --- 4. 通信許可 --------------------------------------------------------------
log "通信許可を設定"
policy="$(openshell policy get -g "$GATEWAY" "$SANDBOX" --full 2>/dev/null | strip_ansi)"
grep -q "_provider_tokenfactory_v2:" <<<"$policy" || die "ポリシーに _provider_tokenfactory_v2 が入っていません"
add_rule() {  # rule-name endpoint
    grep -q "^  $1:" <<<"$policy" && return 0
    openshell policy update -g "$GATEWAY" "$SANDBOX" --add-endpoint "$2" "${SANDBOX_BINARIES[@]}" --rule-name "$1" --wait >/dev/null
}
for host in "${SEARCH_HOSTS[@]}"; do
    add_rule "web_search_$(echo "$host" | cut -d. -f1-2 | tr '.-' '__')" "$host:443:full"
done

mcp_scheme="${KAKERATTA_MCP_URL%%://*}"
mcp_hostport="${KAKERATTA_MCP_URL#*://}"; mcp_hostport="${mcp_hostport%%/*}"
mcp_host="${mcp_hostport%%:*}"
if [ "$mcp_scheme" = "https" ]; then
    # 公開 HTTPS は NemoClaw の管理 MCP。トークンは OpenShell が通信時に差し込む。
    # 登録済みならキーは替えない（再発行時は mcp remove kakeratta してから再実行）。
    mcp_mode=managed
    if ! nemohermes "$SANDBOX" mcp list 2>/dev/null | strip_ansi | grep -E "^\s+kakeratta\s" | grep -q "env: KAKERATTA_MCP_TOKEN"; then
        nemohermes "$SANDBOX" mcp add kakeratta --url "$KAKERATTA_MCP_URL" --env KAKERATTA_MCP_TOKEN
    fi
else
    # http（ローカル開発）は管理 MCP に登録できない（HTTPS 必須）ので、宛先だけを
    # 許可してトークンは config.yaml に書く。
    mcp_mode=direct
    mcp_port="${mcp_hostport##*:}"; [ "$mcp_port" = "$mcp_hostport" ] && mcp_port=80
    endpoint="$mcp_host:$mcp_port:full:rest:enforce"
    [[ "$mcp_host" =~ ^[0-9.]+$ ]] && endpoint="$endpoint:allowed-ip=$mcp_host"
    add_rule kakeratta_local_mcp "$endpoint"
fi

# --- 5. Hermes の設定 ---------------------------------------------------------
log "Hermes の設定を更新"
# config.yaml はサンドボックス内で直接編集してよい（ハッシュファイルには触れない）。
printf '%s\n%s\n%s\n' "$mcp_mode" "$KAKERATTA_MCP_URL" "$KAKERATTA_MCP_TOKEN" | sx "$HERMES_PY" -I -c '
import os, sys, tempfile, yaml
mode, url, token = sys.stdin.read().split("\n")[:3]
path = "/sandbox/.hermes/config.yaml"
cfg = yaml.safe_load(open(path)) or {}
cfg.setdefault("tools", {}).setdefault("tool_search", {})["enabled"] = "off"
cfg.setdefault("agent", {})["max_turns"] = 15
cfg.setdefault("platform_toolsets", {})["cron"] = ["web", "file", "terminal", "code_execution", "delegation"]
cfg["delegation"] = {"provider": "custom", "base_url": sys.argv[1], "api_key": "${TOKENFACTORY_API_KEY}",
                     "model": sys.argv[2], "reasoning_effort": "high"}
if mode == "direct":
    cfg.setdefault("mcp_servers", {})["kakeratta"] = {"url": url, "headers": {"Authorization": "Bearer " + token}}
st = os.stat(path)
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path), prefix=".config.yaml.")
with os.fdopen(fd, "w") as f:
    yaml.safe_dump(cfg, f, sort_keys=False, allow_unicode=True)
os.chmod(tmp, st.st_mode & 0o777)
os.replace(tmp, path)
' "$TOKENFACTORY_URL" "$ULTRA_MODEL"

# --- 6. Skill・Wake Gate・ワーカーと cron -------------------------------------
log "Skill と Wake Gate を配置"
sx mkdir -p /sandbox/.hermes/skills/kakeratta /sandbox/.hermes/scripts
openshell sandbox upload -g "$GATEWAY" "$SANDBOX" "$INTEGRATION_DIR/SKILL.md" /sandbox/.hermes/skills/kakeratta/ >/dev/null 2>&1
for f in check_kakeratta_gate.py direct_request_worker.py; do
    openshell sandbox upload -g "$GATEWAY" "$SANDBOX" "$INTEGRATION_DIR/scripts/$f" /sandbox/.hermes/scripts/ >/dev/null 2>&1
done
if ! sx hermes cron list 2>/dev/null | grep -q "Name: *kakeratta$"; then
    # ask の取得期限は数分なので 1 分ごと。依頼がなければモデルは起動しない。
    sx hermes cron create "* * * * *" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local >/dev/null
fi

# --- 7. 確認 ------------------------------------------------------------------
log "確認"
sx "$HERMES_PY" -I -c '
import json, os, sys, urllib.request
req = urllib.request.Request(sys.argv[1] + "/chat/completions",
    data=json.dumps({"model": sys.argv[2], "messages": [{"role": "user", "content": "Reply with exactly: pong"}], "max_tokens": 400}).encode(),
    headers={"Authorization": "Bearer " + os.environ["TOKENFACTORY_API_KEY"], "Content-Type": "application/json"})
print("  Ultra via Provider v2:", json.load(urllib.request.urlopen(req, timeout=120)).get("model"))
' "$TOKENFACTORY_URL" "$ULTRA_MODEL"
gate_err="$(sx sh -c 'cd /sandbox/.hermes/scripts && timeout 60 /opt/hermes/.venv/bin/python check_kakeratta_gate.py 2>&1 >/dev/null' || true)"
if grep -q "list_asks failed" <<<"$gate_err"; then
    echo "$gate_err" | grep "list_asks failed" >&2
    die "kakeratta に接続できません。URL がサンドボックスから届くか、kakeratta で使用中の接続先がこのキーかを確認してください"
fi
echo "  kakeratta MCP: list_asks OK"
log "完了: $SANDBOX は 1 分ごとに kakeratta の依頼を確認します"
