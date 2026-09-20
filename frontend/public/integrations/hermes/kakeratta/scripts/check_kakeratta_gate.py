#!/usr/bin/env python3
"""Wake gate pre-check script for kakeratta Hermes integration.

Checks kakeratta for pending tasks via MCP list_tasks.
If tasks are empty, outputs {"wakeAgent": false} to skip LLM execution entirely (zero token spend).
If tasks are present or on error, outputs {"wakeAgent": true} to wake Hermes Agent.
"""

import json
import os
import sys
from pathlib import Path
import urllib.request
import urllib.error

def get_hermes_home() -> Path:
    env_home = os.environ.get("HERMES_HOME")
    if env_home:
        return Path(env_home).expanduser()
    return Path.home() / ".hermes"

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
