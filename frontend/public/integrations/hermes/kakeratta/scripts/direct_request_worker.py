#!/usr/bin/env python3
"""Direct-request and persona-ask worker for the kakeratta Hermes integration.

The Wake Gate (check_kakeratta_gate.py) hands open direct requests
(`list_asks.requests`) and open persona asks (`list_asks.asks`) to this worker
instead of waking the Nemotron Super cron parent.

* Direct requests (`@agent`): claims in code (`claim_request`), runs one Ultra
  `hermes chat` session with the worker's read-only/configured tools, and posts
  the final response in code (`reply_request`).
* Persona asks: claims in code (`claim_ask`), runs one Super VL `hermes chat`
  session with only `read_for_ask` from kakeratta MCP (plus `web` when web
  search is permitted), and submits the structured answer in code
  (`submit_answer`), or releases the ask (`release_ask`) immediately to
  kakeratta's built-in planner on failure.
"""

import fcntl
import copy
import hashlib
import json
import os
import re
import signal
import shutil
import subprocess
import sys
import time
from pathlib import Path
from datetime import datetime, timezone
import urllib.request
import urllib.error

WORKER_VERSION = "16"

# --- defaults -------------------------------------------------------------
DEFAULT_MODEL = "nvidia/Nemotron-3-Ultra-550b-a55b"
DEFAULT_REASONING = "high"
MAX_RESUMES = 2              # later ticks that may resume an interrupted claimed request
MAX_TURNS = 15               # tool-calling iterations in the direct-request run
MAX_API_CALLS = 40           # model calls per direct request, across runs and resumes
MAX_REQUEST_TOKENS = 1_500_000  # input + cache read/write + output tokens per direct request
PERSONA_MAX_TURNS = 6        # tool-calling iterations in a persona-ask run (read_for_ask / web + answer)
PERSONA_MAX_API_CALLS = 8    # model calls per persona ask
PERSONA_MAX_TOKENS = 250_000 # input + cache read/write + output tokens per persona ask
PERSONA_SUBMIT_MARGIN = 30  # seconds reserved for submission and process cleanup
PERSONA_POLL_SECONDS = 1
MAX_REPLY = 20000            # reply_request rejects longer text
DEFAULT_MAX_TOKENS = 16000   # output cap per model call, reasoning included; bounds cost
RUN_BUDGET = 1200            # wall-clock budget per Hermes run, seconds
MCP_TIMEOUT = 300
AUDIT_MAX_BYTES = 1_000_000  # rotate a request's audit file past this
RESPONSE_TAIL = 300          # end of an unusable response kept in the audit log
# Read-only toolsets: the default, and the only ones under which a failed or
# interrupted run is safe to repeat.
DEFAULT_TOOLSETS = "web,kakeratta,files"
READ_ONLY_TOOLSETS = {"web", "kakeratta", "files"}

# The only kakeratta MCP tools the direct-request worker's Hermes home loads.
# Writes (claim_request, reply_request, comment_on_entry, submit_answer, ...)
# are not loaded at all, so a model cannot call them.
READONLY_MCP_TOOLS = ["get_timeline", "get_thread", "search_timeline", "list_schedules",
                      "list_personas", "list_notes", "read_note"]

# The only kakeratta MCP tool the persona-ask worker's Hermes home loads.
# claim_ask, submit_answer and release_ask are called by Python code, so the
# persona model only ever sees read_for_ask for kakera records and persona tools.
PERSONA_MCP_TOOLS = ["read_for_ask"]

# --- config and environment ----------------------------------------------

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

    # The sandbox has no route around the OpenShell proxy, so kakeratta and the
    # web search backends must go through it and be allowed by policy. Drop
    # them from no_proxy, including entries an older script left behind.
    proxied = {"kakeratta.net", "api.keenable.ai", "mcp.exa.ai", "search.parallel.ai"}
    for np in ("no_proxy", "NO_PROXY"):
        val = os.environ.get(np, "")
        if val:
            os.environ[np] = ",".join(h.strip() for h in val.split(",") if h.strip() and h.strip() not in proxied)


_load_env_file()


def read_config(home: Path):
    """Read config.yaml, or None when absent or unreadable."""
    path = home / "config.yaml"
    try:
        if not path.exists():
            return None
        import yaml  # Hermes runtime always has PyYAML
        with open(path, "r", encoding="utf-8") as f:
            return yaml.safe_load(f) or {}
    except Exception:
        return None


def resolve_env(value):
    """Resolve a `${VAR}` or `env:VAR` reference to its environment value.

    Used only to know the provider key's value so it can be masked; the key
    itself is passed to Hermes as the unexpanded reference.
    """
    if not isinstance(value, str):
        return value
    m = re.match(r"^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$", value.strip())
    if m:
        return os.environ.get(m.group(1), "")
    if value.startswith("env:"):
        return os.environ.get(value[4:], "")
    return value


def model_config(cfg):
    """The Ultra run settings, from config.yaml.

    Prefers a dedicated `kakeratta_worker` section, then Hermes's `delegation`
    section (already pinned to Ultra over Provider v2). The parent's `model`
    section is never used: it names Super, which must not handle direct
    requests.
    """
    section = None
    if isinstance(cfg, dict):
        for key in ("kakeratta_worker", "delegation"):
            if isinstance(cfg.get(key), dict):
                section = cfg[key]
                break
    if section is None:
        section = {}
    reasoning = section.get("reasoning_effort", section.get("reasoning", DEFAULT_REASONING))
    # A configured endpoint is kept as is: Token Factory over Provider v2 is
    # how Ultra is reached by name. OpenShell's inference.local substitutes the
    # gateway's model (Super), so it is only the fallback when none is set.
    # NemoClaw bakes NEMOCLAW_INFERENCE_BASE_URL into the sandbox image for the
    # parent, so it must not override the section either.
    base_url = (section.get("base_url")
                or os.environ.get("NEMOCLAW_INFERENCE_BASE_URL")
                or "https://inference.local/v1")
    return {
        "model": section.get("model") or DEFAULT_MODEL,
        "provider": section.get("provider") or "custom",
        "base_url": base_url,
        "api_key_ref": section.get("api_key"),
        "api_key": resolve_env(section.get("api_key") or "${TOKENFACTORY_API_KEY}"),
        "reasoning": "" if reasoning in (None, "none", "off") else reasoning,
        "max_tokens": int(section.get("max_tokens") or DEFAULT_MAX_TOKENS),
        "run_budget": int(section.get("run_budget") or RUN_BUDGET),
        "max_turns": int(section.get("max_turns") or MAX_TURNS),
        "toolsets": str(section.get("toolsets") or DEFAULT_TOOLSETS),
        # Parent MCP servers to load as they are configured there, by name.
        "mcp_servers": [str(n) for n in (section.get("mcp_servers") or []) if str(n) not in ("kakeratta", "files")],
        "max_api_calls": int(section.get("max_api_calls") or MAX_API_CALLS),
        "max_request_tokens": int(section.get("max_request_tokens") or MAX_REQUEST_TOKENS),
        "file_roots": [str(Path(r).expanduser()) for r in (section.get("file_roots") or [Path.home()])],
        "hermes_bin": section.get("hermes_bin") or shutil.which("hermes") or "/usr/local/bin/hermes",
    }


def prepare_worker_home(home: Path, main_cfg, cfg, url, headers, parent: Path = None) -> Path:
    """Write the worker's own Hermes home and return it.

    Model calls go through the Hermes CLI with this home, never straight to
    the provider. Its config.yaml carries Ultra with the output cap, the
    parent's provider settings (credentials stay `${VAR}` references), and
    the kakeratta MCP server restricted to READONLY_MCP_TOOLS, and this
    script's read-only file server (`files`) over the readable roots, plus the
    parent MCP servers named in `kakeratta_worker.mcp_servers`. The parent's
    skills, memory, cron jobs and sessions are not shared.

    ``home`` holds the worker's own files; ``parent`` is the Hermes home whose
    .env and config the worker borrows (the same unless a judge workspace
    keeps its files apart).
    """
    import yaml
    parent = parent or home
    main_cfg = main_cfg if isinstance(main_cfg, dict) else {}
    whome = _work_root(home) / "hermes-home"
    whome.mkdir(parents=True, exist_ok=True)
    os.chmod(whome, 0o700)

    model = dict(main_cfg["model"]) if isinstance(main_cfg.get("model"), dict) else {}
    model.update({"default": cfg["model"], "provider": cfg["provider"], "max_tokens": cfg["max_tokens"]})
    if cfg.get("base_url"):
        model["base_url"] = cfg["base_url"]
    if cfg.get("api_key_ref"):
        model["api_key"] = cfg["api_key_ref"]

    servers = main_cfg.get("mcp_servers") if isinstance(main_cfg.get("mcp_servers"), dict) else {}
    # A shared judge run selects its workspace at execution time. Never retain
    # the parent's configured per-user URL in the generated worker home.
    mcp = dict(servers.get("kakeratta") or {})
    mcp["url"] = url
    mcp["headers"] = {k: v for k, v in (headers or {}).items() if k.lower() not in ("content-type", "accept")}
    mcp["tools"] = {"include": list(READONLY_MCP_TOOLS), "prompts": False, "resources": False}

    files = {
        "command": sys.executable,
        "args": [str(Path(__file__).resolve()), "--fs-mcp", "--home", str(parent)]
                + [a for root in cfg["file_roots"] for a in ("--root", root)],
        "tools": {"prompts": False, "resources": False},
    }
    worker_servers = {"kakeratta": mcp}
    if not cfg.get("judge_isolated"):
        worker_servers["files"] = files
    for name in cfg["mcp_servers"]:
        if isinstance(servers.get(name), dict):
            worker_servers[name] = servers[name]
    worker_cfg = {
        "model": model,
        "agent": {"max_turns": cfg["max_turns"]},
        "tools": {"tool_search": {"enabled": "off"}},
        "mcp_servers": worker_servers,
    }
    if isinstance(main_cfg.get("providers"), dict):
        provs = copy.deepcopy(main_cfg["providers"])
        for p in provs.values():
            if isinstance(p, dict) and "tokenfactory.nebius.com" in str(p.get("api", "")):
                p["api"] = "https://inference.local/v1"
        worker_cfg["providers"] = provs
    if isinstance(main_cfg.get("custom_providers"), list):
        cps = copy.deepcopy(main_cfg["custom_providers"])
        for cp in cps:
            if isinstance(cp, dict) and "tokenfactory.nebius.com" in str(cp.get("base_url", "")):
                cp["base_url"] = "https://inference.local/v1"
        worker_cfg["custom_providers"] = cps
    _atomic_write(whome / "config.yaml", yaml.safe_dump(worker_cfg, allow_unicode=True), mode=0o600)

    # Credentials Hermes loads from .env stay in the parent's file.
    env_file, link = parent / ".env", whome / ".env"
    if env_file.exists() and not link.is_symlink():
        link.symlink_to(env_file)
    return whome


# --- MCP -----------------------------------------------------------------

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("MCP redirect refused")


def mcp_call(url, headers, name, arguments=None, timeout=MCP_TIMEOUT):
    """Call one kakeratta MCP tool with arguments and return structured data."""
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


# --- audit and durable state --------------------------------------------

def _work_root(home: Path) -> Path:
    return home / "kakeratta"


def _audit_path(home: Path, request_id: str) -> Path:
    return _work_root(home) / "audit" / f"{request_id}.jsonl"


def _state_path(home: Path, request_id: str) -> Path:
    return _work_root(home) / "state" / f"{request_id}.json"


def acquire_lock(home: Path):
    """Take the worker lock without waiting, or return None when another tick holds it.

    The lock lives as long as the returned file stays open, and the kernel
    drops it when the process exits, so a crashed run never leaves it stale.
    """
    path = _work_root(home) / "worker.lock"
    path.parent.mkdir(parents=True, exist_ok=True)
    f = open(path, "w")
    try:
        fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        f.close()
        return None
    return f


def _atomic_write(path: Path, text: str, mode=None) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(text, encoding="utf-8")
    if mode is not None:
        os.chmod(tmp, mode)
    os.replace(tmp, path)


def audit(home: Path, request_id: str, event: str, fields=None) -> None:
    """Append one JSON line, flushed, rotating the file past a size cap.

    Secrets are never recorded: only event metadata, hashes, counts, verdicts,
    durations, exit codes and truncated output are written.
    """
    record = {"event": event, "utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    if fields:
        record.update(fields)
    line = json.dumps(record, ensure_ascii=False) + "\n"
    path = _audit_path(home, request_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists() and path.stat().st_size + len(line) > AUDIT_MAX_BYTES:
        rotated = path.with_name(path.name + ".1")
        os.replace(path, rotated)
    with open(path, "a", encoding="utf-8") as f:
        f.write(line)
        f.flush()


def load_state(home: Path, request_id: str):
    try:
        raw = _state_path(home, request_id).read_text(encoding="utf-8")
        return json.loads(raw)
    except Exception:
        return {}


def save_state(home: Path, request_id: str, state) -> None:
    _atomic_write(_state_path(home, request_id), json.dumps(state, ensure_ascii=False))


def _usage_path(home: Path, request_id: str) -> Path:
    return _work_root(home) / "state" / "usage" / f"{request_id}.json"


# --- redaction -----------------------------------------------------------

_SECRET_ENV = re.compile(r"(?i)KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|AUTH")


def secret_values(cfg, headers):
    """Values that must never reach the model, a reply or a log: the provider
    key, the MCP request headers' credentials, and secret-named variables."""
    values = {cfg.get("api_key") or ""}
    for key, value in (headers or {}).items():
        if isinstance(value, str) and key.lower() not in ("content-type", "accept"):
            values.add(value)
            values.add(re.sub(r"(?i)^bearer\s+", "", value))
    for key, value in os.environ.items():
        if _SECRET_ENV.search(key):
            values.add(value)
    # Short values would mask ordinary words.
    return sorted((v for v in values if isinstance(v, str) and len(v) >= 8), key=len, reverse=True)


def redact(text, secrets=()):
    """Strip bearer tokens and any supplied secret values from a text."""
    if not isinstance(text, str):
        text = str(text)
    out = re.sub(r"(?i)Bearer\s+\S+", "Bearer ***", text)
    for secret in secrets:
        if secret and isinstance(secret, str):
            out = out.replace(secret, "***")
    return out


# --- Hermes runs ---------------------------------------------------------

class BudgetExceeded(RuntimeError):
    """The request reached its model-call or token limit, or a run's usage
    could not be measured. Nothing more runs; the request is answered."""


# Token and call counters of Hermes's `sessions` table. input_tokens excludes
# cache reads; reasoning_tokens is a breakdown of output_tokens.
USAGE_COLUMNS = ("api_call_count", "input_tokens", "cache_read_tokens",
                 "cache_write_tokens", "output_tokens", "reasoning_tokens")
BILLED_COLUMNS = ("input_tokens", "cache_read_tokens", "cache_write_tokens", "output_tokens")


def session_usage(worker_home: Path, session, timeout=10):
    """The usage Hermes recorded for one session, or None when the
    session, the database or its counters cannot be read."""
    path = Path(worker_home) / "state.db"
    if not session or not path.exists():
        return None
    import sqlite3
    try:
        con = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=timeout)
        try:
            columns = {row[1] for row in con.execute("PRAGMA table_info(sessions)")}
            if not {"api_call_count", "input_tokens", "output_tokens"} <= columns:
                return None
            wanted = [c for c in USAGE_COLUMNS if c in columns]
            row = con.execute(f"SELECT {', '.join(wanted)} FROM sessions WHERE id = ?",
                              (session,)).fetchone()
        finally:
            con.close()
    except sqlite3.Error:
        return None
    if row is None:
        return None
    return {c: int(v or 0) for c, v in zip(wanted, row)}


def session_ids(worker_home):
    """Snapshot the isolated home's sessions, without waiting on SQLite locks.

    None means unreadable; an absent database is a new home with no sessions.
    Never guess which session is ours when a snapshot failed.
    """
    import sqlite3
    path = Path(worker_home) / "state.db"
    if not path.exists():
        return set()
    try:
        con = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=0.1)
        try:
            return {row[0] for row in con.execute("SELECT id FROM sessions")}
        finally:
            con.close()
    except sqlite3.Error:
        return None


class Meter:
    """One request's model usage, saved so the limits hold across resumes."""

    def __init__(self, cfg, home: Path, request_id: str):
        self.cfg, self.home, self.request_id = cfg, home, request_id
        try:
            self.totals = json.loads(_usage_path(home, request_id).read_text(encoding="utf-8"))
        except Exception:
            self.totals = {}
        for key in ("runs", "unmeasured", "tokens") + USAGE_COLUMNS:
            self.totals.setdefault(key, 0)

    def check(self, usage=None):
        t, cfg = dict(self.totals), self.cfg
        if usage is not None:
            t["api_call_count"] += usage.get("api_call_count", 0)
            t["tokens"] += sum(usage.get(c, 0) for c in BILLED_COLUMNS)
        if t["unmeasured"]:
            raise BudgetExceeded("a model run's token usage could not be read")
        if t["api_call_count"] >= cfg["max_api_calls"]:
            raise BudgetExceeded(f"{t['api_call_count']} model calls reached the limit of {cfg['max_api_calls']}")
        if t["tokens"] >= cfg["max_request_tokens"]:
            raise BudgetExceeded(f"{t['tokens']} tokens reached the limit of {cfg['max_request_tokens']}")

    def record(self, stage, session, usage):
        t = self.totals
        t["runs"] += 1
        if usage is None:
            t["unmeasured"] += 1
        else:
            for key, value in usage.items():
                t[key] += value
            t["tokens"] += sum(usage.get(c, 0) for c in BILLED_COLUMNS)
        _atomic_write(_usage_path(self.home, self.request_id), json.dumps(t))
        audit(self.home, self.request_id, "model_usage", {
            "stage": stage, "session": session, "model": self.cfg["model"],
            "reasoning": self.cfg["reasoning"] or None, "usage": usage,
            "request_api_calls": t["api_call_count"], "request_tokens": t["tokens"],
        })


def _stop_persona_process(proc):
    """Reap the CLI and stop its tools, including after the CLI has exited."""
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        return proc.communicate(timeout=1)
    except subprocess.TimeoutExpired:
        pass
    finally:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    return proc.communicate()


def _run_persona_cli(cmd, prompt, env, cfg):
    """Watch live persisted usage and the deadline; never parse partial output.

    Counters are observed after Hermes saves them, so an in-flight provider
    call can exceed the token limit. A normally completed answer is retained.
    """
    home = cfg["worker_home"]
    before = session_ids(home)
    if before is None:
        raise BudgetExceeded("persona session usage database is unreadable")
    deadline = cfg["persona_deadline"]
    if time.monotonic() >= deadline:
        raise BudgetExceeded("persona answer deadline reached")
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True, env=env, cwd=str(home),
                            start_new_session=True)
    session, usage = None, None
    try:
        first = True
        while True:
            if proc.poll() is not None:
                stdout, stderr = proc.communicate(timeout=1)
                return subprocess.CompletedProcess(cmd, proc.returncode, stdout, stderr)
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise BudgetExceeded("persona answer deadline reached")
            try:
                stdout, stderr = proc.communicate(input=prompt if first else None,
                                                  timeout=min(PERSONA_POLL_SECONDS, remaining))
                return subprocess.CompletedProcess(cmd, proc.returncode, stdout, stderr)
            except subprocess.TimeoutExpired:
                first = False
            if before is not None and session is None:
                current = session_ids(home)
                new = current - before if current is not None else set()
                if len(new) == 1:
                    session = new.pop()
            if session:
                usage = session_usage(home, session, timeout=0.1)
                cfg["meter"].check(usage)
    except BaseException:
        _stop_persona_process(proc)
        cfg["meter"].record("persona", session, session_usage(home, session, timeout=0.1) or usage)
        raise


def hermes_run(cfg, prompt, toolsets, max_turns, stage):
    """One fresh, single-query Hermes CLI session with the worker's home.

    Returns (final response text, session id). A failed run raises. The run's
    usage is recorded on the request's meter whatever the outcome. Persona
    runs monitor live usage and their deadline, but keep a completed answer
    even if its final usage exceeds the limit; no further run is allowed.
    """
    meter = cfg["meter"]
    meter.check()
    cmd = [cfg["hermes_bin"], "chat", "-Q", "--ignore-rules",
           "--provider", cfg["provider"], "-m", cfg["model"], "-t", toolsets,
           "--max-turns", str(max_turns), "--run-budget", str(cfg["run_budget"]),
           "--query-file", "-"]
    if cfg.get("reasoning"):
        cmd += ["--reasoning", cfg["reasoning"]]
    env = dict(os.environ, HERMES_HOME=str(cfg["worker_home"]))
    try:
        if stage == "persona":
            proc = _run_persona_cli(cmd, prompt, env, cfg)
        else:
            proc = subprocess.run(cmd, input=prompt, env=env, cwd=str(cfg["worker_home"]),
                                  capture_output=True, text=True, timeout=cfg["run_budget"] + 120)
    except subprocess.TimeoutExpired:
        meter.record(stage, None, None)
        raise RuntimeError(f"hermes chat exceeded {cfg['run_budget'] + 120} seconds")
    m = re.search(r"session_id:\s*(\S+)", proc.stderr + "\n" + proc.stdout)
    session = m.group(1) if m else None
    meter.record(stage, session, session_usage(cfg["worker_home"], session,
                                              timeout=0.1 if stage == "persona" else 10))
    if proc.returncode != 0:
        raise RuntimeError(f"hermes chat exited with {proc.returncode}: {proc.stderr.strip()[-300:]}")
    if stage != "persona":
        meter.check()
    return proc.stdout, session


# --- prompts -------------------------------------------------------------

def _context_text(context):
    """Render the DirectContext for the model without omitting any of it."""
    parts = []
    parts.append("Instructions: " + str(context.get("instructions", "")))
    parts.append("User: " + str(context.get("user", "")))
    parts.append("Now: " + str(context.get("now", "")))
    parts.append("Time zone: " + str(context.get("timeZone", "")))
    parts.append("Place: " + str(context.get("place", "")))
    trigger = context.get("trigger") or {}
    parts.append("Trigger: " + json.dumps(trigger, ensure_ascii=False))
    thread = context.get("thread") or []
    lines = []
    for entry in thread:
        lines.append(f"- [{entry.get('speaker', '')}] {entry.get('text', '')} "
                     f"(rendered: {entry.get('rendered', '')})")
    parts.append("Thread (oldest first):\n" + "\n".join(lines))
    return "\n\n".join(parts)


AGENT_SYSTEM = """You are handling a request the user addressed directly to you, an external agent connected to the user's kakeratta. kakeratta's instructions for direct requests, the user, the time, the place, the trigger and the whole thread follow.

Do what the trigger asks, using the tools this session provides; some are found with tool_search and called with tool_call. Your final message is posted as it is, as your reply in the user's thread (Markdown), so write it for the user, in the user's language: the answer itself, or what you did and its result, or why you could not. Never claim an action you did not perform. Keep it under 18,000 characters. Do not call claim_request or reply_request; your reply is posted for you.
"""


def _agent_prompt(context):
    return AGENT_SYSTEM + "\n" + _context_text(context)


# Lines Hermes prints before the response in quiet mode.
_NOTICE = re.compile(r"^\s*(⚠|'[^']*' for \w+\.$)")
_SESSION_LINE = re.compile(r"^\s*session_id:\s*\S+\s*$")
# What Hermes answers when the run ends at its turn limit.
_TURN_LIMIT = re.compile(r"(?i)reached the (maximum number of tool-calling iterations|iteration limit)")


def clean_response(text):
    """The run's final response without the notices Hermes prints first."""
    lines = (text or "").strip().splitlines()
    while lines and _NOTICE.match(lines[0]):
        lines.pop(0)
    while lines and _SESSION_LINE.match(lines[-1]):
        lines.pop()
    return "\n".join(lines).strip()


def side_effects_possible(cfg):
    """Whether the worker's tools go beyond reading."""
    toolsets = {t.strip() for t in cfg["toolsets"].split(",") if t.strip()}
    return bool(cfg["mcp_servers"]) or not toolsets <= READ_ONLY_TOOLSETS


def _is_english(context):
    """Detect whether the user's request appears to be in English (non-Japanese)."""
    trigger = (context or {}).get("trigger") or {}
    text = (trigger.get("rendered") or trigger.get("text") or "").strip()
    if not text:
        thread = (context or {}).get("thread") or []
        for entry in reversed(thread):
            t = (entry.get("rendered") or entry.get("text") or "").strip()
            if t:
                text = t
                break
    # If Japanese characters (Hiragana, Katakana, CJK ideographs) are present, treat as Japanese.
    if re.search(r'[\u3040-\u309f\u30a0-\u30ff\u4e00-\u9fff]', text):
        return False
    return True


def run_agent(cfg, context, request_id, home):
    """Run Ultra once as a Hermes agent; returns the reply text."""
    text, session = hermes_run(cfg, _agent_prompt(context), cfg["toolsets"], cfg["max_turns"], "agent")
    reply = clean_response(text)
    audit(home, request_id, "agent_run", {"session": session, "reply_chars": len(reply)})
    if not reply or (_TURN_LIMIT.search(reply) and len(reply) < 300):
        audit(home, request_id, "agent_unfinished", {"session": session,
                                                     "response_tail": redact(text[-RESPONSE_TAIL:], cfg["secrets"])})
        if _is_english(context):
            return ("Could not complete your request. "
                    f"Did not finish formulating a response within the limit of tool calls ({cfg['max_turns']}).")
        return ("依頼を完了できませんでした。"
                f"ツール呼び出しの上限（{cfg['max_turns']}回）までに回答をまとめられませんでした。")
    return reply


# --- one request ---------------------------------------------------------

def process_one(url, headers, request, cfg, home):
    request_id = request["id"]
    claim_id = "hermes-" + request_id
    secrets = cfg["secrets"]
    state = load_state(home, request_id)
    stage = state.get("stage")
    if stage == "done":
        return

    # A previous run computed the reply but failed to send it: resend the
    # saved text without regenerating or re-running anything.
    if stage == "reply" and state.get("reply_text"):
        audit(home, request_id, "reply_retry")
        _send_reply(cfg, url, headers, request_id, claim_id, state["reply_text"], home)
        return

    # A claimed request is resumed only when this worker claimed it.
    resuming = stage in ("claimed", "reply")
    if request.get("status") != "pending" and not resuming:
        return
    resumes = int(state.get("resumes", -1)) + 1 if resuming else 0

    # Claim (idempotent with a stable claimId across ticks).
    try:
        claimed = mcp_call(url, headers, "claim_request",
                           {"requestId": request_id, "claimId": claim_id}, timeout=MCP_TIMEOUT)
    except Exception as e:
        # Before a claim: leave it pending, retry next tick. No fallback.
        audit(home, request_id, "claim_error", {"error": redact(e, secrets)})
        return
    save_state(home, request_id, {"stage": "claimed", "resumes": resumes})
    context = claimed.get("context") or {}
    is_en = _is_english(context)
    audit(home, request_id, "resumed" if resuming else "claimed", {
        "claim_id": claim_id, "model": cfg["model"], "provider": "hermes-cli/" + cfg["provider"],
        "reasoning": cfg["reasoning"] or None, "resume": resumes,
    })

    # A run that started with action tools may already have acted: report
    # it instead of running again.
    if state.get("ran") and side_effects_possible(cfg):
        audit(home, request_id, "rerun_skipped", {"reason": "an interrupted run may have acted"})
        msg = ("The previous run was interrupted. To avoid repeating actions that may have already been executed, it was not re-run. "
               "Please check the results and submit the request again if needed." if is_en else
               "前回の処理が途中で中断されました。操作が実行済みの可能性があるため、再実行していません。"
               "必要であれば、結果を確認してからもう一度依頼してください。")
        _send_reply(cfg, url, headers, request_id, claim_id, msg, home)
        return

    if resumes > MAX_RESUMES:
        audit(home, request_id, "resume_limit", {"resume": resumes})
        msg = ("Could not complete your request because execution was repeatedly interrupted." if is_en else
               "処理が繰り返し中断されたため、依頼を完了できませんでした。")
        _send_reply(cfg, url, headers, request_id, claim_id, msg, home)
        return

    cfg["meter"] = Meter(cfg, home, request_id)
    save_state(home, request_id, {"stage": "claimed", "resumes": resumes, "ran": True})
    try:
        reply = run_agent(cfg, context, request_id, home)
    except BudgetExceeded as e:
        # Fail-closed: no further model call and no resume.
        t = cfg["meter"].totals
        audit(home, request_id, "budget_exceeded", {
            "reason": str(e), "request_api_calls": t["api_call_count"], "request_tokens": t["tokens"],
        })
        msg = (f"Execution was stopped because the model call limit per request "
               f"({cfg['max_api_calls']} calls, {cfg['max_request_tokens']:,} tokens) was reached "
               f"or token usage could not be verified.\n\nReason: {e}" if is_en else
               "1件の依頼で使えるモデル呼び出しの上限"
               f"（{cfg['max_api_calls']}回・{cfg['max_request_tokens']:,}トークン）に達したか、"
               "使用量を確認できなかったため、処理を止めました。\n\n"
               f"理由: {e}")
        _send_reply(cfg, url, headers, request_id, claim_id, msg, home)
    except Exception as e:
        # A Hermes, provider or network failure. With read-only tools the
        # request stays claimed and a later tick runs it again, until the
        # resume limit; with action tools it may have acted, so report it.
        error = redact(e, secrets)
        audit(home, request_id, "failed", {"error": error, "resume": resumes})
        if resumes >= MAX_RESUMES or side_effects_possible(cfg):
            prefix = "Could not process your request.\n\n" if is_en else "依頼を処理できませんでした。\n\n"
            _send_reply(cfg, url, headers, request_id, claim_id, prefix + error, home)
        else:
            save_state(home, request_id, {"stage": "claimed", "resumes": resumes})
    else:
        _send_reply(cfg, url, headers, request_id, claim_id, reply, home)


def _send_reply(cfg, url, headers, request_id, claim_id, text, home):
    text = redact(text, cfg["secrets"]).strip()
    if not text:
        text = "処理できませんでした。"
    if len(text) > MAX_REPLY:
        text = text[:MAX_REPLY]
    # Save the reply before sending so a crash after execution but before the
    # reply is resent next tick, never re-running the operation.
    save_state(home, request_id, {"stage": "reply", "reply_text": text})
    try:
        result = mcp_call(url, headers, "reply_request",
                          {"requestId": request_id, "claimId": claim_id, "text": text},
                          timeout=MCP_TIMEOUT)
        audit(home, request_id, "reply_sent", {"entry_id": result.get("entryId")})
        save_state(home, request_id, {"stage": "done"})
    except Exception as e:
        audit(home, request_id, "reply_error", {"error": redact(e, cfg["secrets"])})


# --- read-only file MCP server ------------------------------------------
#
# The worker's Hermes home starts this script with --fs-mcp as a stdio MCP
# server, so the model can read local files without Hermes's `file` toolset,
# which also writes. Reading is all it can do: paths must resolve inside the
# configured roots, and hidden entries (dotfiles such as .hermes, .env, .ssh)
# and secret-looking names are refused. Known secret values are masked.

_DENY_PART = re.compile(
    r"(?i)^(\..*|config\.ya?ml|id_(rsa|dsa|ecdsa|ed25519).*|.*\.(pem|key|p12|pfx)|"
    r".*(secret|token|credential|password|auth).*)$")
FILE_TEXT_CAP = 200_000      # characters returned per read_file call
FILE_MAX_BYTES = 5_000_000   # larger files are refused

FS_TOOLS = [
    {"name": "read_file",
     "description": "Read a local text file (read-only). Long files come in parts: pass the offset the previous part reports.",
     "inputSchema": {"type": "object", "properties": {
         "path": {"type": "string", "description": "Absolute path, or relative to the first readable root"},
         "offset": {"type": "integer", "description": "Character offset to start from"}},
         "required": ["path"]}},
    {"name": "list_directory",
     "description": "List a local directory (read-only).",
     "inputSchema": {"type": "object", "properties": {
         "path": {"type": "string", "description": "Absolute path, or relative to the first readable root"}}}},
]


def _fs_resolve(roots, path):
    p = Path(str(path or ".")).expanduser()
    candidates = [p] if p.is_absolute() else [Path(r) / p for r in roots]
    for candidate in candidates:
        target = candidate.resolve()
        for root in roots:
            root = Path(root).resolve()
            if target == root or root in target.parents:
                if any(_DENY_PART.match(part) for part in target.relative_to(root).parts):
                    raise PermissionError("this path may not be read")
                return target
    raise PermissionError("the path is outside the readable roots: " + ", ".join(roots))


def fs_read_file(roots, args, secrets):
    target = _fs_resolve(roots, args.get("path"))
    if not target.is_file():
        raise FileNotFoundError("not a file")
    if target.stat().st_size > FILE_MAX_BYTES:
        raise ValueError("the file is too large")
    text = target.read_bytes().decode("utf-8", errors="replace")
    offset = max(0, int(args.get("offset") or 0))
    part = text[offset:offset + FILE_TEXT_CAP]
    out = redact(part, secrets)
    if offset + len(part) < len(text):
        out += f"\n\n[part of {len(text)} characters; continue with offset={offset + len(part)}]"
    return out


def fs_list_directory(roots, args, secrets):
    target = _fs_resolve(roots, args.get("path") or roots[0])
    if not target.is_dir():
        raise NotADirectoryError("not a directory")
    names = [c.name + ("/" if c.is_dir() else "") for c in sorted(target.iterdir())
             if not _DENY_PART.match(c.name)]
    return "\n".join(names[:500])


def serve_fs_mcp(roots, secrets):
    """Answer MCP JSON-RPC over stdin/stdout, one message per line."""
    def send(message):
        sys.stdout.write(json.dumps(message, ensure_ascii=False) + "\n")
        sys.stdout.flush()
    tools = {"read_file": fs_read_file, "list_directory": fs_list_directory}
    for line in sys.stdin:
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        mid, method = message.get("id"), message.get("method")
        if mid is None:
            continue  # notifications need no answer
        params = message.get("params") or {}
        if method == "initialize":
            result = {"protocolVersion": params.get("protocolVersion") or "2024-11-05",
                      "capabilities": {"tools": {}},
                      "serverInfo": {"name": "kakeratta-readonly-files", "version": WORKER_VERSION}}
        elif method == "ping":
            result = {}
        elif method == "tools/list":
            result = {"tools": FS_TOOLS}
        elif method == "tools/call":
            try:
                fn = tools.get(params.get("name"))
                if fn is None:
                    raise ValueError("unknown tool")
                text = fn(roots, params.get("arguments") or {}, secrets)
                result = {"content": [{"type": "text", "text": text}]}
            except Exception as e:
                result = {"content": [{"type": "text", "text": redact(str(e), secrets)}], "isError": True}
        else:
            send({"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "method not found"}})
            continue
        send({"jsonrpc": "2.0", "id": mid, "result": result})


def fs_mcp_main(argv):
    """Entry for --fs-mcp: --home names the parent Hermes home (for the
    secrets to mask), each --root a readable directory."""
    home, roots = None, []
    for i, arg in enumerate(argv):
        if arg == "--home" and i + 1 < len(argv):
            home = Path(argv[i + 1])
        if arg == "--root" and i + 1 < len(argv):
            roots.append(argv[i + 1])
    main_cfg = read_config(home) if home else None
    servers = (main_cfg or {}).get("mcp_servers") or {}
    headers = {k: resolve_env(v) for k, v in ((servers.get("kakeratta") or {}).get("headers") or {}).items()}
    serve_fs_mcp(roots or [str(Path.home())], secret_values(model_config(main_cfg), headers))
    return 0


# --- entry points --------------------------------------------------------

def process(url, headers, requests, cfg=None, home=None, workspace_id=None):
    """Process open direct requests, oldest first: pending ones, and claimed
    ones this worker left unfinished. Returns nothing; each request is handled
    independently and errors are audited, never raised. Only one worker runs
    at a time; an overlapping tick returns at once."""
    parent = home or get_hermes_home()
    main_cfg = read_config(parent)
    # State, audit, usage and locks must not collide when identical request IDs
    # occur in two judge workspaces. Keep the ordinary layout for legacy calls.
    home = parent
    if workspace_id and workspace_id != "configured":
        home = _judge_root(parent, workspace_id)
    cfg = cfg or model_config(main_cfg)
    if workspace_id and workspace_id != "configured":
        cfg = dict(cfg, toolsets="web,kakeratta", mcp_servers=[], file_roots=[], judge_isolated=True)
    cfg["secrets"] = secret_values(cfg, headers)
    lock = acquire_lock(home)
    if lock is None:
        print("kakeratta direct-request worker: another run is in progress; skipping", file=sys.stderr)
        return
    try:
        cfg["worker_home"] = prepare_worker_home(home, main_cfg, cfg, url, headers, parent=parent)
        for request in sorted(requests, key=lambda r: r.get("id", "")):
            rid = request.get("id")
            if not rid or request.get("status") not in ("pending", "claimed"):
                continue
            try:
                process_one(url, headers, request, cfg, home)
            except Exception as e:
                audit(home, rid, "worker_error", {"error": redact(e, cfg["secrets"])})
    finally:
        lock.close()


def _judge_root(parent: Path, workspace_id: str) -> Path:
    if not re.fullmatch(r"[a-f0-9]{64}", workspace_id):
        raise ValueError("invalid judge workspace ID")
    return parent / "kakeratta-judge-workspaces" / workspace_id


def spawn_persona_asks(url, headers, workspace_id, asks=None, home=None):
    """Start process_persona_asks for one judge workspace in a detached
    process and return at once, so the Wake Gate is never held by a Hermes
    run. The MCP credential goes over stdin, never on the command line."""
    parent = home or get_hermes_home()
    root = _judge_root(parent, workspace_id)
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(root, 0o700)
    log = open(root / "persona.log", "a", encoding="utf-8")
    try:
        proc = subprocess.Popen(
            [sys.executable, str(Path(__file__).resolve()), "--persona-asks"],
            stdin=subprocess.PIPE, stdout=log, stderr=log, text=True,
            env=dict(os.environ, HERMES_HOME=str(parent)), start_new_session=True)
        payload = {"url": url, "headers": headers, "workspaceId": workspace_id}
        if asks is not None:
            payload["asks"] = asks
        proc.stdin.write(json.dumps(payload))
        proc.stdin.close()
    finally:
        log.close()


def spawn_judge_requests(url, headers, workspace_id, requests, home=None):
    parent = home or get_hermes_home()
    root = _judge_root(parent, workspace_id)
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(root, 0o700)
    fd = os.open(root / "requests.log", os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    with os.fdopen(fd, "a") as log:
        proc = subprocess.Popen(
            [sys.executable, str(Path(__file__).resolve()), "--judge-requests"],
            stdin=subprocess.PIPE, stdout=log, stderr=log, text=True,
            env=dict(os.environ, HERMES_HOME=str(parent)), start_new_session=True)
        proc.stdin.write(json.dumps({"url": url, "headers": headers, "workspaceId": workspace_id, "requests": requests}))
        proc.stdin.close()


def judge_requests_main():
    args = json.loads(sys.stdin.read())
    try:
        process(args["url"], args["headers"], args["requests"], workspace_id=args["workspaceId"])
    except Exception:
        print("judge direct-request worker failed", file=sys.stderr)
        return 1
    return 0


def persona_asks_main():
    """Entry for --persona-asks: the arguments arrive as JSON on stdin."""
    args = json.loads(sys.stdin.read())
    try:
        process_persona_asks(args["url"], args["headers"], args["workspaceId"], asks=args.get("asks"))
    except Exception as e:
        secrets = secret_values(model_config(read_config(get_hermes_home())), args.get("headers"))
        print(f"kakeratta persona worker failed for {args.get('workspaceId')}: {redact(e, secrets)}", file=sys.stderr)
        return 1
    return 0


PARENT_SUPER_VL = "nvidia/Nemotron-3.5-Super-VL"
LEGACY_PARENT_SUPER = "nvidia/nemotron-3-super-120b-a12b"


def persona_model_config(main_cfg):
    """Upgrade the legacy Token Factory Super in generated persona homes.

    Respect deliberate model/provider choices; Ultra direct requests use the
    separate model_config path and are unaffected.
    """
    model = copy.deepcopy((main_cfg or {}).get("model") or {})
    if (model.get("provider") == "custom"
            and str(model.get("base_url", "")).rstrip("/") == "https://api.tokenfactory.nebius.com/v1"
            and str(model.get("default", "")).lower() == LEGACY_PARENT_SUPER):
        model["default"] = PARENT_SUPER_VL
    return model


def persona_run_config(main_cfg):
    """Build the runtime config for code-driven persona-ask runs on Super VL."""
    model = persona_model_config(main_cfg)
    base = model_config(main_cfg)
    api_key_ref = model.get("api_key") or base.get("api_key_ref")
    reasoning = model.get("reasoning_effort", model.get("reasoning", ""))
    return {
        "model": model.get("default") or model.get("model") or PARENT_SUPER_VL,
        "provider": model.get("provider") or base.get("provider") or "custom",
        "base_url": model.get("base_url") or base.get("base_url"),
        "api_key_ref": api_key_ref,
        "api_key": resolve_env(api_key_ref or "${TOKENFACTORY_API_KEY}"),
        "reasoning": "" if reasoning in (None, "none", "off", "") else str(reasoning),
        "max_tokens": int(model.get("max_tokens") or DEFAULT_MAX_TOKENS),
        "run_budget": int(model.get("run_budget") or base["run_budget"]),
        "max_turns": int(model.get("max_turns") or PERSONA_MAX_TURNS),
        "max_api_calls": int(model.get("max_api_calls") or PERSONA_MAX_API_CALLS),
        "max_request_tokens": int(model.get("max_request_tokens") or PERSONA_MAX_TOKENS),
        "hermes_bin": base["hermes_bin"],
    }


def prepare_persona_home(home: Path, main_cfg, cfg, url, headers, parent: Path = None, judge_isolated: bool = False) -> Path:
    """Write the persona worker's isolated Hermes home and return it.

    Persona asks claim and submit in Python code, so the generated home loads
    only `read_for_ask` from the kakeratta MCP server (`PERSONA_MCP_TOOLS`) and
    zero skills. `list_asks`, `claim_ask`, `submit_answer` and `release_ask`
    are not loaded at all.
    """
    import yaml
    parent = parent or home
    main_cfg = main_cfg if isinstance(main_cfg, dict) else {}
    run_home = (home if judge_isolated else _work_root(home)) / "persona-hermes-home"
    run_home.mkdir(parents=True, exist_ok=True)
    os.chmod(run_home, 0o700)

    model = persona_model_config(main_cfg)
    model.update({"default": cfg["model"], "provider": cfg["provider"], "max_tokens": cfg["max_tokens"]})
    if cfg.get("base_url"):
        model["base_url"] = cfg["base_url"]
    if cfg.get("api_key_ref"):
        model["api_key"] = cfg["api_key_ref"]

    servers = main_cfg.get("mcp_servers") if isinstance(main_cfg.get("mcp_servers"), dict) else {}
    mcp = dict(servers.get("kakeratta") or {})
    mcp["url"] = url
    mcp["headers"] = {k: v for k, v in (headers or {}).items() if k.lower() not in ("content-type", "accept")}
    mcp["tools"] = {"include": list(PERSONA_MCP_TOOLS), "prompts": False, "resources": False}

    persona_cfg = {
        "model": model,
        "agent": {"max_turns": cfg["max_turns"]},
        "tools": {"tool_search": {"enabled": "off"}},
        "mcp_servers": {"kakeratta": mcp},
    }
    if isinstance(main_cfg.get("providers"), dict):
        provs = copy.deepcopy(main_cfg["providers"])
        for p in provs.values():
            if isinstance(p, dict) and "tokenfactory.nebius.com" in str(p.get("api", "")):
                p["api"] = "https://inference.local/v1"
        persona_cfg["providers"] = provs
    if isinstance(main_cfg.get("custom_providers"), list):
        cps = copy.deepcopy(main_cfg["custom_providers"])
        for cp in cps:
            if isinstance(cp, dict) and "tokenfactory.nebius.com" in str(cp.get("base_url", "")):
                cp["base_url"] = "https://inference.local/v1"
        persona_cfg["custom_providers"] = cps
    _atomic_write(run_home / "config.yaml", yaml.safe_dump(persona_cfg, allow_unicode=True), mode=0o600)

    skills_link = run_home / "skills"
    if skills_link.is_symlink():
        skills_link.unlink()
    env_file, link = parent / ".env", run_home / ".env"
    if env_file.exists() and not link.exists():
        link.symlink_to(env_file)
    return run_home


PERSONA_SYSTEM = """あなたは kakeratta の担当ペルソナとして、今回のターンの最終回答を作成します。
claim_ask や submit_answer などの取得・提出操作は外側のワーカーが自動で処理するため、呼び出さないでください。

調査ツールについて:
- スレッドと「記憶（最初に渡したもの）」だけで答えられる場合は、ツールを呼ばずにすぐ回答してください。
- 過去のかけら記録（`records`）や、下に列挙された担当固有の読み取りツール（`timeline_search`, `weather_forecast`, `place_search`, `schedules_list`, `kakeratta_help` 等）で調べる必要がある場合は、`mcp__kakeratta__read_for_ask` を直接呼び出してください（`askId` と `claimId` は下に示された値をそのまま渡し、`steps` に `[{"type": "records" または "tool", "tool": "ツール名（recordsなら空文字）", "args": "{...JSON文字列...}"}]` を指定します）。
- 外部Webの最新情報が必要な場合は `web_search`（または `read_for_ask` の `web_search`）を使えます。

出力形式:
- 発言する場合は、ユーザーに表示する最終回答の本文（Markdown）だけをそのまま出力してください（JSON で包まないでください）。
- `read_for_ask` の `timeline_search` で見つけた過去の投稿IDを出典（sources）として引用する場合は、回答末尾の行に `[SOURCES: e_xxx, e_yyy]`（最大3件）と添えてください。
- 回答の中で予定の作成・変更・取消を約束した場合（または予定操作が必要な場合）のみ、回答の最終行に `[SCHEDULE]` と1行だけ添えてください。
- 名指しされておらず、対象発言があなたの担当領域と無関係で黙る場合は `[UNRELATED]` とだけ出力してください。
- 名指しされておらず、担当領域には関係あるが今回は言うことがなく黙る場合は `[SILENT]` とだけ出力してください。
"""


def _persona_context_text(context, ask_id="", claim_id=""):
    persona = context.get("persona") or {}
    parts = [
        f"あなたは{persona.get('name', '')}（ID: {persona.get('id', '')}）です。{persona.get('tone', '')}",
        f"askId: {ask_id}\nclaimId: {claim_id}",
        "あなたの担当領域:\n" + str(persona.get("domain", "")),
        "あなた自身への指示:\n" + str(context.get("instructions", "")),
        str(context.get("rules", "")),
        "いまの日時: " + str(context.get("now", "")),
        str(context.get("place", "")),
        "できること:\n" + str(context.get("capabilities", "")),
    ]
    if context.get("turn"):
        parts.append(str(context["turn"]).strip())
    if context.get("tools"):
        parts.append("利用可能な read_for_ask ツール:\n" + json.dumps(context.get("tools"), ensure_ascii=False))
    if context.get("limits"):
        parts.append("調査上限 (limits): " + json.dumps(context.get("limits"), ensure_ascii=False))
    parts.append(f"今回の起動理由 ({context.get('wake', '')}):\n{context.get('wakeInstructions', '')}")
    parts.append("記憶（最初に渡したもの）:\n" + str(context.get("memory", "")))
    parts.append("既存の予定:\n" + str(context.get("schedules", "")))
    trigger = context.get("trigger") or {}
    parts.append("対象発言 (Trigger):\n" + str(trigger.get("rendered") or trigger.get("text") or ""))
    thread = context.get("thread") or []
    lines = []
    for entry in thread:
        if isinstance(entry, dict):
            lines.append(entry.get("rendered") or f"[{entry.get('speaker', '')}] {entry.get('text', '')}")
    parts.append("スレッド全体の流れ (古い順):\n" + "\n\n".join(lines))
    return "\n\n".join(parts)


def _persona_prompt(context, ask_id="", claim_id=""):
    return PERSONA_SYSTEM + "\n" + _persona_context_text(context, ask_id=ask_id, claim_id=claim_id)


def _persona_toolsets(context):
    limits = context.get("limits") if isinstance(context.get("limits"), dict) else {}
    searches = limits.get("searches")
    if isinstance(searches, int) and searches <= 0:
        return "kakeratta"
    tools = context.get("tools")
    if isinstance(tools, list) and tools:
        if any(isinstance(t, dict) and t.get("name") == "web_search" for t in tools):
            return "kakeratta,web"
        return "kakeratta"
    return "kakeratta,web"


_SCHEDULE_MARKER = re.compile(r"(?:^\s*\[SCHEDULE\]\s*(?:\r?\n|$)|(?:\r?\n|\s)*\[SCHEDULE\]\s*$)", re.IGNORECASE)
_SOURCES_MARKER = re.compile(r"(?:^\s*\[SOURCES:\s*([^\]]*)\]\s*(?:\r?\n|$)|(?:\r?\n|\s)*\[SOURCES:\s*([^\]]*)\]\s*$)", re.IGNORECASE)
_FENCED_JSON = re.compile(r"^```(?:json)?\s*\n([\s\S]*?)\n```\s*$")


def parse_persona_answer(raw_text, context):
    """Turn the persona model's response into a kakeratta Answer dict, or None
    when the response is unusable and the ask should be released to the
    built-in planner."""
    turn = str((context or {}).get("turn") or "")
    wake = str((context or {}).get("wake") or "")
    listening = wake == "listening" or "このターンは発言できません" in turn
    named = "あなたは名指しで呼ばれています" in turn and not listening

    if listening or wake == "post-deleted":
        return {"text": "", "sources": [], "schedule": False, "unrelated": False}

    cleaned = clean_response(raw_text)
    if not cleaned or (_TURN_LIMIT.search(cleaned) and len(cleaned) < 300):
        return None

    candidate = cleaned
    m = _FENCED_JSON.match(cleaned)
    if m:
        candidate = m.group(1).strip()
    if candidate.startswith("{") and candidate.endswith("}"):
        try:
            obj = json.loads(candidate)
            if isinstance(obj, dict) and isinstance(obj.get("text"), str):
                text = obj["text"].replace("\\n", "\n").strip()
                if named and not text:
                    return None
                raw_sources = obj.get("sources") if isinstance(obj.get("sources"), list) else []
                sources = [str(s).strip() for s in raw_sources if str(s).strip()][:3]
                unrelated = bool(obj.get("unrelated")) and not text and not named
                return {
                    "text": text,
                    "sources": sources,
                    "schedule": bool(obj.get("schedule")),
                    "unrelated": unrelated,
                }
        except Exception:
            pass

    schedule = False
    sources = []
    # Strip leading or trailing [SCHEDULE] and [SOURCES: ...] markers in either order.
    for _ in range(2):
        if _SCHEDULE_MARKER.search(cleaned):
            schedule = True
            cleaned = _SCHEDULE_MARKER.sub("", cleaned).strip()
        sm = _SOURCES_MARKER.search(cleaned)
        if sm:
            raw_list = sm.group(1) if sm.group(1) is not None else (sm.group(2) or "")
            sources = [s.strip() for s in raw_list.split(",") if s.strip()][:3]
            cleaned = _SOURCES_MARKER.sub("", cleaned).strip()

    upper = cleaned.strip().upper()
    if upper == "[UNRELATED]":
        if named:
            return None
        return {"text": "", "sources": [], "schedule": False, "unrelated": True}
    if upper == "[SILENT]":
        if named:
            return None
        return {"text": "", "sources": [], "schedule": schedule, "unrelated": False}
    if named and not cleaned.strip():
        return None
    return {"text": cleaned, "sources": sources, "schedule": schedule, "unrelated": False}


def _submit_ask_answer(cfg, url, headers, ask_id, claim_id, answer, home, state_key):
    cleaned = dict(answer)
    cleaned["text"] = redact(cleaned.get("text", ""), cfg["secrets"]).strip()
    cleaned["sources"] = list(cleaned.get("sources") or [])[:3]
    cleaned["schedule"] = bool(cleaned.get("schedule"))
    cleaned["unrelated"] = bool(cleaned.get("unrelated")) and not cleaned["text"]
    save_state(home, state_key, {"stage": "submit", "answer": cleaned})
    try:
        verdict = mcp_call(url, headers, "submit_answer",
                           {"askId": ask_id, "claimId": claim_id, "answer": cleaned},
                           timeout=MCP_TIMEOUT)
    except Exception as e:
        audit(home, state_key, "submit_error", {"error": redact(e, cfg["secrets"])})
        return
    if verdict.get("accepted") or verdict.get("closed"):
        audit(home, state_key, "answer_submitted", {
            "accepted": bool(verdict.get("accepted")),
            "closed": bool(verdict.get("closed")),
            "schedule": cleaned["schedule"],
            "unrelated": cleaned["unrelated"],
            "chars": len(cleaned["text"]),
        })
        save_state(home, state_key, {"stage": "done"})
        return
    problems = "; ".join(str(p) for p in (verdict.get("problems") or [])) or "rejected"
    _release_ask(cfg, url, headers, ask_id, claim_id, problems, home, state_key)


def _release_ask(cfg, url, headers, ask_id, claim_id, reason, home, state_key):
    reason = redact(reason or "released", cfg["secrets"])[:200]
    try:
        mcp_call(url, headers, "release_ask",
                 {"askId": ask_id, "claimId": claim_id, "reason": reason},
                 timeout=MCP_TIMEOUT)
        audit(home, state_key, "ask_released", {"reason": reason})
        save_state(home, state_key, {"stage": "done"})
    except Exception as e:
        audit(home, state_key, "release_error", {"error": redact(e, cfg["secrets"])})


def _persona_deadline_config(cfg, claimed):
    """Use the server's original deadline, including when resuming a claim."""
    plan_by = (claimed.get("ask") or {}).get("planBy")
    try:
        end = datetime.fromisoformat(plan_by.replace("Z", "+00:00"))
        if end.tzinfo is None:
            raise ValueError("planBy needs a timezone")
    except (AttributeError, TypeError, ValueError) as e:
        raise BudgetExceeded("missing or invalid persona planBy") from e
    remaining = (end - datetime.now(timezone.utc)).total_seconds() - PERSONA_SUBMIT_MARGIN
    budget = min(int(remaining), int(cfg["run_budget"]))
    if budget < 1:
        raise BudgetExceeded("insufficient time before persona planBy")
    return dict(cfg, run_budget=budget, persona_deadline=time.monotonic() + budget)


def process_one_ask(url, headers, ask, cfg, home):
    ask_id = ask["id"]
    claim_id = "hermes-" + ask_id
    state_key = "ask-" + ask_id
    secrets = cfg["secrets"]
    state = load_state(home, state_key)
    stage = state.get("stage")
    if stage == "done":
        return

    if stage == "submit" and isinstance(state.get("answer"), dict):
        audit(home, state_key, "submit_retry")
        _submit_ask_answer(cfg, url, headers, ask_id, claim_id, state["answer"], home, state_key)
        return

    resuming = stage in ("claimed", "submit")
    if ask.get("status") != "pending" and not resuming:
        return
    resumes = int(state.get("resumes", -1)) + 1 if resuming else 0

    try:
        claimed = mcp_call(url, headers, "claim_ask",
                           {"askId": ask_id, "claimId": claim_id}, timeout=MCP_TIMEOUT)
    except Exception as e:
        audit(home, state_key, "claim_error", {"error": redact(e, secrets)})
        return
    save_state(home, state_key, {"stage": "claimed", "resumes": resumes})
    context = claimed.get("context") or {}
    wake = str(context.get("wake") or ask.get("wake") or "")
    turn = str(context.get("turn") or "")
    audit(home, state_key, "resumed" if resuming else "claimed", {
        "claim_id": claim_id, "persona": (context.get("persona") or {}).get("id") or ask.get("persona"),
        "wake": wake, "model": cfg["model"], "resume": resumes,
    })

    # Listening and post-deleted turns require an empty answer text; submit
    # immediately in code without spending any model tokens.
    if wake in ("listening", "post-deleted") or "このターンは発言できません" in turn:
        _submit_ask_answer(cfg, url, headers, ask_id, claim_id,
                           {"text": "", "sources": [], "schedule": False, "unrelated": False},
                           home, state_key)
        return

    if resumes > MAX_RESUMES:
        _release_ask(cfg, url, headers, ask_id, claim_id, "resume limit reached", home, state_key)
        return

    toolsets = _persona_toolsets(context)
    try:
        cfg = _persona_deadline_config(cfg, claimed)
        cfg["meter"] = Meter(cfg, home, state_key)
        text, session = hermes_run(cfg, _persona_prompt(context, ask_id=ask_id, claim_id=claim_id),
                                   toolsets, cfg["max_turns"], "persona")
    except Exception as e:
        error = redact(e, secrets)
        audit(home, state_key, "failed", {"error": error, "resume": resumes})
        _release_ask(cfg, url, headers, ask_id, claim_id, error, home, state_key)
        return

    answer = parse_persona_answer(text, context)
    audit(home, state_key, "persona_run", {
        "session": session, "toolsets": toolsets,
        "accepted_shape": answer is not None,
        "reply_chars": len(answer["text"]) if answer else 0,
    })
    if answer is None:
        _release_ask(cfg, url, headers, ask_id, claim_id, "unusable persona response", home, state_key)
        return
    _submit_ask_answer(cfg, url, headers, ask_id, claim_id, answer, home, state_key)


def acquire_persona_lock(home: Path, judge_isolated: bool = False):
    path = (home if judge_isolated else _work_root(home)) / "persona.lock"
    path.parent.mkdir(parents=True, exist_ok=True)
    f = open(path, "w")
    try:
        fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        f.close()
        return None
    return f


def process_asks(url, headers, asks=None, cfg=None, home=None, workspace_id=None):
    """Process open persona asks in code, oldest first: claim_ask in Python,
    one Super VL `hermes chat -Q` session with only `read_for_ask` from
    kakeratta MCP (plus `web` when permitted), and submit_answer (or
    release_ask on error) in Python."""
    parent = home or get_hermes_home()
    main_cfg = read_config(parent)
    if not isinstance(main_cfg, dict):
        raise RuntimeError("config.yaml not found")
    judge_isolated = bool(workspace_id and workspace_id != "configured")
    home = _judge_root(parent, workspace_id) if judge_isolated else parent
    if judge_isolated:
        home.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(home, 0o700)
    cfg = cfg or persona_run_config(main_cfg)
    cfg["secrets"] = secret_values(cfg, headers)
    lock = acquire_persona_lock(home, judge_isolated=judge_isolated)
    if lock is None:
        print("kakeratta persona worker: another run is in progress; skipping", file=sys.stderr)
        return
    try:
        cfg["worker_home"] = prepare_persona_home(home, main_cfg, cfg, url, headers, parent=parent, judge_isolated=judge_isolated)
        if asks is None:
            listed = mcp_call(url, headers, "list_asks", timeout=MCP_TIMEOUT)
            asks = listed.get("asks") if isinstance(listed, dict) else []
        open_asks = [a for a in (asks or []) if isinstance(a, dict) and a.get("status") in ("pending", "claimed")]
        for ask in sorted(open_asks, key=lambda a: a.get("id", "")):
            aid = ask.get("id")
            if not aid:
                continue
            try:
                process_one_ask(url, headers, ask, cfg, home)
            except Exception as e:
                audit(home, "ask-" + aid, "worker_error", {"error": redact(e, cfg["secrets"])})
    finally:
        lock.close()


def process_persona_asks(url, headers, workspace_id, home=None, asks=None):
    """Run code-driven persona-ask handling against one workspace."""
    process_asks(url, headers, asks=asks, home=home, workspace_id=workspace_id)


def main():
    home = get_hermes_home()
    cfg_dict = read_config(home)
    if not cfg_dict:
        print("direct_request_worker: config.yaml not found", file=sys.stderr)
        return 1
    mcp_cfg = (cfg_dict.get("mcp_servers") or {}).get("kakeratta") or {}
    url = mcp_cfg.get("url")
    if not url:
        print("direct_request_worker: kakeratta MCP url not configured", file=sys.stderr)
        return 1
    headers = dict(mcp_cfg.get("headers") or {})
    headers["Content-Type"] = "application/json"
    headers["Accept"] = "application/json, text/event-stream"
    cfg = model_config(cfg_dict)
    try:
        result = mcp_call(url, headers, "list_asks")
        requests = result.get("requests") or []
        asks = result.get("asks") or []
    except Exception as e:
        print(f"direct_request_worker: list_asks failed: {redact(e, secret_values(cfg, headers))}", file=sys.stderr)
        return 1
    open_requests = [r for r in requests if isinstance(r, dict) and r.get("status") in ("pending", "claimed")]
    if open_requests:
        process(url, headers, open_requests, cfg=cfg, home=home)
    open_asks = [a for a in asks if isinstance(a, dict) and a.get("status") in ("pending", "claimed")]
    if open_asks:
        process_asks(url, headers, open_asks, home=home)
    return 0


if __name__ == "__main__":
    if "--fs-mcp" in sys.argv:
        sys.exit(fs_mcp_main(sys.argv[1:]))
    if "--judge-requests" in sys.argv:
        sys.exit(judge_requests_main())
    if "--persona-asks" in sys.argv:
        sys.exit(persona_asks_main())
    sys.exit(main())
