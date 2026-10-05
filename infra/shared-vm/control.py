"""Host-only credential broker, backend health checks.

Each Docker bridge maps to one existing runtime identity. Containers cannot
reach the real metadata server; neither keys nor bootstrap tokens leave here.
"""
import ipaddress
import json
import socket
import threading
import time
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

RUNTIME = Path('/opt/shared-apps/runtime.json')
SUBNETS = {'172.28.1.0/24': 'gemihub', '172.28.2.0/24': 'kakeratta', '172.28.3.0/24': 'geo'}
TOKEN_CACHE = {}
TOKEN_LOCK = threading.Lock()

def metadata(path):
    return urllib.request.urlopen(urllib.request.Request(
        'http://metadata.google.internal/computeMetadata/v1/' + path,
        headers={'Metadata-Flavor': 'Google'}), timeout=10).read()

def iam(account, operation, body):
    token = json.loads(metadata('instance/service-accounts/default/token'))['access_token']
    request = urllib.request.Request(
        'https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/' + account + ':' + operation,
        data=json.dumps(body).encode(),
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    return json.load(urllib.request.urlopen(request, timeout=20))

def access_token(account):
    with TOKEN_LOCK:
        cached = TOKEN_CACHE.get(account)
        if not cached or cached['until'] < time.time():
            value = iam(account, 'generateAccessToken', {'scope': ['https://www.googleapis.com/auth/cloud-platform'], 'lifetime': '3600s'})
            cached = {'token': value['accessToken'], 'until': time.time() + 3300}
            TOKEN_CACHE[account] = cached
        return {'access_token': cached['token'], 'expires_in': max(1, int(cached['until'] - time.time())), 'token_type': 'Bearer'}

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # Never log identity-token queries, auth headers or secret values.

    def answer(self, code, value, json_value=False):
        body = json.dumps(value).encode() if json_value else str(value).encode()
        self.send_response(code)
        self.send_header('Metadata-Flavor', 'Google')
        self.send_header('Content-Type', 'application/json' if json_value else 'text/plain')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        try:
            if self.server.server_port == 8085:
                return self.health()
            if self.headers.get('Metadata-Flavor') != 'Google':
                return self.answer(403, 'Metadata header required')
            client = ipaddress.ip_address(self.client_address[0])
            app = next((v for k, v in SUBNETS.items() if client in ipaddress.ip_network(k)), None)
            if not app:
                return self.answer(403, 'Unknown runtime network')
            config = json.loads(RUNTIME.read_text())['identities'][app]
            account, project = config['account'], config['project']
            url = urllib.parse.urlsplit(self.path)
            path = url.path.removeprefix('/computeMetadata/v1/')
            if path == 'instance/service-accounts/default/token':
                return self.answer(200, access_token(account), True)
            if path == 'instance/service-accounts/default/email':
                return self.answer(200, account)
            if path == 'instance/service-accounts/default/identity':
                audience = urllib.parse.parse_qs(url.query).get('audience', [''])[0]
                if not audience:
                    return self.answer(400, 'Audience required')
                return self.answer(200, iam(account, 'generateIdToken', {'audience': audience, 'includeEmail': True})['token'])
            if path in ['instance/service-accounts/default/', 'instance/service-accounts/default']:
                return self.answer(200, {'email': account, 'aliases': ['default'], 'scopes': ['https://www.googleapis.com/auth/cloud-platform']}, True)
            if path == 'instance/service-accounts/':
                return self.answer(200, 'default/\n')
            if path == 'instance/service-accounts/default/scopes':
                return self.answer(200, 'https://www.googleapis.com/auth/cloud-platform\n')
            if path == 'project/project-id':
                return self.answer(200, project)
            if path == 'instance/zone':
                return self.answer(200, metadata('instance/zone').decode())
            return self.answer(404, 'Metadata path unavailable')
        except Exception as error:
            print('Control request failed:', type(error).__name__, flush=True)
            self.answer(503, 'Temporarily unavailable')

    def health(self):
        if self.path.startswith('/health/'):
            ports = {'gemihub': [8101], 'kakeratta': [8102, 8103], 'geo': [8104]}.get(self.path[8:])
            if not ports:
                return self.answer(404, 'Unknown backend')
            for port in ports:
                with socket.create_connection(('127.0.0.1', port), timeout=2):
                    pass
            return self.answer(200, 'ok')
        return self.answer(404, 'Not found')

if __name__ == '__main__':
    threading.Thread(target=ThreadingHTTPServer(('0.0.0.0', 8088), Handler).serve_forever, daemon=True).start()
    ThreadingHTTPServer(('0.0.0.0', 8085), Handler).serve_forever()
