---
type: "Component"
title: "Browser application and device state"
description: "Frontend composition, requests, live updates, reminder push notifications, offline delivery, speech input, and read-aloud."
tags: ["frontend", "ui", "api", "identity", "offline", "outbox", "speech", "browser", "push"]
updated: "2026-10-01"
---

Frontend composition, requests, live updates, reminder push notifications, offline delivery, speech input, and read-aloud.

# Browser application composition

## Initialization and components

`bootstrap.js` checks authentication before loading `app.js`. `app.js` assembles the composer, persona list, timeline, and settings, search, memory, and other panels. `screen.js` provides the basic layout; `feed.js` renders conversations and their controls.

Panels have dedicated modules for persona creation and editing, routing checks, reply corrections, memory browsing, and other functions. The app opens and closes them. `dom.js` centralizes DOM creation and notifications; `i18n.js` handles interface language. `compose.js` starts the text field at three lines and includes an @ mention hint in its placeholder. `room.js` initially hides all persona badges under the composer on every screen size; **Show personas** expands all badges, grouped into interests and people, and **Collapse** hides them again; a tap on a badge writes `@name` in the composer through `compose.mention`, so a newcomer sees how to ask someone; right-clicking a badge or holding it for 550 ms on touch opens a dialog with **Settings**, **View memory**, and **Edit**, wired to the persona panel and existing memory viewer/editor; moving the touch cancels the hold so a badge can be dragged to a new place within its group (pointer events, so it works by touch), and the order is kept in this browser's `localStorage` (`kakeratta.room.order`) and applied by `arranged`. Badges that step aside glide into place (Web Animations, skipped under reduced motion), and the first time a group has badges to swap, the first one nudges sideways once (`kakeratta.room.hinted`) so they read as movable. `text-size.js` keeps the text size (standard, large, extra large) in this browser's `localStorage`, like the language, and applies it by scaling the root font size at startup and when the Settings sheet changes it; the page is sized in rem, so all of it follows.

## OpenRouter model selection

When an OpenRouter key is entered but no model list has been loaded, settings shows a hint to click **Check OpenRouter connection** to select models. This hint remains visible even before OpenRouter-only mode can be enabled, and disappears once the connection check loads models ([settings.js](../../frontend/src/settings.js)).

## Names and speaker lists

A name on the timeline - a persona's, or the user's; not the app's monthly summary - is a button when the feed is given `onSpeaker` (the main timeline, the search sheet's thread and the speaker sheet itself all are). It opens a sheet of its own, **発言一覧**, which is not a search: `app.js` `openSpeaker` makes it the open panel and `createSpeaker().show` fills it ([speaker.js](../../frontend/src/speaker.js)). It reads `api.speaker` a page at a time - one request, which returns the conversations the page touches with it - and shows them in a feed as the timeline is read - each remark under its post, remarks unfolded - with a "load more". Over the list is a card built like the personas list's - the relationship or condition, a person's names, keywords and collapsed basic information (`basicInfoBox`) - with buttons for the persona's settings, its editor (also opened at the basic information) and what it remembers, wired in `app.js` to the personas panel (`reveal`, `showMemoryFor`, `edit`). The card is redrawn when the personas change (`redrawCard`), and entries arriving on the live channel are upserted (`changed`). A page arriving after another speaker was opened is ignored. The user has no card.

## Merging data

Startup displays cached device conversations before fetching server pages. SSE `entry` events add records, while `routed` and `turn` update routing displays. Reconnection refreshes the timeline and sends the outbox.

Polling runs for a limited period after posting to cover missed SSE events. Returning to a visible tab also checks pages and routing. Pending device entries and server records are merged so posts appear in the same conversation before and after delivery.

Dedicated code renders Markdown and creates links. User text and persona Markdown have different display handling. Links are restricted to supported protocols.

External-agent answers (`model: agent`) normalize literal escaped newlines for Markdown display and read-aloud, without rewriting the stored entry.

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
- [speaker.test.js](../../tests/speaker.test.js): speaker lists and their card
- [Screen.stories.js](../../frontend/stories/Screen.stories.js)
- [Personas.stories.js](../../frontend/stories/Personas.stories.js)
- [captures.mjs](../../frontend/captures.mjs)


# User-approved note organization

Each persona's memory dialog contains **Organize notes** ([memory-organize.js](../../frontend/src/memory-organize.js), wired by [personas.js](../../frontend/src/personas.js)). The user describes the desired organization and chooses the built-in AI or, when connected and unpaused, the external agent using the currently selected connection. Research presents a policy and expandable full replacement bodies and deletion paths. Only **OK, apply this plan** calls the apply endpoint. Editing the request or changing the AI invalidates the displayed approval. External research is polled every three seconds while the dialog is attached; closing it stops polling. Successful application reloads the memory page and overview. The research endpoint uses the transport's 250-second timeout.

Verification: [memory-organize.test.js](../../tests/memory-organize.test.js) checks approval, request changes, agent selection, and safe rendering of proposed bodies.

# Frontend requests and account checks

## Ordinary requests

`api.js` exposes endpoint-specific operations. `createAPIRequest` contains shared transport logic, supplied with the current identity accessor, verification function, and fetch implementation.

When an account exists, the request first verifies its workspace and stops if the account changes during verification. It sends `X-Kakeratta-Workspace`. If the account changes while awaiting a response, that data is not returned to the caller.

Bodies are normally JSON. Persona installation uses `raw: true` to send the original definition string while retaining the same verification, timeout, and error handling. Ordinary requests have a 45-second timeout; longer publishing and trial operations use 250 seconds. API errors retain their HTTP `status`; empty successful responses return `null`.

## Live events

SSE uses `EventSource` and sends the workspace key as a query parameter. Connection failures retry with increasing delays. Registered listeners receive event names and data. With a local account identity, posts and replies enter the outbox rather than being sent directly.

`auth.js` handles Firebase login, session creation, and `/api/auth/identity` verification. It uses cached identity and device storage and reloads the application when the account changes.

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

Only scheduled persona reminders, including birthdays, anniversaries, appointments, and persona-created schedules, trigger push. Ordinary replies, daily check-ins, monthly summaries, and post edits do not. The lock screen shows a generic bilingual reminder rather than the persona's private message. The [Service Worker](../../frontend/public/sw.js) verifies `/api/auth/identity` against the payload's workspace before displaying a notification and again before opening it. A mismatched account, failed identity check, or expired login suppresses delivery. A click focuses and navigates an existing app window, or opens `/?entry=…`, using the existing post-opening path.

HTTPS (or a secure localhost context), Service Worker, PushManager, and Notifications support are required. On iPhone/iPad the app must be added to the Home Screen and opened there. Unsupported browsers retain the timeline reminders. Logout and detected account switches attempt server deregistration, revoke the browser subscription even when the deletion request fails, and close displayed notifications. There is no automatic subscription refresh after a browser-side subscription expires; enable notifications again from settings.

Verification: [push.test.js](../../tests/push.test.js) covers permission refusal, failed registration rollback, disable order, offline logout, active-account filtering, and notification navigation.

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
