#!/bin/bash
# ==============================================================================
# NemoClaw Hermes Agent Safe Autostop Script (v2 - Idempotent)
# ==============================================================================
# PC シャットダウン時やサービス停止時に、Docker デーモンが生存している間に
# 安全かつ確実にサンドボックスをクリーン停止 (Phase: Stopped) させます。
# 二重呼び出しされても安全に即時復帰します。
# ==============================================================================

set -u

NEMOCLAW_BIN="${NEMOCLAW_BIN:-$HOME/.local/bin/nemoclaw}"
OPENSHELL_BIN="${OPENSHELL_BIN:-$HOME/.local/bin/openshell}"
LOG_TAG="[nemoclaw-autostop]"
STOP_LOCK="/tmp/.hermes-autostop.lock"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $LOG_TAG $*"
}

# 多重実行防止 (排他ロック)
exec 200>"$STOP_LOCK"
if ! flock -n 200; then
    log "Another autostop instance is already running; waiting for it to complete..."
    flock 200
    log "Previous autostop finished. Exiting."
    exit 0
fi

log "Executing safe pre-shutdown for Hermes sandbox..."

# 1. Docker デーモン疎通確認
if ! docker info >/dev/null 2>&1; then
    log "Notice: Docker daemon is already unreachable. Skipping sandbox stop command."
    exit 0
fi

# 2. 現在のサンドボックス状態を確認 (すでに停止していればスキップ)
current_phase=$("$OPENSHELL_BIN" sandbox list 2>/dev/null | awk '$1 == "hermes-sandbox" { print $3 }' || true)
if [ "$current_phase" = "Stopped" ]; then
    log "Sandbox 'hermes-sandbox' is already in Phase: Stopped. Nothing to do."
    exit 0
fi

# 3. クリーン停止の実行 (タイムアウト 40 秒)
log "Sandbox is currently '$current_phase'. Executing 'nemoclaw hermes-sandbox stop'..."
if timeout 40 "$NEMOCLAW_BIN" hermes-sandbox stop; then
    log "SUCCESS: Hermes sandbox cleanly stopped (Phase: Stopped)."
else
    log "Warning: 'nemoclaw hermes-sandbox stop' returned non-zero or timed out; ignoring to allow system shutdown."
fi

exit 0
