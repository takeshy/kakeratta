#!/bin/bash
# ==============================================================================
# NemoClaw Hermes Agent Self-Healing Autostart Script (v2)
# ==============================================================================
# PC 起動時に Hermes サンドボックスを自律的に起動します。
# 試行ループごとに DB の Phase: Error デッドロックを検査・自動修復し、
# Web Dashboard (ポート 18789) の健全性を確認するまでを自動実行します。
# ==============================================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEMOCLAW_BIN="${NEMOCLAW_BIN:-$HOME/.local/bin/nemoclaw}"
OPENSHELL_BIN="${OPENSHELL_BIN:-$HOME/.local/bin/openshell}"
RECONCILE_BIN="${RECONCILE_BIN:-$SCRIPT_DIR/nemoclaw-reconcile-sandbox.py}"
if [ ! -x "$RECONCILE_BIN" ] && [ -x "$HOME/.local/bin/nemoclaw-reconcile-sandbox.py" ]; then
    RECONCILE_BIN="$HOME/.local/bin/nemoclaw-reconcile-sandbox.py"
fi
LOG_TAG="[nemoclaw-autostart]"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $LOG_TAG $*"
}

# 1. Docker デーモンの起動待機 (最大60秒)
log "Waiting for Docker daemon to become responsive..."
docker_ready=0
for i in $(seq 1 60); do
    if docker info >/dev/null 2>&1; then
        docker_ready=1
        break
    fi
    sleep 1
done

if [ "$docker_ready" -ne 1 ]; then
    log "ERROR: Docker daemon did not respond within 60s"
    exit 1
fi
log "Docker daemon is ready."

# 2. 孤立した古い PID ロックファイルの確認・削除
for lockfile in "$HOME/.nemoclaw-portable-host.lock" "$HOME/.openshell.lock"; do
    if [ -f "$lockfile" ]; then
        lock_pid=$(cat "$lockfile" 2>/dev/null || true)
        if [ -n "$lock_pid" ] && ! kill -0 "$lock_pid" 2>/dev/null; then
            log "Notice: Removing stale lock file: $lockfile (PID: $lock_pid was dead)"
            rm -f "$lockfile"
        fi
    fi
done

# 3. OpenShell Gateway の起動待機 (最大30秒)
log "Waiting for OpenShell Gateway..."
gw_ready=0
for i in $(seq 1 30); do
    if "$OPENSHELL_BIN" status >/dev/null 2>&1; then
        gw_ready=1
        break
    fi
    sleep 1
done

if [ "$gw_ready" -ne 1 ]; then
    log "Warning: OpenShell Gateway not responding yet; restarting gateway service..."
    systemctl --user restart nemoclaw-openshell-gateway.service || true
    sleep 3
fi

# 4. サンドボックスの自律修復付き起動ループ (最大5回試行)
log "Entering start loop with self-healing deadlock reconciler..."
start_success=0

for attempt in 1 2 3 4 5; do
    log "Start attempt $attempt of 5..."

    # 4-a. 試行ごとに DB の Phase: Error デッドロックを検査・修復
    reconcile_out=$("$RECONCILE_BIN" 2>&1 || true)
    echo "$reconcile_out"
    if echo "$reconcile_out" | grep -q "Successfully repaired"; then
        log "DB repair performed. Waiting 3s for Gateway settlement..."
        sleep 3
    fi

    # 4-b. nemoClaw 経由で起動
    if "$NEMOCLAW_BIN" hermes-sandbox start; then
        log "Start attempt $attempt succeeded!"
        start_success=1
        break
    else
        log "Start attempt $attempt returned failure."
        # コンテナが動いているか確認
        if docker ps | grep -q "hermes-sandbox"; then
            log "Notice: Sandbox container is running in Docker; waiting 6s for internal agent gateway initialization..."
            sleep 6
        else
            log "Notice: Sandbox container not running; waiting 3s before retry..."
            sleep 3
        fi
    fi
done

if [ "$start_success" -ne 1 ]; then
    log "ERROR: Failed to start Hermes sandbox after 5 attempts."
    exit 1
fi

# 5. Web Dashboard & 稼働検証 (ポート 18789)
log "Verifying Hermes Web Dashboard responsiveness on http://127.0.0.1:18789..."
dashboard_ready=0
for i in $(seq 1 30); do
    if curl -s -f -o /dev/null "http://127.0.0.1:18789/"; then
        dashboard_ready=1
        break
    fi
    sleep 1
done

if [ "$dashboard_ready" -eq 1 ]; then
    log "SUCCESS: Hermes Agent and Web Dashboard are fully up and running!"
    exit 0
else
    log "Notice: Hermes sandbox started, but Web Dashboard port 18789 did not answer HTTP 200 within 30s. Port forward might still be establishing."
    exit 0
fi
