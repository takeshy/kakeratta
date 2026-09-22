#!/usr/bin/env python3
"""Wake gate pre-check script for kakeratta Hermes integration.

Checks kakeratta for pending tasks via MCP list_tasks.
If tasks are empty, outputs {"wakeAgent": false} to skip LLM execution entirely (zero token spend).
If tasks are present or on error, outputs {"wakeAgent": true} to wake Hermes Agent.

It also self-updates the installed skill: once per day (state flag file), it
fetches the distributed SKILL.md from the kakeratta origin and overwrites
~/.hermes/skills/kakeratta/SKILL.md when the distributed metadata.version
differs from the installed copy. Any failure in this path is silent and must
not affect the wake-agent decision.
"""

import json
import os
import re
import sys
from datetime import date
from pathlib import Path
import urllib.request
import urllib.error

def get_hermes_home() -> Path:
    env_home = os.environ.get("HERMES_HOME")
    if env_home:
        return Path(env_home).expanduser()
    return Path.home() / ".hermes"

SKILL_PATH_REL = "integrations/hermes/kakeratta/SKILL.md"

def install_dir() -> Path:
    return get_hermes_home() / "skills" / "kakeratta"

def state_file() -> Path:
    return get_hermes_home() / "state" / "kakeratta_gate_skill_check"

def extract_version(text: str) -> str | None:
    m = re.match(r"^---\n(.*?)\n---", text, re.DOTALL)
    if not m:
        return None
    vm = re.search(r"^\s*version:\s*[\"']?([^\"'\n]+)", m.group(1), re.MULTILINE)
    return vm.group(1).strip() if vm else None

def fetch_skill(url: str) -> bytes | None:
    try:
        req = urllib.request.Request(url, headers={"Accept": "text/markdown"}, method="GET")
        with urllib.request.urlopen(req, timeout=5) as response:
            if response.status != 200:
                return None
            return response.read()
    except Exception:
        return None

def skill_origin(mcp_url: str) -> str | None:
    m = re.match(r"^(https?://[^/]+)", mcp_url)
    return f"{m.group(1)}/{SKILL_PATH_REL}" if m else None

def maybe_update_skill(mcp_url: str) -> None:
    """Once per day, refresh the installed skill from the origin. Fail silently."""
    today = date.today().isoformat()
    state = state_file()
    if state.exists() and state.read_text(encoding="utf-8").strip() == today:
        return

    try:
        origin = skill_origin(mcp_url)
        if not origin:
            return
        body = fetch_skill(origin)
        if not body:
            return
        text = body.decode("utf-8")
        if extract_version(text) is None:
            return  # Not a versioned skill distribution; leave the local copy alone.
        installed = install_dir() / "SKILL.md"
        if installed.exists():
            current = installed.read_text(encoding="utf-8")
            if extract_version(current) == extract_version(text):
                return
        installed.parent.mkdir(parents=True, exist_ok=True)
        tmp = installed.with_suffix(".md.tmp")
        tmp.write_text(text, encoding="utf-8")
        os.replace(tmp, installed)
    finally:
        try:
            state.parent.mkdir(parents=True, exist_ok=True)
            state.write_text(today, encoding="utf-8")
        except Exception:
            pass

def main():
    try:
        config_path = get_hermes_home() / "config.yaml"
        if not config_path.exists():
            # Fallback to wake agent if config cannot be found
            print(json.dumps({"wakeAgent": True}))
            return

        import yaml  # Hermes runtime always has PyYAML
        with open(config_path, "r", encoding="utf-8") as f:
            cfg = yaml.safe_load(f) or {}

        mcp_cfg = cfg.get("mcp_servers", {}).get("kakeratta", {})
        url = mcp_cfg.get("url")
        if not url:
            print(json.dumps({"wakeAgent": True}))
            return

        headers = dict(mcp_cfg.get("headers", {}))
        headers["Content-Type"] = "application/json"
        headers["Accept"] = "application/json, text/event-stream"

        try:
            maybe_update_skill(url)
        except Exception:
            pass

        payload = json.dumps({
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {
                "name": "list_tasks",
                "arguments": {}
            }
        }).encode("utf-8")

        req = urllib.request.Request(url, data=payload, headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=5) as response:
            data = json.loads(response.read().decode("utf-8"))

        res_obj = data["result"]
        if res_obj.get("isError"):
            raise ValueError("list_tasks returned an MCP error")
        structured = res_obj.get("structuredContent")
        if isinstance(structured, dict) and isinstance(structured.get("tasks"), list):
            tasks = structured["tasks"]
        else:
            content = res_obj.get("content") or []
            if not content:
                raise ValueError("list_tasks returned no task data")
            parsed = json.loads(content[0]["text"])
            tasks = parsed["tasks"]
            if not isinstance(tasks, list):
                raise ValueError("list_tasks.tasks is not a list")

        if len(tasks) == 0:
            print(json.dumps({"wakeAgent": False}))
        else:
            print(json.dumps({"wakeAgent": True}))

    except Exception as e:
        # On error (e.g. network timeout), fail-open to wake the agent so no tasks are lost
        print(f"Pre-check error: {e}", file=sys.stderr)
        print(json.dumps({"wakeAgent": True}))

if __name__ == "__main__":
    main()
