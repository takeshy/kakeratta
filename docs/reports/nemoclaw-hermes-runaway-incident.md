# Hermes Agent Runaway Loop Incident (Hermes-4-405B)

Date: 2026-09-27 to 2026-09-28 (JST)
Environment: NemoClaw OpenShell sandbox (`hermes-sandbox`), Nebius Token Factory (`eu-north1`)

[Integration report](nemoclaw-hermes-integration.md) · [Model comparison](nemoclaw-hermes-model-comparison.md)

This is a post-incident analysis of a period in which Hermes Agent, running on `NousResearch/Hermes-4-405B`, was repeatedly woken without usable MCP tools and spent its turn budget debugging its own environment. It records only what was measured from the bill and the sandbox logs.

## Cost

Final Token Factory charges for `Hermes-4-405B`:

| Item | Tokens | Charge |
| :--- | ---: | ---: |
| Input | 34.54M | $34.54 |
| Output | 0.15M | $0.44 |
| **Total** | — | **$34.98** |

The spend kept growing while the cause was being investigated and stopped once `Hermes-4-405B` was no longer used. The session audit below was taken partway through, so it covers fewer sessions than the bill.

Input was billed at $1.00 per million tokens. Hermes logged high prompt-cache rates on this model (for example `cache=93%` on one call), but whether cached input was discounted is not visible in the charges above, so this report does not claim either way.

## Cause

1. **Policy eviction.** Adding the egress preset `personal-open-internet` to the sandbox replaced the composed policy (revision 1) with an uncomposed one (revision 2). The `mcp_bridge_kakeratta` network policy, which holds the credential binding (`credential_binding: { provider: "hermes-sandbox-mcp-kakeratta" }`), was dropped.
2. **Credential failure.** Without the binding, OpenShell's L7 proxy could not resolve the token placeholder (`openshell:resolve:env:...`) and answered the Wake Gate's call to `kakeratta.net` with:
   ```text
   HTTP 500 {"error": "credential_unavailable", "message": "Credential placeholder could not be resolved"}
   ```
3. **Fail-open Wake Gate.** `check_kakeratta_gate.py` returned `{"wakeAgent": true}` on any error, so the HTTP 500 woke Hermes every minute.
4. **Self-debugging loop.** Once awake, Hermes found the `mcp__kakeratta__*` tools unavailable for the same reason. Instead of stopping, it investigated why, running web searches over GitHub and technical documentation until it reached the 60-turn limit. Each turn re-read the accumulated search history, so almost all of the spend was input.

## Session audit (partway through)

From `/sandbox/.hermes/logs/agent.log` and `/sandbox/.hermes/cron/usage_audit.jsonl`, 270 sessions:

| Turns | Sessions |
| :--- | ---: |
| 60 (limit reached) | 20 |
| 16–59 | 19 |
| 1–15 | 231 |

A single 60-turn session used between 1,129,946 and 3,421,088 tokens. Sessions of 16–59 turns also contributed; this report does not break the total down by session.

## Remediation

- The Wake Gate now fails closed: it wakes Hermes only after confirming a pending ask, and returns `{"wakeAgent": false}` on any error or missing configuration.
- `agent.max_turns: 15` is set in `/sandbox/.hermes/config.yaml`.
- The composed policy, including `mcp_bridge_kakeratta` and its credential binding, was restored with `openshell policy set`.
- `Hermes-4-405B` is no longer used for this sandbox.

## Lessons

- A wake-up check that fails open turns an infrastructure error into repeated inference spend. For a scheduled agent, an error should mean "do not wake".
- An agent that cannot use its tools will try to fix its environment. A turn limit bounds that.
- After any policy change, confirm that composed entries are still present (`openshell policy get`) before assuming the sandbox works.
- Token Factory had no hard spending limit that would have stopped this.
