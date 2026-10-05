"""Deploy an immutable image; restore the previous container on failed startup."""
import argparse
import base64
import fcntl
import json
import os
import socket
import subprocess
import time
import tarfile
import io
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path('/opt/shared-apps')
os.umask(0o077)

def command(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)

def fetch_secret(project, name, version, account):
    from control import access_token
    value = json.load(urllib.request.urlopen(urllib.request.Request(
        f'https://secretmanager.googleapis.com/v1/projects/{project}/secrets/{name}/versions/{version}:access',
        headers={'Authorization': 'Bearer ' + access_token(account)['access_token']}), timeout=30))
    return base64.b64decode(value['payload']['data']).decode()

def run_container(name, app):
    group = app['identity']
    meta_host = {'gemihub': '172.28.1.1', 'kakeratta': '172.28.2.1', 'geo': '172.28.3.1'}[group] + ':8088'
    env = dict(app['env'])
    for key, secret in app.get('secrets', {}).items():
        env[key] = fetch_secret(secret['project'], secret['name'], secret['version'], app['account'])
    env['GCE_METADATA_HOST'] = meta_host
    # Both google-auth-library and Go's metadata client use this override.
    env['GCE_METADATA_IP'] = meta_host
    command('docker', 'rm', '-f', name, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) if subprocess.run(['docker', 'inspect', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0 else None
    args = ['docker', 'run', '-d', '--name', name, '--restart', 'unless-stopped', '--network', 'shared-' + group,
            '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--memory', app['memory'], '--memory-swap', app['memory'],
            '--label', 'app=' + name, '--log-driver', 'json-file', '--log-opt', 'labels=app', '--log-opt', 'max-size=10m', '--log-opt', 'max-file=3',
            '-p', '127.0.0.1:' + str(app['port']) + ':8080']
    # Passing only names on the CLI preserves multiline secrets and keeps
    # their values out of process arguments and deployment output.
    for key in env:
        args += ['--env', key]
    if group == 'geo':
        (ROOT / 'geo-data').mkdir(exist_ok=True)
        os.chmod(ROOT / 'geo-data', 0o755)
        os.chown(ROOT / 'geo-data', 1000, 1000)
        args += ['-v', str(ROOT / 'geo-data') + ':/tmp/geo']
    args += [app['image']]
    command(*args, env={**os.environ, **env}, stdout=subprocess.DEVNULL)

def deploy(name, app):
    from control import metadata
    registry = app['image'].split('/')[0]
    token = json.loads(metadata('instance/service-accounts/default/token'))['access_token']
    command('docker', 'login', '-u', 'oauth2accesstoken', '--password-stdin', 'https://' + registry,
            input=token.encode(), stdout=subprocess.DEVNULL)
    command('docker', 'pull', app['image'])
    if subprocess.run(['docker', 'inspect', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
        command('docker', 'stop', '-t', '600', name, stdout=subprocess.DEVNULL)
    run_container(name, app)
    deadline = time.monotonic() + 240
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(('127.0.0.1', app['port']), timeout=2):
                pass
            time.sleep(3)
            state = subprocess.check_output(['docker', 'inspect', '--format={{.State.Running}}', name], text=True).strip()
            if state == 'true':
                print(name + ' ready', flush=True)
                return
        except OSError:
            pass
        time.sleep(2)
    raise RuntimeError(name + ' did not become ready')

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--all', action='store_true')
    parser.add_argument('--app', choices=['gemihub', 'kakeratta', 'geo'])
    parser.add_argument('--image')
    parser.add_argument('--frontend', help='Immutable gs:// tar archive containing the frontend files')
    args = parser.parse_args()
    config_file = ROOT / 'runtime.json'
    config = json.loads(config_file.read_text())
    names = list(config['apps']) if args.all else [name for name, app in config['apps'].items() if app['identity'] == args.app]
    if not names:
        parser.error('Select --all or --app')
    previous = json.loads(json.dumps(config))
    try:
        for name in names:
            if args.image:
                config['apps'][name]['image'] = args.image
            deploy(name, config['apps'][name])
        if args.frontend:
            if args.app != 'kakeratta':
                parser.error('--frontend requires --app kakeratta')
            from control import metadata
            bucket, key = args.frontend.removeprefix('gs://').split('/', 1)
            token = json.loads(metadata('instance/service-accounts/default/token'))['access_token']
            url = 'https://storage.googleapis.com/storage/v1/b/' + bucket + '/o/' + urllib.parse.quote(key, safe='') + '?alt=media'
            data = urllib.request.urlopen(urllib.request.Request(url, headers={'Authorization': 'Bearer ' + token})).read()
            target = ROOT / ('frontend-' + str(time.time_ns()))
            target.mkdir(mode=0o755)
            with tarfile.open(fileobj=io.BytesIO(data)) as archive:
                for member in archive.getmembers():
                    resolved = (target / member.name).resolve()
                    if not resolved.is_relative_to(target) or not (member.isfile() or member.isdir()):
                        raise ValueError('Unsafe frontend archive member')
                archive.extractall(target)
            if not (target / 'index.html').exists():
                raise ValueError('Frontend archive must contain index.html')
            link = ROOT / 'frontend.next'
            link.symlink_to(target.name)
            link.replace(ROOT / 'frontend')
        config_file.write_text(json.dumps(config, indent=2))
    except Exception:
        if not args.all:
            for name in names:
                deploy(name, previous['apps'][name])
        raise

if __name__ == '__main__':
    with open(ROOT / 'deploy.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        main()
