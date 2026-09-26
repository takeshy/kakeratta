# NemoClaw × Hermes Agent × Nebius Token Factory × kakeratta

## Autonomous External Planner Integration and Verification Report

[README](../README.md) · [日本語](nemoclaw-hermes-integration_ja.md) · [Hermes setup](hermes.md)

This report documents an integration in which Hermes Agent runs inside NVIDIA NemoClaw / OpenShell, acts as an external **planner** (thinking engine) for designated kakeratta personas, uses Nebius Token Factory for inference, and safely plans turns, formulates workflows, and keeps itself updated via kakeratta's MCP server. Credentials, workspace IDs, access tokens, and internal task IDs are intentionally omitted.

## Architecture and responsibilities

```mermaid
flowchart TD
    U["User"] -->|"Conversation / Mention"| K["kakeratta production web app / MCP"]
    subgraph H["NemoClaw / OpenShell host"]
      M["Managed MCP / network policy"]
      P["Secure Inference Proxy"]
    end
    subgraph S["OpenShell sandbox"]
      G["Hermes Gateway"]
      C["Hermes Cron + Wake Gate / every 1m"]
      A["Hermes Agent + kakeratta Skill"]
      G --> C --> A
    end
    A -->|"list_asks / claim_ask / read_for_ask / submit_workflow"| M -->|"MCP over HTTPS"| K
    A -->|"inference"| P --> N["Nebius Token Factory / Nemotron Super 120B"]
```

| Component | Responsibility |
| --- | --- |
| kakeratta | Manages conversations, asks, MCP authentication, workflow validation and execution |
| Hermes Agent (Planner) | Interprets asks according to Skill, performs lookups, and returns persona workflows |
| NemoClaw / OpenShell | Isolates the sandbox, manages Gateway lifecycle, network policy, and inference proxy |
| Nebius Token Factory | Provides high-performance model inference (`nvidia/nemotron-3-super-120b-a12b`) |

kakeratta does not start the Hermes process, and Hermes does not access kakeratta's database directly. MCP (Model Context Protocol) is the boundary between them. Model inference routes through OpenShell's proxy, keeping credentials completely hidden. The recurring Wake Gate also serves as a heartbeat: if no MCP calls arrive within the claim wait window (3 minutes by default), kakeratta's built-in model answers instead.

## MCP and autonomous planning

kakeratta exposes 12 MCP tools for external planners.

| Tool | Purpose |
| --- | --- |
| `list_personas` | List AI personas |
| `get_timeline` | Read the timeline |
| `get_thread` | Read a conversation thread |
| `list_notes` / `read_note` | List and read persona notes |
| `list_asks` | Inspect pending and claimed asks (includes distributed integration versions) |
| `claim_ask` | Claim an ask exclusively and retrieve context (persona identity, instructions, memory) |
| `read_for_ask` | Inspect additional persona memory or search tools during planning |
| `submit_workflow` | Submit a structured workflow (`speak`, `write_note`, etc.) for kakeratta to run |
| `release_ask` | Release an ask back to kakeratta's built-in model if planning cannot proceed |
| `get_integration` | Fetch latest distributed Skill and Wake Gate script texts over MCP |
| `list_tasks` | Legacy endpoint retained for backward compatibility |

Within Hermes, callable tool names carry the `mcp__kakeratta__` prefix, such as `mcp__kakeratta__list_asks`.

### Planning loop

Guided by the distributed [kakeratta Skill](../frontend/public/integrations/hermes/kakeratta/SKILL.md), Hermes operates through the following loop:

```text
list_asks → claim_ask → interpret context → read_for_ask (if needed) → submit_workflow
```

1. **`list_asks`**: Inspect pending asks (`status: "pending"`). Stop immediately if none are pending.
2. **`claim_ask`**: Exclusively claim the ask and receive its detailed `context` (persona tone, memory layout, thread history, etc.).
3. **Reasoning & Lookups**: Perform additional research using `read_for_ask` or Hermes's own web tools if necessary.
4. **`submit_workflow`**: Submit the persona's action plan as a structured JSON workflow (`speak`, `write_note`, schedule, etc.). kakeratta validates the workflow and executes it in the persona's own voice. If planning cannot proceed, call `release_ask` to return the ask immediately.

### Zero-token standby (Wake Gate)

Running an agent every minute without work causes unnecessary token consumption and inference costs. The **Wake Gate** pre-check script (`check_kakeratta_gate.py`) attaches to the cron job to eliminate idle inference:

- **No pending asks**: Returns `{"wakeAgent": false}` and exits immediately without invoking LLM inference (idle tick ~0.06s, 0 LLM calls).
- **Pending ask or error**: Returns `{"wakeAgent": true}` to wake Hermes Agent and execute the planning skill.

### Autonomous updates over MCP

Wake Gate also maintains Skill and script freshness:
- `list_asks` reports the active distributed versions (`integration: {skill, gate}`).
- When a version mismatch is detected, the script calls `get_integration` over the established MCP connection, fetching updated `SKILL.md` and `check_kakeratta_gate.py` files and replacing them atomically via temporary files.
- This ensures sandboxes with strict egress rules stay automatically updated without opening arbitrary external HTTP GET egress paths.

## Key operational issues resolved

| Observed issue | Root cause and resolution |
| --- | --- |
| Container stops on MCP registration | Hermes Gateway exits with code `75` (EX_TEMPFAIL) to request a process restart on MCP changes. Default startup scripts treat this as fatal under `set -e`. Resolved by building a custom image with a restart loop on code 75. |
| Non-interactive onboarding picks unexpected provider | Specified `NEMOCLAW_PROVIDER="custom"`, `NEMOCLAW_MODEL="nvidia/nemotron-3-super-120b-a12b"`, and `NEMOCLAW_ENDPOINT_URL="https://api.tokenfactory.nebius.com/v1"` explicitly to route through Nebius. |
| MCP tool calls fail due to JSON string escape corruption | When Progressive Tool Disclosure (`tools.tool_search.enabled: true`) is active, Hermes hides MCP tools behind a `tool_call` wrapper expecting arguments as a stringified JSON blob. Complex nested workflow schemas failed during string escaping. Resolved by disabling Progressive Tool Disclosure via `tools.tool_search.enabled: "off"` in `/sandbox/.hermes/config.yaml` to expose tools directly (Direct disclosure). |
| MCP returned `401 Unauthorized` | OpenShell's L7 proxy expects generation-scoped placeholders (e.g., `openshell:resolve:env:v<REVISION>_KAKERATTA_MCP_TOKEN`). Synchronizing the placeholder revision in `config.yaml` with the provider environment revision restored authentication. |
| NemoClaw CLI commands hang indefinitely | Orphan lock files (`~/.nemoclaw-portable-host.lock`) remained with defunct owner PIDs after interrupted operations. Pruning stale locks whose owner PIDs were dead restored responsiveness. |
| Sandbox stuck in `Phase: Error` (Reconciliation deadlock) | Abrupt container exit caused Gateway to record `Phase: Error`. OpenShell CLI `start` requires `Stopped` and `stop` requires `Ready`, creating a deadlock. Reconciled the sandbox state directly in `openshell.db` back to `Ready` (2) to recover. |
| Sandbox does not start on host reboot (`GatewayAuthorityError`) | Running the Gateway as a systemd user service drifted authority against the onboarding checkpoint (`standalone`). Resolved by configuring `~/.config/openshell/gateway.env` (`0600`), using `nemoclaw-openshell-gateway.service`, and reconciling authority to `"packaged-service"`. |
| Host shutdown triggers `Phase: Error` deadlock on subsequent boot | Abrupt host shutdown killed the container with SIGTERM (exit code 143) without clean stopping. Resolved by adding `ExecStop=/home/takeshy/.local/bin/nemoclaw hermes-sandbox stop` and `TimeoutStopSec=60` to `nemoclaw-hermes-start.service` to ensure clean transition to `Stopped`. |
| Web search and page extraction blocked with `403 Forbidden` causing hallucinations | OpenShell's L7 proxy default egress policy blocked search backends. After failed research, the agent hallucinated confident but false answers. Resolved by whitelisting search/extraction endpoints per host. |
| Cron stops executing inference after model change | Hermes suspended execution with `global inference config drifted since this job was created`. Pinning the model explicitly in the job resolved it. This is a built-in safety feature against unintended drift and cost spikes. |
| Granted egress approvals never take effect | Approvals surfaced by OpenShell may remain pending as `draft_policy_chunk` and not merge into the active policy. Never consider an approval active until confirmed directly via `openshell policy get` or `nemoclaw policy explain`. |
| `personal-open-internet` rejected next to host-specific policies | Overlapping endpoints on port 443 with differing metadata trigger `network endpoint ambiguity validation failed`. Resolved by adding keyless endpoints individually per host via `openshell policy update --add-endpoint` instead of broad opening. |
| Applying policy preset during `Phase: Error` wipes composed policy | Running `policy add` while the sandbox is in `Phase: Error` drops composed entries (`mcp_bridge_kakeratta`, `managed_inference`), breaking token resolution. Resolved by restoring the pre-incident policy backup via `openshell policy set --policy <file> --wait`. |

### Opening egress for web search and page extraction (preventing hallucinations)

To ensure Hermes can accurately look up external and up-to-date documentation before planning, it needs egress to keyless search and page extraction backends: Tavily (`api.tavily.com`), Exa (`mcp.exa.ai`), Parallel (`search.parallel.ai`), Firecrawl (`api.firecrawl.dev`), Keenable (`api.keenable.ai`), Brave (`api.search.brave.com`), and DuckDuckGo (`duckduckgo.com`, `html.duckduckgo.com`).

Initial sandbox policies block external egress, risking hallucinations when models guess instead of researching. Add per-host endpoints via `openshell policy update`:

```bash
openshell policy update -g <gateway> <sandbox> \
  --add-endpoint "api.tavily.com:443:full" \
  --add-endpoint "mcp.exa.ai:443:full" \
  --add-endpoint "search.parallel.ai:443:full" \
  --add-endpoint "api.firecrawl.dev:443:full" \
  --add-endpoint "api.keenable.ai:443:full" \
  --add-endpoint "api.search.brave.com:443:full" \
  --add-endpoint "duckduckgo.com:443:full" \
  --add-endpoint "html.duckduckgo.com:443:full" \
  --binary "/usr/local/bin/hermes" --binary "/usr/bin/python3*" --binary "/opt/hermes/.venv/bin/python*" \
  --wait
```

### Restoring policy after accidental removal

`policy set` rewrites the base document entirely. Writing an incomplete base removes composed entries (`mcp_bridge_kakeratta`, `managed_inference`, etc.). To recover:

1. Return the sandbox to `Ready` first (writes during non-ready states fail with `sandbox is not ready`).
2. Extract the complete pre-incident policy from exports or backups, and restore via `openshell policy set -g <gateway> --policy <file.yaml> --wait <sandbox>`.
3. Confirm revision hashes match using `openshell policy list -g <gateway> <sandbox>`.
4. Re-add any per-host endpoints added after the backup.

Prefer `openshell policy update --add-endpoint` for delta changes on running sandboxes.

### Keep the Gateway restart fix across sandbox recreation

Hermes Gateway exits with **code 75** on configuration reloads. Standard NemoClaw startup scripts exit abruptly on code 75. Wrap the wait call in a restart loop:

```bash
# Extract and patch start.sh from container
docker cp <hermes_container_id>:/usr/local/bin/nemoclaw-start ./start.sh
chmod 755 start.sh
```

Patch `start.sh` wait loop:

```bash
while true; do
  if wait "$GATEWAY_PID"; then
    rc=0
  else
    rc=$?
  fi

  if [ "$rc" -eq 75 ]; then
    echo "[gateway] Hermes requested restart (exit code 75). Restarting gateway..." >&2
    launch_hermes_gateway_current_user || exit 1
    wait_for_hermes_gateway_internal "$GATEWAY_PID" || exit 1
    ensure_hermes_supervised_auxiliaries || exit 1
    continue
  fi

  exit "$rc"
done
```

Create a custom Dockerfile and onboard:

```dockerfile
# ~/.config/nemoclaw/hermes-image/Dockerfile
FROM ghcr.io/nvidia/nemoclaw/hermes-sandbox@sha256:73fec1566a2cdf859a8c2832f77f90d7101755843847ecf78d5cd270f92790ed
ARG NEMOCLAW_TOOL_DISCLOSURE=direct
ENV NEMOCLAW_TOOL_DISCLOSURE=${NEMOCLAW_TOOL_DISCLOSURE}

COPY --chown=root:root start.sh /usr/local/bin/nemoclaw-start
```

```bash
nemoclaw onboard --from ~/.config/nemoclaw/hermes-image/Dockerfile --tool-disclosure direct
```

### Automatic startup and deadlock avoidance on host reboot

For resilient 24/7 background operation across host reboots:

#### 1. Wait for Docker on boot
Prevent `Failure layer: docker_unreachable` by waiting for Docker in `ExecStartPre`:

```bash
# nemoclaw-wait-docker.sh
until docker info >/dev/null 2>&1; do
  sleep 1
done
```

#### 2. Authority reconciliation (`GatewayAuthorityError`)
Use managed `nemoclaw-openshell-gateway.service`, configure `~/.config/openshell/gateway.env` (`0600`), and set `checkpoint.gatewayAuthority.value.source` to `"packaged-service"` in `~/.nemoclaw/onboard-session.json`.

#### 3. Clean stop handler (`ExecStop`)
Prevent abrupt SIGTERM exits from marking `Phase: Error` during host shutdown:

```ini
[Unit]
Description=Start Hermes sandbox through NemoClaw
Requires=nemoclaw-openshell-gateway.service
After=nemoclaw-openshell-gateway.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=/home/takeshy/.local/bin/nemoclaw-wait-docker.sh
ExecStart=/home/takeshy/.local/bin/nemoclaw hermes-sandbox start
ExecStop=/home/takeshy/.local/bin/nemoclaw hermes-sandbox stop
TimeoutStartSec=600
TimeoutStopSec=60
Restart=on-failure
RestartSec=10
```

### `Phase: Error` deadlock mechanism and manual recovery procedure

A critical operational issue encountered in OpenShell sandboxes is the **`Phase: Error` reconciliation deadlock**.

#### 1. Deadlock mechanism
OpenShell Gateway's sandbox state machine enforces strict preconditions:
- `openshell sandbox start` requires the sandbox to be in **`Stopped`** phase.
- `openshell sandbox stop` requires the sandbox to be in **`Ready`** phase.

When abrupt container termination (such as host shutdown SIGTERM / exit code 143), Docker daemon race conditions during boot, or stale locks force Gateway into **`Phase: Error`**, the system enters an unresolvable loop:
- `openshell sandbox start` fails with `sandbox must be Stopped to start (current phase: Error)`.
- `openshell sandbox stop` fails because the sandbox is not in `Ready`.
- Restarting the container via `docker restart` leaves Gateway's SQLite database state in `Error`, continuing to reject CLI operations.
- `nemoclaw recover` or `doctor --fix` call the same underlying OpenShell APIs and cannot bypass the precondition check.

#### 2. Emergency manual recovery (Gateway DB Byte-Level Repair)
When locked in `Phase: Error`, you can directly reconcile Gateway's internal SQLite database (`openshell.db`) back to `Ready` without destroying or recreating the sandbox:

```bash
# 1. Stop the start service
systemctl --user stop nemoclaw-hermes-start.service

# 2. Prune orphan locks
rm -f ~/.nemoclaw-portable-host.lock
rm -f ~/.nemoclaw/state/mcp-lifecycle-locks/*

# 3. Back up Gateway DB
DB_PATH="$HOME/.local/state/nemoclaw/openshell-docker-gateway/openshell.db"
cp "$DB_PATH" "${DB_PATH}.bak.$(date +%Y%m%d%H%M%S)"

# 4. Deserialize Protobuf payload and reconcile phase to Ready (enum 2)
python3 -c "
import sqlite3

db_path = '$DB_PATH'
conn = sqlite3.connect(db_path)
c = conn.cursor()
payload = bytearray(c.execute(\"SELECT payload FROM objects WHERE object_type='sandbox' AND name='hermes-sandbox'\").fetchone()[0])

def encode_varint(val):
    b = bytearray()
    while val > 0x7F:
        b.append((val & 0x7F) | 0x80)
        val >>= 7
    b.append(val & 0x7F)
    return bytes(b)

def encode_tag(field_num, wire_type):
    return encode_varint((field_num << 3) | wire_type)

def encode_len_delim(field_num, data):
    return encode_tag(field_num, 2) + encode_varint(len(data)) + data

def parse_protobuf(data):
    i = 0
    items = []
    while i < len(data):
        start_pos = i
        shift = 0; key = 0
        while True:
            if i >= len(data): return items
            b = data[i]; i += 1
            key |= (b & 0x7F) << shift
            shift += 7
            if not (b & 0x80): break
        fnum = key >> 3
        wtype = key & 7
        if wtype == 0:
            shift = 0; val = 0
            while True:
                b = data[i]; i += 1
                val |= (b & 0x7F) << shift
                shift += 7
                if not (b & 0x80): break
            items.append((fnum, wtype, val, start_pos, i))
        elif wtype == 2:
            shift = 0; length = 0
            while True:
                b = data[i]; i += 1
                length |= (b & 0x7F) << shift
                shift += 7
                if not (b & 0x80): break
            val = data[i:i+length]
            i += length
            items.append((fnum, wtype, val, start_pos, i))
        else:
            items.append((fnum, wtype, 'unknown', start_pos, i))
    return items

top_items = parse_protobuf(payload)
f3 = [it for it in top_items if it[0] == 3][0]

field3_data = f3[2]
f3_items = parse_protobuf(field3_data)
cond_data = encode_len_delim(1, b'Ready') + encode_len_delim(2, b'True')

rebuilt_f3 = bytearray()
for fnum, wtype, val, s, e in f3_items:
    if fnum == 5:
        rebuilt_f3 += encode_len_delim(5, cond_data)
    elif fnum in (6, 7):
        rebuilt_f3 += encode_tag(fnum, 0) + encode_varint(2) # SANDBOX_PHASE_READY (enum 2)
    elif fnum == 9:
        pass # Remove exit_code 143
    else:
        if wtype == 0:
            rebuilt_f3 += encode_tag(fnum, 0) + encode_varint(val)
        elif wtype == 2:
            rebuilt_f3 += encode_len_delim(fnum, val)

new_payload = bytearray(payload[:f3[3]]) + encode_len_delim(3, bytes(rebuilt_f3))

c.execute(\"UPDATE objects SET payload=?, status='Ready' WHERE object_type='sandbox' AND name='hermes-sandbox'\", (bytes(new_payload),))
conn.commit()
print('Successfully restored hermes-sandbox to Ready')
"

# 5. Restart Gateway service
systemctl --user restart nemoclaw-openshell-gateway.service

# 6. Verify and resume service
openshell sandbox list
systemctl --user start nemoclaw-hermes-start.service
```

This reconciles Gateway's database safely back to `Ready`, immediately restoring CLI responsiveness and background automatic restarts.

## End-to-end verification

Verified in production on `https://kakeratta.net` with personas configured for external planning:

```text
User post → Cron (every 1m with Wake Gate) → list_asks (detects pending ask)
          → claim_ask (fetches context)
          → Nemotron-3-Super-120B reasoning & lookups (read_for_ask / Web search)
          → submit_workflow (submits speak, write_note steps)
          → kakeratta review, execution, and thread posting
```

The entire cycle completed autonomously with zero human intervention.

### Model selection and reliability (Direct Disclosure)

Multiple models were evaluated during development:
- **`NousResearch/Hermes-4-405B`**: Highly capable, but simple prompts took ~45 seconds, adding unacceptable latency to a 1-minute scheduling cadence.
- **`nvidia/Nemotron-3_5-Lightning`**: Extremely fast (2–3 seconds), but prone to argument omission, JSON structure syntax errors during multi-step MCP operations, and getting caught in troubleshooting loops (attempting unwanted configuration changes and shell commands) upon transient network or tool hiccups.
- **`nvidia/nemotron-3-super-120b-a12b`**: Provided the best operational balance between response speed and sophisticated context understanding/tool judgment, making it the standard model for production autonomous planning.

Furthermore, applying `tools.tool_search.enabled: "off"` (Direct disclosure) in Hermes allows the model to pass complex, nested workflow structures as pure native JSON objects without double-escaping, structurally eliminating parse failures.

## Summary

The boundary between kakeratta and Hermes is minimal: **MCP Server + Hermes Skill + Wake Gate script**. NemoClaw / OpenShell guarantees isolation and network governance, while Nebius Token Factory delivers fast inference for high-quality persona planning.
