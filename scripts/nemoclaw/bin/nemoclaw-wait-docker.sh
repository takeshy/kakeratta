#!/bin/bash
# ==============================================================================
# NemoClaw: Wait for Docker daemon
# ==============================================================================
# Docker デーモンが応答可能になるまで最大 60 秒間待機します。
# systemd サービス（nemoclaw-openshell-gateway.service）の ExecStartPre 等で使用。
# ==============================================================================

set -eu

for i in $(seq 1 60); do
  if docker info >/dev/null 2>&1; then
    exit 0
  fi
  sleep 1
done

echo "Timed out waiting for Docker daemon" >&2
exit 1
