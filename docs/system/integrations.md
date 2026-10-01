---
type: "Integration"
title: "External agents, tools, and sharing"
description: "External-agent answers over MCP, permitted external tools, weather forecasts, and selected publication to Bluesky."
tags: ["mcp", "agent", "tools", "bluesky", "sharing", "weather"]
updated: "2026-10-01"
---

External-agent answers over MCP, permitted external tools, weather forecasts, and selected publication to Bluesky.

# External-agent answers

## Connection and role

The user creates an MCP connection and selects `external` as a persona's planner. The server exposes `/api/mcp/users/{workspace}` per workspace and manages connection keys and settings. This is separate from the application's MCP client for calling external tools.

The external agent researches as the persona and submits only the final answer. The host's subsequent processing decides, reviews, and executes schedules and other actions, then updates the persona's memory itself. `accepted` acknowledges receipt of the answer; it does not mean timeline publication is complete.

## Ask lifecycle

1. Use `list_asks` to find pending asks with deadlines.
2. Send the ask ID and a stable `claimId` to `claim_ask` to receive the persona, trigger, conversation, memory (`memory`: field summaries whole and the latest kakera records), and available read tools.
3. Research through permitted `read_for_ask` calls: `records` steps (`tag`, `query`, `before`, or `ids` - up to 10 - to read the entries records point to, which may then be cited) and read-use tools.
4. Submit text and sources with `submit_answer`. Invalid submissions receive correction requests.
5. If unable to respond, call `release_ask` to return the turn to the built-in model.

Claiming and answering have separate deadlines. Unclaimed asks, answer timeouts, connection changes, explicit release, and exhausted correction attempts can trigger fallback. A connection with no recent MCP activity, or paused external-agent settings, can select the built-in model immediately.

An ask is an `asks` runtime record, not a waiting goroutine: opening it ends the turn, and claim, reads, answer, and release are transactions on the record, so the agent may reach any process serving the workspace and an ask survives a restart. The agent's reads run where they arrive, under the lock `ask:{id}` (a read stops if the lock is lost), and their results - reads used, lookups, cited posts - are kept on the record. Deadlines are enforced through the workspace's alarm: opening an ask asks to be woken at its claim wait, claiming it at its plan wait, and the due reaction closes what has run out (see [Times and wakes](./architecture.md#times-and-wakes)). A closed ask queues the reaction `ask-{id}` in the conversation's lane, which carries the turn on and deletes the ask. The agent's last call is the `presence` record, written at most every 30 seconds. An MCP request opens a workspace only if it exists.

## Implementation and verification

- [planner.go](../../internal/persona/planner.go): ask types and context
- [asks.go](../../internal/persona/asks.go): `openAsk`, `ClaimAsk`, `ReadForAsk`, `SubmitAskAnswer`, `sweepAsks`, `resumeAsk`
- [mcp_server.go](../../internal/server/mcp_server.go)
- [mcp_tools.go](../../internal/server/mcp_tools.go)
- [mcp_connections.go](../../internal/server/mcp_connections.go)
- [planner_test.go](../../internal/persona/planner_test.go)
- [hermes.md](../hermes.md)

# Persona tools and external MCP

## Selecting capabilities

`Registry` holds built-in tools and tools from operator-configured MCP servers. `For(skill.Tools).As(skill.ID)` builds the persona's permitted set. Shared timeline search and access to the persona's own memory are also available. A persona permitted to use `schedule` automatically receives the read-only `schedules_list` tool, including in external-agent asks, to inspect filtered, paginated workspace schedules on demand.

A tool `Declaration` carries a name, description, JSON Schema, and read-only status. Research tools are separated from schedule and action tools. The host validates model-supplied arguments and makes the actual network requests.

## Connections

| Tool or path | Configuration and implementation |
| --- | --- |
| `web_search` | User search key and Tavily client |
| `place_search`, `route` | Operator map endpoints |
| `weather_forecast` | Operator WeatherAPI key and base URL; read-only current weather and forecasts |
| `timeline_search` | Workspace search Service |
| `schedule`, `schedules_list` | User schedules, persona names, and time zone; only `schedules_list` is a research read |
| `server.tool` | External MCP configured through `KAKERATTA_MCP_SERVERS` |

The external MCP Catalogue caches tool lists and refreshes them as needed. Some built-in map names prefer corresponding MCP tools when configured. Returned external text is model input data, not an instruction to expand permissions.

## Weather forecasts

`weather_forecast` is a declared read-only tool available during built-in research and external-agent `read_for_ask`. It calls WeatherAPI's `forecast.json` endpoint over HTTPS by default. `query` accepts a place name or coordinates; when omitted, the post's coordinates are used. Without either, the call fails rather than looking up the server's location; `auto:ip` is refused. Days default to three and must be an integer from 1 through 14. An optional `date` selects a `YYYY-MM-DD` forecast date, `hour` selects an integer local hour from 0 through 23, and `lang` is `ja` (default) or `en`. Availability depends on the provider's subscription. A shorter returned period includes a warning; a requested date absent from the response is an error.

Responses contain the matched location and timezone, current weather with its update time, and daily temperatures, rainfall/probability, wind, UV, and conditions. Hourly values are included only for a requested hour. Icons, unrelated API fields, and unrequested hourly data are omitted. Source attribution points to WeatherAPI.com without a credential-bearing API URL. Unknown fields are not passed through. HTTP, provider, transport, malformed-response, and missing-configuration failures remain errors rather than empty or invented forecasts. Requests have the shared 25-second HTTP timeout, bounded response decoding, and do not follow redirects. Provider error text and HTTP request URLs are not exposed because they may include the key.

The operator configures `KAKERATTA_WEATHERAPI_KEY`, or `KAKERATTA_WEATHERAPI_KEY_FILE`; a direct key takes precedence. Without either, an existing `tmp/weatherapi.key` relative to the process working directory is used for local development. An absent default file leaves weather unconfigured; an explicitly unreadable or empty file fails configuration. `KAKERATTA_WEATHERAPI_URL` defaults to `https://api.weatherapi.com/v1`. Keys stay in server-side tool configuration, outside persona settings, tool schemas, and browser responses. `tmp/` is excluded from Git and the Docker build context. Production Terraform and Cloud Build inject the existing `kakeratta-wheatherapi-key` secret through `KAKERATTA_WEATHERAPI_KEY`; see [Deployment](./operations.md#deployment).

The [WeatherAPI documentation](https://www.weatherapi.com/docs/) defines forecast parameters, response fields, and provider error codes.

## Implementation and verification

- [tools.go](../../internal/tools/tools.go)
- [config.go](../../internal/tools/config.go)
- [weather.go](../../internal/tools/weather.go): WeatherAPI requests, validation, projection, and safe errors
- [weather_test.go](../../internal/tools/weather_test.go): forecasts, location fallback, filtering, provider failures, credentials, and redirects; opt-in live check
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
