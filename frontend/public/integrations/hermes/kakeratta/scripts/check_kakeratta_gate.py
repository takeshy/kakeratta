#!/usr/bin/env python3
"""Wake gate pre-check script for kakeratta Hermes integration.

Checks kakeratta for pending planning asks and direct requests via MCP list_asks.
If nothing is pending or on any error/missing config, outputs {"wakeAgent": false}
to skip LLM execution entirely (fail-closed, zero token spend).

Both pending direct requests and pending persona asks are handled here, in code,
by the distributed worker (direct_request_worker.py):
- Direct requests (`@agent`): claim_request -> one Ultra session -> reply_request.
- Persona asks: claim_ask -> one Super VL session with only `read_for_ask` and `web` tools -> submit_answer.
Because the worker claims, runs and submits in code without waking the Cron
parent, normal ticks always return {"wakeAgent": false} with zero parent-model
overhead.

It also keeps the integration current. list_asks carries the versions of the
distributed skill, this script and the worker; when any differs from what is
installed, the script fetches all three through the same MCP endpoint
(get_integration) and replaces ~/.hermes/skills/kakeratta/SKILL.md, itself and
the worker. No other host or path is needed, so a sandbox that allows only the
MCP endpoint can still update. Updates and failures are written to stderr; a
failure never changes the wake decision.
"""

import json
import os
import re
import sys
from pathlib import Path
import urllib.request
import urllib.error

# GATE_VERSION is this script's version, compared with the one list_asks reports.
GATE_VERSION = "11"


def adjust_max_turns(config_path: Path, target_turns: int) -> bool:
    """Adjust agent.max_turns in config.yaml under the agent: section.

    Returns True if agent.max_turns was found and set/verified.
    Returns False if config_path doesn't exist, agent.max_turns line is missing, or an error occurs.
    """
    try:
        if not config_path.exists():
            return False
        text = config_path.read_text(encoding="utf-8")
        pattern = r"(^[ \t]*agent\s*:(?:(?!\n\S)\n[\s\S])*?\n[ \t]+max_turns\s*:\s*)[\x22\x27]?\d+[\x22\x27]?"
        if not re.search(pattern, text, re.MULTILINE):
            print(f"Pre-check error: 'agent.max_turns' not found in {config_path}", file=sys.stderr)
            return False
        new_text, count = re.subn(pattern, rf"\g<1>{target_turns}", text, count=1, flags=re.MULTILINE)
        if count > 0 and new_text != text:
            replace_file(config_path, new_text)
        return True
    except Exception as e:
        print(f"Pre-check error: failed to adjust max_turns to {target_turns}: {e}", file=sys.stderr)
        return False


def get_hermes_home() -> Path:
    env_home = os.environ.get("HERMES_HOME")
    if env_home:
        return Path(env_home).expanduser()
    sandbox_home = Path("/sandbox/.hermes")
    if sandbox_home.exists():
        return sandbox_home
    return Path.home() / ".hermes"


def _load_env_file():
    env_file = get_hermes_home() / ".env"
    if env_file.exists():
        try:
            for line in env_file.read_text(encoding="utf-8").splitlines():
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                k = k.strip()
                v = v.strip().strip("'\"")
                if k and k not in os.environ:
                    os.environ[k] = v
        except Exception:
            pass

    proxy_env = Path("/tmp/nemoclaw-proxy-env.sh")
    if proxy_env.exists():
        try:
            for line in proxy_env.read_text(encoding="utf-8").splitlines():
                line = line.strip()
                if line.startswith("export "):
                    line = line[len("export "):].strip()
                if "=" in line and not line.startswith("#"):
                    k, v = line.split("=", 1)
                    k = k.strip()
                    v = v.strip().strip("'\"")
                    if k and k not in os.environ:
                        os.environ[k] = v
        except Exception:
            pass

    if Path("/etc/openshell-tls/ca-bundle.pem").exists():
        for ca_var in ("SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"):
            if ca_var not in os.environ:
                os.environ[ca_var] = "/etc/openshell-tls/ca-bundle.pem"
        for p_var in ("http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"):
            if p_var not in os.environ:
                os.environ[p_var] = "http://10.200.0.1:3128"

    extra_no_proxy = "kakeratta.net,api.keenable.ai,mcp.exa.ai,search.parallel.ai"
    for np in ("no_proxy", "NO_PROXY"):
        val = os.environ.get(np, "")
        for host in extra_no_proxy.split(","):
            if host not in val:
                val = (val + "," + host).strip(",")
        os.environ[np] = val


def installed_skill() -> Path:
    return get_hermes_home() / "skills" / "kakeratta" / "SKILL.md"


def scripts_dir() -> Path:
    return Path(__file__).resolve().parent


def installed_worker() -> Path:
    return scripts_dir() / "direct_request_worker.py"


def extract_version(text: str) -> str | None:
    m = re.match(r"^---\n(.*?)\n---", text, re.DOTALL)
    if not m:
        return None
    vm = re.search(r"^\s*version:\s*[\"']?([^\"'\n]+)", m.group(1), re.MULTILINE)
    return vm.group(1).strip() if vm else None


def extract_worker_version(text: str) -> str | None:
    m = re.search(r"^WORKER_VERSION\s*=\s*[\"']([^\"']+)[\"']", text, re.MULTILINE)
    return m.group(1).strip() if m else None


def call_tool(url: str, headers: dict, name: str, arguments: dict = None, timeout: float = 5) -> dict:
    """Call one kakeratta MCP tool with arguments and return its structured result."""
    payload = json.dumps({
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": {"name": name, "arguments": arguments or {}},
    }).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers=headers, method="POST")
    with urllib.request.build_opener(NoRedirect()).open(req, timeout=timeout) as response:
        data = json.loads(response.read().decode("utf-8"))
    if "error" in data:
        raise ValueError(f"{name}: {data['error'].get('message')}")
    res_obj = data["result"]
    if res_obj.get("isError"):
        raise ValueError(f"{name} returned an MCP error")
    structured = res_obj.get("structuredContent")
    if isinstance(structured, dict):
        return structured
    content = res_obj.get("content") or []
    if not content:
        raise ValueError(f"{name} returned no data")
    return json.loads(content[0]["text"])


def judge_workspaces(url: str, headers: dict) -> list[dict]:
    """Discover shared-key judge endpoints from the configured kakeratta host.

    Each MCP URL is joined to the host this gate was configured with, never
    taken from the response, so the shared key only goes where it already does.
    """
    marker = "/api/mcp/users/"
    if marker not in url:
        return []
    base = url.split(marker, 1)[0]
    endpoint = base + "/internal/judge-workspaces"
    req_headers = {k: v for k, v in headers.items() if k.lower() not in ("content-type", "accept")}
    req = urllib.request.Request(endpoint, headers=req_headers, method="GET")
    with urllib.request.build_opener(NoRedirect()).open(req, timeout=5) as response:
        data = json.loads(response.read().decode("utf-8"))
    rows = data.get("workspaces")
    if not isinstance(rows, list):
        raise ValueError("judge-workspaces.workspaces is not a list")
    targets = []
    for row in rows:
        workspace = row.get("workspaceId") if isinstance(row, dict) else None
        if isinstance(workspace, str) and re.fullmatch(r"[0-9a-f]{64}", workspace):
            targets.append({"workspaceId": workspace, "mcpUrl": base + marker + workspace})
    return targets


def replace_file(path: Path, text: str) -> None:
    """Write text to path through a temporary file, so a failure leaves the old one."""
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(text, encoding="utf-8")
    try:
        os.chmod(tmp, os.stat(path).st_mode if path.exists() else 0o644)
    except OSError:
        pass
    os.replace(tmp, path)


def update_integration(url: str, headers: dict, versions: dict) -> None:
    """Bring the skill, this script and the worker to the versions kakeratta reports."""
    skill_path = installed_skill()
    installed = None
    if skill_path.exists():
        installed = extract_version(skill_path.read_text(encoding="utf-8"))
    worker_path = installed_worker()
    worker_installed = None
    if worker_path.exists():
        try:
            worker_installed = extract_worker_version(worker_path.read_text(encoding="utf-8"))
        except Exception:
            worker_installed = None
    want_skill = versions.get("skill")
    want_gate = versions.get("gate")
    want_worker = versions.get("worker")
    def is_newer(remote_v: str | None, local_v: str | None) -> bool:
        if not remote_v or remote_v == local_v:
            return False
        try:
            return int(remote_v) > int(local_v)
        except (ValueError, TypeError):
            return remote_v != local_v

    skill_newer = is_newer(want_skill, installed)
    gate_newer = is_newer(want_gate, GATE_VERSION)
    worker_newer = is_newer(want_worker, worker_installed)
    if not skill_newer and not gate_newer and not worker_newer:
        return

    bundle = call_tool(url, headers, "get_integration")
    skill = bundle.get("skill") or {}
    if skill_newer and skill.get("text"):
        try:
            replace_file(skill_path, skill["text"])
            print(f"kakeratta skill updated: {installed} -> {skill.get('version')}", file=sys.stderr)
        except Exception as e:
            print(f"kakeratta skill update failed: {e}", file=sys.stderr)
    gate = bundle.get("gate") or {}
    if gate_newer and gate.get("text"):
        try:
            replace_file(Path(__file__).resolve(), gate["text"])
            print(f"kakeratta wake gate updated: {GATE_VERSION} -> {gate.get('version')}", file=sys.stderr)
        except Exception as e:
            print(f"kakeratta wake gate update failed: {e}", file=sys.stderr)
    worker = bundle.get("worker") or {}
    if worker_newer and worker.get("text"):
        try:
            replace_file(worker_path, worker["text"])
            print(f"kakeratta direct-request worker updated: {worker_installed} -> {worker.get('version')}", file=sys.stderr)
        except Exception as e:
            print(f"kakeratta direct-request worker update failed: {e}", file=sys.stderr)


def judge_headers():
    key = os.environ.get("JUDGE_MCP_KEY", "")
    if len(key) != 64:
        raise ValueError("JUDGE_MCP_KEY must have 64 characters")
    return {"Authorization": "Bearer " + key, "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream"}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("judge discovery redirect refused")


def discover_judges(endpoint, headers):
    from urllib.parse import urlsplit
    origin = urlsplit(endpoint)
    if origin.scheme != "https" or not origin.netloc or origin.username or origin.password or origin.query or origin.fragment or origin.path != "/internal/judge-workspaces":
        raise ValueError("invalid judge discovery URL")
    req = urllib.request.Request(endpoint, headers=headers)
    with urllib.request.build_opener(NoRedirect()).open(req, timeout=10) as response:
        rows = json.loads(response.read(1_000_001))["workspaces"]
    if not isinstance(rows, list):
        raise ValueError("invalid workspace list")
    result = {}
    for row in rows:
        wid = row.get("workspaceId", "")
        if not re.fullmatch(r"[a-f0-9]{64}", wid):
            raise ValueError("invalid workspace ID")
        url = f"https://{origin.netloc}/api/mcp/users/{wid}"
        if row.get("mcpUrl") != url:
            raise ValueError("workspace URL differs from trusted origin")
        result[wid] = url
    return result


def judge_tick():
    sys.path.insert(0, str(scripts_dir()))
    import direct_request_worker as worker
    headers = judge_headers()
    cfg = worker.read_config(get_hermes_home())
    if not cfg:
        raise ValueError("Hermes configuration unavailable")
    targets = discover_judges(os.environ["JUDGE_WORKSPACES_URL"], headers)
    ready = []
    # Poll everyone before doing inference so presence stays current.
    for wid, url in targets.items():
        try:
            result = call_tool(url, headers, "list_asks")
            if not isinstance(result.get("asks"), list) or not isinstance(result.get("requests", []), list):
                raise ValueError("invalid list_asks result")
            ready.append((wid, url, result))
        except Exception:
            print(f"judge poll failed: {wid}", file=sys.stderr)
    # Updates use the trusted origin and the existing authenticated MCP bundle.
    if ready:
        try:
            update_integration(ready[0][1], headers, ready[0][2].get("integration") or {})
        except Exception:
            print("judge integration update failed", file=sys.stderr)
    for wid, url, result in ready:
        requests = [r for r in result.get("requests", []) if isinstance(r, dict) and r.get("status") in ("pending", "claimed")]
        if requests:
            worker.spawn_judge_requests(url, headers, wid, requests)
        asks = [a for a in result.get("asks", []) if isinstance(a, dict) and a.get("status") in ("pending", "claimed")]
        if asks:
            worker.spawn_persona_asks(url, headers, wid, asks)


def main():
    try:
        _load_env_file()
        if os.environ.get("JUDGE_WORKSPACES_URL"):
            judge_tick()
            print(json.dumps({"wakeAgent": False}))
            return
        config_path = get_hermes_home() / "config.yaml"
        if not config_path.exists():
            print("Pre-check warning: config.yaml not found; skipping wake", file=sys.stderr)
            print(json.dumps({"wakeAgent": False}))
            return

        import yaml  # Hermes runtime always has PyYAML
        with open(config_path, "r", encoding="utf-8") as f:
            cfg = yaml.safe_load(f) or {}

        mcp_cfg = cfg.get("mcp_servers", {}).get("kakeratta", {})
        url = mcp_cfg.get("url")
        if not url:
            print("Pre-check warning: kakeratta MCP url not configured in config.yaml; skipping wake", file=sys.stderr)
            print(json.dumps({"wakeAgent": False}))
            return

        headers = dict(mcp_cfg.get("headers", {}))
        headers["Content-Type"] = "application/json"
        headers["Accept"] = "application/json, text/event-stream"

        targets = [{"workspaceId": "configured", "mcpUrl": url}]
        try:
            discovered = judge_workspaces(url, headers)
            targets = discovered
        except Exception as e:
            # A normal per-user key cannot call the judge endpoint. Preserve
            # the existing single-workspace behavior in that case.
            print(f"kakeratta judge discovery unavailable: {e}", file=sys.stderr)

        # Each workspace is read on its own: one that fails (being deleted,
        # say) is skipped this tick and never holds up the others.
        pending_asks = []
        ask_batches = []
        batches = []
        result, result_url = None, url
        for target in targets:
            try:
                listed = call_tool(target["mcpUrl"], headers, "list_asks")
                asks = listed.get("asks")
                if not isinstance(asks, list):
                    raise ValueError("list_asks.asks is not a list")
                # Direct requests are listed only when the user turned them
                # on; an older kakeratta leaves the key out.
                requests = listed.get("requests") or []
                if not isinstance(requests, list):
                    raise ValueError("list_asks.requests is not a list")
            except Exception as e:
                print(f"kakeratta list_asks failed for {target['workspaceId']}: {e}", file=sys.stderr)
                continue
            pending_asks.extend((target, a) for a in asks if isinstance(a, dict) and a.get("status") == "pending")
            ask_batches.append((target, asks))
            batches.append((target, requests))
            if result is None:
                # The integration is fetched from a workspace this key can
                # read, which for the shared judge key is not the configured one.
                result, result_url = listed, target["mcpUrl"]

        if result is None:
            adjust_max_turns(config_path, 15)
            print(json.dumps({"wakeAgent": False}))
            return

        versions = result.get("integration")
        if isinstance(versions, dict):
            try:
                update_integration(result_url, headers, versions)
            except Exception as e:
                print(f"kakeratta integration update failed: {e}", file=sys.stderr)

        # Both direct requests and persona asks are handled in code by the
        # distributed worker, not by waking the Cron parent. It is imported
        # only now, after the integration (including the worker) has been
        # brought current above.
        worker = None
        try:
            sys.path.insert(0, str(scripts_dir()))
            import direct_request_worker as worker
        except Exception as e:
            print(f"kakeratta direct-request worker unavailable: {e}", file=sys.stderr)

        for target, requests in batches:
            # Claimed requests go to the worker too: it resumes the ones it
            # claimed and left unfinished from its own records.
            open_requests = [r for r in requests if isinstance(r, dict) and r.get("status") in ("pending", "claimed")]
            if not open_requests or worker is None:
                continue
            try:
                if target["workspaceId"] == "configured":
                    worker.process(target["mcpUrl"], headers, open_requests)
                else:
                    worker.spawn_judge_requests(target["mcpUrl"], headers, target["workspaceId"], open_requests)
            except Exception as e:
                # Fail-closed: a worker failure never wakes Super for a direct
                # request. The request stays pending or claimed and is
                # retried next tick.
                print(f"kakeratta direct-request worker failed: {e}", file=sys.stderr)

        shared = targets and targets[0].get("workspaceId") != "configured"
        if worker is not None:
            for target, asks in ask_batches:
                open_asks = [a for a in asks if isinstance(a, dict) and a.get("status") in ("pending", "claimed")]
                if not open_asks:
                    continue
                try:
                    if target["workspaceId"] == "configured":
                        worker.process_asks(target["mcpUrl"], headers, open_asks)
                    else:
                        worker.spawn_persona_asks(target["mcpUrl"], headers, target["workspaceId"], open_asks)
                except Exception as e:
                    print(f"kakeratta persona worker failed for {target['workspaceId']}: {e}", file=sys.stderr)
            # Persona asks are handled by the worker above (or fall back on
            # kakeratta's deadline on failure); never wake the Cron parent.
            pending_asks = []
        elif shared:
            pending_asks = []

        # The 15-turn boundary covers fallback parent runs if the worker script
        # is missing.
        if not shared and not adjust_max_turns(config_path, 15):
            print("Pre-check warning: agent.max_turns line missing or invalid in config; skipping wake", file=sys.stderr)
            print(json.dumps({"wakeAgent": False}))
            return

        if len(pending_asks) == 0:
            print(json.dumps({"wakeAgent": False}))
        else:
            print(json.dumps({"wakeAgent": True}))

    except Exception as e:
        # Fail-closed: on error (e.g. network timeout or 401/403/500), do NOT wake
        # the agent to prevent token runaway. Kakeratta's built-in model will
        # automatically answer if the external agent does not claim the turn.
        print("Pre-check error; skipping wake to protect tokens", file=sys.stderr)
        print(json.dumps({"wakeAgent": False}))


if __name__ == "__main__":
    main()
