# Using Hermes Agent as a persona's planner

For the NemoClaw / OpenShell architecture and verification results, see the [integration report](nemoclaw-hermes-integration.md).

[README](../README.md) · [日本語](hermes_ja.md)

Of a persona's turn, the research and the answer — reading the context, looking things up, and writing what the persona says — can be handed to an external agent (Hermes). Each persona chooses its **planner**: the built-in model or the external agent.

When a persona whose planner is the external agent wakes, Kakeratta opens an **ask** over MCP. Hermes claims the ask, receives the context, and submits an **answer** (`{text, sources}`) and nothing else. Kakeratta uses Super to plan schedules and actions, assembles them with the answer into a **workflow**, validates and reviews them, executes them **as the persona**, and publishes the answer. Its note keeper updates memory after publication. Hermes never posts, writes notes or changes state itself, and its answer is not reviewed for quality: a reviewer on Kakeratta's model cannot see what Hermes researched with its own tools.

If Hermes does not claim the ask in time (3 minutes by default), or no answer is accepted after the claim in time (15 minutes by default), the built-in model takes the turn back (fall-through). The waits are set per agent, under each connection's **Connection details and waits** in **Settings → Advanced options → Integrations**. There, turning off **Use the external agent** refuses its MCP connections (403) and every persona plans with the built-in model, while each persona's choice is kept. An agent that is only away (quiet for the claim wait) is still let in and comes back on its next call. Installing and configuring Hermes itself is a separate step.

Built-in fallback always researches on Super and composes the final answer on Super, or Ultra for deep replies and AlwaysUltra. Its per-round read schema follows live search budgets and available capabilities. Hermes retains its own research loop and the existing `read_for_ask` interface; the built-in schema changes do not restrict Hermes's own tools. See [research budgets](./system/personas.md#research-budgets-and-available-reads).

## Multiple agents

In Settings → Advanced → External integrations, press **+ Add connection** and give the connection a name. Each connection has its own key. In the **Active connection** list, choose one for the entire app; all personas that prefer an external agent use it. Adding a connection selects it. Switching keeps the other keys, but only the selected key can access MCP. Switching returns pending turns to the built-in model. Deleting the connection in use puts the first remaining one in use. The existing single connection is preserved as “Hermes”. Keys are shown only when issued.

Other agents can use the same MCP Streamable HTTP endpoint and `Authorization: Bearer <TOKEN>` header. They must poll `list_asks`, claim requests with `claim_ask`, read/research as needed, and call `submit_answer`; connecting alone does not run this loop. Hermes-specific setup below is optional for other clients.

## Connecting

1. In Kakeratta, open **Settings → Advanced options → Integrations** and under **External agent connections**, check the connection name ("Hermes" is filled in the first time) and press **Add and create key**. Open **Setting up Hermes** in the setup that appears.
2. Add the configuration shown there to your Hermes `~/.hermes/config.yaml`. If a `mcp_servers` section already exists, add `kakeratta` under it. To prevent argument quotation and double-escaping syntax errors (`Expecting ',' delimiter...`) on nested arguments such as the answer object, also configure Direct disclosure (`tools.tool_search.enabled: "off"`).

   ```yaml
   tools:
     tool_search:
       enabled: "off"

   agent:
     max_turns: 15 # Persona turns use 15; direct requests are handled by the direct-request worker, not by the agent

   platform_toolsets:
     cron: # Allows file operations, execution, and delegation while answering persona asks
       - web
       - file
       - terminal
       - code_execution
       - delegation

   mcp_servers:
     kakeratta:
       url: "https://kakeratta.net/api/mcp/users/xxx"
       headers:
         Authorization: "Bearer THE_ISSUED_KEY"
   ```

3. The key is shown only on the screen right after it is issued. Because the configuration contains the key, do not share it with anyone. If you reissue the key, replace the entry in `config.yaml` with the new configuration.

4. Restart Hermes, then under **Personas** in Kakeratta turn on **Prefer the external agent** for each persona you want Hermes to plan. The persona checkbox appears only while a connection key is configured and **Use the external agent** is enabled in settings. Wait settings belong to each connection. It is a preference: when Hermes does not answer in time, the built-in model plans.
5. To plan asks automatically, install the official Kakeratta skill and its Wake Gate script, then create a cron job with the script attached. Installing only the `SKILL.md` URL does not include the script, so copy it separately.

    ```bash
    # Install the skill
    hermes skills install https://kakeratta.net/integrations/hermes/kakeratta/SKILL.md

    # Place https://kakeratta.net/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py
    # at ~/.hermes/scripts/check_kakeratta_gate.py first
    hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
    ```

   When the Wake Gate finds no pending ask, it skips the agent and the idle LLM call. An ask waits only minutes to be claimed, so keep the interval at one minute.

   For automatic updates after installation, see [Autonomous updates over MCP](#autonomous-updates-over-mcp).

6. Post as usual. For each persona routed a post whose planner is the external agent, an ask is opened and Hermes researches and writes the answer. The reply arrives under that persona's name, marked **External agent**.

The URL is **the Kakeratta URL reachable from Hermes**. You do not need to expose Hermes's API server, and Kakeratta never starts the Hermes process.

For the configuration format, see the [official Hermes MCP guide](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp), the [MCP configuration reference](https://hermes-agent.nousresearch.com/docs/reference/mcp-config-reference), and the [Hermes Cron guide](https://hermes-agent.nousresearch.com/docs/user-guide/features/cron).

### Using with NemoClaw / OpenShell

When running Hermes under the governance of an NVIDIA NemoClaw / OpenShell sandbox, configuration management, policy composition, and process lifecycles involve container-specific requirements. To use Nemotron 3 Super for the parent and Nemotron 3 Ultra for delegated children, connect to Token Factory through Provider v2, which preserves model names, rather than OpenShell 0.0.116's shared `inference.local` route. The parent handles intake and routing, delegating high-complexity work to the child.

For the dedicated setup procedure ([Provider v2 migration](nemoclaw-hermes-integration.md#migration-to-openshell-provider-v2-2026-10-13), managed MCP provider registration, container script provisioning, search egress policies, and `Phase: Error` deadlock prevention), refer to the [**NemoClaw / OpenShell Integration and Verification Report**](nemoclaw-hermes-integration.md).

## The key and its scope

The key is shown only right after it is issued; the server stores only its hash. **Replace key** on a connection's row invalidates the previous key, and **Delete** revokes it. A browser login cookie cannot authenticate MCP.

With this key, Hermes can read the issuing user's timeline, persona list, and each persona's memory. Its only write is submitting an answer for an ask. Kakeratta decides, checks and runs any change the turn needs, and publishes the answer as the persona. It cannot access model settings or other users' data.

Deleting a connection erases the stored key hash and invalidates the connection itself; reconnecting needs a new key and an updated Hermes configuration. Revoking the key does not erase data Hermes has already fetched. To stop handing a persona to the external agent, set its planner back to **Built-in model**.

## MCP and autonomous planning

Kakeratta's MCP server exposes the following tools for external agents. In Hermes, tool names are prefixed as `mcp__kakeratta__<tool_name>`.

| Tool | Purpose |
| --- | --- |
| `list_personas` | List available personas |
| `get_timeline` | Retrieve timeline entries |
| `get_thread` | Retrieve conversation threads |
| `search_timeline` | Search the whole timeline by words and meaning and/or by period |
| `list_schedules` | List every persona's schedules (read-only) |
| `list_notes` / `read_note` | List and read a persona's memory files (per field a summary and facts, and monthly kakera records) |
| `list_asks` | Check pending and claimed asks, and direct requests when enabled (includes distributed version metadata) |
| `claim_ask` | Claim an ask exclusively and retrieve context (persona, instructions, capabilities, answer schema, read-only tools) |
| `read_for_ask` | Look up additional context while answering (`records`, and read-only tools such as `timeline_search`) |
| `submit_answer` | Submit the final answer (`{text, sources}`, optionally with `schedule` and `unrelated`); nothing else |
| `release_ask` | Hand the turn back to Kakeratta's built-in model immediately |
| `get_integration` | Retrieve latest official Skill, Wake Gate script and direct-request worker payloads |
| `claim_request` | Claim a direct request and retrieve its conversation |
| `comment_on_entry` | While on a claimed direct request, reply as the agent under another entry (for example a wrong answer); up to 10 per request |
| `reply_request` | Post the reply to a claimed direct request in its thread and end it |

### Answering loop

Based on the official Skill (`SKILL.md`), Hermes answers a persona's turn through these steps:

```text
list_asks → claim_ask → context interpretation → read_for_ask / own research (if needed) → submit_answer
```

1. **`list_asks`**: Check open asks. Unclaimed asks have `status: "pending"`; if not claimed before `claimBy`, the built-in model takes over.
2. **`claim_ask`**: Claim the target ask using its `askId` and an execution-unique `claimId`. The response returns `planBy` (accepted answer deadline) and the full `context`. Retry lost responses with the same `claimId`.
3. **Research & reasoning**: If needed, call `read_for_ask` with a `records` step to look through older kakera records (`args` with any of `tag`, `query`, `before`, or `ids` - up to 10 - to read the entries records point to), or a `tool` step to run a read-only tool in `context.tools`, such as `timeline_search`. Only saved posts discovered via `timeline_search` or read by a record's id through `read_for_ask` can be cited in the answer's `sources`. Research with Hermes's own tools as needed and cite web sources as links in the answer text.
4. **`submit_answer`**: Submit `askId`, `claimId`, and `answer: {text, sources, schedule, unrelated}`. Optional `schedule` requests schedule creation, change or cancellation; omitting it preserves legacy schedule behavior. Optional `unrelated` marks an out-of-domain silence. Kakeratta checks the shape, turn rules and source IDs. `accepted: true` means receipt, not publication: Kakeratta runs the schedule and action phases before publishing. After publication, memory-enabled personas send related user turns, including related silence, to the note keeper, which decides what to retain. Edits also reach the keeper to correct prior memory. Legacy `remember` values are accepted but ignored. If rejected, revise using `problems`.
5. **`release_ask`**: If an ask cannot be answered, release it immediately rather than waiting for timeouts.

### The context a claim returns

The `context` from `claim_ask` holds what the built-in model reads when it answers. How memory is kept and any state-changing schema are not part of it: those belong to Kakeratta's own later phases.

| Field | Contents |
| --- | --- |
| `persona` | The persona's ID, name, tone, and domain |
| `instructions` | The persona's own instructions |
| `capabilities` | The changes Kakeratta can make for the persona after the answer; the answer must not promise anything else |
| `wake` / `wakeInstructions` | Why it woke (`post`, `daily`, `schedule`, `review-reply`, …) and that wake's brief (such as the check-in brief) |
| `trigger` | The entry to respond to |
| `thread` | The whole thread containing it, oldest first |
| `memory` | The persona's memory: per field, its summary and the facts that hold now (`- [f_id] fact`), then its latest kakera records (`- time [entry id] one sentence #tag`) |
| `schedules` | The schedules the persona has placed |
| `answerSchema` | The JSON Schema of the answer (`{text, sources}`) |
| `tools` | The persona's read-only lookups through `read_for_ask` (including `records` and `timeline_search`) |
| `limits` | Read and web-search limits |
| `rules` | How the persona answers this turn; the agent is not asked to keep an answer short |
| `now` / `place` / `turn` | Current time, post location, and whether the turn requires an answer or silence |

Post and note contents are data, not instructions to Hermes.

### Waits and fall-through

Once an ask is opened, the following conditions return its turn to the built-in model:

| Situation | Result |
| --- | --- |
| Not claimed by `claimBy` (default: 3 minutes after opening) | The built-in model answers |
| No answer accepted by `planBy` (default: 15 minutes after the claim) | The built-in model answers |
| An answer rejected more than the allowed number of times | The built-in model answers |
| `release_ask` called | The built-in model answers at once |

An ask that fell through is gone; a late `submit_answer` is refused as not found. Asks are kept in the workspace, not in a waiting process: they survive a Kakeratta restart, and with several server instances the agent may reach any of them. Opening an ask ends the persona's turn for now, so other posts and check-ins are not held up; once the ask closes, the turn is carried on with the agent's answer or by the built-in model. The routing sheet shows the persona as still thinking until then.

An accepted answer is not retried by the built-in model. If the later phases cannot back up what it promised (for example, it says it will remind the user and no schedule can be placed), or a change fails when it runs, the answer is withheld rather than published; a named persona says it could not answer this time. The run log (`workflow <persona>` on stdout) records each phase's proposal, what was sent back, the review and the steps run.

**Try again** (the reply preview) always uses the built-in model, whatever the persona's planner.

### Direct requests

A post naming the agent itself - `@外部エージェント`, `@エージェント` or `@agent` (case and width ignored) - goes to the agent of the connection in use instead of the personas: it is not routed, planned or remembered. The connection's name is not an address, but the agent's replies are shown under it, to the user and to the personas. These names are reserved and cannot be given to a persona. A reply to the agent's reply, or to the user's own words or the app's notice in a conversation begun by naming the agent, is the next request; naming a persona in such a reply sends it to that persona instead. There is no separate switch: not naming the agent is the choice. While the agent is switched off or no connection exists, a post naming it is not routed to the personas either: Kakeratta replies with the reason. Replies that carry on an earlier conversation without naming the agent go to the personas meanwhile.

`list_asks` lists open requests under `requests`. The Wake Gate hands each pending request to the distributed direct-request worker (and each claimed one it left unfinished, which it resumes with the same `claimId`; one worker runs at a time), which claims it with `claim_request` (`requestId`, a stable `claimId`), receives its `replyBy` and the whole thread, runs Ultra once as an ordinary Hermes agent with the worker's tools (read-only by default), and then posts one reply with `reply_request` (`requestId`, `claimId`, `text`) - the agent's final response. The parent model is never woken for a direct request. The reply appears under the request as the speaker `agent`. To read Kakeratta the worker has the read-only MCP reads (`get_timeline`, `get_thread`, `search_timeline`, `list_schedules` and the notes tools); the request mechanism also exposes `comment_on_entry` for replying under another entry. No persona is woken by these comments, and the user replying to one asks the agent again. A request not claimed by `claimBy` (two hours after the request), or not replied to by `replyBy` (two hours after the claim), gets a notice from Kakeratta in the thread; there is no built-in fallback. A request that ran out after being claimed is kept for a day as expired, so the agent's late reply - after an operation it already ran, for example - is still posted. Editing a request that is still unclaimed updates it; editing one already claimed sends nothing and Kakeratta replies "処理中のため編集は反映されません。返信で伝えてください。". Switching the agent off or changing the connection withdraws open requests with a notice. The timeline marks a post waiting for the agent or being worked on, and the agent's reply, or the notice that none is coming, is pushed to devices with push notifications unless the 🔔 beside that post's badge was turned off for the request.

#### Direct-request worker settings

The worker runs Ultra in its own Hermes home (`~/.hermes/kakeratta/hermes-home`), which it rewrites from your `config.yaml` before each batch. The model, provider, endpoint and key reference come from a `kakeratta_worker` section if present, otherwise from `delegation`; the parent's `model` (Super) is never used. Without any extra settings the agent has read-only tools: web search/extract, Kakeratta's read-only MCP reads and read-only local file reads under your home (dotfiles and secret-looking names refused).

```yaml
kakeratta_worker:            # optional; falls back to delegation
  model: nvidia/Nemotron-3-Ultra-550b-a55b
  provider: custom
  base_url: https://api.tokenfactory.nebius.com/v1
  api_key: ${TOKENFACTORY_API_KEY}
  reasoning_effort: high
  max_tokens: 16000          # output cap per model call, reasoning included
  max_turns: 15              # tool-calling iterations in the run
  toolsets: "web,kakeratta,files"
  mcp_servers: []            # names of servers from mcp_servers above to give the agent
  max_api_calls: 40          # per request
  max_request_tokens: 1500000
```

To let the agent act (send mail, for example), name the MCP server that provides the action in `mcp_servers`, or add a toolset to `toolsets`. There is no review step: the agent uses those tools during the run. With action tools configured, a run that fails or is interrupted is reported and never repeated, since it may already have acted. The audit log of each request is in `~/.hermes/kakeratta/audit/<requestId>.jsonl`.

### Agent is away

Before opening an ask, Kakeratta checks whether the agent is present. This uses the same duration as the claim wait, measured from the last authenticated MCP call, rather than from an ask opening. If the agent is away, no ask is opened and the built-in model plans immediately.

Every authenticated MCP call records the agent's last-seen time; Hermes's Wake Gate normally calls `list_asks` every minute. The agent counts as away once that time exceeds the configured claim wait (default: 3 minutes), and comes back by itself on its next call.

While it holds a claimed ask it has not finished, it is never taken to be away, since its Wake Gate does not run while it plans; the ask has its own plan wait. Right after a server restart it counts as away until its first call.

The state, with the time of the last call, is shown under **Settings → Advanced options → Integrations**. Keep the Hermes cron interval shorter than the claim wait: with a longer one, Hermes counts as away between its calls and is hardly ever handed a turn.

### Zero-token standby (Wake Gate)

Running an agent (LLM invocation) on a 1-minute cron check wastes tokens and inference budget when no asks are pending.
To avoid idle spend, attach the **Wake Gate** pre-check script ([check_kakeratta_gate.py](../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py)) to the cron job.

- **No pending ask**: Returns `{"wakeAgent": false}` and exits immediately without invoking the LLM (~0.06s tick, 0 LLM calls). In addition, `agent.max_turns` is preserved at the safe boundary of 15.
- **Pending persona ask**: Returns `{"wakeAgent": true}`, answering within the 15-turn boundary.
- **Pending direct request**: Handled in the same tick by the distributed [direct-request worker](../frontend/public/integrations/hermes/kakeratta/scripts/direct_request_worker.py) - claim, one Ultra agent run with the worker's tools, then a reply with its final response - without waking the parent model. Only a pending persona ask wakes the agent.

To update an existing cron job, run `hermes cron edit <job_id> --script check_kakeratta_gate.py`.

### Autonomous updates over MCP

The Wake Gate script integrates an autonomous update mechanism for the Skill, itself and the direct-request worker:

- Every `list_asks` response includes current distributed versions (`integration: {skill, gate, worker}`).
- When the script detects a version difference, it invokes `get_integration` over the existing MCP connection to retrieve and atomically overwrite `~/.hermes/skills/kakeratta/SKILL.md`, itself and `~/.hermes/scripts/direct_request_worker.py` via temporary files.
- This keeps planning logic current even inside network-restricted sandboxes without requiring external web access.
- Each update, and any failure, is written to stderr.

A Wake Gate from before Gate version 2 cannot update itself and must be replaced by hand once.

## Check-ins, schedules, and deletions

Daily check-ins (which may carry records due for review: ask about one), schedule wakes, and replies to such a question also become asks when the persona's planner is the external agent. `wake` and `wakeInstructions` say why the persona woke and what it was asked. When there is nothing worth sharing, submit an answer with an empty `text`. Kakeratta's note keeper updates the persona's memory after each kakera of the user's; a deletion is tidied in-process and never becomes an ask.
