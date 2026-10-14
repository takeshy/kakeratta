#!/usr/bin/env python3
"""Direct-request worker for the kakeratta Hermes integration.

The Wake Gate (check_kakeratta_gate.py) hands open direct requests
(`list_asks.requests` with `status` `pending`, or `claimed` by an earlier run
of this worker) to it instead of waking the Nemotron Super parent. The worker claims, generates, reviews and
delivers each request entirely in code, so the parent model is never involved
in a direct request and its natural-language role separation cannot leak.

Flow, one request at a time, oldest first:

    claim_request (code)
      -> Ultra generation (read-only tools only)
      -> a separate Ultra request reviews the candidate
      -> on rejection, a fresh Ultra correction, reviewed again (max 2 corrections)
      -> deliverable request: reply_request with the accepted artifact unchanged
      -> operation request: run the accepted Python exactly once, then
         reply_request with what was done and its result

Safety:

* Every model call is a fresh single-query `hermes chat` session run with
  the worker's own Hermes home (kakeratta/hermes-home), never a direct
  provider request. That home loads only kakeratta's read-only MCP tools and
  this script's own read-only file server (--fs-mcp), and each run enables
  only the `web` toolset besides them, so there is no write, terminal,
  code-execution or side-effect tool: the read-only boundary is enforced by
  Hermes's tool configuration and this code, not by a prompt. Known secret
  values are masked in file reads, the reply and the audit log.
* A deliverable may come with a program for a side effect on it (saving it,
  for example). The program runs under the same rules as an operation and
  reads the accepted deliverable from $KAKERATTA_DELIVERABLE; the reply is
  the deliverable itself.
* Review runs as a separate model request that never shares the generation
  session. It returns machine-readable JSON; only `accepted: true` with a
  matching candidate SHA-256 passes.
* An operation's Python runs only after review acceptance and a byte-for-byte
  hash match, exactly once (an executed record keyed by request ID guards
  retries), with a code-enforced timeout, output caps, working directory, and
  an environment-variable allowlist that drops every secret.
* Every event is appended as one JSON line per request ID under Hermes home
  and flushed at each stage, so progress survives a crash. Secrets are never
  recorded.
* One worker at a time: a non-blocking lock under Hermes home makes an
  overlapping tick skip instead of processing the same request twice.
* Fail-closed: any error before a claim leaves the request pending for the
  next tick. After a claim the worker records its stage, so a later tick
  resumes the claimed request with the same claimId (at most MAX_RESUMES
  times), resends a saved reply, and never repeats an executed operation.
  There is no fallback to the parent model.
* Cost boundary: generation and correction runs may take at most
  GENERATION_MAX_TURNS tool-calling iterations, a review REVIEW_MAX_TURNS.
  After every run the worker reads the session's token counts from the
  worker home's Hermes state.db and audits them. Before and after each run it
  checks the request's totals (kept across resumes) against MAX_API_CALLS and
  MAX_REQUEST_TOKENS; past either limit, or when a run's usage cannot be read,
  it stops without executing anything and replies with the reason.

The worker's Hermes config reuses the existing Provider v2 setup: the model,
provider, endpoint and `${VAR}` API-key reference come from Hermes's
config.yaml (`kakeratta_worker`, then `delegation`; never the parent's
`model`), and the key stays an unexpanded reference that Hermes resolves.
"""

import fcntl
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

WORKER_VERSION = "3"

# --- defaults -------------------------------------------------------------
DEFAULT_MODEL = "nvidia/Nemotron-3-Ultra-550b-a55b"
DEFAULT_REASONING = "high"
MAX_CORRECTIONS = 2          # correction attempts after the initial generation
MAX_RESUMES = 2              # later ticks that may resume an interrupted claimed request
GENERATION_MAX_TURNS = 10    # tool-calling iterations per generation/correction run
REVIEW_MAX_TURNS = 4         # tool-calling iterations per review run
MAX_API_CALLS = 40           # model calls per request, across runs and resumes
MAX_REQUEST_TOKENS = 1_500_000  # input + cache read/write + output tokens per request
MAX_REPLY = 20000            # reply_request rejects longer text
DEFAULT_MAX_TOKENS = 64000   # output cap per model call, reasoning included
RUN_BUDGET = 1200            # wall-clock budget per Hermes run, seconds
MCP_TIMEOUT = 300
MAX_STREAM = 10000           # cap for retained stdout/stderr
EXEC_TIMEOUT = 60            # operation python timeout, seconds
AUDIT_MAX_BYTES = 1_000_000  # rotate a request's audit file past this
RESPONSE_TAIL = 300          # end of an unusable response kept in the audit log

# The only kakeratta MCP tools the worker's Hermes home loads. Writes
# (claim_request, reply_request, comment_on_entry, submit_answer, ...) are not
# loaded at all, so a model cannot call them.
READONLY_MCP_TOOLS = ["get_timeline", "get_thread", "search_timeline", "list_schedules",
                      "list_personas", "list_notes", "read_note"]
# Web search/extract, the read-only kakeratta tools and the worker's own
# read-only file server; review uses the same reads to check facts.
GENERATION_TOOLSETS = "web,kakeratta,files"
REVIEW_TOOLSETS = "web,files"

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
        "exec_timeout": int(section.get("exec_timeout") or EXEC_TIMEOUT),
        "generation_max_turns": int(section.get("generation_max_turns") or GENERATION_MAX_TURNS),
        "review_max_turns": int(section.get("review_max_turns") or REVIEW_MAX_TURNS),
        "max_api_calls": int(section.get("max_api_calls") or MAX_API_CALLS),
        "max_request_tokens": int(section.get("max_request_tokens") or MAX_REQUEST_TOKENS),
        "file_roots": [str(Path(r).expanduser()) for r in (section.get("file_roots") or [Path.home()])],
        "hermes_bin": section.get("hermes_bin") or shutil.which("hermes") or "/usr/local/bin/hermes",
    }


def prepare_worker_home(home: Path, main_cfg, cfg, url, headers) -> Path:
    """Write the worker's own Hermes home and return it.

    Model calls go through the Hermes CLI with this home, never straight to
    the provider. Its config.yaml carries Ultra with the output cap, the
    parent's provider settings (credentials stay `${VAR}` references), and
    the kakeratta MCP server restricted to READONLY_MCP_TOOLS, and this
    script's read-only file server (`files`) over the readable roots. The parent's
    skills, memory, cron jobs and sessions are not shared.
    """
    import yaml
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
    mcp = dict(servers.get("kakeratta") or {
        "url": url,
        "headers": {k: v for k, v in (headers or {}).items() if k.lower() not in ("content-type", "accept")},
    })
    mcp["tools"] = {"include": list(READONLY_MCP_TOOLS), "prompts": False, "resources": False}

    files = {
        "command": sys.executable,
        "args": [str(Path(__file__).resolve()), "--fs-mcp", "--home", str(home)]
                + [a for root in cfg["file_roots"] for a in ("--root", root)],
        "tools": {"prompts": False, "resources": False},
    }
    max_turns = max(cfg["generation_max_turns"], cfg["review_max_turns"])
    worker_cfg = {"model": model, "agent": {"max_turns": max_turns},
                  "mcp_servers": {"kakeratta": mcp, "files": files}}
    if isinstance(main_cfg.get("providers"), dict):
        worker_cfg["providers"] = main_cfg["providers"]
    _atomic_write(whome / "config.yaml", yaml.safe_dump(worker_cfg, allow_unicode=True), mode=0o600)

    # Credentials Hermes loads from .env stay in the parent's file.
    env_file, link = home / ".env", whome / ".env"
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


def _executed_path(home: Path, request_id: str) -> Path:
    return _work_root(home) / "state" / "executed" / f"{request_id}.json"


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


def load_executed(home: Path, request_id: str):
    try:
        raw = _executed_path(home, request_id).read_text(encoding="utf-8")
        return json.loads(raw)
    except Exception:
        return None


def save_executed(home: Path, request_id: str, record) -> None:
    _atomic_write(_executed_path(home, request_id), json.dumps(record, ensure_ascii=False))


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

class InvalidOutput(ValueError):
    """The model answered, but not with a usable result: unparsable or
    truncated JSON, or a wrong envelope. It counts as a rejection and goes to
    correction, unlike a Hermes, provider or network failure."""


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

    Returns (final response text, session id). Each call is its own session,
    so generation and review never share one. A failed run raises. The run's
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


GENERATION_SYSTEM = """You answer a request a user addressed directly to an external agent. You may use only the read-only tools this session provides: web search and web extract; local file reads (read_file, list_directory); and kakeratta lookups (get_timeline, get_thread, search_timeline, list_schedules, list_personas, list_notes, read_note). Find tools you do not see with tool_search and call them with tool_call. You cannot write files, run commands, run code, send anything, post, or change any state; when the user asks for that, it goes into a program as described below, and a request to use a particular tool means the matching read here.

Return ONLY a JSON object (no Markdown, no commentary) of exactly this shape:

{"kind": "deliverable" | "operation",
 "deliverable": "<complete content, required when kind=deliverable>",
 "program": "<complete Python source, required when kind=operation; optional with a deliverable>",
 "result_format": "<how to report the result, required when kind=operation>",
 "include_program": <true|false>}

Rules:
- kind=deliverable for content the user wants to receive (HTML, an article, an answer, a report, code they asked to see). Put the complete, final content in "deliverable" - never a summary or a fragment.
- kind=operation only for an action with side effects the user clearly asked for (for example sending an email, or changing files). Put a complete, self-contained, runnable Python program in "program" that does exactly what was asked and nothing more, and "result_format" describing what the program did and how to report it. Do not run the program yourself.
- When the user wants a deliverable and also asks for a side effect on it (for example saving it to a path), use kind=deliverable and add "program" and "result_format" for that side effect. The program runs once after review; it must read the deliverable, byte for byte, from the file named by the environment variable KAKERATTA_DELIVERABLE instead of embedding another copy. The reply is still the deliverable itself.
- Set include_program true only when the user explicitly asked to see the code.
- Satisfy every constraint the user stated (format, casing, exact names, lengths, viewport, overflow, readability, character budgets). Facts and names must be correct. Write the deliverable in the user's language.
"""


# Repeated after the request, which can be long: models follow the last
# instructions they read.
GENERATION_REMINDER = """

Before you answer: you never perform a side effect yourself and never need a tool for one. Anything the user wants done (saving or writing a file, sending, posting) goes into "program", which runs after review. Do not inspect or prepare the destination. A program for a deliverable reads it with open(os.environ["KAKERATTA_DELIVERABLE"], encoding="utf-8").read() and never pastes the deliverable into its source. Your final message must be only the JSON object described above."""


def _generation_prompt(context, problems, previous):
    text = GENERATION_SYSTEM + "\n\n" + _context_text(context)
    if problems:
        if previous:
            text += "\n\nYour previous candidate, in full:\n" + previous
        text += ("\n\nIt was rejected with these problems:\n"
                 + "\n".join("- " + p for p in problems)
                 + "\nCorrect exactly these issues and return a new complete candidate.")
    return text + GENERATION_REMINDER


REVIEW_SYSTEM = """You independently review a candidate produced for a user's direct request. You did not create it. Judge it against the original request and every constraint. You may use web search and local file reads to check facts; you cannot change anything. A deliverable may come with a program section: a program that runs once after acceptance, reading the deliverable from the file named by KAKERATTA_DELIVERABLE; review both.

Check:
1. It satisfies the request and every constraint.
2. Facts, names, casing, lengths and formats are correct.
2a. It contains no API key, token, password or other secret.
3. HTML: viewport, overflow, overlap and readability are correct.
4. Python (operation): it performs exactly the requested operation and no extra side effects; it does not read or exfiltrate secrets, destroy arbitrary paths, escalate privileges, start a persistent process, or use unbounded network.
5. If the operation is not idempotent, it is designed not to duplicate side effects on retry.

Return ONLY a JSON object of exactly this shape:

{"accepted": <true|false>, "problems": ["<specific problem>", ...], "candidate_sha256": "<the value given below, copied verbatim>"}

- accepted must be false with at least one specific problem when anything is wrong.
- candidate_sha256 must be exactly the value provided; copy it verbatim.
"""


def _review_prompt(context, candidate, sha):
    return (REVIEW_SYSTEM + "\n\nOriginal request context:\n" + _context_text(context)
            + "\n\nCandidate under review:\n" + candidate
            + "\n\ncandidate_sha256 (copy exactly): " + sha)


# --- JSON envelope helpers ----------------------------------------------

def _answer(text):
    """The JSON object in a run's final response, or InvalidOutput.

    Hermes may print notices before the response; the object is taken from
    the first `{` to the last `}`. A response cut off at the output limit is
    not valid JSON and so is rejected here."""
    cleaned = (text or "").strip()
    if not cleaned:
        raise InvalidOutput("empty model response")
    cleaned = re.sub(r"^```(?:json)?\s*|\s*```$", "", cleaned)
    try:
        value = json.loads(cleaned)
    except json.JSONDecodeError:
        m = re.search(r"\{.*\}", cleaned, re.DOTALL)
        if not m:
            raise InvalidOutput("the response was not JSON (it may have been cut off)")
        try:
            value = json.loads(m.group(0))
        except json.JSONDecodeError as e:
            raise InvalidOutput(f"the response was not valid JSON (it may have been cut off): {e}")
    if not isinstance(value, dict):
        raise InvalidOutput("the response was not a JSON object")
    return value


def candidate_of(envelope):
    """Return (candidate_text, kind) for hashing and review."""
    kind = envelope.get("kind")
    if kind == "deliverable":
        text = envelope.get("deliverable")
        if not isinstance(text, str) or not text.strip():
            raise InvalidOutput("deliverable is empty")
        if len(text.strip()) > MAX_REPLY:
            raise InvalidOutput(f"deliverable has {len(text.strip())} characters; the reply limit is {MAX_REPLY}")
        program = envelope.get("program")
        if program is None or program == "":
            return text, "deliverable"
        if not isinstance(program, str) or not program.strip():
            raise InvalidOutput("program is empty")
        # Review and the accepted hash cover the deliverable and its program.
        return (text + "\n\n----- program (runs once after acceptance; reads the deliverable "
                "from $KAKERATTA_DELIVERABLE) -----\n" + program), "deliverable+operation"
    if kind == "operation":
        program = envelope.get("program")
        if not isinstance(program, str) or not program.strip():
            raise InvalidOutput("program is empty")
        return program, "operation"
    raise InvalidOutput("kind must be deliverable or operation")


def deliverable_reply(record):
    """The reply for a deliverable whose program ran (or was meant to): the
    deliverable itself, unchanged, when the program succeeded; otherwise the
    operation's result first, so truncation cannot cut it off."""
    if record.get("state") == "done" and record.get("exit_code") == 0:
        return record["deliverable"]
    return _operation_reply(record) + "\n\n" + record["deliverable"]


def sha256_of(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


# --- generation and review ----------------------------------------------

def generate(cfg, context, problems, previous, stage="generation"):
    """A fresh generation (or correction) session; returns (envelope, session).

    A Hermes failure raises as it is; a response the worker cannot use raises
    InvalidOutput."""
    text, session = hermes_run(cfg, _generation_prompt(context, problems, previous), GENERATION_TOOLSETS,
                               cfg["generation_max_turns"], stage)
    try:
        return _answer(text), session
    except InvalidOutput as e:
        e.session, e.tail = session, text[-RESPONSE_TAIL:]
        raise


def review(cfg, context, envelope, sha):
    """A separate review session; an unusable verdict is a rejection."""
    candidate, _ = candidate_of(envelope)
    text, session = hermes_run(cfg, _review_prompt(context, candidate, sha), REVIEW_TOOLSETS,
                               cfg["review_max_turns"], "review")
    try:
        verdict = _answer(text)
    except InvalidOutput as e:
        return {"accepted": False, "problems": [f"the reviewer's verdict was unusable: {e}"],
                "echoed_sha": None, "session": session, "tail": text[-RESPONSE_TAIL:]}
    problems = verdict.get("problems")
    if not isinstance(problems, list):
        problems = []
    return {
        "accepted": verdict.get("accepted") is True and verdict.get("candidate_sha256") == sha,
        "problems": [str(p) for p in problems],
        "echoed_sha": verdict.get("candidate_sha256"),
        "session": session,
    }


# --- operation execution -------------------------------------------------

_EXEC_ENV_ALLOWLIST = ("PATH", "LANG", "LC_ALL", "TZ", "HOME", "TMPDIR", "PYTHONUNBUFFERED")
AUDIT_STREAM = 2000          # stdout/stderr kept in an audit line


def run_program(cfg, home, request_id, program, accepted_sha, envelope, deliverable=None):
    """Run the accepted Python exactly once, guarded by an executed record.

    The program is written to disk first and the bytes there must hash to the
    accepted SHA-256, or nothing runs. The executed record is saved before the
    process starts, so a crash mid-run is reported later, never run again.
    """
    workdir = _work_root(home) / "run" / request_id
    workdir.mkdir(parents=True, exist_ok=True)
    script = workdir / "operation.py"
    script.write_text(program, encoding="utf-8")
    on_disk = hashlib.sha256(script.read_bytes()).hexdigest()
    if on_disk != accepted_sha:
        audit(home, request_id, "execution_hash_mismatch",
              {"code_sha256": on_disk, "accepted_sha256": accepted_sha})
        return {"state": "not_run", "code_sha256": on_disk,
                **({"deliverable": deliverable} if deliverable is not None else {})}

    # What the reply needs, kept with the record so a resumed run can report
    # an execution without regenerating anything.
    report = {"result_format": envelope.get("result_format") or "",
              "include_program": envelope.get("include_program") is True}
    if report["include_program"]:
        report["program"] = program
    if deliverable is not None:
        report["deliverable"] = deliverable
        (workdir / "deliverable.txt").write_text(deliverable, encoding="utf-8")
    save_executed(home, request_id, dict(report, state="running", code_sha256=accepted_sha, at=time.time()))
    audit(home, request_id, "execution_start", {"code_sha256": accepted_sha})

    env = {k: v for k, v in os.environ.items() if k in _EXEC_ENV_ALLOWLIST}
    env["PYTHONUNBUFFERED"] = "1"
    if deliverable is not None:
        env["KAKERATTA_DELIVERABLE"] = str(workdir / "deliverable.txt")
    start = time.time()
    try:
        proc = subprocess.run(
            [sys.executable, str(script)],
            cwd=str(workdir), env=env,
            capture_output=True, timeout=cfg.get("exec_timeout", EXEC_TIMEOUT),
        )
        record = dict(report, **{
            "state": "done", "code_sha256": accepted_sha, "at": time.time(),
            "exit_code": proc.returncode,
            "stdout": redact(proc.stdout.decode("utf-8", errors="replace")[:MAX_STREAM], cfg["secrets"]),
            "stderr": redact(proc.stderr.decode("utf-8", errors="replace")[:MAX_STREAM], cfg["secrets"]),
            "duration": round(time.time() - start, 3),
        })
    except subprocess.TimeoutExpired:
        record = dict(report, **{
            "state": "done", "code_sha256": accepted_sha, "at": time.time(),
            "exit_code": None, "stdout": "", "stderr": "timeout",
            "duration": round(time.time() - start, 3), "timeout": True,
        })
    save_executed(home, request_id, record)
    audit(home, request_id, "execution_end", {
        "code_sha256": accepted_sha, "exit_code": record.get("exit_code"),
        "duration": record.get("duration"), "timeout": bool(record.get("timeout")),
        "stdout": record["stdout"][:AUDIT_STREAM], "stderr": record["stderr"][:AUDIT_STREAM],
    })
    return record


# --- reply composition ---------------------------------------------------

def _operation_reply(record):
    """What was done and its result, from an executed record."""
    if record.get("state") == "not_run":
        return "承認されたPythonと実行直前の内容が一致しなかったため、実行しませんでした。"
    lines = [record.get("result_format") or "実行結果", ""]
    if record.get("state") != "done":
        lines.append("前回の実行が途中で中断されました。操作は実行済みの可能性があるため、再実行していません。結果は確認できていません。")
    else:
        lines.append("成功: " + ("はい" if record.get("exit_code") == 0 else "いいえ"))
        if record.get("exit_code") is not None:
            lines.append("終了コード: " + str(record["exit_code"]))
        if record.get("duration") is not None:
            lines.append("所要時間: " + str(record["duration"]) + "秒")
        if record.get("timeout"):
            lines.append("タイムアウトしました")
        out = (record.get("stdout") or "").strip()
        err = (record.get("stderr") or "").strip()
        if out:
            lines.append("\n出力:\n```\n" + out + "\n```")
        if err:
            lines.append("\nエラー:\n```\n" + err + "\n```")
    if record.get("include_program") and record.get("program"):
        lines.append("\n実行したPython:\n```python\n" + record["program"] + "\n```")
    return "\n".join(lines)


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

    # An operation that already ran, or started, is reported, never repeated.
    executed = load_executed(home, request_id)
    if executed is not None:
        audit(home, request_id, "execution_skipped", {
            "reason": "already executed" if executed.get("state") == "done" else "interrupted",
            "code_sha256": executed.get("code_sha256"),
        })
        text = deliverable_reply(executed) if "deliverable" in executed else _operation_reply(executed)
        _send_reply(cfg, url, headers, request_id, claim_id, text, home)
        return

    if resumes > MAX_RESUMES:
        audit(home, request_id, "resume_limit", {"resume": resumes})
        _send_reply(cfg, url, headers, request_id, claim_id,
                    "処理が繰り返し中断されたため、依頼を完了できませんでした。", home)
        return

    cfg["meter"] = Meter(cfg, home, request_id)
    try:
        _run_generate_review_loop(cfg, url, headers, context, request_id, home, claim_id)
    except BudgetExceeded as e:
        # Fail-closed: no further model call, nothing executed, and no resume.
        t = cfg["meter"].totals
        audit(home, request_id, "budget_exceeded", {
            "reason": str(e), "request_api_calls": t["api_call_count"], "request_tokens": t["tokens"],
        })
        _send_reply(cfg, url, headers, request_id, claim_id,
                    "1件の依頼で使えるモデル呼び出しの上限"
                    f"（{cfg['max_api_calls']}回・{cfg['max_request_tokens']:,}トークン）に達したか、"
                    "使用量を確認できなかったため、処理を止めました。操作は実行していません。\n\n"
                    f"理由: {e}", home)
    except Exception as e:
        # A provider, network or tool failure: the request stays claimed and a
        # later tick resumes it, until the resume limit; then report it.
        error = redact(e, secrets)
        audit(home, request_id, "failed", {"error": error, "resume": resumes})
        if resumes >= MAX_RESUMES:
            _send_reply(cfg, url, headers, request_id, claim_id,
                        "依頼を処理できませんでした。\n\n" + error, home)


def _run_generate_review_loop(cfg, url, headers, context, request_id, home, claim_id):
    problems = []
    previous = None
    corrections = 0
    while True:
        attempt = corrections + 1
        stage = "generation" if corrections == 0 else "correction"
        session = None
        try:
            envelope, session = generate(cfg, context, problems, previous, stage)
            candidate, kind = candidate_of(envelope)
        except InvalidOutput as e:
            # An unusable answer is a rejection, corrected like any other.
            problems = [str(e)]
            audit(home, request_id, stage + "_invalid", {
                "attempt": attempt, "session": getattr(e, "session", session),
                "error": redact(e, cfg["secrets"]),
                "response_tail": redact(getattr(e, "tail", ""), cfg["secrets"]),
            })
        else:
            previous = json.dumps(envelope, ensure_ascii=False)
            sha = sha256_of(candidate)
            audit(home, request_id, stage, {
                "attempt": attempt, "session": session, "kind": kind,
                "candidate_sha256": sha, "candidate_chars": len(candidate),
            })
            verdict = review(cfg, context, envelope, sha)
            audit(home, request_id, "review", {
                "attempt": attempt, "session": verdict.get("session"),
                "accepted": verdict["accepted"], "problems": verdict["problems"],
                **({"response_tail": redact(verdict["tail"], cfg["secrets"])} if verdict.get("tail") else {}),
            })
            if verdict["accepted"]:
                break
            problems = verdict["problems"] or ["(reviewer returned no specific problems)"]
        if corrections >= MAX_CORRECTIONS:
            reply = ("依頼を完了できませんでした。残った問題:\n"
                     + "\n".join("- " + p for p in problems))
            _send_reply(cfg, url, headers, request_id, claim_id, reply, home)
            return
        corrections += 1

    if kind == "deliverable":
        # The accepted artifact itself, unchanged.
        _send_reply(cfg, url, headers, request_id, claim_id, candidate, home)
        return
    if kind == "deliverable+operation":
        # The accepted hash covers deliverable and program together; the
        # program's own bytes are checked on disk against its accepted text.
        program = envelope["program"]
        record = run_program(cfg, home, request_id, program, sha256_of(program), envelope,
                             deliverable=envelope["deliverable"])
        _send_reply(cfg, url, headers, request_id, claim_id, deliverable_reply(record), home)
        return
    record = run_program(cfg, home, request_id, candidate, sha, envelope)
    _send_reply(cfg, url, headers, request_id, claim_id, _operation_reply(record), home)


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

def process(url, headers, requests, cfg=None, home=None):
    """Process open direct requests, oldest first: pending ones, and claimed
    ones this worker left unfinished. Returns nothing; each request is handled
    independently and errors are audited, never raised. Only one worker runs
    at a time; an overlapping tick returns at once."""
    home = home or get_hermes_home()
    main_cfg = read_config(home)
    cfg = cfg or model_config(main_cfg)
    cfg["secrets"] = secret_values(cfg, headers)
    lock = acquire_lock(home)
    if lock is None:
        print("kakeratta direct-request worker: another run is in progress; skipping", file=sys.stderr)
        return
    try:
        cfg["worker_home"] = prepare_worker_home(home, main_cfg, cfg, url, headers)
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
    sys.exit(main())
