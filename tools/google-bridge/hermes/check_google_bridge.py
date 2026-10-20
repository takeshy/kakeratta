#!/usr/bin/env python3
"""A single Hermes cron tick. No Google secret, mail output or private loop.
Queued work starts the code-driven Google worker; the cron parent never wakes.

Copy to HERMES_HOME/scripts; put google_bridge.json beside it, mode 0600:
{"url":"http://127.0.0.1:8094/mcp","key":"the separately issued MCP key"}
The owner connects google_bridge and kakeratta separately in Hermes.
Bridge never stores a kakeratta destination or credential.
"""
import json
import os
from pathlib import Path
import stat
import sys
import subprocess
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def config(path):
    if not path.exists():
        value = {"url": os.environ.get("GOOGLE_BRIDGE_URL", "http://127.0.0.1:8094/mcp"), "key": ""}
    else:
        info = path.stat()
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
            raise ValueError("Private configuration required")
        value = json.loads(path.read_text(encoding="utf-8"))
    url = urllib.parse.urlsplit(value["url"])
    loopback = url.scheme == "http" and url.hostname in ("127.0.0.1", "localhost", "host.openshell.internal")
    if (url.path != "/mcp" or url.query or url.fragment or url.username or not url.hostname
            or not (url.scheme == "https" or loopback)):
        raise ValueError("Invalid MCP URL")
    key = value.get("key", "")
    if not (loopback and key == "") and (len(key) != 64 or any(c not in "0123456789abcdef" for c in key)):
        raise ValueError("Invalid MCP key")
    value["key"] = key
    return value


def hermes_home():
    if os.environ.get("HERMES_HOME"):
        return Path(os.environ["HERMES_HOME"]).expanduser()
    return Path("/sandbox/.hermes") if Path("/sandbox/.hermes").exists() else Path.home() / ".hermes"


def spawn_worker(value, service):
    worker = Path(__file__).with_name("google_bridge_worker.py")
    if not worker.is_file():
        raise ValueError("Install google_bridge_worker.py beside the gate")
    home = hermes_home()
    root = home / "google-bridge"
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(root, 0o700)
    fd = os.open(root / (service + "-worker.log"), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, "a") as log:
        proc = subprocess.Popen([sys.executable, str(worker)], stdin=subprocess.PIPE,
                                stdout=subprocess.DEVNULL, stderr=log, text=True,
                                env=dict(os.environ, HERMES_HOME=str(home)), start_new_session=True)
        try:
            proc.stdin.write(json.dumps({"bridge": value, "service": service}))
        finally:
            proc.stdin.close()


def tick(value, opener=None, tool="gmail_sync", _remaining=10, dispatch=None):
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call",
            "params": {"name": tool, "arguments": {}}}
    headers = {"Content-Type": "application/json",
               "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2025-11-25"}
    if value.get("key"):
        headers["Authorization"] = "Bearer " + value["key"]
    request = urllib.request.Request(value["url"], data=json.dumps(body).encode(), headers=headers)
    opener = opener or urllib.request.build_opener(NoRedirect)
    with opener.open(request, timeout=35) as response:
        data = response.read(131073)
        if len(data) > 131072:
            raise ValueError("Oversize response")
        result = json.loads(data)
    if result.get("error") or result.get("result", {}).get("isError"):
        raise ValueError("Sync failed")
    if not isinstance(result.get("result", {}).get("structuredContent"), dict):
        raise ValueError("Unexpected response")
    content = result["result"]["structuredContent"]
    pending = content.get("triagePending", 0)
    delivery = content.get("deliveryPending", 0)
    for count, maximum in ((pending, 50), (delivery, 200)):
        if not isinstance(count, int) or isinstance(count, bool) or count < 0 or count > maximum:
            raise ValueError("Unexpected pending count")
    service = "calendar" if tool == "calendar_sync" else "gmail"
    if pending or delivery:
        (dispatch or spawn_worker)(value, service)
        return {"wakeAgent": False}
    # Read a bounded backlog without spending model tokens; the code worker
    # handles selection, delivery and receipts when any work is ready.
    if content.get("hasMore") and _remaining > 1:
        return tick(value, opener, tool, _remaining - 1, dispatch)
    if content.get("hasMore"):
        (dispatch or spawn_worker)(value, service)
    return {"wakeAgent": False}


def main(tool="gmail_sync"):
    try:
        value = config(Path(__file__).with_name("google_bridge.json"))
        print(json.dumps(tick(value, tool=tool)))
        return 0
    except Exception:
        print("Google Bridge sync failed; check its Web UI and private MCP configuration.", file=sys.stderr)
        print(json.dumps({"wakeAgent": False}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
