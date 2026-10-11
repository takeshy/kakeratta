---
type: "Component"
title: "API and persistent state"
description: "Authentication including Alexa account linking, entry handling, timeline data, persistence, settings, profile and avatar validation, model usage logging and monthly cost accounting, schedule editing, reminder push delivery, favorites, and search."
tags: ["http", "auth", "entry", "capture", "timeline", "pagination", "store", "firestore", "settings", "llm", "usage", "search", "index", "push", "favorites", "profile", "avatar"]
updated: "2026-10-11"
---

Authentication, entry handling, timeline data, persistence, settings, profile and avatar validation, model usage logging and monthly cost accounting, schedule editing, reminder push delivery, favorites, and search.

# HTTP API and authentication

## Resolving the user

In a hosted deployment, `Gateway` verifies Firebase sessions. The browser sends an ID token to `/api/auth/session`, and the server issues a `__session` cookie. It derives a workspace key from the user's UID and forwards requests to that user's `Server`. Combined mode restores pending workspaces on startup. Split web/worker mode opens workspaces on demand and executes queued jobs through authenticated push requests; it does not run the restoration worker.

`Server.guard` compares `X-Kakeratta-Workspace` with the current workspace. For SSE, it uses the `workspace` query parameter. This key detects account mixups; `Gateway` verifies Firebase sessions. `guard` itself does not authenticate requests that omit the key. For every method other than GET, HEAD, and OPTIONS it also refuses a request whose `Origin` is not the page's own (`sameOrigin`, which honours `X-Forwarded-Host` and `KAKERATTA_PUBLIC_URL`), so another site cannot post into or reconfigure a local app that has no login. Requests without `Origin`, such as tools and tests, pass.

Any instance of a shared deployment answers any request; nothing is routed to a particular instance (see [Multiple instances](./architecture.md#multiple-instances)).

## Handler organization

| File | Main APIs |
| --- | --- |
| `routes.go` | Paths and handler registration |
| `entries.go` | Posts, replies, edits, deletions, images, URL fetching |
| `favorites.go` | Favorite entries, recent favorites, and speaker filtering |
| `personas.go` | Listing, activation, installation, removal, schedule editing, daily and planner settings |
| `memory.go` | Memory files (fields and monthly records): listing with a record-tag filter, reading, editing, deletion; one fact edited or forgotten (`editFact`, `forgetFact`) |
| `settings.go` | Settings, model lists, search connection checks, version information |
| `routing.go`, `preview.go` | Routing and reply checks and application |
| `social.go`, `bluesky.go` | Sharing previews, publishing, OAuth |
| `mcp_server.go`, `mcp_tools.go` | MCP for external agents |
| `alexa.go`, `alexa_auth.go` | Signed Alexa requests, dedicated OAuth linking, voice posts and read-aloud |

`readJSONLimit` uses `http.MaxBytesReader` to bound input. `fail` produces JSON errors and `writeJSON` produces responses. The API server does not serve the frontend; unknown paths receive a JSON 404.

Alexa has a separate public Gateway boundary at `POST /api/alexa` and `POST /api/alexa/token`; it verifies Alexa signatures or OAuth client credentials, then resolves only an existing workspace. Browser-authenticated `POST /api/alexa/authorize` and `GET`/`DELETE /api/alexa/connection` grant, inspect and revoke this access. These credentials do not replace Firebase sessions. See [Alexa custom skill](./integrations.md#alexa-custom-skill) for token storage and verification. Both browser `land` and Alexa submissions use `acceptEntry` for atomic entry/routing acceptance and duplicate detection.

## Schedule editing

Guarded `PUT /api/personas/{id}/schedules/{schedule}` accepts a partial update with `at` (a future RFC3339 instant) and/or `notify` (boolean). Unknown fields, invalid values, empty updates, and trailing JSON are rejected with 400. Missing schedules and schedules owned by another persona return 404. The store applies the patch under its file mutex or a Firestore transaction, preserving ID, owner, recurrence, timezone, task origin, note, and person-event metadata. A claimed one-off cannot be recreated by editing it. A successful edit rearms the workspace and returns the refreshed persona list. The existing DELETE endpoint still cancels a schedule.

`Schedule.Notify` is optional: missing/null means notifications enabled for older data. `false` suppresses Push only; the scheduled turn still runs and can publish its answer. The preference is read at claim time; edits after an occurrence has been claimed affect remaining occurrences. `TimeEdited` records an explicitly chosen time. Daily recurrences retain the chosen wall-clock time in the schedule timezone. Annual recurrences retain the event's calendar date and chosen clock time; changing the next occurrence's date does not change the birthday/anniversary itself. Calendar synchronization merges saved notification preferences and, for the same event and timezone, explicit time edits and already-advanced annual occurrences at the storage write.

Verification: [schedule_edit_test.go](../../internal/server/schedule_edit_test.go), [schedule_edit_test.go](../../internal/store/schedule_edit_test.go) (file and optional Firestore emulator contracts).

## Implementation and verification

- [gateway.go](../../internal/server/gateway.go)
- [guard.go](../../internal/server/guard.go)
- [routes.go](../../internal/server/routes.go)
- [server.go](../../internal/server/server.go)
- [gateway_test.go](../../internal/server/gateway_test.go)
- [hardening_test.go](../../internal/server/hardening_test.go): cross-site refusal

# Posts, replies, edits, and deletions

## Accepting a post

`postEntry` reads the body and client ID, then checks the ID, timestamp, and hash of any existing content. Automatic speech submissions optionally run text formatting. Mentions of multiple personas are rejected. Images are decoded from Base64 and saved as blobs with a 20 MiB limit. Only PNG, JPEG, GIF, WebP, HEIC/HEIF, and AVIF are accepted. `getBlob` serves a stored blob with `nosniff` and a sandboxing `Content-Security-Policy`, and serves any other stored type as `application/octet-stream`, since blobs share the page's origin.

For a post without an image, `readLink` extracts an explicit URL or a URL in the text and attaches the fetched page content. `capture.Fetch` checks HTTP(S), connection IPs, and response size, and removes scripts and other non-content elements.

Posts and replies accept optional `language` (`ja` or `en`) from the browser and persist it as `Entry.Language`; other values become empty. Image interpretation uses it for descriptions and origin hints: English submissions request English explanations while OCR retains the original visible language. Missing or Japanese language keeps the existing Japanese interpretation prompt. Verification: [capture_test.go](../../internal/capture/capture_test.go). Infographics use it to translate their content to the display language selected at submission. Existing entries without a language remain compatible. Persona HTML answers stay in `Entry.Text` and use the usual timeline persistence and APIs; HTML is rendered only in the browser's sandboxed frame. Bluesky sharing accepts temporary browser-generated image pages bound to the current HTML source hash; it preserves the saved answer and rejects raw persona HTML publication without a capture (see [sharing](./integrations.md#selected-sharing-to-bluesky)).

`land` calls `CreateEntry` with the entry and its initial routing reaction. Firestore commits both in one transaction; file storage writes the reaction first under the same mutex as the entry, so routing cannot claim it between writes. A failed reaction write rejects the submission before the entry is saved. After acceptance, `land` emits an event and calls `Runner.RouteAccepted` to start the already durable request. Restart recovery works even if the process stopped before that call. Only the request that created the entry queues routing; a concurrent duplicate returns the stored entry, or 409 for different content, and its image is removed. The HTTP response does not wait for persona answers. Runner analyzes an image once before waking personas, so they all receive the same reading.

## Post location labels

Browser posts validate coordinates and persist immediately, without waiting for MCP. Invalid coordinates are omitted; custom labels are retained. The initial durable routing request queues a separate `place-{entryID}` reaction before routing completes, so location lookup can recover after restart without blocking persona delivery. [place.go](../../internal/server/place.go) calls the first configured `reverse_geocode` tool with `lat`, `lng`, and the saved `language` (`ja` default, or `en`). Each background attempt has a 60-second budget, with initial catalogue discovery bounded to 15 seconds. Transient failures, unavailable tools, and malformed responses are retried at 30-second intervals, up to three claimed attempts; a valid no-match response ends the job. Coordinates remain available when no label can be obtained.

[geocode.Resolve](../../internal/geocode/place.go) preserves the existing display format: administrative levels 2–4 and the nearest settlement name, without country or level 1. Japanese names concatenate and English names use commas, suppressing repeated parents. `Place.CountryCode`, localized `Country` (from the ISO region using Unicode country names), and level-1 `Region` are stored separately. These describe the nearest settlement, not verified boundary containment. `search.Text` includes these fields for keyword retrieval and RAG embeddings while `Place.Label` and browser display stay short. A query for “Akita last week” can use the region term with the existing search time window.

`Workspace.UpdatePlace` conditionally changes only an unchanged location, under the file lock or a Firestore transaction. It preserves concurrent text/media changes and never recreates a deleted entry. A successful change emits the existing `entry` event, updating connected browsers and queuing search reindexing; the changed search text invalidates old embeddings. Request hashes remain based on the original submission, and duplicate post retries do not duplicate lookup work. Existing records can be enriched with the [metadata-only backfill](./operations.md#backfilling-post-locations).

Verification: [place_test.go](../../internal/server/place_test.go) checks immediate acceptance while discovery is blocked, saved enrichment, duplicate submissions, and retryable outages; [store location tests](../../internal/store/place_test.go) cover concurrent edits and deletion; [queue tests](../../internal/persona/queue_test.go) cover restart, retry success, and the attempt limit; [geocode tests](../../internal/geocode/place_test.go) and [hybrid search tests](../../internal/search/hybrid_test.go) check separate display/search fields and region-plus-period retrieval.

## Retries and changes

The device outbox supplies `clientId` and `clientAt`. An existing ID with the same hash returns the existing entry; different content for the same ID receives a 409. `clientEntry` answers retries early, before formatting or fetching; `CreateEntry` in `land` makes acceptance exclusive when simultaneous requests with the same ID pass that check.

Replies check that the target exists and is not deleted. Editing a post saves its routing request with `UpdateText` using the same persistence ordering, clearing the old routing result before the request can be claimed, then notifies its previous recipients and routes the new content. Monthly app summaries are edited without routing. Deletion respects thread and reply relationships, takes the deleted entries' kakera records out, and wakes the personas that had recorded them to tidy their field summaries (see [Deleted kakera](./personas.md#deleted-kakera)). The images of deleted entries are removed from the blob store after the entries. Saving image analysis requires the entry to still exist.

## Implementation and verification

- [entries.go](../../internal/server/entries.go): `postEntry`, `postReply`, `land`, `clientEntry`
- [fetch.go](../../internal/capture/fetch.go): `Fetch`
- [runner.go](../../internal/persona/runner.go): `readCapture`
- [offline_test.go](../../internal/server/offline_test.go)
- [acceptance_test.go](../../internal/server/acceptance_test.go): queue-write failure rejects acceptance and the same client ID can retry
- [acceptance_test.go](../../internal/store/acceptance_test.go): posts, replies, edits, duplicate acceptance, and restart
- [delete_test.go](../../internal/server/delete_test.go)
- [hardening_test.go](../../internal/server/hardening_test.go): concurrent resends, image types, image removal

# Timeline entries and threads

## Entry fields

| Field | Purpose |
| --- | --- |
| `ID` | Entry identifier |
| `Text`, `Language` | Body (including complete HTML answers), optional submission display language |
| `Kind` | `post`, `remark`, or `reply` |
| `Speaker` | `user`, `kakeratta`, or a persona ID |
| `Root` | ID of the post starting the conversation |
| `ReplyTo` | Direct reply target |
| `Media`, `Link`, `Place` | Image and analysis, URL, location |
| `Sources`, `Notes` | Source links and related memory paths |
| `Deleted` | Transient removal notification for the browser; never persisted |

Persona check-ins can also start conversations as `post` entries. Monthly summaries are authored by the app. `RootOf` and `Thread` resolve a conversation and exclude deleted parents and their descendants from display and model input.

## Pagination

`TimelinePage.Entries` includes selected parent posts and their replies. The limit counts parent posts rather than replies: 20 by default, up to 100. The `Next` cursor encodes a timestamp and ID as JSON and Base64URL; IDs break ties between posts with the same timestamp.

The frontend's `createTimelinePages` merges fetched pages with live changes. Replies and deletions received during a slow HTTP request take precedence over its snapshot. Opening an existing conversation from search results integrates with the feed.

## Implementation and verification

- [timeline.go](../../internal/timeline/timeline.go)
- [timeline_page.go](../../internal/store/timeline_page.go)
- [timeline-pages.js](../../frontend/src/timeline-pages.js)
- [timeline_page_test.go](../../internal/store/timeline_page_test.go)
- [timeline-pages.test.js](../../tests/timeline-pages.test.js)

# Favorites

Guarded `PUT /api/entry/{id}/favorite` saves an existing post, persona remark, or user reply. Repeating PUT retains the original saved timestamp; removing and adding it again gives it a new timestamp. `DELETE /api/entry/{id}/favorite` is idempotent. Both emit a `favorite` event so connected browsers refresh their state.

`GET /api/favorites?speaker=&limit=&offset=` returns `items` containing each entry and its favorite timestamp, sorted newest-saved first with ID tie-breaking. Pages default to 20 entries and allow up to 100. `total` counts the filtered favorites; `ids` and `speakers` describe all current favorites for button state and filter choices. Missing/deleted entries are omitted, and the entry deletion handler removes favorites for every deleted descendant. This listing reads all favorite records before applying pagination.

Favorites use the atomic record contract with no expiry. Files live at `favorites/{entry-id}.json`, outside the non-exported runtime directory; Firestore uses `users/{key}/favorites/{entry-id}`. They survive restarts, are included in portable exports, and are removed with the workspace. They reference current entry content and do not retain a snapshot after a post is deleted.

`POST /api/timeline/search` accepts `favoritesOnly: true`. `search.SearchSelected` restricts lexical and semantic candidates before ranking, totals, and pagination. A blank query lists selected entries newest-posted first. Selected semantic searches use the common vector path rather than the native global query, keeping saved candidates eligible even when unselected entries would fill the native result limit. Persona timeline searches retain their existing scope.

Verification: [favorites.go](../../internal/server/favorites.go), [favorites_test.go](../../internal/server/favorites_test.go), [search.go](../../internal/search/search.go), [hybrid.go](../../internal/search/hybrid.go), [runtime.go](../../internal/store/runtime.go), and [export_test.go](../../internal/store/export_test.go).

# Workspace persistence

## Push dispatch persistence

Split web/worker mode adds the `dispatch` generation to Firestore reactions and `users/{key}/dispatchOutbox/{generation}` records containing `job` (workspace/reaction/dispatch IDs) and `readyAt`. Creation and phase changes atomically save the reaction and its outbox request. Publishing removes the request only after broker acceptance; interrupted publishing can duplicate a message, and workers check the generation and live claim. Push workers process reactions with dispatch generations; legacy pending reactions are outside this queue and must finish before switching. Combined/file operation creates no outbox records. Existing recursive workspace destruction removes the new subcollection.

`POST /internal/work` is available only in worker mode; `POST /internal/dispatch` repairs outbox records only in web mode. Both verify a Google identity token with the configured endpoint audience and runtime service-account email. Push also checks the full subscription name, limits the envelope to 16 KiB, and validates IDs. Busy/failure responses use 503; completed/stale jobs use 204. Sources and tests: [dispatch.go](../../internal/store/dispatch.go), [dispatch_test.go](../../internal/store/dispatch_test.go), [pubsub.go](../../internal/dispatch/pubsub.go), [worker.go](../../internal/server/worker.go).

## Persistence contract

`Workspace` is a per-user contract covering the timeline, favorites, memory, settings, persona definitions, schedules, routing records, note retries, the reaction queue with its claims, runtime records (asks, trials, agent presence, browser push subscriptions), named locks, the closing mark, publication history, exports, and deletion. `BlobStore` handles image storage separately, including deletion of single blobs.

## Two implementations

| Implementation | Durable state | Local directory |
| --- | --- | --- |
| `Store` | Files | The workspace itself |
| `FirestoreWorkspace` | Documents and subcollections under `users/{key}` | Temporary materialization of persona definitions and related files |

The file implementation stores current entries as JSON arrays in `timeline/YYYY/MM/DD.json`, using the configured timezone when an entry is first saved. Updates replace the entry in its original daily file through a synced temporary file and atomic rename; previous versions are not retained. Deletion physically removes entries from daily files, removing empty daily files, or deletes Firestore documents and their search passages. A post includes its whole conversation; a reply includes its descendants. Late updates require an existing record, and new replies require existing parents. Deleted IDs are not retained for deduplication: explicitly resending a deleted root post creates it again. On startup, an in-memory ID/date/thread index is rebuilt from these files. Individual reads, timeline pages, conversations, and date-filtered searches read only relevant days; `TimelineWindow` (both implementations) serves date-filtered searches, monthly summaries, and monthly check-ins, and the persona builder reads only the disputed conversation. Searches without a date range still read all current entries. Locks protect consistency, and individual records have a size limit. No migration from the previous JSONL format is provided.

Memory uses files per persona: one file per field holding its summary and facts, and monthly records files, all written by the persona runner (see [Memory: facts, summaries, and kakera records](./personas.md#memory-facts-summaries-and-kakera-records)). `WriteNote` validates paths and size, preserves any source links a note already carries, and replaces content through a temporary file. A reverse index from source entries to notes is also persisted; the current memory writes no source links, so it serves notes from before. Firestore provides corresponding operations using documents and transactions; its timeline entries are current-state documents.

With GCS configured, blobs use user-specific prefixes. If Firestore is configured without a supplied blob store, images are stored as files in the temporary directory. Durable image storage, and images shared between instances, therefore require a separate BlobStore; `main` logs a warning otherwise.

Persona definitions are read from the workspace each time they are needed (`Skills`); the file store keeps them as `skills/{id}/skill.json`, Firestore in the `skills` collection. `FirestoreWorkspace.Close` removes its scratch directory.

## Implementation and verification

- [workspace.go](../../internal/store/workspace.go): `Workspace`
- [store.go](../../internal/store/store.go): `Store`, `WriteNote`, `writeNoteFile`
- [timeline_files.go](../../internal/store/timeline_files.go): daily snapshots and ID/thread indexes
- [delete_entry.go](../../internal/store/delete_entry.go): physical deletion, image removal, and late-write protection
- [reactions.go](../../internal/store/reactions.go): durable reaction queue
- [cluster_firestore_test.go](../../internal/store/cluster_firestore_test.go): time windows, reactions, lock loss, skills reads, and image removal on the emulator
- [timeline_files_test.go](../../internal/store/timeline_files_test.go): cross-date reads and timezone behavior
- [firestore.go](../../internal/store/firestore.go)
- [blobs.go](../../internal/store/blobs.go)
- [store_test.go](../../internal/store/store_test.go)
- [note_sources_test.go](../../internal/store/note_sources_test.go)

# Settings and model requests

## Reading settings

`Settings.Profile` stores the workspace user's display name, emoji icon, and optional photo data URL separately from authentication and timeline speaker IDs. The settings API trims names/icons, limits names to 40 Unicode characters and emoji icons to four, and accepts an empty profile value for UI defaults. `Skill.Avatar` carries the same optional photo for persona definitions. [avatar.go](../../internal/avatar/avatar.go) validates photos at settings and persona save boundaries: only Base64 JPEG data URLs are accepted, decoded bytes are limited to 32 KiB, dimensions are positive and at most 128 × 128, and a full JPEG decode must succeed. URLs, SVG and other formats, oversized or malformed photos are rejected before persistence. Photos are embedded in settings/definition JSON and Firestore fields, so exports include them without separate blobs. The AI builder strips avatars from existing-definition context and prior assistant draft messages, then preserves the user's avatar on the returned draft. Verification: [avatar_test.go](../../internal/avatar/avatar_test.go) and [profile_test.go](../../internal/server/profile_test.go).

`Settings.DailyTime` (`dailyTime`) is the account's daily check-in time as `HH:MM` in the saved time zone; the settings API trims it and refuses anything but `HH:MM` or empty, and saving settings re-arms the workspace's alarm (see [Periodic checks](./personas.md#periodic-checks)).

`ConfiguredSettings` returns saved choices. `Settings` applies defaults and model fallbacks for execution. This separation prevents an empty field in the editor from being overwritten with its runtime fallback.

New workspaces default to the built-in sona (`routine`), kakeratta support (`support`), and infographics (`infographic`) being active. Persona definitions save the optional `memoryEnabled` flag; false disables private memory and periodic check-ins (see [Optional persona memory](./personas.md#optional-persona-memory)), while external-agent preference remains an independent saved setting. Saved active lists, including an explicitly empty or null list, override that initial default in both file storage and Firestore.

`Server.client` creates an `llm.Client` from effective settings. The **Use OpenRouter** checkbox (`openRouterMode`) selects its own key and model choices. Legacy `jevEnabled` settings normalize to OpenRouter enabled with `~typesafe/jev-latest` selected as Router and the legacy flag cleared. Built-in turns select depth with explicit substrings in the current post, without a model judgment or preliminary planning call. Daily and monthly check-ins skip this substring check because their input includes historical material; AlwaysUltra still applies. Ordinary reads and answers use middle. Explicit deep requests and AlwaysUltra use high for the final answer; their reads stay on middle when the persona has lookup tools of its own, and run on high with the answer when it has none (the infographic). The Router default is Super; existing saved Router choices are retained. All subject conditions are evaluated together in one routing request. The low model retains optional dictation polishing; standalone reply escalation is removed. Vision and routing models have separate roles.

## Requests and storage

`llm.Client` handles OpenAI-compatible chat completions, tool calls, JSON responses, and attachments. Model credentials are not sent directly from the browser to model providers. Model names and defaults are defined in `store/settings.go`, which is the place to check when changing defaults.

The settings API masks keys as `********`. When that value returns in a save request, the current key is retained. An omitted or null `apiKey` also retains the current model key. Changing the model endpoint (`Settings.Endpoint`) while retaining a nonempty key is refused: the request must explicitly supply the key for the new endpoint or clear it with an empty string. A judge account's settings are reset to Token Factory with the operator's judge key whenever its key, provider, endpoint, or OpenRouter mode differ. Daily and planner settings have dedicated endpoints and are protected from stale general settings forms. This separation alone does not eliminate all concurrent update conflicts.

Map endpoints and external MCP server lists come from operator environment variables, separately from user settings.

## Model usage logging

Every outbound chat completion emits one structured stdout record with `path: "model-usage"` and `message: "model usage"`, including transport, HTTP, decoding, provider, and empty-choice failures. Calls rejected before sending (for example an unconfigured model) do not produce an API-call record. `llm.Reply.Usage` retains provider-reported `prompt_tokens`, `completion_tokens`, `total_tokens`, and optional reasoning/cache token details. Missing usage or individual counts remain null rather than estimated from text length or silently treated as zero. Usage is recorded before caller validation, so a subsequently rejected tool response remains accounted for.

The provider's first-choice `finish_reason` is retained as `llm.Reply.FinishReason` and logged as optional `finishReason`. A value of `length` identifies provider output truncation; API `outcome: "success"` still describes the successful response, not acceptance of the answer. Missing finish reasons remain omitted.

Records identify `provider` (`nebius`, `openrouter`, or `custom`), `model`, `purpose`, HTTP `status`, a fixed `outcome` category, and `durationMs`. Purpose tags cover routing (one record per subject persona), answer (including read/tool rounds), schedule/action planning, review, memory update/summary, the internal day's summary (`daily_summary`), monthly summary, person schedules, persona building, dictation, image reading, and embeddings. Context carries available persona, trigger, wake category, and durable job ID; standalone calls may lack these identifiers. Model-list requests and external-agent inference outside this service are not recorded as completions.

`attempt` starts at one and `retryCount` at zero for format/validation corrections within a stage. `round` separately counts normal multi-round research or tool use. `jobAttempt`/`jobRetryCount` describe durable reaction claims; `noteAttempt`/`noteRetryCount` describe durable note attempts; `planAttempt`/`planRetryCount` describe phase regeneration after review. These counters describe different scopes and must not be summed as independent billed requests. Counting records gives the outbound calls observed by this service, including failures. The instrumentation adds no retry policy and changes no retry limits.

Embedding requests use the same format with `purpose: "embedding"` and retain returned input/total counts; invalid vectors can still have reported usage. Jev Decisions API routing also emits a `routing` usage record, with null usage when the provider omits counts. Records exclude API keys, request/response text, tool arguments, provider error messages, and endpoint URLs, including userinfo, paths, queries and custom hostnames. Operational records continue to go to stdout. Workspace monthly aggregates additionally retain request counts and known costs; see the following section.

Implementation and verification: [usage.go](../../internal/llm/usage.go), [llm.go](../../internal/llm/llm.go), [search.go](../../internal/search/search.go), [usage_test.go](../../internal/llm/usage_test.go) (reported/missing/zero usage, failed calls, context and privacy), and [persona usage tests](../../internal/persona/usage_test.go) (research read/answer rounds versus retries and rejected-answer accounting).

## Monthly usage accounting

Guarded `GET /api/usage?month=YYYY-MM&persona={id}` returns a workspace's monthly aggregates and the rate review date. The month defaults to the current month in the saved workspace timezone. Invalid months return 400; storage failures return 500. An optional persona filter selects its rows. Responses use `Cache-Control: no-store`. Empty months return an empty row list. No historical operational logs are imported.

One atomic `usage` record per workspace/calendar month retains first/last recording times and aggregate rows by persona, wake category, and component. Files use `runtime/usage/YYYY-MM.json`; Firestore uses the existing record collection contract under `users/{key}/usage/{month}`. Aggregates survive restarts and persona removal, have no expiry, are excluded from portable exports, and are removed with the workspace. Concurrent instance updates use the existing file mutex or Firestore transaction. Each completed observed API attempt adds once, including rejected answers and retries; an interrupted or failed aggregate write can leave the totals incomplete. Persistence uses a separate ten-second context and logs a fixed warning on failure, without breaking the answer workflow or exposing private request data.

Reply categories cover `post`, `listening`, `post-edited`, and `review-reply`; scheduled work covers `daily` and `schedule`; remaining wakes are Other. Research, memory updates, and retries keep their turn's attribution. Routing and embeddings are separate shared components even when their context names a persona. Unattributed calls (including application summaries, image reading, dictation, and persona building) remain shared Other. The browser groups embeddings into Other alongside shared summaries rather than allocating shared costs across personas.

OpenRouter uses the API's optional `usage.cost`, including a reported zero. Nebius estimates use recorded input/output tokens and a fixed, reviewed USD-per-million-token table for Nemotron Lightning/Nano/Super/Ultra, GLM-5.3-Flash, and Qwen3-Embedding-8B; embedding requires only input counts. Rates were reviewed on 2026-10-09 against [Nemotron pricing](https://nebius.com/services/token-factory/models/nvidia-nemotron-models-inference), [GLM pricing](https://nebius.com/services/token-factory/models/glm-models-inference), and [Qwen pricing](https://nebius.com/services/token-factory/models/qwen-models-inference). Amounts are computed when recorded, so later rate changes do not reprice saved totals. Missing counts/cost, invalid amounts, custom providers, and unsupported models increment `unpriced` rather than implying zero. Reasoning/cache counts are not charged again. The UI presents the combined known costs as estimates; provider billing remains the reconciliation source. Nebius estimates do not apply discounts or free allowances; OpenRouter retains the reported billed cost. Search charges and inference billed directly to external agents are outside these totals. See [OpenRouter usage accounting](https://openrouter.ai/docs/cookbook/administration/usage-accounting).

Web search counts actual Tavily HTTP attempts, including failures, through the workspace's tool configuration callback. Invalid arguments, missing credentials, empty queries, cached results, and exhausted read budgets do not add requests. The settings connection check adds a shared Other search. External-agent reads through the application retain their persona/wake attribution; the external agent's own inference is not observed.

Implementation and verification: [usage.go](../../internal/usage/usage.go), [usage_test.go](../../internal/usage/usage_test.go) (unknown/zero pricing, OpenRouter cost, concurrent aggregation, restart, timezone boundaries), [usage API](../../internal/server/usage.go), [usage API tests](../../internal/server/usage_test.go), [runtime.go](../../internal/store/runtime.go), [websearch.go](../../internal/websearch/websearch.go), [Web search accounting tests](../../internal/websearch/usage_test.go), and [search.go](../../internal/search/search.go).

## Implementation and verification

- [settings.go](../../internal/store/settings.go)
- [settings.go](../../internal/server/settings.go)
- [server.go](../../internal/server/server.go): `client`
- [llm.go](../../internal/llm/llm.go)
- [settings_test.go](../../internal/store/settings_test.go)
- [hardening_test.go](../../internal/server/hardening_test.go): endpoint-bound keys, including omitted/null keys and explicit clearing, and the pinned judge endpoint

# Timeline search and indexing

## Search scope

Frontend search and the persona tool `timeline_search` share the search Service. `Unified` validates the query, time window, limits, and score threshold. With a time window alone, it lists records newest first.

Lexical search derives terms from character-script runs and contiguous Han characters. Semantic search embeds entry text and related content and ranks by similarity. The two branches fuse ranks rather than adding scores with different meanings. Semantic search has an eight-second budget, allowing lexical results to be returned when it is unavailable.

## Speaker lists

`GET /api/speaker/{id}?limit=&offset=` (`speakerEntries`) lists what one speaker - the user, or a persona by its id - put on the timeline, newest first, 20 by default and up to 50 a page, as the hits a search returns (`Result`: `hits`, `total`; the snippet is the first 240 characters of the entry's text). `search.BySpeaker` reads them through `Workspace.SpeakerEntries`: the file store's index of current entries carries each entry's speaker, so only the days the speaker spoke on are read; Firestore queries the timeline with an equality filter on `Speaker`, which needs no composite index. A deleted conversation takes what the speaker said in it, as everywhere.

The response also carries `entries`: the whole conversation of each remark on the page, read once each, five at a time (`Server.conversations`, through `store.SearchThread`), so the list can show a remark under its post without a request per conversation. A conversation whose post has gone is left out.

## Index updates and changing records

`entry` events after persistence or image analysis, and settings changes, schedule indexing: `Runner.ScheduleIndex` queues the `index` reaction, of which at most one waits, so a workspace is indexed by one worker at a time in whichever process claims it. The pass keeps renewing its claim while `IndexPending` runs. Changes during the pass set a durable `IndexAgain` marker; finishing preserves one follow-up pass if marked. A failure defers the retained reaction for 30 seconds rather than dropping it.

Where the workspace implements the native indexing contract, search uses that implementation; otherwise, it uses the local index path. Persisted vectors and dirty state allow updates to continue at startup or during searches.

After slow semantic work, search rereads current entries and excludes deleted conversations and hits whose content has changed. This prevents stale results from resurfacing records deleted during a search.

## Implementation and verification

- [hybrid.go](../../internal/search/hybrid.go): `Unified`
- [search.go](../../internal/search/search.go)
- [index.go](../../internal/search/index.go): `IndexPending`
- [native.go](../../internal/search/native.go)
- [index.go](../../internal/server/index.go)
- [queue.go](../../internal/persona/queue.go): `indexPending`
- [search.go](../../internal/server/search.go)
- [hybrid_test.go](../../internal/search/hybrid_test.go)
- [speaker.go](../../internal/search/speaker.go): `BySpeaker`
- [speaker.go](../../internal/store/speaker.go): `SpeakerEntries`
- [speaker_test.go](../../internal/search/speaker_test.go)

# Reminder push delivery

[push.go](../../internal/server/push.go) exposes guarded `GET /api/push`, `PUT /api/push`, and `DELETE /api/push`. GET returns availability and the public VAPID key; an optional `endpoint` query checks whether that endpoint is registered in the current workspace. The private VAPID key never reaches the browser. PUT validates HTTPS endpoints from Google FCM, Mozilla, Apple, or Windows push services, a 16-byte authentication secret, and a P-256 public key. It saves an idempotent record keyed by the SHA-256 endpoint digest. Registration takes the `push-subscriptions` workspace lock and permits at most 20 devices. DELETE removes only the current workspace's record.

Subscriptions are `push-subscriptions` runtime records: under `runtime/push-subscriptions/` in file storage and `users/{key}/push-subscriptions` in Firestore. They survive restarts, are shared across instances, are excluded from exports, and are removed with the workspace. They have no expiry until explicitly revoked or rejected by the provider.

The Runner emits an internal `reminder` event after persisting a successful scheduled utterance only when that occurrence has notifications enabled. `Server.publishEvent` consumes that event directly on the executing instance, without relaying it over the cross-instance SSE bus. Before signing, delivery removes the `mailto:` prefix from the configured contact because webpush-go adds that prefix itself; HTTPS contacts pass through unchanged. Delivery sends encrypted Web Push with VAPID credentials to the workspace's registered devices concurrently, with a ten-second HTTP timeout and a thirty-second overall deadline. The encrypted payload carries a title derived from the reminder text (collapsed whitespace, truncated after 120 Unicode characters), the kakeratta app name as its body, and the entry and workspace IDs. A one-hour TTL and a per-entry topic limit stale pending messages; the browser notification tag also uses the entry ID. Provider 404/410 responses remove a subscription only if its stored value has not changed during delivery. Other failures are logged without endpoint credentials. This is best-effort delivery: there is no durable push queue or automatic retry, and interruption after the post is saved can lose its push. The timeline post remains available.

Verification: [push_test.go](../../internal/server/push_test.go) checks registration, account/origin guards, invalid endpoints and keys, event selection, encrypted requests, VAPID JWT subjects for mailto and HTTPS contacts, and stale-subscription removal.

# Related chapters

- [Persona execution and memory](./personas.md)
- [Browser application and device state](./frontend.md)
- [External agents, tools, and sharing](./integrations.md)
- [Development and workspace operations](./operations.md)
## Listing filters

`GET /api/timeline` and `GET /api/speaker/{id}` accept optional `favoritesOnly=true`, `from`, and `to`; `POST /api/timeline/search` accepts the same fields in JSON. Dates are inclusive RFC3339 timestamps. Invalid timestamps or reversed bounds return 400. Conditions apply before ranking, totals, and pagination, including blank-query period searches. Unfiltered timeline requests retain the indexed cursor path. Filtered timeline requests use [FilteredTimelinePage](../../internal/store/timeline_filter.go): date-only matches parent posts, while favorites-only matches saved entries by their own dates and returns their full conversations, ordered and paged by parent post date with the existing cursor. Speaker filters match the utterance itself and preserve conversation context. Search restricts candidate IDs before lexical or semantic ranking, so out-of-range results cannot crowd out eligible hits. The favorites persistence/API remains available although the separate browser Favorites panel is removed.

Verification: [list_filters_test.go](../../internal/server/list_filters_test.go) checks combined conditions, saved replies with older parents, date-only results, search without a query, and invalid dates.
