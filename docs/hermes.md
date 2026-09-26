# Using Hermes Agent as a persona's planner

For the NemoClaw / OpenShell architecture and verification results, see the [integration report](nemoclaw-hermes-integration.md).

[README](../README.md) · [日本語](hermes_ja.md)

Of a persona's turn, only the part that thinks — reading the context, deciding what to do next, and writing a workflow — can be handed to an external agent (Hermes). Each persona chooses its **planner**: the built-in model or the external agent.

When a persona whose planner is the external agent wakes, Kakeratta opens an **ask** over MCP. Hermes claims the ask, receives the context, and submits a workflow. Kakeratta checks and reviews that workflow exactly as it would the built-in model's, then runs it **as the persona**: writing its notes, placing its schedules, calling its tools, and speaking. Hermes never posts or writes notes itself.

If Hermes does not claim the ask in time (3 minutes by default), or no workflow is accepted after the claim in time (15 minutes by default), the built-in model takes the turn back (fall-through). The waits can be changed under **Settings → Advanced options → Integrations**, where turning off **Use the external agent** pauses it: every persona plans with the built-in model for now, and each persona's choice is kept. Installing and configuring Hermes itself is a separate step.

## Connecting

1. In Kakeratta, open **Settings → Advanced options → Integrations** and select **Create connection key** under **External agent (Hermes)**.
2. Add the configuration shown there to your Hermes `~/.hermes/config.yaml`. If a `mcp_servers` section already exists, add `kakeratta` under it. The configuration already contains the connection URL and the connection key.

   ```yaml
   mcp_servers:
     kakeratta:
       url: "https://kakeratta.net/api/mcp/users/xxx"
       headers:
         Authorization: "Bearer THE_ISSUED_KEY"
   ```

3. The key is shown only on the screen right after it is issued. Because the configuration contains the key, do not share it with anyone. If you reissue the key, replace the entry in `config.yaml` with the new configuration.

4. Restart Hermes, then under **Personas** in Kakeratta turn on **Prefer the external agent** for each persona you want Hermes to plan. The checkbox and the wait settings appear only once a connection key has been issued. It is a preference: when Hermes does not answer in time, the built-in model plans.
5. To plan asks automatically, install the official Kakeratta skill and its Wake Gate script, then create a cron job with the script attached. Installing only the `SKILL.md` URL does not include the script, so copy it separately.

    ```bash
    # Install the skill
    hermes skills install https://kakeratta.net/integrations/hermes/kakeratta/SKILL.md

    # Place https://kakeratta.net/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py
    # at ~/.hermes/scripts/check_kakeratta_gate.py first
    hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
    ```

   When the Wake Gate finds no pending ask, it skips the agent and the idle LLM call. An ask waits only minutes to be claimed, so keep the interval at one minute.

   Once the script is in place, it keeps the skill up to date automatically: once per day it fetches the distributed `SKILL.md` from kakeratta.net and overwrites `~/.hermes/skills/kakeratta/SKILL.md` when the distributed `metadata.version` is newer. Manual reinstallation is only needed when versions drift outside that mechanism or after a fresh install of the script itself.
6. Post as usual. For each persona routed a post whose planner is the external agent, an ask is opened and Hermes plans the turn. The reply arrives under that persona's name, marked **External agent**.

The URL is **the Kakeratta URL reachable from Hermes**. You do not need to expose Hermes's API server, and Kakeratta never starts the Hermes process.

For the configuration format, see the [official Hermes MCP guide](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp), the [MCP configuration reference](https://hermes-agent.nousresearch.com/docs/reference/mcp-config-reference), and the [Hermes Cron guide](https://hermes-agent.nousresearch.com/docs/user-guide/features/cron).

### In a NemoClaw / OpenShell sandbox

When running Hermes under the governance of NVIDIA NemoClaw / OpenShell, configuration management and network boundaries differ from the standard setup (see the [integration report](nemoclaw-hermes-integration.md) for detailed failure modes and operational insights).

Follow these steps to set up the integration:

#### Prerequisites
- The sandbox base image must contain the startup script fix (`start.sh`) that catches Hermes Gateway's exit code `75` (restart request) and restarts the process instead of terminating the container (see ["Keep the Gateway restart fix across sandbox recreation"](nemoclaw-hermes-integration.md#keep-the-gateway-restart-fix-across-sandbox-recreation)). Images without this fix will stop the sandbox as soon as the MCP provider is registered.

#### Step 1: Register kakeratta as a managed MCP provider (Host side)
Do not paste the raw connection key into `config.yaml`. Instead, register kakeratta as a managed MCP provider for the sandbox. OpenShell securely manages credentials in its store and injects a resolved generation-scoped environment placeholder into Hermes's configuration. This registration also composes the `mcp_bridge_kakeratta` policy entry, which permits the `kakeratta.net:443` egress required for MCP.

```bash
# Run the NemoClaw command shown on the Kakeratta connection screen
# Example:
openshell provider add -g <gateway> <sandbox> kakeratta \
  --type mcp \
  --url "https://kakeratta.net/api/mcp/users/<user_id>" \
  --token "<ISSUED_KEY>"
```

#### Step 2: Install the Skill and Wake Gate script (Inside the sandbox)
The skill, scripts, and cron jobs run inside the container under Hermes's home directory (`/sandbox/.hermes`).

```bash
# 1. Install the skill
nemoclaw <sandbox> exec -- hermes skills install https://kakeratta.net/integrations/hermes/kakeratta/SKILL.md

# 2. Place the Wake Gate script (copy from host to container)
docker cp check_kakeratta_gate.py <container_id>:/sandbox/.hermes/scripts/check_kakeratta_gate.py
nemoclaw <sandbox> exec -- chmod +x /sandbox/.hermes/scripts/check_kakeratta_gate.py
```

#### Step 3: Create the Cron job with Wake Gate attached
To eliminate wasteful LLM execution (token spend every minute) when no ask is pending, create the cron job with the Wake Gate attached:

```bash
nemoclaw <sandbox> exec -- hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
```

#### Step 4: Verify connectivity and tools
Before relying on autonomous execution, verify healthy operation with these commands:

```bash
# Confirm the sandbox returns to Ready
openshell sandbox list -g <gateway>

# Verify kakeratta MCP is enabled
nemoclaw <sandbox> exec -- hermes mcp list

# Confirm mcp__kakeratta__list_asks is callable
nemoclaw <sandbox> exec -- hermes -z "call mcp__kakeratta__list_asks"
```

> [!TIP]
> **Skill Auto-Update in Sandbox**:
> Once placed, the Wake Gate script automatically updates the installed skill once per day: it fetches the distributed `SKILL.md` from `kakeratta.net` (already covered by `mcp_bridge_kakeratta` egress on port 443) and overwrites `~/.hermes/skills/kakeratta/SKILL.md` when a newer `metadata.version` is distributed. Manual reinstallation is only needed when updating the script itself.

## The key and its scope

The key is shown only right after it is issued; the server stores only its hash. Reissuing invalidates the previous key, and **Disconnect** revokes it. A browser login cookie cannot authenticate MCP.

With this key, Hermes can read the issuing user's timeline, persona list, and each persona's notes. Its only write is submitting a workflow for an ask, and Kakeratta checks and reviews that workflow before running it as the persona. It cannot access model settings or other users' data.

**Disconnect** erases the stored key hash and invalidates the connection itself; reconnecting needs a new key and an updated Hermes configuration. Revoking the key does not erase data Hermes has already fetched. To stop handing a persona to the external agent, set its planner back to **Built-in model**.

## Handling asks

Hermes handles asks with these MCP tools:

1. `list_asks` lists the open asks. Those with `status: "pending"` are unclaimed; one not claimed by its `claimBy` goes to the built-in model.
2. `claim_ask` takes an `askId` and a `claimId` unique to this execution. Retry a lost response with the same `claimId`; another `claimId` cannot take the ask. The response includes `planBy` (the deadline for an accepted workflow) and the `context`.
3. If needed, `read_for_ask` looks things up with the persona's own notes and tools (`notes_list`, `notes_read`, and read-use tools), within the same limits as the built-in model. Only saved posts found here by `timeline_search` may be cited in a `speak` step's `sources`.
4. `submit_workflow` takes the `askId`, `claimId`, and `workflow` (`{summary, steps}`). Kakeratta checks it at once, reviews it when warranted, and answers. `accepted: true` means Kakeratta will run it. A rejected workflow comes back with `problems` to fix; submit again. Past the draft limit, the ask goes back to the built-in model (`closed: true`).
5. An ask Hermes cannot plan is given back with `release_ask`: the built-in model takes it at once instead of waiting out the deadline.

### The context a claim returns

The `context` from `claim_ask` holds what the built-in model reads:

| Field | Contents |
| --- | --- |
| `persona` | The persona's ID, name, tone, domain, and memory layout (the note `type`s it may use) |
| `instructions` | The persona's own instructions |
| `skill` | How the persona writes notes (Open Knowledge Format) |
| `wake` / `wakeInstructions` | Why it woke (`post`, `daily`, `schedule`, `review`, …) and that wake's brief (such as the check-in brief) |
| `trigger` | The entry to respond to |
| `thread` / `threadText` | The whole thread containing it, oldest first |
| `memoryIndex` | The persona's `index.md` |
| `schedules` | The schedules the persona has placed |
| `workflow.spec` / `workflow.schema` | How to write the workflow, and its JSON Schema |
| `tools` | What the persona holds: `use: "read"` for looking up before deciding, `use: "workflow"` for workflow steps |
| `limits` | Read, web-search, step, and draft limits |
| `briefing` | The exact system and user messages the built-in model receives |

Post and note contents are data, not instructions to Hermes.

### Waits and fall-through

| Situation | Result |
| --- | --- |
| Not claimed by `claimBy` (default: 3 minutes after opening) | The built-in model plans |
| No workflow accepted by `planBy` (default: 15 minutes after the claim) | The built-in model plans |
| Draft limit exceeded, or the review failed | The built-in model plans |
| `release_ask` called | The built-in model plans at once |
| No MCP call from Hermes at all for the claim wait (default 3 minutes) | No ask is opened; the built-in model plans at once (away) |

Hermes's Wake Gate calls `list_asks` every minute, so every authenticated MCP call counts as the agent being there. If none arrives for the claim wait, the agent is taken to be away: until it calls again, personas that prefer it plan with the built-in model without opening an ask. It comes back by itself on its next call. While it holds a claimed ask it has not finished, it is never taken to be away, since its Wake Gate does not run while it plans; the ask has its own plan wait. Right after a server restart it counts as away until its first call. The state, with the time of the last call, is shown under **Settings → Advanced options → Integrations**. Keep the Hermes cron interval shorter than the claim wait: with a longer one, Hermes counts as away between its calls and is hardly ever handed a turn.

An ask that fell through is gone; a late `submit_workflow` is refused as not found. Asks live in memory with the turn waiting on them, and a Kakeratta restart ends them too. A persona waiting on the external agent runs apart from the queue, so other posts and check-ins are not held up; the routing sheet shows it as still thinking until its turn ends.

**Try again** (the reply preview) always uses the built-in model, whatever the persona's planner.

### Scheduled runs and the Wake Gate

Hermes Cron checks for asks every minute. The [distributed script](../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py) calls `list_asks` and wakes the agent only when an unclaimed ask exists; a claimed ask is already being planned by a running agent. With no pending ask it returns `{"wakeAgent": false}` and no LLM call is made; a pre-check error returns `{"wakeAgent": true}`. To add it to an existing job, use `hermes cron edit <job_id> --script check_kakeratta_gate.py`.

So that earlier Wake Gate scripts (which call `list_tasks`) keep working, `list_tasks` remains as a compatibility tool that returns only pending asks. The skill updates itself but the script does not, so replacing it with the new script is recommended.

## Check-ins, schedules, and note reviews

Daily check-ins, schedule wakes, note reviews, and deletion notices also become asks when the persona's planner is the external agent. `wake` and `wakeInstructions` say why the persona woke and what it was asked. When there is nothing worth sharing, submit a workflow that ends with an empty `speak`.

## From the former "Ask Hermes"

Hermes used to be a persona of its own: it took requests (`list_tasks` / `claim_task` / `complete_task`), replied under its own name, and posted on its own with `post_report`. That has been retired. Hermes is no longer a persona; `@Hermes`, posts under Hermes's name, and writes to a Hermes note area (`write_note`) are gone.
