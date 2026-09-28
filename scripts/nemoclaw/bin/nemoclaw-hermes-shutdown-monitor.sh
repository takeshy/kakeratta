#!/bin/bash
# ==============================================================================
# NemoClaw Hermes Pre-Shutdown Watcher
# ==============================================================================
# systemd-inhibit (--what=shutdown --mode=delay) を保持し、
# systemd-logind からの PrepareForShutdown(true) シグナルを受信した瞬間に
# Docker デーモンが生存している間に Hermes をクリーン停止させます。
# ==============================================================================

set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AUTOSTOP_BIN="${AUTOSTOP_BIN:-$SCRIPT_DIR/nemoclaw-hermes-autostop.sh}"
if [ ! -x "$AUTOSTOP_BIN" ] && [ -x "$HOME/.local/bin/nemoclaw-hermes-autostop.sh" ]; then
    AUTOSTOP_BIN="$HOME/.local/bin/nemoclaw-hermes-autostop.sh"
fi

LOG_TAG="[nemoclaw-shutdown-monitor]"
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $LOG_TAG $*"
}

consume_shutdown_events() {
    local line
    log "Listening for PrepareForShutdown signals via D-Bus..."
    while IFS= read -r line; do
        if [[ "$line" == *"boolean true"* ]]; then
            log "PrepareForShutdown(true) signal received! Stopping Hermes sandbox cleanly before Docker stops..."
            "$AUTOSTOP_BIN" || true
            log "Hermes sandbox stopped. Releasing inhibitor lock to allow system shutdown."
            return 0
        fi
    done
}

monitor_shutdown_events() {
    local monitor_pid
    coproc SHUTDOWN_EVENTS {
        exec dbus-monitor --system \
            "type='signal',sender='org.freedesktop.login1',interface='org.freedesktop.login1.Manager',member='PrepareForShutdown'"
    }
    local monitor_fd=${SHUTDOWN_EVENTS[0]}
    monitor_pid=$SHUTDOWN_EVENTS_PID

    cleanup_monitor() {
        kill "$monitor_pid" 2>/dev/null || true
        wait "$monitor_pid" 2>/dev/null || true
    }
    trap cleanup_monitor EXIT

    consume_shutdown_events <&"$monitor_fd"
    local status=$?
    cleanup_monitor
    trap - EXIT
    return "$status"
}

case ${1:-} in
    --inhibited)
        monitor_shutdown_events
        exit 0
        ;;
esac

log "Starting systemd-inhibit delay lock for Hermes pre-shutdown..."
exec systemd-inhibit \
    --what=shutdown \
    --mode=delay \
    --who="NemoClaw Hermes" \
    --why="Stop Hermes sandbox cleanly before Docker stops" \
    "$0" --inhibited
