#!/usr/bin/env bash
# Point this deployment's personas at the geo-home-mcp server.
#
# Reads the MCP URL and the bearer token from geo-home-mcp's Terraform outputs
# and writes them into infra/terraform.tfvars as the `mcp_servers` list, so the
# key never has to be copied by hand. Run ./infra/deploy.sh afterwards.
set -euo pipefail

cd "$(dirname "$0")"

GEO_HOME_DIR=${GEO_HOME_DIR:-../../geo-home-mcp}
GEO_TF="$GEO_HOME_DIR/terraform"
if [[ ! -d "$GEO_TF" ]]; then
  printf 'geo-home-mcp not found at %s (set GEO_HOME_DIR)\n' "$GEO_HOME_DIR" >&2
  exit 1
fi

URL=$(terraform -chdir="$GEO_TF" output -raw mcp_url)
KEY=$(terraform -chdir="$GEO_TF" output -raw mcp_api_key 2>/dev/null || true)
if [[ -z "$KEY" ]]; then
  printf 'geo-home-mcp has no mcp_api_key yet. Set it in %s/terraform.tfvars and apply first.\n' "$GEO_TF" >&2
  exit 1
fi

if [[ -f terraform.tfvars ]] && grep -q '^mcp_servers' terraform.tfvars; then
  # Replace only our block; anything else in the file is the operator's.
  python3 - terraform.tfvars <<'PY'
import re, sys
path = sys.argv[1]
text = open(path).read()
text = re.sub(r'^mcp_servers\s*=\s*\[.*?^\]\n', '', text, flags=re.S | re.M)
open(path, 'w').write(text)
PY
fi
cat >> terraform.tfvars <<TFVARS
mcp_servers = [
  { name = "geo", url = "$URL", headers = { Authorization = "Bearer $KEY" } },
]
TFVARS

printf 'infra/terraform.tfvars: mcp_servers -> geo (%s)\n' "$URL"
printf 'Next: ./infra/deploy.sh\n'
