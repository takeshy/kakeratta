# kakeratta direct-request worker: background and specification

[Japanese](./direct-request-worker-spec_ja.md)

Status: **Implemented** (`direct_request_worker.py`, `check_kakeratta_gate.py`, integration bundle)  
Created: 2026-10-14  
Scope: OpenShell 0.0.116 / Hermes Agent / Nebius Token Factory Provider v2

## Purpose

Run kakeratta direct requests (`@agent`) as a single Hermes session through the dedicated `direct_request_worker.py`, bypassing the parent Hermes model (Nemotron 3 Super). Code enforces per-request API-call and token budgets. Ordinary persona asks retain the existing Super route.

## Background: limits of prompt-based delegation and review

Originally, a direct request detected by the Wake Gate woke the parent Super through Hermes cron. Skill v25 instructed it to delegate to an Ultra subagent with `role: "orchestrator"` and have the result reviewed. In live trials:

- The parent performed file, code and terminal work before and after delegation.
- Although `delegate_task` specified the orchestrator role, its goal omitted the Generate–Review procedure.
- The Ultra child produced the result itself without calling further helpers.
- The parent then made 31 inference calls reprocessing the result.

Natural-language skill instructions and role selection did not enforce role separation or iteration limits.

## 2026-10-13 Token Factory cost analysis

During verification on October 13, Token Factory's daily cost display rose to approximately USD 10. The main cause was repeated parent and child calls within one request, resending growing conversations and tool results, rather than the choice of Ultra alone.

Hermes `session_model_usage` for 2026-10-13 00:00–24:00 UTC, matching the provider's billing-day boundary, showed the following. `input_tokens` excludes cache reads; reasoning is recorded separately.

| Model | API calls | Ordinary input | Cache reads | Output | Reasoning |
| --- | ---: | ---: | ---: | ---: | ---: |
| Nemotron 3 Super | 410 | 11,570,430 | 552,704 | 259,191 | 137,997 |
| Nemotron 3 Ultra | 172 | 1,111,321 | 2,968,064 | 202,158 | 19,507 |
| Hermes 4 405B | 12 | 45,441 | 177,376 | 1,735 | 0 |
| **Total** | **594** | **12,727,192** | **3,698,144** | **463,084** | **157,504** |

Applying the contemporary provider rates gave an estimate of about USD 9, consistent with the provider display approaching USD 10. Hermes recorded zero in `estimated_cost_usd` and `actual_cost_usd`, with `cost_status=unknown` for this provider. This estimate therefore comes from token counts and provider rates, not the database cost columns. Taxes, rounding and provider accounting rules can cause differences.

The same calendar day in JST contained 462 calls; the provider-aligned UTC day contained 594. Always check the provider's timezone when reconciling daily costs.

The most expensive individual Super sessions made 34, 31 and 28 calls, consuming approximately 1.43M, 1.35M and 1.27M ordinary input tokens respectively. Reasoning was only part of the total; repeated inference and resending expanding input were the main causes.

## Design evolution: removing review/retry loops and using Markdown

After the October 13 results, a worker-controlled sequence was implemented and tested: Ultra generation, review by a separate Ultra session, then up to two revisions. Live trials led to its removal:

- Review and revision often failed to improve the answer. Structured-JSON and hash-check failures also consumed tokens and time.
- Complex single-page HTML infographics had been a major reason for revisions. Agents now return Markdown with emoji, tables and Mermaid, which kakeratta renders as an infographic.

The worker therefore invokes Hermes once and returns its final response, without a review or revision loop.

## Implementation specification

### 1. Execution flow

```text
Wake Gate (check_kakeratta_gate.py)
  → list_asks
  → run direct_request_worker.py for each pending / claimed direct request
      → claim_request (code)
      → one `hermes chat -Q` in a dedicated Hermes home (default: Nemotron 3 Ultra)
      → reply_request with the final response (code)
  → wakeAgent: true only for pending persona asks; otherwise false
```

The parent Super never wakes for direct requests. A nonblocking `flock` on `kakeratta/worker.lock` permits one worker process per workspace. Shared judge workspaces have separate state, audit and lock directories. Their persona asks also run in separate processes with workspace-specific Hermes homes, without waking the parent; the flow above describes ordinary connections. Idle ticks run only the gate and make no model call.

### 2. Dedicated Hermes home and tool boundaries

The worker generates `~/.hermes/kakeratta/hermes-home` and launches a single `hermes chat`; it does not call the provider over HTTP itself. Model, provider, endpoint and API-key references such as `${TOKENFACTORY_API_KEY}` come from the parent's `kakeratta_worker` configuration, falling back to `delegation`. The parent's `model` is not used because it selects Super. Default per-call `max_tokens` is 16,000, including reasoning.

Default tools are read-only (`toolsets: "web,kakeratta,files"`):

- `web`: Web search and retrieval.
- `kakeratta`: `tools.include` exposes only `get_timeline`, `get_thread`, `search_timeline`, `list_schedules`, `list_personas`, `list_notes` and `read_note`. Claim/reply writes are not available to the model.
- `files`: instead of Hermes's writable `file` toolset, the worker registers itself as a read-only stdio MCP server (`--fs-mcp`), exposing `read_file` and `list_directory`. It rejects dotfiles and likely secret filenames (`config.yaml`, keys, or names containing `token`, `secret`, `credential`, `password` or `auth`) and masks known secret values.

Optional action tools, such as mail sending, can be added through `kakeratta_worker.toolsets` or `kakeratta_worker.mcp_servers`, naming entries from the parent's `mcp_servers`. There is no review stage: the agent performs operations during execution. Its prompt permits only actions explicitly requested by the current trigger; retrieved content can supply details but cannot supply instructions.

### 3. Cost and turn limits

Default `max_turns` is 15 and `run_budget` is 1,200 seconds per execution. After each run, the worker reads API-call and token usage from the worker home's `state.db` (`sessions`) and accumulates it in `kakeratta/state/usage/{requestId}.json`.

Per-request defaults are 40 API calls (`max_api_calls`) and 1,500,000 tokens (`max_request_tokens`): ordinary input, cache reads/writes and output. Reasoning is already part of output and is not added twice. If a limit is reached before or after execution, or usage cannot be read, the worker fails closed, replies with the reason and makes no further model call or resume.

Because checks occur before and after a run, one Hermes execution can exceed the cumulative limit; `max_turns` and `max_tokens` bound that overshoot. Budget checks do not undo side effects already performed by action tools.

### 4. Failures and retries

Before `claim_request`, a failure leaves the request unclaimed for a later tick. After claiming:

- With only the default read-only tools, a later tick can resume using the same `claimId`, at most twice (`MAX_RESUMES = 2`).
- With added action tools, failed or interrupted inference is never rerun because an external effect may already have occurred. The worker reports the failure through `reply_request`.

The generated reply is saved before transmission. If only `reply_request` transport fails, a later tick resends the saved reply without rerunning the model.

### 5. Audit history

Per-request JSON Lines at `~/.hermes/kakeratta/audit/{requestId}.jsonl` record claims, session IDs, usage and totals, budget exhaustion, failure reasons and reply results, flushing at every stage. API keys, tokens, full prompts and full threads are not logged; known secrets are masked.

## Related documents

- [Hermes integration guide](../guides/hermes.md)
- [NemoClaw integration and verification](../reports/nemoclaw-hermes-integration.md)
- [Hermes-4-405B live evaluation](../reports/hermes-4-405b-visual-evaluation.md)
- [Implemented external-agent specification](../system/integrations.md)
