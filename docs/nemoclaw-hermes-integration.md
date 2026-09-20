# NemoClaw × Hermes Agent × Nebius Token Factory × kakeratta

## Autonomous personal AI agent integration and verification report

[README](../README.md) · [日本語](nemoclaw-hermes-integration_ja.md) · [Hermes setup and retries](hermes.md)

This report documents an integration in which Hermes Agent runs inside NVIDIA NemoClaw / OpenShell, uses Nebius Token Factory for inference, and retrieves, processes, and replies to requests through kakeratta's MCP server. The goal was to delegate work safely from a production personal AI application to an autonomous agent and return the result to the original conversation. Credentials, workspace IDs, access tokens, and internal task IDs are intentionally omitted.

## Architecture and responsibilities

```mermaid
flowchart TD
    U[User] -->|@Hermes post| K[kakeratta production web app / MCP]
    subgraph H[NemoClaw / OpenShell host]
      M[Managed MCP / network policy]
      P[Secure Inference Proxy]
    end
    subgraph S[OpenShell sandbox]
      G[Hermes Gateway]
      C[Blueprint Cron / every 1m]
      A[Hermes Agent + kakeratta Skill]
      G --> C --> A
    end
    A -->|list / claim / read / complete| M -->|MCP over HTTPS| K
    A -->|inference| P --> N[Nebius Token Factory / Nemotron 3.5 Lightning]
```

| Component | Responsibility |
| --- | --- |
| kakeratta | Manages conversations, tasks, MCP authentication, and access scope |
| Hermes Agent | Interprets tasks, works with its own tools, and returns results according to the Skill |
| NemoClaw / OpenShell | Manages the sandbox, Gateway lifecycle, network policy, and inference route |
| Nebius Token Factory | Provides model inference |

kakeratta does not start the Hermes process, and Hermes does not access kakeratta's database directly. MCP is the boundary between them. Model credentials are kept outside Hermes's own configuration and used through the environment-managed inference route.

## MCP and autonomous execution

The production connection exposed 11 MCP tools during verification.

| Tool | Purpose |
| --- | --- |
| `list_personas` | List AI personas |
| `get_timeline` | Read the timeline |
| `get_thread` | Read a conversation thread |
| `list_notes` / `read_note` | List and read persona notes |
| `write_note` | Save notes in Hermes's own area |
| `list_tasks` | Inspect pending and claimed tasks |
| `claim_task` | Claim a task exclusively |
| `cancel_task` | Cancel a task |
| `complete_task` | Save a result and post it to the original thread |
| `post_report` | Post an unsolicited report from Hermes |

The Hermes connection test succeeded and discovered all 11 tools. Within Hermes, their callable names have the `mcp__kakeratta__` prefix, such as `mcp__kakeratta__list_tasks`.

The distributed [kakeratta Skill](../frontend/public/integrations/hermes/kakeratta/SKILL.md) defines an `every 1m` blueprint and the following workflow:

```text
list_tasks → claim_task → get_thread → reason / use tools → complete_task
```

If there is no task, Hermes returns `[SILENT]`. In the verification environment, an idle run finished in about 3 seconds. The final architecture removed the custom polling script and output-diff monitor used earlier. That monitor could leave a failed task stuck: when the pending task ID matched the previous output, it treated the result as unchanged and did not start Hermes again.

The design principle from this failure is that **unfinished tasks must remain eligible for processing until they are completed**. Blueprint Cron checks tasks on every run, regardless of whether the list changed. A pending task can be picked up on the next run. For a claimed task, checking its state is separate from repeating an external action whose outcome may be unknown; see [Hermes setup and retries](hermes.md#if-hermes-crashes-after-claiming).

## Problems encountered during integration

| Problem | Cause and resolution |
| --- | --- |
| Adding MCP stopped the entire sandbox | Hermes Gateway returned exit code `75` to request a restart, but `set -e` in `start.sh` treated it as a normal process failure. The startup script was changed to catch `75` and restart only the Gateway. |
| Noninteractive onboarding chose the wrong inference provider | Explicitly passing `NEMOCLAW_PROVIDER="custom"`, `NEMOCLAW_MODEL="nvidia/Nemotron-3_5-Lightning"`, and `NEMOCLAW_ENDPOINT_URL="https://api.tokenfactory.nebius.com/v1"` selected Nebius as the OpenAI-compatible custom endpoint. |
| The smaller model wandered into unnecessary tool discovery and shell commands | `--tool-disclosure direct` exposed tool definitions directly, and the Skill named the official `mcp__kakeratta__*` tools and their order. |
| Cron stopped running inference after a model change | Hermes logged `global inference config drifted since this job was created` and skipped the run. Explicitly pinning the model resolved it. This protects scheduled jobs from unintended model and cost changes. |

Early tests with Nemotron-3.5-Lightning also produced incorrect MCP arguments, malformed JSON, repeated tool calls, and unnecessary configuration checks in complex multistep flows. A temporary design moved deterministic task management into a Python worker. It was stable but required custom orchestration code. The final design uses Hermes's native **Skill + Blueprint Cron + MCP** capabilities, with direct tool disclosure and explicit Skill instructions.

## End-to-end verification

The production site at `https://kakeratta.net` received this post:

> `@Hermes こんにちは！自己紹介して`

The autonomous path was:

```text
post → Cron → list_tasks → claim_task → get_thread
     → Nemotron-3.5-Lightning response generation
     → complete_task → reply in the original thread
```

The user posted at **09:08:53 UTC** and Hermes replied at **09:09:13 UTC**. This run completed in about **20 seconds**, including the one-minute scheduling interval. No one operated the Hermes CLI or management UI during the run. This is one observed run, not a general latency guarantee.

Development tested both `NousResearch/Hermes-4-405B` and `nvidia/Nemotron-3_5-Lightning`. In this environment, a simple prompt sometimes took about 45 seconds with the former and about 2–3 seconds with the latter. Lightning was selected for a personal AI workload with frequent invocations and relatively small individual tasks.

## Verified scope and possible extensions

Verification covered Hermes startup inside OpenShell, inference through Nebius, production kakeratta access through Managed MCP, discovery of all 11 tools, Skill installation and one-minute Cron execution, `[SILENT]` on idle runs, task detection and claim, conversation retrieval, autonomous inference, and automatic posting to the original thread.

The final kakeratta-side pieces are **MCP Server + Hermes Skill + Blueprint schedule metadata**. Hermes owns autonomous execution, NemoClaw / OpenShell controls the execution boundary, and Nebius Token Factory provides inference. No custom daemon or orchestration service is needed.

The same boundary can support research, file conversion, code execution, data analysis, other MCP services, and reports back to the timeline. External actions follow the approval policy on the Hermes side; they do not pass through kakeratta's usual workflow review.
