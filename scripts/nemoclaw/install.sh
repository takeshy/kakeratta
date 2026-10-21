#!/bin/bash
# ==============================================================================
# NemoClaw Scripts & systemd Units Installer
# ==============================================================================
# リポジトリ内の NemoClaw 運用スクリプトおよび systemd ユニットファイルを
# ユーザー環境 (~/.local/bin, ~/.config/systemd/user) に配備します。
# デフォルトではシンボリックリンクを作成し、リポジトリ側の変更が即座に反映されます。
#
# 使用法:
#   ./install.sh           # シンボリックリンクで配備 (推奨)
#   ./install.sh --copy    # 実ファイルコピーで配備
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_SRC_DIR="$SCRIPT_DIR/bin"
SYSTEMD_SRC_DIR="$SCRIPT_DIR/systemd"

BIN_DEST_DIR="$HOME/.local/bin"
SYSTEMD_DEST_DIR="$HOME/.config/systemd/user"

MODE="symlink"
if [ "${1:-}" = "--copy" ]; then
    MODE="copy"
fi

echo "=========================================================="
echo " NemoClaw Integration Scripts Installer"
echo " Mode: $MODE"
echo " Target User: $(whoami)"
echo "=========================================================="

mkdir -p "$BIN_DEST_DIR"
mkdir -p "$SYSTEMD_DEST_DIR"

install_file() {
    local src="$1"
    local dest="$2"
    local is_executable="${3:-false}"

    if [ "$is_executable" = "true" ]; then
        chmod +x "$src"
    fi

    if [ -e "$dest" ] || [ -L "$dest" ]; then
        # 既に同一のリンク先を指している場合はスキップ
        if [ -L "$dest" ] && [ "$(readlink -f "$dest")" = "$(readlink -f "$src")" ]; then
            echo "  [OK] Already linked: $dest -> $src"
            return
        fi
        # バックアップ作成
        local backup="${dest}.bak.$(date +%Y%m%d%H%M%S)"
        mv "$dest" "$backup"
        echo "  [BACKUP] $dest -> $backup"
    fi

    if [ "$MODE" = "symlink" ]; then
        ln -s "$src" "$dest"
        echo "  [LINK] $dest -> $src"
    else
        cp "$src" "$dest"
        if [ "$is_executable" = "true" ]; then
            chmod +x "$dest"
        fi
        echo "  [COPY] $dest"
    fi
}

echo ""
echo "--- Installing Executable Scripts to $BIN_DEST_DIR ---"
for file in "$BIN_SRC_DIR"/*; do
    if [ -f "$file" ]; then
        fname="$(basename "$file")"
        install_file "$file" "$BIN_DEST_DIR/$fname" true
    fi
done

# Optional: Build and install google-bridge binary if source is present
GOOGLE_BRIDGE_SRC="$SCRIPT_DIR/../../tools/google-bridge/cmd/google-bridge"
if [ -d "$GOOGLE_BRIDGE_SRC" ]; then
    echo ""
    echo "--- Building google-bridge companion binary ---"
    GO_CMD=""
    if command -v mise >/dev/null 2>&1; then
        GO_CMD="mise exec -- go"
    elif command -v go >/dev/null 2>&1; then
        GO_CMD="go"
    fi

    if [ -n "$GO_CMD" ]; then
        $GO_CMD build -o "$BIN_DEST_DIR/google-bridge" "$GOOGLE_BRIDGE_SRC"
        chmod +x "$BIN_DEST_DIR/google-bridge"
        echo "  [BUILD] $BIN_DEST_DIR/google-bridge"
    else
        echo "  [SKIP] Go or mise not found in PATH; skipping google-bridge build"
    fi
fi

echo ""
echo "--- Installing systemd User Services to $SYSTEMD_DEST_DIR ---"
for file in "$SYSTEMD_SRC_DIR"/*.service; do
    if [ -f "$file" ]; then
        fname="$(basename "$file")"
        dest="$SYSTEMD_DEST_DIR/$fname"
        # Newer NemoClaw installs and manages its own gateway unit (with TLS
        # certificate generation). Replacing it with ours breaks the gateway.
        if [ "$fname" = "nemoclaw-openshell-gateway.service" ] && [ -f "$dest" ] && [ ! -L "$dest" ] \
            && grep -q "NVIDIA CORPORATION" "$dest"; then
            echo "  [KEEP] NemoClaw-managed gateway unit: $dest"
            continue
        fi
        install_file "$file" "$dest" false
    fi
done

echo ""
echo "--- Reloading systemd user daemon ---"
systemctl --user daemon-reload
echo "Daemon reloaded successfully."

# Enable and start google-bridge.service if configured
if [ -f "$HOME/.config/google-bridge/config.json" ]; then
    echo ""
    echo "--- Activating google-bridge.service (found ~/.config/google-bridge/config.json) ---"
    systemctl --user enable --now google-bridge.service || true
fi

echo ""
echo "=========================================================="
echo " Installation completed successfully!"
echo " Services can be checked with:"
echo "   systemctl --user status nemoclaw-openshell-gateway.service"
echo "   systemctl --user status nemoclaw-hermes-start.service"
echo "   systemctl --user status nemoclaw-hermes-shutdown-watch.service"
if [ -f "$HOME/.config/google-bridge/config.json" ]; then
    echo "   systemctl --user status google-bridge.service"
fi
echo "=========================================================="
