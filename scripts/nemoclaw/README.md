# NemoClaw / OpenShell / Hermes Agent operations scripts

[日本語](README_ja.md)

Scripts and systemd user units that build and keep kakeratta's external agent (NVIDIA NemoClaw + OpenShell + Hermes Agent) running on a host machine.

---

## 1. Layout

```text
scripts/nemoclaw/
├── README.md / README_ja.md               # This document (English / Japanese)
├── setup-hermes.sh                        # Builds everything from NemoClaw install to the kakeratta connection (rerunnable)
├── install.sh                             # Links scripts and units into ~/.local/bin and ~/.config/systemd/user
├── profiles/
│   └── tokenfactory.yaml                  # Nebius Token Factory provider profile for Provider v2
├── bin/                                   # Scripts
│   ├── nemoclaw-wait-docker.sh            # Waits for the Docker daemon
│   ├── nemoclaw-reconcile-sandbox.py      # Repairs a sandbox stuck in Phase: Error
│   ├── nemoclaw-hermes-autostart.sh       # Self-healing start wrapper
│   ├── nemoclaw-hermes-autostop.sh        # Locked, early clean stop wrapper
│   └── nemoclaw-hermes-shutdown-monitor.sh # systemd-inhibit pre-shutdown watcher
└── systemd/                               # systemd user units
    ├── nemoclaw-openshell-gateway.service # Gateway daemon (only for NemoClaw versions that do not install their own)
    ├── nemoclaw-hermes-start.service      # Starts and cleanly stops the Hermes sandbox
    ├── nemoclaw-hermes-shutdown-watch.service # Delays shutdown until Hermes has stopped
    └── google-bridge.service              # (optional) Google Bridge companion
```

---

## 2. Building Hermes (`setup-hermes.sh`)

One run installs NemoClaw, creates the sandbox, and sets up Provider v2, egress, the Hermes settings, and kakeratta's skill, wake gate and cron. If it stops partway, running the same command again converges on the same state.

| Runs as | Model | Route |
| :--- | :--- | :--- |
| Parent (persona answers, persona asks) | `nvidia/Nemotron-3.5-Super-VL` | `inference.local`, created by onboarding |
| Child (`@agent` direct requests, delegation) | `nvidia/Nemotron-3-Ultra-550b-a55b` | Token Factory directly over Provider v2 (the model name is kept) |

Create these two files first (both `chmod 600`; keep the values out of shell history):

```bash
# ~/.config/nemoclaw/tokenfactory.env
TOKENFACTORY_API_KEY=...

# ~/.config/nemoclaw/kakeratta-mcp.env (issued in kakeratta under Settings → Advanced options → Integrations)
KAKERATTA_MCP_URL=https://kakeratta.net/api/mcp/users/<workspace>
KAKERATTA_MCP_TOKEN=...
```

```bash
scripts/nemoclaw/setup-hermes.sh
```

What the script does:

1. **Prerequisites**: Docker is reachable, and an unused `127.0.0.1` port refuses connections. Under WSL mirrored networking such a connection hangs instead, and NemoClaw's port probe fails with `Cannot allocate dashboard port: The OpenShell forward operation timed out`; set `networkingMode=nat` in `.wslconfig` (neither `wsl --update` nor `firewall=false` fixed it).
2. **NemoClaw and the sandbox**: runs the official installer at `NEMOCLAW_INSTALL_TAG` (default `v0.0.135`) non-interactively, with `custom` inference (Token Factory) and Super-VL as the parent model.
3. **Provider v2**: imports `profiles/tokenfactory.yaml`, creates the `tokenfactory-v2` provider and attaches it to the sandbox. **`providers_v2_enabled` is enabled at gateway-global scope (`--global`)**; the sandbox-scoped setting does not compose `_provider_tokenfactory_v2` into the policy. Provider credentials enter the sandbox when it starts, so it restarts the sandbox when needed.
4. **Egress**: allows the keyless web search backends Hermes uses (`mcp.exa.ai`, `search.parallel.ai`, `api.firecrawl.dev`, `api.keenable.ai`) for `hermes` and Python only. The sandbox has no route or name resolution around the proxy, so `no_proxy` cannot reach them. For kakeratta, an `https://` URL becomes NemoClaw managed MCP (`mcp add`; OpenShell injects the token), while an `http://` URL (local development) cannot, as managed MCP requires HTTPS: the destination is allowed by policy and the token is written to `config.yaml`.
5. **Hermes settings**: edits `/sandbox/.hermes/config.yaml` in place (never the hash files): `tool_search: "off"`, `agent.max_turns: 15`, the cron toolsets, and `delegation` (Ultra, Token Factory directly, `reasoning_effort: high`).
6. **Skill, wake gate and worker**: uploads them from `frontend/public/integrations/hermes/kakeratta/` in this repository and creates the one-minute cron job `kakeratta`.
7. **Checks**: that Ultra can be called by name from inside the sandbox, and that the wake gate's `list_asks` succeeds.

To connect to a local development kakeratta, start it with `KAKERATTA_ADDR=:8787 task dev` and use a host address the sandbox can reach (under WSL, the address from `ip -4 addr show eth0`). If the WSL address changes, update the URL and run the script again. `list_asks` returns 401 unless kakeratta's selected connection is the one this key belongs to.

Managed MCP over `https://` writes `Authorization: Bearer openshell:resolve:env:v<revision>_KAKERATTA_MCP_TOKEN` into `config.yaml`, and OpenShell replaces it with the real key in transit. `mcp update` cannot change the key, so after reissuing it in kakeratta, update `kakeratta-mcp.env`, run `nemohermes <sandbox> mcp remove kakeratta`, and run the script again.

The script was verified on 2026-10-21 with NemoClaw v0.0.135 / OpenShell 0.0.116 on WSL2 (ARM64, NAT) and Docker Desktop, against a local kakeratta over `http://`. The managed MCP path for `https://` was not part of that verification.

---

## 3. Components

### Scripts (`bin/`)

| File | Type | Role |
| :--- | :--- | :--- |
| `nemoclaw-wait-docker.sh` | Bash | Waits up to 60 seconds for the Docker daemon to respond, before the gateway service starts. |
| `nemoclaw-reconcile-sandbox.py` | Python 3 | Audits the OpenShell gateway's SQLite database (`openshell.db`). When a forced stop or power loss leaves the sandbox deadlocked in `Phase: Error` (3) or with an abnormal exit code, it rewrites the protobuf payload to `Phase: Ready` (2) and restarts the gateway. |
| `nemoclaw-hermes-autostart.sh` | Bash | The main start script at host startup: waits for Docker, removes stale locks, runs the reconciler before each attempt, starts the sandbox, and checks the web dashboard (port 18789). |
| `nemoclaw-hermes-autostop.sh` | Bash | Stops the sandbox safely. It holds a `flock` against concurrent runs, skips at once when the container is already stopped, and runs `nemoclaw hermes-sandbox stop` while Docker is still alive. |
| `nemoclaw-hermes-shutdown-monitor.sh` | Bash | Holds `systemd-inhibit --what=shutdown --mode=delay` and watches D-Bus for `PrepareForShutdown(true)`. On shutdown it delays power-off by up to 15 seconds so that **Hermes stops cleanly (Phase: Stopped / exit 0) before Docker goes down**. |

### systemd units (`systemd/`)

| Unit | Mode | Role |
| :--- | :--- | :--- |
| `nemoclaw-openshell-gateway.service` | `Type=simple` | Runs the OpenShell gateway daemon. |
| `nemoclaw-hermes-start.service` | `Type=oneshot`<br>`RemainAfterExit=yes` | Runs `nemoclaw-hermes-autostart.sh` at startup and `nemoclaw-hermes-autostop.sh` when stopped. |
| `nemoclaw-hermes-shutdown-watch.service` | `Type=simple` | Keeps `nemoclaw-hermes-shutdown-monitor.sh` running to avoid error exits from Docker stopping first at OS shutdown. |
| `google-bridge.service` | `Type=simple` | Runs the Google Bridge companion. `ConditionPathExists=%h/.config/google-bridge/config.json` starts it only where Google Bridge has been initialized; elsewhere it is skipped without an error. |

---

## 4. Defense in depth (no sudo)

1. **Normal restart or shutdown**:
   * `nemoclaw-hermes-shutdown-watch.service` receives the OS shutdown notice.
   * It has logind delay power-off by up to 15 seconds and stops Hermes before the Docker daemon begins stopping.
   * This prevents the error exit (exit 143) itself.
2. **Sudden power loss or crash**:
   * If the sandbox is left in `Phase: Error`, `nemoclaw-reconcile-sandbox.py`, run through `nemoclaw-hermes-autostart.sh` at the next start, detects it.
   * It resets the status in the database to `Phase: Ready` and the sandbox starts again.

---

## 5. Installing the autostart units

Run from the repository root or this directory:

```bash
# 1. Link into the user environment (~/.local/bin, ~/.config/systemd/user)
#    (a gateway unit installed by NemoClaw is kept)
cd scripts/nemoclaw
./install.sh

# 2. Enable and start the systemd user services
systemctl --user enable --now nemoclaw-openshell-gateway.service   # already enabled when NemoClaw installed it
systemctl --user enable --now nemoclaw-hermes-shutdown-watch.service
systemctl --user enable --now nemoclaw-hermes-start.service

# (Only with Google Bridge)
systemctl --user enable --now google-bridge.service

# 3. Check them
systemctl --user status nemoclaw-openshell-gateway.service
systemctl --user status nemoclaw-hermes-start.service
systemctl --user status nemoclaw-hermes-shutdown-watch.service
# (With Google Bridge)
systemctl --user status google-bridge.service

# 4. Keep user services running without a login session
loginctl enable-linger "$USER"
```

From NemoClaw v0.0.135 the installer places its own `nemoclaw-openshell-gateway.service`, which also generates TLS certificates. `install.sh` keeps it (printing `[KEEP]`) and places this repository's unit of the same name only for older NemoClaw versions that do not install one. `nemoclaw-hermes-start.service` uses `~/.local/bin/nemoclaw`, a wrapper that selects its own Node, so the unit does not name one.

Under WSL, signing in to Windows starts neither the WSL distribution nor Docker Desktop. To start Hermes at sign-in, turn on Docker Desktop's "Start Docker Desktop when you sign in" and start the distribution separately, for example with a Windows Task Scheduler task. Otherwise, start Docker Desktop and open the distribution by hand; the enabled units then start the sandbox. On 2026-10-21 enabling and starting the services was verified on WSL2 (ARM64, NAT) with Docker Desktop; a full machine restart was not.

---

## 6. Operating commands

```bash
# Sandbox status
nemoclaw hermes-sandbox status

# Status from OpenShell
openshell sandbox list

# Web dashboard (port 18789)
curl -I http://127.0.0.1:18789/

# Run the repair by hand
python3 ~/.local/bin/nemoclaw-reconcile-sandbox.py
```

---

## 7. Recommended Hermes settings (`/sandbox/.hermes/config.yaml`)

Settings for running direct requests with file operations and autonomous work reliably. Onboarding sets `model`; `setup-hermes.sh` sets the rest. `delegation` points `base_url` at Token Factory directly over Provider v2; with `inference.local` the model would be replaced by the gateway's Super:

```yaml
model: # set by onboarding (OpenShell relays it to Token Factory)
  provider: custom
  base_url: https://inference.local/v1
  default: nvidia/Nemotron-3.5-Super-VL

delegation:
  provider: custom
  base_url: https://api.tokenfactory.nebius.com/v1
  api_key: ${TOKENFACTORY_API_KEY}
  model: nvidia/Nemotron-3-Ultra-550b-a55b
  reasoning_effort: high

tools:
  tool_search:
    enabled: "off" # direct disclosure (avoids broken JSON escaping)

agent:
  max_turns: 15 # for persona asks; direct requests run Ultra in a separate worker

platform_toolsets:
  cron: # lets direct requests use files, autonomous work and delegation
    - web
    - file
    - terminal
    - code_execution
    - delegation
```
