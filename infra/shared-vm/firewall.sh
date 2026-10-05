#!/bin/bash
set -euo pipefail
# Docker reuses this chain. Install it before Docker can restart containers.
iptables -N DOCKER-USER 2>/dev/null || true
for app in gemihub kakeratta geo; do
  for port in 80 443; do
    iptables -C DOCKER-USER -i "br-$app" -d 169.254.169.254 -p tcp --dport "$port" -j REJECT 2>/dev/null || iptables -I DOCKER-USER -i "br-$app" -d 169.254.169.254 -p tcp --dport "$port" -j REJECT
  done
done
