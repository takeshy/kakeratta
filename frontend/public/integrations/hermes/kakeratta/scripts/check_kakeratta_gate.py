#!/usr/bin/env python3
"""Wake gate pre-check script for kakeratta Hermes integration.

Checks kakeratta for pending planning asks via MCP list_asks.
If no ask is pending or on any error/missing config, outputs {"wakeAgent": false}
to skip LLM execution entirely (fail-closed, zero token spend).
If an unclaimed ask is pending, outputs {"wakeAgent": true} to wake Hermes Agent.

It also keeps the integration current. list_asks carries the versions of the
distributed skill and of this script; when either differs from what is
installed, the script fetches both through the same MCP endpoint
(get_integration) and replaces ~/.hermes/skills/kakeratta/SKILL.md and itself.
No other host or path is needed, so a sandbox that allows only the MCP
endpoint can still update. Updates and failures are written to stderr; a
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
GATE_VERSION = "3"


def get_hermes_home() -> Path:
    env_home = os.environ.get("HERMES_HOME")
    if env_home:
        return Path(env_home).expanduser()
    return Path.home() / ".hermes"


def installed_skill() -> Path:
    return get_hermes_home() / "skills" / "kakeratta" / "SKILL.md"


def extract_version(text: str) -> str | None:
    m = re.match(r"^---\n(.*?)\n---", text, re.DOTALL)
    if not m:
        return None
    vm = re.search(r"^\s*version:\s*[\"']?([^\"'\n]+)", m.group(1), re.MULTILINE)
    return vm.group(1).strip() if vm else None


def call_tool(url: str, headers: dict, name: str) -> dict:
    """Call one kakeratta MCP tool and return its structured result."""
    payload = json.dumps({
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": {"name": name, "arguments": {}},
    }).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=5) as response:
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
    """Bring the skill and this script up to the versions kakeratta reports."""
    skill_path = installed_skill()
    installed = None
    if skill_path.exists():
        installed = extract_version(skill_path.read_text(encoding="utf-8"))
    want_skill = versions.get("skill")
    want_gate = versions.get("gate")
    if (not want_skill or want_skill == installed) and (not want_gate or want_gate == GATE_VERSION):
        return

    bundle = call_tool(url, headers, "get_integration")
    skill = bundle.get("skill") or {}
    if want_skill and want_skill != installed and skill.get("text"):
        try:
            replace_file(skill_path, skill["text"])
            print(f"kakeratta skill updated: {installed} -> {skill.get('version')}", file=sys.stderr)
        except Exception as e:
            print(f"kakeratta skill update failed: {e}", file=sys.stderr)
    gate = bundle.get("gate") or {}
    if want_gate and want_gate != GATE_VERSION and gate.get("text"):
        try:
            replace_file(Path(__file__).resolve(), gate["text"])
            print(f"kakeratta wake gate updated: {GATE_VERSION} -> {gate.get('version')}", file=sys.stderr)
        except Exception as e:
            print(f"kakeratta wake gate update failed: {e}", file=sys.stderr)


def main():
    try:
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

        result = call_tool(url, headers, "list_asks")
        asks = result.get("asks")
        if not isinstance(asks, list):
            raise ValueError("list_asks.asks is not a list")

        versions = result.get("integration")
        if isinstance(versions, dict):
            try:
                update_integration(url, headers, versions)
            except Exception as e:
                print(f"kakeratta integration update failed: {e}", file=sys.stderr)

        # A claimed ask is already being planned by a running agent; only an
        # unclaimed one needs a new wake.
        pending = [a for a in asks if isinstance(a, dict) and a.get("status") == "pending"]
        if len(pending) == 0:
            print(json.dumps({"wakeAgent": False}))
        else:
            print(json.dumps({"wakeAgent": True}))

    except Exception as e:
        # Fail-closed: on error (e.g. network timeout or 401/403/500), do NOT wake
        # the agent to prevent token runaway. Kakeratta's built-in model will
        # automatically answer if the external agent does not claim the turn.
        print(f"Pre-check error: {e}; skipping wake to protect tokens", file=sys.stderr)
        print(json.dumps({"wakeAgent": False}))


if __name__ == "__main__":
    main()
