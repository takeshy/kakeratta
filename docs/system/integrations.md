---
type: "Integration"
title: "External agents, tools, and sharing"
description: "External-agent answers over MCP, permitted external tools, and selected publication to Bluesky."
tags: ["mcp", "agent", "tools", "bluesky", "sharing"]
updated: "2026-09-30"
---

External-agent answers over MCP, permitted external tools, and selected publication to Bluesky.

# External-agent answers

## Connection and role

The user creates an MCP connection and selects `external` as a persona's planner. The server exposes `/api/mcp/users/{workspace}` per workspace and manages connection keys and settings. This is separate from the application's MCP client for calling external tools.

The external agent researches as the persona and submits only the final answer. The host's subsequent processing decides, reviews, and executes memory, schedules, and other actions. `accepted` acknowledges receipt of the answer; it does not mean timeline publication is complete.

## Ask lifecycle

1. Use `list_asks` to find pending asks with deadlines.
2. Send the ask ID and a stable `claimId` to `claim_ask` to receive the persona, trigger, conversation, memory index, and available read tools.
3. Research through permitted `read_for_ask` calls.
4. Submit text and sources with `submit_answer`. Invalid submissions receive correction requests.
5. If unable to respond, call `release_ask` to return the turn to the built-in model.

Claiming and answering have separate deadlines. Unclaimed asks, answer timeouts, connection changes, explicit release, and exhausted correction attempts can trigger fallback. Shutdown aborts waiting. A connection with no recent MCP activity, or paused external-agent settings, can select the built-in model immediately.

Asks live in the memory of the instance that owns the workspace. With several instances, every `/api/mcp/users/{workspace}` request is carried to that owner, so an agent may reach any instance; an MCP request never makes an instance take a workspace over, and answers 503 while nobody holds it. A takeover ends waiting asks like a restart does.

## Implementation and verification

- [planner.go](../../internal/persona/planner.go): `desk`, `ClaimAsk`, `planExternally`
- [mcp_server.go](../../internal/server/mcp_server.go)
- [mcp_tools.go](../../internal/server/mcp_tools.go)
- [mcp_connections.go](../../internal/server/mcp_connections.go)
- [planner_test.go](../../internal/persona/planner_test.go)
- [hermes.md](../hermes.md)

# Persona tools and external MCP

## Selecting capabilities

`Registry` holds built-in tools and tools from operator-configured MCP servers. `For(skill.Tools).As(skill.ID)` builds the persona's permitted set. Shared timeline search and access to the persona's own memory are also available.

A tool `Declaration` carries a name, description, JSON Schema, and read-only status. Research tools are separated from schedule and action tools. The host validates model-supplied arguments and makes the actual network requests.

## Connections

| Tool or path | Configuration and implementation |
| --- | --- |
| `web_search` | User search key and Tavily client |
| `place_search`, `route` | Operator map endpoints |
| `timeline_search` | Workspace search Service |
| `schedule` | User schedules and time zone |
| `server.tool` | External MCP configured through `KAKERATTA_MCP_SERVERS` |

The external MCP Catalogue caches tool lists and refreshes them as needed. Some built-in map names prefer corresponding MCP tools when configured. Returned external text is model input data, not an instruction to expand permissions.

## Implementation and verification

- [tools.go](../../internal/tools/tools.go)
- [config.go](../../internal/tools/config.go)
- [mcp.go](../../internal/tools/mcp.go)
- [client.go](../../internal/mcp/client.go)
- [catalogue.go](../../internal/mcp/catalogue.go)
- [websearch.go](../../internal/websearch/websearch.go)
- [mcp_test.go](../../internal/tools/mcp_test.go)

# Selected sharing to Bluesky

## Publication plan

The frontend selects entries to publish and sends the root post, target, and selected IDs to `previewSocial`. The server builds `PlanSelection` from the current thread, resolving persona names, text, images, split posts, and the connected account.

A digest seals the plan. Publishing rebuilds it and compares the digest so changed content or accounts are not published after preview approval. Unselected replies are not added automatically.

## Authentication and publication state

Bluesky connections use OAuth. Callback information and the browser secret are combined to complete the connection. Durable connection state is managed through Workspace social values.

Publication history is keyed by target, account, and post, and each publication is claimed before sending. Already published items are not resent, supporting retries after partial progress. Images are loaded and prepared for Bluesky before publishing. Partial success is possible; publication as a whole is not atomic.

## Implementation and verification

- [social.go](../../internal/server/social.go)
- [bluesky.go](../../internal/server/bluesky.go)
- [plan.go](../../internal/social/plan.go)
- [oauth_store.go](../../internal/social/oauth_store.go)
- [social.go](../../internal/store/social.go)
- [social.js](../../frontend/src/social.js)
- [social_test.go](../../internal/server/social_test.go)
- [social-sharing.md](../social-sharing.md)

# Related chapters

- [API and persistent state](./backend.md)
- [Persona execution and memory](./personas.md)
- [Browser application and device state](./frontend.md)
