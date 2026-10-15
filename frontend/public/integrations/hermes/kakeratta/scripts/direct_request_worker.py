#!/usr/bin/env python3
"""Direct-request worker for the kakeratta Hermes integration.

The Wake Gate (check_kakeratta_gate.py) hands open direct requests
(`list_asks.requests` with `status` `pending`, or `claimed` by an earlier run
of this worker) to it instead of waking the Nemotron Super parent. The worker
claims and replies in code and runs Ultra as an ordinary Hermes agent for the
work in between, so the parent model is never involved in a direct request.

Flow, one request at a time, oldest first:

    claim_request (code)
      -> one Ultra `hermes chat` session with the worker's tools does the work
      -> reply_request (code) with that session's final response

Safety:

* The run is a fresh single-query `hermes chat` session with the worker's
  own Hermes home (kakeratta/hermes-home), never a direct provider request.
  By default that home loads only kakeratta's read-only MCP tools, this
  script's own read-only file server (--fs-mcp) and the `web` toolset. More
  tools are added the usual Hermes way: toolsets in `kakeratta_worker.toolsets`
  and parent MCP servers named in `kakeratta_worker.mcp_servers`. Known secret
  values are masked in file reads, the reply and the audit log.
* Every event is appended as one JSON line per request ID under Hermes home
  and flushed at each stage, so progress survives a crash. Secrets are never
  recorded.
* One worker at a time: a non-blocking lock under Hermes home makes an
  overlapping tick skip instead of processing the same request twice.
* Fail-closed: any error before a claim leaves the request pending for the
  next tick. After a claim the worker records its stage, so a later tick
  resumes the claimed request with the same claimId and resends a saved
  reply. A run that failed or was interrupted is retried (at most MAX_RESUMES
  times) only when the worker's tools are read-only; once action tools are
  configured it may already have acted, so it is reported, never repeated.
  There is no fallback to the parent model.
* Cost boundary: the run may take at most MAX_TURNS tool-calling iterations.
  Afterwards the worker reads the session's token counts from the worker
  home's Hermes state.db and audits them. Before and after the run it checks
  the request's totals (kept across resumes) against MAX_API_CALLS and
  MAX_REQUEST_TOKENS; past either limit, or when a run's usage cannot be
  read, it stops and replies with the reason.

The worker's Hermes config reuses the existing Provider v2 setup: the model,
provider, endpoint and `${VAR}` API-key reference come from Hermes's
config.yaml (`kakeratta_worker`, then `delegation`; never the parent's
`model`), and the key stays an unexpanded reference that Hermes resolves.
"""

import fcntl
import copy
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path
import urllib.request
import urllib.error

WORKER_VERSION = "5"

# --- defaults -------------------------------------------------------------
DEFAULT_MODEL = "nvidia/Nemotron-3-Ultra-550b-a55b"
DEFAULT_REASONING = "high"
MAX_RESUMES = 2              # later ticks that may resume an interrupted claimed request
MAX_TURNS = 15               # tool-calling iterations in the run
MAX_API_CALLS = 40           # model calls per request, across runs and resumes
MAX_REQUEST_TOKENS = 1_500_000  # input + cache read/write + output tokens per request
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

# The only kakeratta MCP tools the worker's Hermes home loads. Writes
# (claim_request, reply_request, comment_on_entry, submit_answer, ...) are not
# loaded at all, so a model cannot call them.
READONLY_MCP_TOOLS = ["get_timeline", "get_thread", "search_timeline", "list_schedules",
                      "list_personas", "list_notes", "read_note"]

# --- config and environment ----------------------------------------------

def get_hermes_home() -> Path:
    env_home = os.environ.get("HERMES_HOME")
    if env_home:
        return Path(env_home).expanduser()
    return Path.home() / ".hermes"


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
    return {
        "model": section.get("model") or DEFAULT_MODEL,
        "provider": section.get("provider") or "custom",
        "base_url": section.get("base_url"),
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
    worker_servers = {"kakeratta": mcp, "files": files}
    for name in cfg["mcp_servers"]:
        if isinstance(servers.get(name), dict):
            worker_servers[name] = servers[name]
    worker_cfg = {"model": model, "agent": {"max_turns": cfg["max_turns"]}, "mcp_servers": worker_servers}
    if isinstance(main_cfg.get("providers"), dict):
        worker_cfg["providers"] = main_cfg["providers"]
    _atomic_write(whome / "config.yaml", yaml.safe_dump(worker_cfg, allow_unicode=True), mode=0o600)

    # Credentials Hermes loads from .env stay in the parent's file.
    env_file, link = parent / ".env", whome / ".env"
    if env_file.exists() and not link.is_symlink():
        link.symlink_to(env_file)
    return whome


# --- MCP -----------------------------------------------------------------

def mcp_call(url, headers, name, arguments=None, timeout=MCP_TIMEOUT):
    """Call one kakeratta MCP tool with arguments and return structured data."""
    payload = json.dumps({
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": {"name": name, "arguments": arguments or {}},
    }).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=timeout) as response:
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


def session_usage(worker_home: Path, session):
    """The usage Hermes recorded for one finished session, or None when the
    session, the database or its counters cannot be read."""
    path = Path(worker_home) / "state.db"
    if not session or not path.exists():
        return None
    import sqlite3
    try:
        con = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=10)
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

    def check(self):
        t, cfg = self.totals, self.cfg
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


def hermes_run(cfg, prompt, toolsets, max_turns, stage):
    """One fresh, single-query Hermes CLI session with the worker's home.

    Returns (final response text, session id). A failed run raises. The run's
    usage is recorded on the request's meter whatever the outcome, and a run
    that leaves the request past its limits raises BudgetExceeded, discarding
    its result.
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
        proc = subprocess.run(cmd, input=prompt, env=env, cwd=str(cfg["worker_home"]),
                              capture_output=True, text=True, timeout=cfg["run_budget"] + 120)
    except subprocess.TimeoutExpired:
        meter.record(stage, None, None)
        raise RuntimeError(f"hermes chat exceeded {cfg['run_budget'] + 120} seconds")
    m = re.search(r"session_id:\s*(\S+)", proc.stderr + "\n" + proc.stdout)
    session = m.group(1) if m else None
    meter.record(stage, session, session_usage(cfg["worker_home"], session))
    if proc.returncode != 0:
        raise RuntimeError(f"hermes chat exited with {proc.returncode}: {proc.stderr.strip()[-300:]}")
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
# What Hermes answers when the run ends at its turn limit.
_TURN_LIMIT = re.compile(r"(?i)reached the (maximum number of tool-calling iterations|iteration limit)")


def clean_response(text):
    """The run's final response without the notices Hermes prints first."""
    lines = (text or "").strip().splitlines()
    while lines and _NOTICE.match(lines[0]):
        lines.pop(0)
    return "\n".join(lines).strip()


def side_effects_possible(cfg):
    """Whether the worker's tools go beyond reading."""
    toolsets = {t.strip() for t in cfg["toolsets"].split(",") if t.strip()}
    return bool(cfg["mcp_servers"]) or not toolsets <= READ_ONLY_TOOLSETS


def run_agent(cfg, context, request_id, home):
    """Run Ultra once as a Hermes agent; returns the reply text."""
    text, session = hermes_run(cfg, _agent_prompt(context), cfg["toolsets"], cfg["max_turns"], "agent")
    reply = clean_response(text)
    audit(home, request_id, "agent_run", {"session": session, "reply_chars": len(reply)})
    if not reply or (_TURN_LIMIT.search(reply) and len(reply) < 300):
        audit(home, request_id, "agent_unfinished", {"session": session,
                                                     "response_tail": redact(text[-RESPONSE_TAIL:], cfg["secrets"])})
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
    audit(home, request_id, "resumed" if resuming else "claimed", {
        "claim_id": claim_id, "model": cfg["model"], "provider": "hermes-cli/" + cfg["provider"],
        "reasoning": cfg["reasoning"] or None, "resume": resumes,
    })

    # A run that started with action tools may already have acted: report
    # it instead of running again.
    if state.get("ran") and side_effects_possible(cfg):
        audit(home, request_id, "rerun_skipped", {"reason": "an interrupted run may have acted"})
        _send_reply(cfg, url, headers, request_id, claim_id,
                    "前回の処理が途中で中断されました。操作が実行済みの可能性があるため、再実行していません。"
                    "必要であれば、結果を確認してからもう一度依頼してください。", home)
        return

    if resumes > MAX_RESUMES:
        audit(home, request_id, "resume_limit", {"resume": resumes})
        _send_reply(cfg, url, headers, request_id, claim_id,
                    "処理が繰り返し中断されたため、依頼を完了できませんでした。", home)
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
        _send_reply(cfg, url, headers, request_id, claim_id,
                    "1件の依頼で使えるモデル呼び出しの上限"
                    f"（{cfg['max_api_calls']}回・{cfg['max_request_tokens']:,}トークン）に達したか、"
                    "使用量を確認できなかったため、処理を止めました。\n\n"
                    f"理由: {e}", home)
    except Exception as e:
        # A Hermes, provider or network failure. With read-only tools the
        # request stays claimed and a later tick runs it again, until the
        # resume limit; with action tools it may have acted, so report it.
        error = redact(e, secrets)
        audit(home, request_id, "failed", {"error": error, "resume": resumes})
        if resumes >= MAX_RESUMES or side_effects_possible(cfg):
            _send_reply(cfg, url, headers, request_id, claim_id,
                        "依頼を処理できませんでした。\n\n" + error, home)
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
    safe = re.sub(r"[^A-Za-z0-9_.-]", "_", workspace_id)
    return parent / "kakeratta-judge-workspaces" / safe


def spawn_persona_asks(url, headers, workspace_id, home=None):
    """Start process_persona_asks for one judge workspace in a detached
    process and return at once, so the Wake Gate is never held by a Hermes
    run. The MCP credential goes over stdin, never on the command line."""
    parent = home or get_hermes_home()
    root = _judge_root(parent, workspace_id)
    root.mkdir(parents=True, exist_ok=True)
    log = open(root / "persona.log", "a", encoding="utf-8")
    try:
        proc = subprocess.Popen(
            [sys.executable, str(Path(__file__).resolve()), "--persona-asks"],
            stdin=subprocess.PIPE, stdout=log, stderr=log, text=True,
            env=dict(os.environ, HERMES_HOME=str(parent)), start_new_session=True)
        proc.stdin.write(json.dumps({"url": url, "headers": headers, "workspaceId": workspace_id}))
        proc.stdin.close()
    finally:
        log.close()


def persona_asks_main():
    """Entry for --persona-asks: the arguments arrive as JSON on stdin."""
    args = json.loads(sys.stdin.read())
    try:
        process_persona_asks(args["url"], args["headers"], args["workspaceId"])
    except Exception as e:
        secrets = secret_values(model_config(read_config(get_hermes_home())), args.get("headers"))
        print(f"kakeratta persona worker failed for {args.get('workspaceId')}: {redact(e, secrets)}", file=sys.stderr)
        return 1
    return 0


def process_persona_asks(url, headers, workspace_id, home=None):
    """Run the normal kakeratta skill against one discovered judge workspace.

    A fresh workspace-scoped Hermes home carries a generated config whose
    kakeratta MCP server points only at ``url``. The parent config is never
    edited and Hermes does not need a restart. This is invoked only after the
    gate has proved that this workspace has a pending persona ask. Only one
    run per workspace at a time: an overlapping tick returns at once.
    """
    parent = home or get_hermes_home()
    main_cfg = read_config(parent)
    if not isinstance(main_cfg, dict):
        raise RuntimeError("config.yaml not found")
    root = _judge_root(parent, workspace_id)
    root.mkdir(parents=True, exist_ok=True)
    lock = open(root / "persona.lock", "w")
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        lock.close()
        return
    try:
        _run_persona_asks(parent, main_cfg, root, url, headers)
    finally:
        lock.close()


def _run_persona_asks(parent, main_cfg, root, url, headers):
    run_home = root / "persona-hermes-home"
    run_home.mkdir(parents=True, exist_ok=True)
    os.chmod(run_home, 0o700)

    cfg = copy.deepcopy(main_cfg)
    servers = cfg.setdefault("mcp_servers", {})
    server = dict(servers.get("kakeratta") or {})
    server["url"] = url
    server["headers"] = {k: v for k, v in headers.items()
                         if k.lower() not in ("content-type", "accept")}
    server.pop("tools", None)  # persona asks need claim/read/submit as well as reads
    servers["kakeratta"] = server
    _atomic_write(run_home / "config.yaml", __import__("yaml").safe_dump(cfg, allow_unicode=True), mode=0o600)
    for name in (".env", "skills"):
        source, link = parent / name, run_home / name
        if source.exists() and not link.exists():
            link.symlink_to(source, target_is_directory=source.is_dir())

    skill_path = parent / "skills" / "kakeratta" / "SKILL.md"
    if not skill_path.exists():
        raise RuntimeError("kakeratta skill is not installed")
    prompt = (skill_path.read_text(encoding="utf-8") +
              "\n\nThe Wake Gate has confirmed pending persona asks in this MCP workspace. "
              "Follow the skill now for every pending ask. End only after each answer is accepted.")
    model = model_config(main_cfg)
    cmd = [model["hermes_bin"], "chat", "-Q", "--ignore-rules", "--max-turns", "15",
           "--run-budget", str(model["run_budget"]), "--query-file", "-"]
    proc = subprocess.run(cmd, input=prompt, env=dict(os.environ, HERMES_HOME=str(run_home)),
                          cwd=str(run_home), capture_output=True, text=True,
                          timeout=model["run_budget"] + 120)
    if proc.returncode != 0:
        raise RuntimeError(f"persona Hermes exited with {proc.returncode}: {proc.stderr.strip()[-300:]}")


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
    except Exception as e:
        print(f"direct_request_worker: list_asks failed: {redact(e, secret_values(cfg, headers))}", file=sys.stderr)
        return 1
    open_requests = [r for r in requests if isinstance(r, dict) and r.get("status") in ("pending", "claimed")]
    if not open_requests:
        return 0
    process(url, headers, open_requests, cfg=cfg, home=home)
    return 0


if __name__ == "__main__":
    if "--fs-mcp" in sys.argv:
        sys.exit(fs_mcp_main(sys.argv[1:]))
    if "--persona-asks" in sys.argv:
        sys.exit(persona_asks_main())
    sys.exit(main())
