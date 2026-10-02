---
type: "Component"
title: "Browser application and device state"
description: "Frontend composition, requests, live updates, reminder push notifications, offline delivery, favorites, saved-entry search, speech input, and read-aloud."
tags: ["frontend", "ui", "api", "identity", "offline", "outbox", "speech", "browser", "push", "favorites"]
updated: "2026-10-02"
---

Frontend composition, requests, live updates, reminder push notifications, offline delivery, favorites, saved-entry search, speech input, and read-aloud.

# Browser application composition

## Initialization and components

`bootstrap.js` checks authentication before loading `app.js`. `app.js` assembles the composer, persona list, timeline, and settings, search, memory, and other panels. `screen.js` provides the basic layout; `feed.js` renders conversations and their controls. Logout is an icon button with a localized accessible label and tooltip. On phones, the brand name and the desktop spacer are hidden; the brand mark stays on the left with an automatic trailing margin, and navigation aligns to the right. Phone navigation uses a one-rem font (16 pixels at the standard setting). Controls can still wrap for narrow screens or enlarged text, with wrapped rows also aligned to the right.

Panels have dedicated modules for persona creation and editing, routing checks, reply corrections, memory browsing, and other functions. The app opens and closes them. `dom.js` centralizes DOM creation and notifications; `i18n.js` handles interface language. `compose.js` starts the text field at three lines and includes an @ mention hint in its placeholder. `room.js` initially hides all persona badges under the composer on every screen size; **Show personas** expands all badges, grouped into interests and people, and **Collapse** hides them again; a tap on a badge writes `@name` in the composer through `compose.mention`, so a newcomer sees how to ask someone; right-clicking a badge or holding it for 550 ms on touch opens a dialog with **Settings**, **View memory**, and **Edit**, wired to the persona panel and existing memory viewer/editor; moving the touch cancels the hold so a badge can be dragged to a new place within its group (pointer events, so it works by touch), and the order is kept in this browser's `localStorage` (`kakeratta.room.order`) and applied by `arranged`. Badges that step aside glide into place (Web Animations, skipped under reduced motion), and the first time a group has badges to swap, the first one nudges sideways once (`kakeratta.room.hinted`) so they read as movable. `text-size.js` keeps the text size (standard, large, extra large) in this browser's `localStorage`, like the language, and applies it by scaling the root font size at startup and when the Settings sheet changes it; the page is sized in rem, so all of it follows.

Persona badges disable browser text selection and the iOS touch callout (`user-select`, `-webkit-user-select`, and `-webkit-touch-callout` in [styles.css](../../frontend/src/styles.css)), so holding a badge opens its menu without leaving native text-selection handles over the controls. Their existing pointer handlers continue to handle taps, holds, and dragging.

The built-in `sona` (`routine`) is labeled **Preparation** and declares scheduling and weather forecasts. Its sharing check-in description includes upcoming plans alongside recent records and memory; its default activation is defined by the backend settings.

The settings panel uses a shrinkable single-column grid, including nested fields and advanced groups. Native controls have zero minimum width and a maximum width of their container, and long text can wrap anywhere; enlarged text and device-specific option names therefore do not force horizontal scrolling. Storybook provides a text-size toolbar and an **ExtraLarge** settings story using the same `text-size.js` scaling as the application.

## Choosing whether a persona remembers

Creation and editing offer **記憶する** (Remember), saved as `memoryEnabled`. It is checked unless the definition explicitly disables memory; support ships unchecked. The builder keeps the user's checkbox choice through draft generation and revisions, and does not save a monthly check-in for an unchecked new person. The editor hides the memory classification when unchecked. With memory off, persona cards and speaker cards omit check-in controls/hints and **覚えていること**, and the room menu omits **記録を見る**. Editing and external-agent preference remain available. Explicit notification schedules can still be viewed and cancelled. The server enforces skipping memory and periodic check-ins; see [Optional persona memory](./personas.md#optional-persona-memory).

The same forms offer **常に熟考する** (Always think deeply), persisted as `alwaysUltra`. The builder preserves a manually selected value across AI drafts and revisions. The infographic persona defaults to memory off and always Ultra on.

The built-in infographic persona displays as **インフォグラフィック** in Japanese and **Infographic** in English across the persona/speaker cards, room badges and menu, timeline speaker names, read-aloud labels, and mention suggestions. English mention insertion uses the stable `@infographic` ID. Customized persona names remain as saved.

## Memory viewer and check-ins

Persona and speaker cards omit the check-in speaking checkbox and runtime descriptions. The landing page and basic manual explain occasional follow-ups as a feature of personas with **Remember** enabled and say that disabling it stops reflection-based follow-ups. A remembering person offers **頻繁に振り返る** (Look back often, daily) and **ときどき振り返る** (Look back occasionally, monthly); changing frequency submits only `every`. Subject personas have no frequency control. Speaking follows the persona character. The external-agent preference appears only when `mcpConfigured` is true and `externalAgentPaused` is false, including for personas with a saved external preference; pausing or removing the connection hides the control without clearing the preference. Verification: [external-agent.test.js](../../tests/external-agent.test.js), [personas-panel.test.js](../../tests/personas-panel.test.js), and [speaker.test.js](../../tests/speaker.test.js).

**View memory** ([memory.js](../../frontend/src/memory.js), opened from [personas.js](../../frontend/src/personas.js) `showMemory`) lists a persona's fields (`{Field}.md`: the summary, the facts that hold now, and those that no longer do) first and its months of kakera records (`records/YYYY-MM.md`) after them, newest month first, under the headings **分野（まとめ文と事実）** and **かけらの記録（月ごと）**; a month's card is titled as the month (2026年9月). The latest three months (`OPEN_MONTHS`) are open and older ones fold into a `<details>`, which a link to it still opens. Pages come from `api.memory` newest-written first; `withFields` fetches any field not on the first page so the fields stay on top. In a field file the summary's heading reads **まとめ（日付 更新）**, a fact's id (`[f_…]`, the persona's handle on it) is not shown, its `from:` ids are **元の投稿** links, and `since:`/`until:`/`by:` read as "…から", "…まで", and "→「the replacing fact」に置き換え" (`isFacts`). Each fact has ✎ and × (`factControls`): ✎ edits its words in place and × forgets it after a confirm, through `api.editFact` and `api.forgetFact`; the field is redrawn from the file the server returns. In a records file each line's id is an **元の投稿** link too. Those links open the post and its conversation in a modal over the memory view ([entry-dialog.js](../../frontend/src/entry-dialog.js) `showEntryDialog`, reading `api.thread`; a post that is gone says so), and fall back to a new tab where no handler is given. In a records file a trailing `#tag` is a chip that narrows the list to the months holding a record with that tag (the server's `?tag=` filter), and `expires:`/`review:` show as **Gone on** / **Review on** chips; `asked:` is not shown. A field or a month can be edited by hand or deleted; nothing is woken for either. A field still kept as prose renders as prose until it is split into facts by hand (see [Splitting prose into facts](./personas.md#splitting-prose-into-facts)).

Verification: [memory.test.js](../../tests/memory.test.js) (records after fields, section headings, older months folded, entry links, tag and date chips, hidden fact ids, sources and dates) and [entry-dialog.test.js](../../tests/entry-dialog.test.js) (the post opened in a modal, a gone post).

## OpenRouter model selection

When an OpenRouter key is entered but no model list has been loaded, settings shows a hint to click **Check OpenRouter connection** to select models. This hint remains visible even before OpenRouter-only mode can be enabled, and disappears once the connection check loads models ([settings.js](../../frontend/src/settings.js)).

## Names and speaker lists

A name on the timeline - a persona's, or the user's; not the app's monthly summary - is a button when the feed is given `onSpeaker` (the main timeline, the search sheet's thread and the speaker sheet itself all are). It opens a sheet of its own, **発言一覧**, which is not a search: `app.js` `openSpeaker` makes it the open panel and `createSpeaker().show` fills it ([speaker.js](../../frontend/src/speaker.js)). It reads `api.speaker` a page at a time - one request, which returns the conversations the page touches with it - and shows them in a feed as the timeline is read - each remark under its post, remarks unfolded - with a "load more". Over the list is a card built like the personas list's: the localized relationship or condition, active/paused and external-agent badges, a person's names, keywords and collapsed basic information (`basicInfoBox`), notification schedules, permitted tools with manual links, and wake count. Schedule dates, time zones and annual/daily labels, and tool links use [persona-display.js](../../frontend/src/persona-display.js), shared with the persona list. Buttons for the persona's settings, its editor (also opened at the basic information) and what it remembers are wired in `app.js` to the personas panel (`reveal`, `showMemoryFor`, `edit`); settings changes are performed there. Notification schedules use `notificationSchedules` in [persona-display.js](../../frontend/src/persona-display.js): the heading has its own line and each schedule appears on a separate row in both cards. Each row includes a × button with a schedule-specific accessible label. After confirmation, it calls `api.deleteSchedule`, disables the button while awaiting the response, and replaces persona state from the returned list through `personas.updateRows`. Deleting from the speaker card refreshes its schedules, the persona panel, the room badges, and the device persona cache. On failure the schedule remains visible, the error is shown, and the button is enabled for retry. The card is redrawn when the personas change (`redrawCard`), and entries arriving on the live channel are upserted (`changed`). A page arriving after another speaker was opened is ignored. The user has no card.

## Favorites and saved-entry search

Each post, persona answer, and user reply has a ☆/★ button with an accessible label and pressed state, including entries opened from search, speaker lists, and memory dialogs. Pending device submissions cannot be saved until delivered. [favorite-state.js](../../frontend/src/favorite-state.js) shares saved IDs across feeds, disables concurrent operations on the same entry, updates buttons after successful requests, and leaves failed changes available for retry. Startup/reconnection and cross-instance `favorite` events refresh the server state.

The header's **お気に入り / Favorites** opens [favorites.js](../../frontend/src/favorites.js). It lists saved entries themselves, newest-added first, with a speaker selector and a load-more button. The view uses the same one-rem padding and grid spacing as speaker lists. Each entry uses the feed's standalone rendering so HTML answers, sources, and favorite controls behave consistently; **元の投稿** opens its full conversation in the existing entry dialog. Standalone entries have no **返す** link; replies are available in the conversation dialog opened from **元の投稿**. Removing a favorite from this list asks for confirmation; cancellation leaves the entry saved and visible. Ordinary timeline buttons do not ask for confirmation. Late pages from an old filter selection are ignored. Removing a favorite refreshes the open list, and entry changes refresh affected visible favorites.

Opening search focuses its input with `preventScroll` so the focus action does not move the sheet. On phones (up to 600px), the search input uses `max(16px, 1rem)` to avoid focus-triggered input zoom while retaining enlarged text settings; the panel uses a shrinkable grid column so long content does not widen it. The **Focused** Search Storybook story opens with that input focused.

The search panel includes **お気に入りのみ / Favorites only**. It sends `favoritesOnly` on every page, resets results when toggled, and allows an empty query while the filter is enabled. The server filters before pagination; an empty saved-entry search lists by post date. Opening a result shows the usual conversation with favorite buttons. Returning to filtered results refreshes them after a favorite change.

[Favorites.stories.js](../../frontend/stories/Favorites.stories.js) previews recent favorites, a persona filter, empty/loading/error states, confirmed interactive removal, and 45 saved entries for scrolling and pagination (20, then 40, then all 45). [Search.stories.js](../../frontend/stories/Search.stories.js) includes saved-only results and their conversations. Both share the favorite-aware fixture API and support the Storybook language toolbar. The capture list includes `favorites` and `search-favorites` in both languages; the bilingual public manual and [usage guide](../usage.md#favorites) explain saving, confirmed removal, pagination, and saved-entry search.

Verification: [feed.test.js](../../tests/feed.test.js) checks saving/removing posts, standalone answer rendering, omitting standalone reply links, confirmation and cancellation for favorite-list removal, immediate timeline removal, favorite list pagination, and stale filter responses; [search.test.js](../../tests/search.test.js) checks filtered pagination, empty queries, and toggling the filter.

## Merging data

Startup displays cached device conversations before fetching server pages. SSE `entry` events add records, while `routed` and `turn` update routing displays. Reconnection refreshes the timeline and sends the outbox.

Polling runs for a limited period after posting to cover missed SSE events. Returning to a visible tab also checks pages and routing. Pending device entries and server records are merged so posts appear in the same conversation before and after delivery.

Dedicated code renders Markdown and creates links. User text and persona Markdown have different display handling. Links are restricted to supported protocols.

External-agent answers (`model: agent`) normalize literal escaped newlines for Markdown display and read-aloud, without rewriting the stored entry.

Complete persona HTML answers (including a single HTML code fence) render through [html-document.js](../../frontend/src/html-document.js) in every feed, including speaker lists and thread dialogs. Recognition requires a whole `<html>…</html>` document; prose, fragments, and ordinary code examples remain Markdown. User posts retain their usual text/Markdown behavior. The frame has an empty sandbox, no referrer, and a CSP before generated content: scripts, origin access, forms, popups and top-level navigation are unavailable; external subresources are blocked while inline CSS and data images work. Its responsive width and scrollable height show long infographics without clipping their lower sections. Read-aloud extracts the document's text with an inert DOM parser and excludes script/style/template contents. When sharing selected persona HTML answers to Bluesky, [html-image.js](../../frontend/src/html-image.js) converts only the chosen documents into JPEG pages before requesting the publication preview. A sanitized offscreen frame allows parent DOM access for layout but blocks scripts, external resources, forms, and navigation; a data-URL SVG with `foreignObject` is rasterized on a canvas. Capture uses a 720-pixel viewport and 2× output, with up to ten 1,440-pixel-high pages; overly wide or long documents fail instead of being cropped. The dialog shows the HTML answer in its usual isolated frame during selection, then the actual capture pages in the preview. Captures and alternative text stay in dialog memory and are reused for publishing/retry; failed conversion disables publishing. See [selected sharing](./integrations.md#selected-sharing-to-bluesky). Verification: [html-document.test.js](../../tests/html-document.test.js), [feed.test.js](../../tests/feed.test.js).

`api.post` and `api.reply` send the resolved browser display language (`ja` or `en`). [outbox.js](../../frontend/src/outbox.js) captures it at enqueue time for both the pending entry and stored request, so switching language before retry does not alter a queued request. Verification: [outbox.test.js](../../tests/outbox.test.js).

A person's card shows its basic information in a collapsed **Basic information** section, with **Edit basic information** opening the editor at that field (`showPersonaEditor` with `focus: "basicInfo"`, which scrolls to it and starts there; nothing is shown for a persona that is not a person). Whether a persona is a person is fixed when it is made: the editor has no switch for it, and the server refuses to save a persona as the other kind (`savePersonaDraft`) and keeps the kind when the AI revises one (`buildPersona`). Person persona creation and editing show an editable **Basic information** field, saved as `basicInfo` with the definition. The builder uses [person-basic-info.js](../../frontend/src/person-basic-info.js), while the editor includes the field in its ordinary form. There is no option to post these details as a fragment. Confirmed dates and appointments register notification schedules when saved. Annual notifications are labeled **Every year** on the persona card. Cards show both host-registered calendar events and persona-created schedules.

The persona tool picker uses [tool_labels.js](../../frontend/src/tool_labels.js) to label `weather_forecast` as **Check the weather forecast** (Japanese: **天気予報を調べる**); persona cards link to its localized [manual](../../frontend/manual/index.html) section. WeatherAPI credentials remain on the server.

[Personas.stories.js](../../frontend/stories/Personas.stories.js) supplies localized sample data for creation, editing, and notification cards. [captures.mjs](../../frontend/captures.mjs) captures the actual UI into `docs/captures/ja/` and `docs/captures/en/`; the manual and landing page reference these files with their current dimensions.

## Implementation and verification

- [bootstrap.js](../../frontend/src/bootstrap.js)
- [app.js](../../frontend/src/app.js)
- [feed.js](../../frontend/src/feed.js)
- [screen.js](../../frontend/src/screen.js)
- [dom.js](../../frontend/src/dom.js)
- [room.js](../../frontend/src/room.js)
- [compose.js](../../frontend/src/compose.js)
- [settings.js](../../frontend/src/settings.js)
- [settings.test.js](../../tests/settings.test.js)
- [text-size.js](../../frontend/src/text-size.js)
- [text-size.test.js](../../tests/text-size.test.js)
- [person-basic-info.js](../../frontend/src/person-basic-info.js)
- [feed.test.js](../../tests/feed.test.js): names as buttons and fully collapsed persona badges
- [speaker.test.js](../../tests/speaker.test.js): speaker lists, persona details, schedule recurrence and time zones, tool links, schedule cancellation and failure handling, and card refresh
- [Screen.stories.js](../../frontend/stories/Screen.stories.js)
- [Personas.stories.js](../../frontend/stories/Personas.stories.js)
- [captures.mjs](../../frontend/captures.mjs)


# Frontend requests and account checks

## Ordinary requests

`api.js` exposes endpoint-specific operations. `createAPIRequest` contains shared transport logic, supplied with the current identity accessor, verification function, and fetch implementation.

When an account exists, the request first verifies its workspace and stops if the account changes during verification. It sends `X-Kakeratta-Workspace`. If the account changes while awaiting a response, that data is not returned to the caller.

Bodies are normally JSON. Persona installation uses `raw: true` to send the original definition string while retaining the same verification, timeout, and error handling. Ordinary requests have a 45-second timeout; longer publishing and trial operations use 250 seconds. API errors retain their HTTP `status`; empty successful responses return `null`.

## Live events

SSE uses `EventSource` and sends the workspace key as a query parameter. Connection failures retry with increasing delays. Registered listeners receive event names and data. With a local account identity, posts and replies enter the outbox rather than being sent directly.

`auth.js` handles Firebase login, session creation, and `/api/auth/identity` verification. It uses cached identity and device storage and reloads the application when the account changes. Without cached identity, startup displays a checking state until authentication resolves, then a loading state through workspace verification and application import. [login.js](../../frontend/src/login.js) shows localized progress with a polite status region and a reduced-motion-aware spinner. The Google popup action immediately replaces the login button with an authorizing state, switches to completing after provider approval, and keeps it visible until the server session finishes and the page reloads. A pending action cannot start another login; failure or popup cancellation restores the actual login action with an error. An authenticated restoration never briefly offers another login while its session is being established. Cached offline startup remains unchanged. Verification: [login.test.js](../../tests/login.test.js) exercises delayed approval/session completion, duplicate clicks, failure/retry, cancellation, and both languages; [Login.stories.js](../../frontend/stories/Login.stories.js) covers each visible state.

## Implementation and verification

- [api.js](../../frontend/src/api.js)
- [api-request.js](../../frontend/src/api-request.js)
- [auth.js](../../frontend/src/auth.js)
- [api-request.test.js](../../tests/api-request.test.js)

# Device storage and the outbox

## Device persistence

`local-store.js` uses IndexedDB's `records` store with keys prefixed by the user's workspace key. It persists the latest 20 parent posts and their replies, pending submissions, acknowledgements, and related records. Local identity also lives in localStorage.

The Service Worker caches public application assets. API responses do not enter that shared cache. The build enumerates JS and CSS assets and derives the cache name from content hashes. Newly added transport modules are included in this enumeration.

## Outbox order

1. Assign a stable `local-…` ID and timestamp and finish writing the submission to IndexedDB.
2. Verify connectivity and identity, then send saved submissions in order, with parent posts before their replies.
3. Persist the server acknowledgement under `ack:`.
4. Remove the corresponding `outbox:` record afterwards.

Lost responses retry the same ID and body, relying on the server's duplicate checks. Tabs of one browser share the outbox, so delivery takes a Web Locks lock named per account (`kakeratta-outbox:{key}`) and only one tab sends at a time; where Web Locks is unavailable, delivery is serialised within the tab only and the server's exclusive `CreateEntry` still stores and reacts to a post once. Account changes stop delivery. Failed local persistence rejects enqueue so the input can remain available.

Authentication and transient failures can be retried later. Non-retryable 4xx responses are recorded as delivery errors and stop automatic retries. Manual retry clears those errors and attempts delivery again. Removing or logging out of an account also clears its device records.

## Implementation and verification

- [local-store.js](../../frontend/src/local-store.js)
- [outbox.js](../../frontend/src/outbox.js)
- [sw.js](../../frontend/public/sw.js)
- [build.mjs](../../frontend/build.mjs)
- [outbox.test.js](../../tests/outbox.test.js): restart, retries, and two tabs flushing at once
- [offline-check.mjs](../../frontend/offline-check.mjs)

# Reminder push notifications

The **This device** section of settings includes [push.js](../../frontend/src/push.js). It reports browser support, server configuration, and this workspace's registered subscription without requesting permission on load. Only **Enable notifications** asks for browser permission and calls `PushManager.subscribe` using the server's public VAPID key. [push-client.js](../../frontend/src/push-client.js) saves the subscription through the account-checked API; a failed initial registration revokes the newly created browser subscription. Disabling removes the server record before unsubscribing. A changed VAPID key is replaced when enabling. Browser permission can also be managed in browser settings.

Only scheduled persona reminders, including birthdays, anniversaries, appointments, and persona-created schedules, trigger push. Ordinary replies, daily check-ins, monthly summaries, and post edits do not. The notification title shows the reminder message, with whitespace collapsed and at most 120 Unicode characters followed by an ellipsis when truncated; its body identifies kakeratta. Reminder content therefore appears on the lock screen according to the device's notification settings. The [Service Worker](../../frontend/public/sw.js) verifies `/api/auth/identity` against the payload's workspace before displaying a notification and again before opening it. A mismatched account, failed identity check, or expired login suppresses delivery. A click focuses and navigates an existing app window, or opens `/?entry=…`, using the existing post-opening path.

HTTPS (or a secure localhost context), Service Worker, PushManager, and Notifications support are required. On iPhone/iPad the app must be added to the Home Screen and opened there. Unsupported browsers retain the timeline reminders. Logout and detected account switches attempt server deregistration, revoke the browser subscription even when the deletion request fails, and close displayed notifications. There is no automatic subscription refresh after a browser-side subscription expires; enable notifications again from settings.

Verification: [push.test.js](../../tests/push.test.js) covers permission refusal, failed registration rollback, disable order, offline logout, active-account filtering, reminder message titles, and notification navigation.

# Speech input and read-aloud

## Speech input

The current recognition path uses browser SpeechRecognition / webkitSpeechRecognition. `createVoiceInput` connects the text field and controls, while `createDictation` builds the recognizer. It does not use a server recording-transcription API. The actual recognition provider and availability depend on the browser.

`browser_speech.js` tracks final and interim recognition results, caret position, and manual edits. `speechDraft` applies send phrases, spoken punctuation and newline commands, and replacement rules. State tracking prevents revisions to interim recognition from resurrecting manually deleted text.

## Automatic submission and read-aloud

Posts and replies marked `speechAutoSend` optionally call `formatDictation` before persistence. The low model adjusts punctuation and line breaks while preserving meaning, tone, names, numbers, and other content. On failure, the entry is not saved, and the input is retained for retry.

Read-aloud uses browser speechSynthesis. It selects a voice matching the interface language, a rate, and a conversation, and handles stopping and switching conversations. Delayed completion from old speech must not alter the new conversation's state.

## Implementation and verification

- [voice_input.js](../../frontend/src/voice_input.js)
- [dictation.js](../../frontend/src/dictation.js)
- [browser_speech.js](../../frontend/src/browser_speech.js)
- [speech.js](../../frontend/src/speech.js)
- [dictation.go](../../internal/server/dictation.go)
- [speak.js](../../frontend/src/speak.js)
- [speech.test.js](../../tests/speech.test.js)
- [dictation_test.go](../../internal/server/dictation_test.go)

# Related chapters

- [API and persistent state](./backend.md)
- [Development and workspace operations](./operations.md)
