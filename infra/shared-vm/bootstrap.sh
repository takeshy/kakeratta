#!/bin/bash
set -euo pipefail
umask 077
export DEBIAN_FRONTEND=noninteractive
install -d -m 0711 /opt/shared-apps
if ! command -v docker >/dev/null; then
  apt-get update
  apt-get install -y docker.io nginx python3 curl iptables
fi
systemctl enable --now docker
python3 - <<'PY'
import io, json, os, tarfile, urllib.request
def meta(path):
    return urllib.request.urlopen(urllib.request.Request('http://metadata.google.internal/computeMetadata/v1/' + path, headers={'Metadata-Flavor': 'Google'})).read()
token = json.loads(meta('instance/service-accounts/default/token'))['access_token']
uri = meta('instance/attributes/shared-bundle').decode()
bucket, key = uri[5:].split('/', 1)
from urllib.parse import quote
url = 'https://storage.googleapis.com/storage/v1/b/' + bucket + '/o/' + quote(key, safe='') + '?alt=media'
if not os.path.exists('/opt/shared-apps/deploy.py'):
    data = urllib.request.urlopen(urllib.request.Request(url, headers={'Authorization': 'Bearer ' + token})).read()
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        root = '/opt/shared-apps'
        for member in archive.getmembers():
            path = os.path.realpath(os.path.join(root, member.name))
            if not path.startswith(root + '/') or member.islnk() or member.isdev():
                raise ValueError('Unsafe bundle member')
            if member.issym() and (member.name != 'frontend' or member.linkname != 'frontend-initial'):
                raise ValueError('Unsafe bundle link')
        archive.extractall(root)
name = meta('instance/attributes/shared-runtime-secret').decode()
import base64
if not os.path.exists('/opt/shared-apps/runtime.json'):
    secret = json.load(urllib.request.urlopen(urllib.request.Request('https://secretmanager.googleapis.com/v1/' + name + ':access', headers={'Authorization': 'Bearer ' + token})))
    with open('/opt/shared-apps/runtime.json', 'wb') as out:
        out.write(base64.b64decode(secret['payload']['data']))
os.chmod('/opt/shared-apps/runtime.json', 0o600)
PY
cd /opt/shared-apps
cp shared-firewall.service /etc/systemd/system/
install -d /etc/systemd/system/docker.service.d
cat > /etc/systemd/system/docker.service.d/shared-firewall.conf <<'EOF'
[Unit]
Requires=shared-firewall.service
After=shared-firewall.service
EOF
systemctl daemon-reload
systemctl enable --now shared-firewall
for app in gemihub kakeratta geo; do
  case "$app" in gemihub) n=1;; kakeratta) n=2;; geo) n=3;; esac
  docker network inspect "shared-$app" >/dev/null 2>&1 || docker network create --subnet="172.28.$n.0/24" --opt="com.docker.network.bridge.name=br-$app" "shared-$app"
  # Containers obtain only their own runtime's credentials from the scoped proxy.
  # Google also provides VM DNS on this address. Keep DNS available while
  # denying HTTP(S) access to the bootstrap identity's metadata endpoints.
  iptables -D DOCKER-USER -i "br-$app" -d 169.254.169.254 -j REJECT 2>/dev/null || true
  for port in 80 443; do
    iptables -C DOCKER-USER -i "br-$app" -d 169.254.169.254 -p tcp --dport "$port" -j REJECT 2>/dev/null || iptables -I DOCKER-USER -i "br-$app" -d 169.254.169.254 -p tcp --dport "$port" -j REJECT
  done
done
cp shared-control.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now shared-control
python3 deploy.py --all
cp nginx.conf /etc/nginx/nginx.conf
nginx -t
systemctl enable nginx
systemctl restart nginx
if ! systemctl is-enabled google-cloud-ops-agent >/dev/null 2>&1; then
  curl -fsSLo /tmp/install-ops-agent.sh https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh
  bash /tmp/install-ops-agent.sh --also-install
fi
cp ops-agent.yaml /etc/google-cloud-ops-agent/config.yaml
systemctl restart google-cloud-ops-agent
