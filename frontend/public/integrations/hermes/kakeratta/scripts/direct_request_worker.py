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

* The generation model is given only read-only tools: kakeratta's read-only
  MCP tools, local file reads under HERMES_HOME, and web search/fetch. There is
  no write, terminal, code-execution or side-effect tool, so the read-only
  boundary is enforced by the exposed tool set, not by a prompt. Secret files
  (config.yaml, .env, keys, tokens) are not readable, and known secret values
  are masked in every tool result and in the reply.
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

The Token Factory call reuses the existing Provider v2 setup: the endpoint,
model and `${VAR}` API-key reference are read from Hermes's config.yaml
(`kakeratta_worker`, then `delegation`; never the parent's `model`), and the key is resolved
from the environment at run time, never written to a file or a log.
"""

import fcntl
import hashlib
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path
import urllib.request
import urllib.error

WORKER_VERSION = "1"

# --- defaults -------------------------------------------------------------
DEFAULT_MODEL = "nvidia/Nemotron-3-Ultra-550b-a55b"
DEFAULT_BASE_URL = "https://api.tokenfactory.nebius.com/v1"
DEFAULT_REASONING = "high"
MAX_CORRECTIONS = 2          # correction attempts after the initial generation
MAX_RESUMES = 2              # later ticks that may resume an interrupted claimed request
MAX_TOOL_ROUNDS = 12         # bounded tool-calling loop
MAX_REPLY = 20000            # reply_request rejects longer text
DEFAULT_MAX_TOKENS = 64000   # output cap per model call, reasoning included
HTTP_TIMEOUT = 300           # model and MCP long-running calls
MCP_TIMEOUT = 300
WEB_TIMEOUT = 20
WEB_MAX_BYTES = 1_000_000    # cap for a fetched page
WEB_TEXT_CAP = 20000         # cap for text returned to the model
MAX_STREAM = 10000           # cap for retained stdout/stderr
EXEC_TIMEOUT = 60            # operation python timeout, seconds
AUDIT_MAX_BYTES = 1_000_000  # rotate a request's audit file past this

# Read-only kakeratta MCP tools the generation model may call, mapped to the
# tool names kakeratta exposes. Writes (claim_request, reply_request,
# comment_on_entry, submit_answer, ...) are never exposed.
READONLY_MCP_TOOLS = {
    "get_timeline": ["limit", "before"],
    "get_thread": ["entryId"],
    "search_timeline": ["query", "from", "to", "limit", "min_score"],
    "list_schedules": ["skill", "query", "from", "to", "limit", "after"],
    "list_personas": [],
    "list_notes": ["persona", "limit", "after"],
    "read_note": ["persona", "path"],
}

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

    A plain string is returned as-is; this is how the Provider v2 setup keeps
    the Token Factory key out of files and logs.
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
    """Endpoint, model and key for Token Factory, from config.yaml.

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
    base_url = section.get("base_url") or DEFAULT_BASE_URL
    model = section.get("model") or DEFAULT_MODEL
    api_key = resolve_env(section.get("api_key") or "${TOKENFACTORY_API_KEY}")
    reasoning = section.get("reasoning_effort", section.get("reasoning", DEFAULT_REASONING))
    max_tokens = int(section.get("max_tokens") or DEFAULT_MAX_TOKENS)
    return {
        "base_url": base_url.rstrip("/"),
        "model": model,
        "api_key": api_key,
        "reasoning": "" if reasoning in (None, "none", "off") else reasoning,
        "max_tokens": max_tokens,
        "exec_timeout": int(section.get("exec_timeout") or EXEC_TIMEOUT),
        "file_root": str((get_hermes_home() / (section.get("file_root") or ".")).resolve()),
    }


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


def _atomic_write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(text, encoding="utf-8")
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


# --- read-only tools the generation model may call ----------------------

def run_readonly_tool(name, arguments, ctx):
    """Execute one read-only tool for the generation model and return its text,
    with every known secret value masked."""
    return redact(_run_readonly_tool(name, arguments, ctx), ctx.get("secrets") or ())


def _run_readonly_tool(name, arguments, ctx):
    url = ctx["url"]
    headers = ctx["headers"]
    if name in READONLY_MCP_TOOLS:
        allowed = READONLY_MCP_TOOLS[name]
        filtered = {k: v for k, v in (arguments or {}).items() if k in allowed}
        try:
            result = mcp_call(url, headers, name, filtered, timeout=MCP_TIMEOUT)
            return json.dumps(result, ensure_ascii=False)
        except Exception as e:
            return json.dumps({"error": str(e)}, ensure_ascii=False)
    if name == "read_local_file":
        return read_local_file(arguments or {}, ctx)
    if name == "web_search":
        return web_search(arguments or {}, ctx)
    if name == "web_fetch":
        return web_fetch(arguments or {}, ctx)
    return json.dumps({"error": f"unknown or forbidden tool: {name}"}, ensure_ascii=False)


# Files the model may not read even under the allowed root: credentials,
# provider and MCP configuration, keys, and the worker's own records.
_DENY_PART = re.compile(
    r"(?i)^(\.env(\..*)?|.*\.env|config\.ya?ml|\.ssh|\.gnupg|\.netrc|id_(rsa|dsa|ecdsa|ed25519).*|"
    r".*\.(pem|key|p12|pfx)|.*(secret|token|credential|password|auth).*)$")


def read_local_file(args, ctx):
    path = args.get("path")
    if not isinstance(path, str) or not path:
        return json.dumps({"error": "path is required"}, ensure_ascii=False)
    root = Path(ctx.get("file_root") or get_hermes_home()).resolve()
    try:
        target = (root / path).resolve()
    except Exception:
        return json.dumps({"error": "invalid path"}, ensure_ascii=False)
    if target != root and root not in target.parents:
        return json.dumps({"error": "path escapes the allowed root"}, ensure_ascii=False)
    rel = target.relative_to(root).parts
    if any(_DENY_PART.match(part) for part in rel) or (rel and rel[0] == "kakeratta"):
        return json.dumps({"error": "this file may not be read"}, ensure_ascii=False)
    if not target.is_file():
        return json.dumps({"error": "not a file"}, ensure_ascii=False)
    try:
        data = target.read_bytes()
    except Exception as e:
        return json.dumps({"error": f"read failed: {e}"}, ensure_ascii=False)
    if len(data) > WEB_MAX_BYTES:
        return json.dumps({"error": "file too large"}, ensure_ascii=False)
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        text = data.decode("utf-8", errors="replace")
    return text[:WEB_TEXT_CAP]


def web_search(args, ctx):
    query = (args.get("query") or "").strip()
    if not query:
        return json.dumps({"error": "query is required"}, ensure_ascii=False)
    tavily = os.environ.get("TAVILY_API_KEY")
    if tavily:
        return _tavily_search(query, tavily)
    return _duckduckgo_search(query)


def _tavily_search(query, key):
    import urllib.parse
    url = "https://api.tavily.com/search"
    payload = json.dumps({
        "query": query, "max_results": 6,
        "search_depth": "basic", "include_answer": True,
    }).encode("utf-8")
    req = urllib.request.Request(
        url, data=payload,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=WEB_TIMEOUT) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    rows = []
    if data.get("answer"):
        rows.append({"title": "answer", "snippet": data["answer"], "url": ""})
    for r in data.get("results", []):
        rows.append({"title": r.get("title", ""), "url": r.get("url", ""),
                     "snippet": r.get("content", "")[:500]})
    return json.dumps(rows, ensure_ascii=False)


def _duckduckgo_search(query):
    import urllib.parse
    import html as htmlmod
    url = "https://html.duckduckgo.com/html/?q=" + urllib.parse.quote(query)
    req = urllib.request.Request(url, headers={"User-Agent": "kakeratta-direct-request-worker/1.0"})
    with urllib.request.urlopen(req, timeout=WEB_TIMEOUT) as resp:
        page = resp.read(WEB_MAX_BYTES).decode("utf-8", errors="replace")
    results = []
    for m in re.finditer(
            r'<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>', page, re.DOTALL):
        link = htmlmod.unescape(m.group(1))
        title = htmlmod.unescape(re.sub(r"<[^>]+>", "", m.group(2))).strip()
        results.append({"title": title, "url": link, "snippet": ""})
        if len(results) >= 6:
            break
    snippets = [
        htmlmod.unescape(re.sub(r"<[^>]+>", "", m.group(1))).strip()
        for m in re.finditer(r'<a[^>]+class="result__snippet"[^>]*>(.*?)</a>', page, re.DOTALL)
    ]
    for i, snippet in enumerate(snippets):
        if i < len(results):
            results[i]["snippet"] = snippet[:500]
    if not results:
        return json.dumps({"error": "no search results"}, ensure_ascii=False)
    return json.dumps(results[:6], ensure_ascii=False)


def web_fetch(args, ctx):
    url = (args.get("url") or "").strip()
    if not re.match(r"^https?://", url):
        return json.dumps({"error": "only http(s) URLs are allowed"}, ensure_ascii=False)
    req = urllib.request.Request(url, headers={"User-Agent": "kakeratta-direct-request-worker/1.0"})
    try:
        with urllib.request.urlopen(req, timeout=WEB_TIMEOUT) as resp:
            raw = resp.read(WEB_MAX_BYTES)
    except Exception as e:
        return json.dumps({"error": f"fetch failed: {redact(e)}"}, ensure_ascii=False)
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        text = raw.decode("utf-8", errors="replace")
    # Crude tag stripping to give the model readable text.
    text = re.sub(r"(?is)<script.*?</script>|<style.*?</style>", " ", text)
    text = re.sub(r"(?s)<[^>]+>", " ", text)
    import html as htmlmod
    text = htmlmod.unescape(re.sub(r"\s+", " ", text)).strip()
    return text[:WEB_TEXT_CAP]


# --- Token Factory chat --------------------------------------------------

class InvalidOutput(ValueError):
    """The model answered, but not with a usable result: unparsable or
    truncated JSON, a wrong envelope, or too many tool rounds. It counts as a
    rejection and goes to correction, unlike a provider or network failure."""


def chat(cfg, messages, tools=None, temperature=0.2):
    """One chat-completions call; returns the first choice (message and finish_reason)."""
    api_key = cfg.get("api_key") or ""
    if not api_key:
        raise RuntimeError("Token Factory API key is not available")
    body = {
        "model": cfg["model"],
        "messages": messages,
        "temperature": temperature,
        "max_tokens": cfg.get("max_tokens", DEFAULT_MAX_TOKENS),
    }
    if tools:
        body["tools"] = tools
    if cfg.get("reasoning"):
        body["reasoning_effort"] = cfg["reasoning"]
    url = cfg["base_url"] + "/chat/completions"
    req = urllib.request.Request(
        url, data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {api_key}"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    if "error" in data:
        raise RuntimeError(data["error"].get("message", "provider error"))
    choices = data.get("choices") or []
    if not choices:
        raise RuntimeError("provider returned no choices")
    return choices[0]


def _tool_schemas():
    """OpenAI-style tool definitions exposed to the generation model (read-only)."""
    tools = []
    for name, props in READONLY_MCP_TOOLS.items():
        properties = {p: {"type": "string"} for p in props}
        if name == "list_personas":
            properties = {}
        tools.append({
            "type": "function",
            "function": {
                "name": name,
                "description": f"Read-only kakeratta lookup: {name}.",
                "parameters": {"type": "object", "properties": properties},
            },
        })
    tools.append({
        "type": "function",
        "function": {
            "name": "read_local_file",
            "description": "Read a file under the agent's home directory (read-only).",
            "parameters": {"type": "object", "properties": {"path": {"type": "string"}},
                           "required": ["path"]},
        },
    })
    tools.append({
        "type": "function",
        "function": {
            "name": "web_search",
            "description": "Search the web for current information.",
            "parameters": {"type": "object", "properties": {"query": {"type": "string"}},
                           "required": ["query"]},
        },
    })
    tools.append({
        "type": "function",
        "function": {
            "name": "web_fetch",
            "description": "Fetch a web page and return its text.",
            "parameters": {"type": "object", "properties": {"url": {"type": "string"}},
                           "required": ["url"]},
        },
    })
    return tools


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


GENERATION_SYSTEM = """You answer a request a user addressed directly to an external agent. You may use only the read-only tools provided (kakeratta lookups, local file reads, web search and web fetch). You cannot write files, run commands, run code, send anything, post, or change any state.

Return ONLY a JSON object (no Markdown, no commentary) of exactly this shape:

{"kind": "deliverable" | "operation",
 "deliverable": "<complete content, required when kind=deliverable>",
 "program": "<complete Python source, required when kind=operation>",
 "result_format": "<how to report the result, required when kind=operation>",
 "include_program": <true|false>}

Rules:
- kind=deliverable for content the user wants to receive (HTML, an article, an answer, a report, code they asked to see). Put the complete, final content in "deliverable" - never a summary or a fragment.
- kind=operation only for an action with side effects the user clearly asked for (for example sending an email, or changing files). Put a complete, self-contained, runnable Python program in "program" that does exactly what was asked and nothing more, and "result_format" describing what the program did and how to report it. Do not run the program yourself.
- Set include_program true only when the user explicitly asked to see the code.
- Satisfy every constraint the user stated (format, casing, exact names, lengths, viewport, overflow, readability, character budgets). Facts and names must be correct. Write the deliverable in the user's language.
"""


def _generation_user(context, problems, previous):
    text = _context_text(context)
    if problems:
        if previous:
            text += "\n\nYour previous candidate, in full:\n" + previous
        text += ("\n\nIt was rejected with these problems:\n"
                 + "\n".join("- " + p for p in problems)
                 + "\nCorrect exactly these issues and return a new complete candidate.")
    return text


REVIEW_SYSTEM = """You independently review a candidate produced for a user's direct request. You did not create it. Judge it against the original request and every constraint.

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


def _review_user(context, candidate, sha):
    return ("Original request context:\n" + _context_text(context)
            + "\n\nCandidate under review:\n" + candidate
            + "\n\ncandidate_sha256 (copy exactly): " + sha)


# --- JSON envelope helpers ----------------------------------------------

def _parse_json(text):
    if not text:
        raise InvalidOutput("empty model response")
    cleaned = text.strip()
    cleaned = re.sub(r"^```(?:json)?\s*|\s*```$", "", cleaned)
    try:
        return json.loads(cleaned)
    except json.JSONDecodeError:
        m = re.search(r"\{.*\}", cleaned, re.DOTALL)
        if not m:
            raise InvalidOutput("the response was not JSON")
        try:
            return json.loads(m.group(0))
        except json.JSONDecodeError as e:
            raise InvalidOutput(f"the response was not valid JSON: {e}")


def _answer(choice):
    """The final JSON object of a choice, or InvalidOutput."""
    if choice.get("finish_reason") == "length":
        raise InvalidOutput("the response was cut off at the output token limit")
    value = _parse_json((choice.get("message") or {}).get("content") or "")
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
        return text, "deliverable"
    if kind == "operation":
        program = envelope.get("program")
        if not isinstance(program, str) or not program.strip():
            raise InvalidOutput("program is empty")
        return program, "operation"
    raise InvalidOutput("kind must be deliverable or operation")


def sha256_of(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


# --- generation and review ----------------------------------------------

def generate(cfg, ctx, context, problems, previous):
    """A fresh generation (or correction) request; returns the JSON envelope.

    Provider and network failures raise as they are; a response the worker
    cannot use raises InvalidOutput."""
    messages = [
        {"role": "system", "content": GENERATION_SYSTEM},
        {"role": "user", "content": _generation_user(context, problems, previous)},
    ]
    tools = _tool_schemas()
    for _ in range(MAX_TOOL_ROUNDS):
        choice = chat(cfg, messages, tools=tools)
        msg = choice.get("message") or {}
        tool_calls = msg.get("tool_calls") or []
        if not tool_calls:
            return _answer(choice)
        messages.append({"role": "assistant", "content": msg.get("content"), "tool_calls": tool_calls})
        for call in tool_calls:
            fn = call.get("function") or {}
            name = fn.get("name", "")
            try:
                args = json.loads(fn.get("arguments") or "{}")
            except json.JSONDecodeError:
                args = {}
            result = run_readonly_tool(name, args, ctx)
            messages.append({
                "role": "tool",
                "tool_call_id": call.get("id", ""),
                "content": result,
            })
    raise InvalidOutput(f"more than {MAX_TOOL_ROUNDS} tool-call rounds without a final answer")


def review(cfg, context, envelope, sha):
    candidate, _ = candidate_of(envelope)
    messages = [
        {"role": "system", "content": REVIEW_SYSTEM},
        {"role": "user", "content": _review_user(context, candidate, sha)},
    ]
    try:
        verdict = _answer(chat(cfg, messages, tools=None, temperature=0.0))
    except InvalidOutput as e:
        # An unusable verdict is a rejection, never an acceptance.
        return {"accepted": False, "problems": [f"the reviewer's verdict was unusable: {e}"], "echoed_sha": None}
    accepted = verdict.get("accepted") is True
    problems = verdict.get("problems")
    if not isinstance(problems, list):
        problems = []
    problems = [str(p) for p in problems]
    return {
        "accepted": accepted and verdict.get("candidate_sha256") == sha,
        "problems": problems,
        "echoed_sha": verdict.get("candidate_sha256"),
    }


# --- operation execution -------------------------------------------------

_EXEC_ENV_ALLOWLIST = ("PATH", "LANG", "LC_ALL", "TZ", "HOME", "TMPDIR", "PYTHONUNBUFFERED")
AUDIT_STREAM = 2000          # stdout/stderr kept in an audit line


def run_program(cfg, home, request_id, program, accepted_sha, envelope):
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
        return {"state": "not_run", "code_sha256": on_disk}

    # What the reply needs, kept with the record so a resumed run can report
    # an execution without regenerating anything.
    report = {"result_format": envelope.get("result_format") or "",
              "include_program": envelope.get("include_program") is True}
    if report["include_program"]:
        report["program"] = program
    save_executed(home, request_id, dict(report, state="running", code_sha256=accepted_sha, at=time.time()))
    audit(home, request_id, "execution_start", {"code_sha256": accepted_sha})

    env = {k: v for k, v in os.environ.items() if k in _EXEC_ENV_ALLOWLIST}
    env["PYTHONUNBUFFERED"] = "1"
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
        "claim_id": claim_id, "model": cfg["model"], "provider": cfg["base_url"],
        "reasoning": cfg["reasoning"] or None, "resume": resumes,
    })

    # An operation that already ran, or started, is reported, never repeated.
    executed = load_executed(home, request_id)
    if executed is not None:
        audit(home, request_id, "execution_skipped", {
            "reason": "already executed" if executed.get("state") == "done" else "interrupted",
            "code_sha256": executed.get("code_sha256"),
        })
        _send_reply(cfg, url, headers, request_id, claim_id, _operation_reply(executed), home)
        return

    if resumes > MAX_RESUMES:
        audit(home, request_id, "resume_limit", {"resume": resumes})
        _send_reply(cfg, url, headers, request_id, claim_id,
                    "処理が繰り返し中断されたため、依頼を完了できませんでした。", home)
        return

    try:
        _run_generate_review_loop(cfg, url, headers, context, request_id, home, claim_id)
    except Exception as e:
        # A provider, network or tool failure: the request stays claimed and a
        # later tick resumes it, until the resume limit; then report it.
        error = redact(e, secrets)
        audit(home, request_id, "failed", {"error": error, "resume": resumes})
        if resumes >= MAX_RESUMES:
            _send_reply(cfg, url, headers, request_id, claim_id,
                        "依頼を処理できませんでした。\n\n" + error, home)


def _run_generate_review_loop(cfg, url, headers, context, request_id, home, claim_id):
    ctx = {"url": url, "headers": headers, "secrets": cfg["secrets"],
           "file_root": cfg.get("file_root")}
    problems = []
    previous = None
    corrections = 0
    while True:
        attempt = corrections + 1
        stage = "generation" if corrections == 0 else "correction"
        try:
            envelope = generate(cfg, ctx, context, problems, previous)
            candidate, kind = candidate_of(envelope)
        except InvalidOutput as e:
            # An unusable answer is a rejection, corrected like any other.
            problems = [str(e)]
            audit(home, request_id, stage + "_invalid",
                  {"attempt": attempt, "error": redact(e, cfg["secrets"])})
        else:
            previous = json.dumps(envelope, ensure_ascii=False)
            sha = sha256_of(candidate)
            audit(home, request_id, stage, {
                "attempt": attempt, "kind": kind,
                "candidate_sha256": sha, "candidate_chars": len(candidate),
            })
            verdict = review(cfg, context, envelope, sha)
            audit(home, request_id, "review", {
                "attempt": attempt, "accepted": verdict["accepted"],
                "problems": verdict["problems"],
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


# --- entry points --------------------------------------------------------

def process(url, headers, requests, cfg=None, home=None):
    """Process open direct requests, oldest first: pending ones, and claimed
    ones this worker left unfinished. Returns nothing; each request is handled
    independently and errors are audited, never raised. Only one worker runs
    at a time; an overlapping tick returns at once."""
    home = home or get_hermes_home()
    cfg = cfg or model_config(read_config(home))
    cfg["secrets"] = secret_values(cfg, headers)
    lock = acquire_lock(home)
    if lock is None:
        print("kakeratta direct-request worker: another run is in progress; skipping", file=sys.stderr)
        return
    try:
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
    sys.exit(main())
