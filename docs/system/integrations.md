---
type: "Integration"
title: "External agents, tools, and sharing"
description: "External-agent answers over MCP, permitted tools and Geo MCP availability, the shipped specification, weather forecasts, and selected publication to Bluesky."
tags: ["mcp", "agent", "tools", "support", "bluesky", "sharing", "weather"]
updated: "2026-10-06"
---

External-agent answers over MCP, permitted tools and Geo MCP availability, the shipped specification, weather forecasts, and selected publication to Bluesky.

# External-agent answers

## Connection and role

The user creates an MCP connection, enables external-agent use in settings, and selects `external` as a persona's planner. The preference control is shown only with a configured connection and external-agent use enabled; saved preferences survive disabling use. The server exposes `/api/mcp/users/{workspace}` per workspace and manages connection keys and settings. This is separate from the application's MCP client for calling external tools.

The external agent researches as the persona and submits only the final answer. The host's subsequent processing decides, reviews, and executes schedules and other actions, then updates the persona's memory itself. `accepted` acknowledges receipt of the answer; it does not mean timeline publication is complete.

## Ask lifecycle

1. Use `list_asks` to find pending asks with deadlines.
2. Send the ask ID and a stable `claimId` to `claim_ask` to receive the persona, trigger, conversation, memory (`memory`: field summaries whole and the latest kakera records), and available read tools.
3. Research through permitted `read_for_ask` calls: `records` steps (`tag`, `query`, `before`, or `ids` - up to 10 - to read the entries records point to, which may then be cited) and read-use tools.
4. Submit text and sources with `submit_answer`. Invalid submissions receive correction requests; source IDs not found by this ask's research are dropped rather than refused.
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

`Registry` holds built-in tools and tools from operator-configured MCP servers. `For(skill.Tools).As(skill.ID)` builds the persona's permitted set. Shared timeline search and access to the persona's own memory are also available. A persona permitted to use `schedule` automatically receives the read-only `schedules_list` tool, including in external-agent asks, to inspect filtered, paginated workspace schedules on demand. The schedule creation argument `user_requested` distinguishes tasks explicitly requested at a time or recurrence from autonomous checks; listings and own-schedule context expose that flag and the effective `notify` preference. The host schedule phase can update an owned schedule's time or Push preference without replacing its identity; external agents still submit answers only. The notification preference is persisted with an external ask and restored when its answer or fallback resumes. The host's schedule phase and review choose and check the flag, including after an external-agent answer, and scheduled wakes use it to select task or brief-reminder instructions; see [Scheduled wakes](./personas.md#scheduled-wakes).

A tool `Declaration` carries a name, description, JSON Schema, and read-only status. Research tools are separated from schedule and action tools. The host validates model-supplied arguments and makes the actual network requests. Built-in tools apply their own argument checks. For external MCP tools, `checkArgs` checks required top-level keys and the basic types of non-null top-level values only; it is not a complete JSON Schema validator. Enum values, ranges, nested constraints, additional properties, and nullability remain the external server's responsibility. Tool schemas are passed to the model unchanged.

For built-in answer research, `Set.ReadAvailable` additionally filters out tools known to be unusable from their configuration or the current post: missing keys/services, unavailable place-search endpoints. A delegated MCP map tool does not require its built-in fallback endpoints to be configured. This availability check does not execute the tool or change the persona's permissions. The research host combines it with live search/lookup budgets to generate the per-round read schema and instructions; see [Research budgets and available reads](./personas.md#research-budgets-and-available-reads). External-agent tool declarations and execution-time argument validation retain their existing behavior.

## Connections

| Tool or path | Configuration and implementation |
| --- | --- |
| `web_search` | User search key and Tavily client |
| `place_search` | Operator name/nearby search endpoints |
| `weather_forecast` | Operator WeatherAPI key and base URL; read-only current weather and forecasts |
| `timeline_search` | Workspace search Service |
| `kakeratta_help` | Read-only OKF specification embedded from `docs/system/`; no credentials or external service |
| `schedule`, `schedules_list` | User schedules, persona names, and time zone; only `schedules_list` is a research read |
| `server.tool` | External MCP configured through `KAKERATTA_MCP_SERVERS` |

The external MCP Catalogue caches tool lists and refreshes them as needed. The current Geo MCP production service exposes `place_search` and `reverse_geocode` with routing disabled. kakeratta has no built-in `route` tool, public OSRM client, route-specific MCP forwarding, or OSRM endpoint configuration. `place_search` prefers a compatible MCP tool when configured and retains its existing place-search fallback. Generic external MCP tools still use their server-qualified names and schemas. Returned external text is model input data, not an instruction to expand permissions.

Verification: [removed_route_test.go](../../internal/tools/removed_route_test.go) confirms route is absent from the registry, cannot be executed, and makes no map-service request; [delegate_test.go](../../internal/tools/delegate_test.go) covers the retained place-search delegation and fallback.

## Automatic post location lookup

The host asynchronously calls an operator-configured `reverse_geocode` tool to label new posts with coordinates, independently of persona tool permissions. The geo-home-mcp contract accepts `{lat, lng, language}` (`ja` or `en`) and returns `structuredContent.found` and `place.{name,countryCode,geonameId,administrativeAreas}`. The country code and its localized country name are saved for search, without being appended to the display label. Administrative areas include `level` and localized `name`; level 1 is retained separately as the search region, and the host combines levels 2–4 with the settlement name using the same shared parser as the backfill command. Japanese alternate names are used when available, with the source name as the geo service fallback. Labels describe the nearest settlement, not an address or administrative-area determination. The browser attributes GeoNames under CC BY 4.0. No separate geocoding service is used as fallback. See [post location labels](./backend.md#post-location-labels) for timeout, persistence, and failure behavior. [Catalogue.ToolsContext](../../internal/mcp/catalogue.go) bounds initial discovery using the caller's context; existing callers use `Tools`.

## Web search and news freshness

`web_search` accepts `query` and optional `topic` (`general` or `news`), `time_range` (`day`, `week`, `month`, `year`), and publication-date bounds `start_date` / `end_date` (`YYYY-MM-DD`). The tool rejects empty or non-string filters, unknown categories or periods, invalid dates, reversed date bounds, and a relative period combined with date bounds before making a provider request. `Client.SearchWithOptions` validates the options and forwards them to Tavily; `Client.Search` keeps the ordinary unfiltered search used by settings key verification.

News searches without any period default to `time_range: day`. Explicit relative periods or date bounds take precedence over this default. Tool instructions ask personas to use `topic: news` with `time_range: day` for latest-news requests and morning news digests, honor a user-specified period, and disclose when they widen the search. Category selection is performed by the persona, not by matching words in the query. Ordinary searches remain unfiltered unless a period is supplied. These filters use the provider's publication-date semantics, rather than a host-enforced guarantee of exact hours or complete news coverage; see [Tavily's date-filter guide](https://help.tavily.com/articles/3347142954-best-practices).

Provider `published_date` values are retained in results and shown with source URLs in research text when available. No publication date is invented when absent. Instructions distinguish publication time from search rank and do not present undated results as today's news. The same declared tool and validation serve built-in research and external-agent reads. Verification: [web_test.go](../../internal/tools/web_test.go) exercises requests through the tool to the HTTP transport, default daily news, explicit weekly and historical periods, ordinary unfiltered searches, returned publication dates and sources, and rejection before network access.

## Reading the kakeratta specification

`kakeratta_help` reads the shipped OKF v0.2 bundle. An omitted `path` returns `index.md`; an explicit path must be one of the seven root Markdown files listed in its schema. The result includes the file name and the entire chapter, including frontmatter and implementation review dates. Unknown files, non-string or empty paths, absolute paths, and traversal are refused. It reads only the embedded bundle, requires no workspace or API key, and exposes neither the host filesystem nor the user's configuration. The internal built-in name is not delegated to external MCP providers. Personas must declare it to use it; research and external-agent reads use the same permission checks as other read-only tools.

## Weather forecasts

`weather_forecast` is a declared read-only tool available during built-in research and external-agent `read_for_ask`. It calls WeatherAPI's `forecast.json` endpoint over HTTPS by default. `query` accepts a place name or coordinates; when omitted, the post's coordinates are used. Without either, the call fails rather than looking up the server's location; `auto:ip` is refused. Days default to three and must be an integer from 1 through 14. An optional `date` selects a `YYYY-MM-DD` forecast date, `hour` selects an integer local hour from 0 through 23, and `lang` is `ja` (default) or `en`. Availability depends on the provider's subscription. A shorter returned period includes a warning; a requested date absent from the response is an error.

Responses contain the matched location and timezone, current weather with its update time, and daily temperatures, rainfall/probability, wind, UV, and conditions. Hourly values are included only for a requested hour. Icons, unrelated API fields, and unrequested hourly data are omitted. Source attribution points to WeatherAPI.com without a credential-bearing API URL. Unknown fields are not passed through. HTTP, provider, transport, malformed-response, and missing-configuration failures remain errors rather than empty or invented forecasts. Requests have the shared 25-second HTTP timeout, bounded response decoding, and do not follow redirects. Provider error text and HTTP request URLs are not exposed because they may include the key.

The operator configures `KAKERATTA_WEATHERAPI_KEY`, or `KAKERATTA_WEATHERAPI_KEY_FILE`; a direct key takes precedence. Without either, an existing `tmp/weatherapi.key` relative to the process working directory is used for local development. An absent default file leaves weather unconfigured; an explicitly unreadable or empty file fails configuration. `KAKERATTA_WEATHERAPI_URL` defaults to `https://api.weatherapi.com/v1`. Keys stay in server-side tool configuration, outside persona settings, tool schemas, and browser responses. `tmp/` is excluded from Git and the Docker build context. Production Terraform and Cloud Build inject the existing `kakeratta-wheatherapi-key` secret through `KAKERATTA_WEATHERAPI_KEY`; see [Deployment](./operations.md#deployment).

The [WeatherAPI documentation](https://www.weatherapi.com/docs/) defines forecast parameters, response fields, and provider error codes.

## Implementation and verification

- [kakeratta_help.go](../../internal/tools/kakeratta_help.go): reading the shipped specification
- [kakeratta_help_test.go](../../internal/tools/kakeratta_help_test.go): chapter retrieval, bundle links, path validation, and declared permissions
- [bundle.go](./bundle.go): embedded OKF bundle
- [tools.go](../../internal/tools/tools.go): permissions, declarations, and `ReadAvailable`
- [read_availability_test.go](../../internal/tools/read_availability_test.go): configuration, location, permissions, and MCP delegation
- [config.go](../../internal/tools/config.go)
- [weather.go](../../internal/tools/weather.go): WeatherAPI requests, validation, projection, and safe errors
- [weather_test.go](../../internal/tools/weather_test.go): forecasts, location fallback, filtering, provider failures, credentials, and redirects; opt-in live check
- [mcp.go](../../internal/tools/mcp.go)
- [client.go](../../internal/mcp/client.go)
- [catalogue.go](../../internal/mcp/catalogue.go)
- [websearch.go](../../internal/websearch/websearch.go): Tavily search options, validation, requests, and publication dates
- [web.go](../../internal/tools/web.go): Web search schema, freshness instructions, and argument forwarding
- [web_test.go](../../internal/tools/web_test.go): provider filters, publication dates, source links, and invalid inputs
- [mcp_test.go](../../internal/tools/mcp_test.go)

# Selected sharing to Bluesky

## Publication plan

The frontend selects entries to publish and sends the root post, target, and selected IDs to `previewSocial`. The server builds `PlanSelectionWithImages` from the current thread, resolving persona names, text, images, split posts, and the connected account. Image answers (`ImageAnswer` in [html.go](../../internal/social/html.go)) require browser-rendered `htmlImages` pages keyed by entry ID. An image answer is either a complete persona HTML document, or the `infographic` persona's Markdown with a heading outside code fences (`InfographicMarkdown`, the same rule the timeline uses to lay it out). The browser's `imageSource` ([infographic-document.js](../../frontend/src/infographic-document.js)) builds the matching document: HTML as it stands, or the infographic laid out by `renderInfographic` with its Mermaid SVG, inside a standalone document carrying the app's same-origin CSS rules without `prefers-color-scheme: dark` rules or `@import` (light theme always). The existing isolated capture then renders it. HTML fragments, user-authored code, and the infographic's prose without headings remain text. Each capture carries a SHA-256 hash of the original entry text. Missing captures or a source hash that differs from the current entry are rejected before publication. Raw HTML never becomes the body of a persona image post.

## Rich text

Bluesky has no markup; a post's rich text is facets over UTF-8 byte ranges. Persona answers that are not image answers (Markdown) go through `FromMarkdown` ([richtext.go](../../internal/social/richtext.go)), which converts them to plain text that reads as the answer:

- Headings become their own line after a blank line, and unordered items become `・` (nested items are indented with ideographic spaces).
- Ordered items keep their numbers, and tasks become ☑/☐.
- A table becomes its header row, then one `・` row per line, with cells joined by ` ｜ `.
- A quotation becomes a `＞` line, and a callout becomes its title.
- Mermaid flowcharts and sequence diagrams become `・A → B（label）` / `・A → B: message` lines; notes become `・📝 note`. They read the same syntax as the timeline renderer: multi-line and `<br>` labels joined with spaces, pipe labels, chains, `&` fan-out, skipped subgraph/style lines, continued message lines, and skipped control blocks. Other diagram types are omitted. Other code fences keep their lines.
- Emphasis, deletion and inline-code markers are removed.
- `[label](http…)` keeps only the label, with a link facet to the URL; other schemes stay plain text.

User posts keep their text exactly. In every post, bare URLs (including appended links and sources) also become link facets.

`SplitRich` ([split.go](../../internal/social/split.go)) treats each link label and URL as one unbreakable unit, which is never a sentence boundary. It returns each part's links as offsets into that part's text, prefix included. Out-of-order or out-of-range input facets are left as plain text. `Post.Links` (`start`, `end`, `uri`) is part of the sealed plan digest. `Publish` sends them as `app.bsky.richtext.facet#link` facets (`byteStart`, `byteEnd`), skipping any outside the text or not http(s). The preview dialog renders the same ranges as links (`richText` in [social.js](../../frontend/src/social.js)). Verification: [richtext_test.go](../../internal/social/richtext_test.go) and [social.test.js](../../tests/social.test.js).

A digest seals the plan. Publishing rebuilds it and compares the digest so changed content or accounts are not published after preview approval. The selection initially contains only the original post, but every entry can be deselected; at least one entry is required. Answers can be published without the original post. No other entry is selected automatically. The first selected entry in timeline/parent order becomes the publication plan root and is standalone. If a selected reply's direct parent is also selected, it stays attached to that parent's final part; otherwise it attaches to the first selected entry's final part. Omitted parents are never published, and the preview shows the resulting reply relationships. The publish action stays hidden until a successful preview is ready, and changing the selection hides it again. The digest includes image data, alternative text, and the source hash, so altering a capture or its source invalidates approval. Preview and publication bodies are limited to 32 MiB. JPEG/PNG captures must decode with valid dimensions and at most 50 million pixels, then use the existing Bluesky image preparation.

## Authentication and publication state

Bluesky connections use OAuth. Callback information and the browser secret are combined to complete the connection. Durable connection state is managed through Workspace social values.

Publication history is keyed by target, account, and post, and each publication is claimed before sending. Already published items are not resent, supporting retries after partial progress. Publication records retain the actual Bluesky root URI and CID; extensions use their saved parent's thread root even if a later selection omits the original post. Older records without those fields resolve their root from the original post's history. Reusing a published answer does not move or repost it into a new thread. Images are loaded and prepared for Bluesky before publishing. Partial success is possible; publication as a whole is not atomic. HTML captures travel as temporary data URLs in the preview/publication requests, are neither stored as timeline media nor written to blob storage, and do not modify the saved answer. The first image uses the entry history key; subsequent image pages use `:image:2` and so on, form a continuation chain, and the entry's replies attach to its final page. Source URLs remain text posts when they need splitting. Image uploads include the capture's alternative text.

## Implementation and verification

- [social.go](../../internal/server/social.go)
- [bluesky.go](../../internal/server/bluesky.go)
- [plan.go](../../internal/social/plan.go)
- [richtext.go](../../internal/social/richtext.go), [split.go](../../internal/social/split.go): Markdown-to-text conversion, link facets, and facet-aware splitting
- [richtext_test.go](../../internal/social/richtext_test.go): conversion, split offsets, plan facets, and published facet records
- [html.go](../../internal/social/html.go): complete-document and infographic detection (`ImageAnswer`), and temporary image input
- [infographic-document.js](../../frontend/src/infographic-document.js), [infographic-document.test.js](../../tests/infographic-document.test.js): standalone infographic documents for capture
- [html_test.go](../../internal/social/html_test.go): source binding, image digests, and reply chains
- [oauth_store.go](../../internal/social/oauth_store.go)
- [social.go](../../internal/store/social.go)
- [social.js](../../frontend/src/social.js)
- [Bluesky.stories.js](../../frontend/stories/Bluesky.stories.js): original-only selection, answer-only preview, and HTML capture stories
- [plan_test.go](../../internal/social/plan_test.go), [social.test.js](../../tests/social.test.js): answer-only selection, optional parents, selected-only publication, and preview-gated publishing
- [html-image.js](../../frontend/src/html-image.js): isolated browser rendering and pagination
- [social_test.go](../../internal/server/social_test.go)
- [social-sharing.md](../social-sharing.md)

# Related chapters

- [API and persistent state](./backend.md)
- [Persona execution and memory](./personas.md)
- [Browser application and device state](./frontend.md)
