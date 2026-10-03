---
type: "Component"
title: "API and persistent state"
description: "Authentication, entry handling, timeline data, persistence, settings, schedule editing, reminder push delivery, favorites, and search."
tags: ["http", "auth", "entry", "capture", "timeline", "pagination", "store", "firestore", "settings", "llm", "search", "index", "push", "favorites"]
updated: "2026-10-04"
---

Authentication, entry handling, timeline data, persistence, settings, schedule editing, reminder push delivery, favorites, and search.

# HTTP API and authentication

## Resolving the user

In a hosted deployment, `Gateway` verifies Firebase sessions. The browser sends an ID token to `/api/auth/session`, and the server issues a `__session` cookie. It derives a workspace key from the user's UID and forwards requests to that user's `Server`. Startup also includes a worker that opens workspaces to be restored.

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

`readJSONLimit` uses `http.MaxBytesReader` to bound input. `fail` produces JSON errors and `writeJSON` produces responses. The API server does not serve the frontend; unknown paths receive a JSON 404.

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

Posts and replies accept optional `language` (`ja` or `en`) from the browser and persist it as `Entry.Language`; other values become empty. Infographics use it to translate their content to the display language selected at submission. Existing entries without a language remain compatible. Persona HTML answers stay in `Entry.Text` and use the usual timeline persistence and APIs; HTML is rendered only in the browser's sandboxed frame. Bluesky sharing accepts temporary browser-generated image pages bound to the current HTML source hash; it preserves the saved answer and rejects raw persona HTML publication without a capture (see [sharing](./integrations.md#selected-sharing-to-bluesky)).

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

`ConfiguredSettings` returns saved choices. `Settings` applies defaults and model fallbacks for execution. This separation prevents an empty field in the editor from being overwritten with its runtime fallback.

New workspaces default to the built-in sona (`routine`), kakeratta support (`support`), and infographics (`infographic`) being active. Persona definitions save the optional `memoryEnabled` flag; false disables private memory and periodic check-ins (see [Optional persona memory](./personas.md#optional-persona-memory)), while external-agent preference remains an independent saved setting. Saved active lists, including an explicitly empty or null list, override that initial default in both file storage and Firestore.

`Server.client` creates an `llm.Client` from effective settings. The **Use OpenRouter** checkbox (`openRouterMode`) selects its own key and model choices. Legacy `jevEnabled` settings normalize to OpenRouter enabled with `~typesafe/jev-latest` selected as Router and the legacy flag cleared. Ordinary persona answers use the middle model; replies asking for a deeper answer use the high model; the decision to escalate uses the low model. Vision and routing models have separate roles.

## Requests and storage

`llm.Client` handles OpenAI-compatible chat completions, tool calls, JSON responses, and attachments. Model credentials are not sent directly from the browser to model providers. Model names and defaults are defined in `store/settings.go`, which is the place to check when changing defaults.

The settings API masks keys as `********`. When that value returns in a save request, the current key is retained, but only for the same endpoint: changing the model endpoint (`Settings.Endpoint`) with a masked key is refused, so a stored key is never sent to an address it was not entered for. A judge account's settings are reset to Token Factory with the operator's judge key whenever its key, provider, endpoint, or OpenRouter mode differ. Daily and planner settings have dedicated endpoints and are protected from stale general settings forms. This separation alone does not eliminate all concurrent update conflicts.

Map endpoints and external MCP server lists come from operator environment variables, separately from user settings.

## Implementation and verification

- [settings.go](../../internal/store/settings.go)
- [settings.go](../../internal/server/settings.go)
- [server.go](../../internal/server/server.go): `client`
- [llm.go](../../internal/llm/llm.go)
- [settings_test.go](../../internal/store/settings_test.go)
- [hardening_test.go](../../internal/server/hardening_test.go): endpoint-bound keys and the pinned judge endpoint

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
