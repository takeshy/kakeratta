---
type: "Component"
title: "Browser application and device state"
description: "Frontend composition, requests, live updates, offline delivery, speech input, and read-aloud."
tags: ["frontend", "ui", "api", "identity", "offline", "outbox", "speech", "browser"]
updated: "2026-09-30"
---

Frontend composition, requests, live updates, offline delivery, speech input, and read-aloud.

# Browser application composition

## Initialization and components

`bootstrap.js` checks authentication before loading `app.js`. `app.js` assembles the composer, persona list, timeline, and settings, search, memory, and other panels. `screen.js` provides the basic layout; `feed.js` renders conversations and their controls.

Panels have dedicated modules for persona creation and editing, routing checks, reply corrections, memory browsing, and other functions. The app opens and closes them. `dom.js` centralizes DOM creation and notifications; `i18n.js` handles interface language.

## Merging data

Startup displays cached device conversations before fetching server pages. SSE `entry` events add records, while `routed` and `turn` update routing displays. Reconnection refreshes the timeline and sends the outbox.

Polling runs for a limited period after posting to cover missed SSE events. Returning to a visible tab also checks pages and routing. Pending device entries and server records are merged so posts appear in the same conversation before and after delivery.

Dedicated code renders Markdown and creates links. User text and persona Markdown have different display handling. Links are restricted to supported protocols.

External-agent answers (`model: agent`) normalize literal escaped newlines for Markdown display and read-aloud, without rewriting the stored entry.

Person persona creation and editing show an editable **Basic information** field, saved as `basicInfo` with the definition. The builder uses [person-basic-info.js](../../frontend/src/person-basic-info.js), while the editor includes the field in its ordinary form. There is no option to post these details as a fragment. Confirmed dates and appointments register notification schedules when saved. Annual notifications are labeled **Every year** on the persona card. Cards show both host-registered calendar events and persona-created schedules.

[Personas.stories.js](../../frontend/stories/Personas.stories.js) supplies localized sample data for creation, editing, and notification cards. [captures.mjs](../../frontend/captures.mjs) captures the actual UI into `docs/captures/ja/` and `docs/captures/en/`; the manual and landing page reference these files with their current dimensions.

## Implementation and verification

- [bootstrap.js](../../frontend/src/bootstrap.js)
- [app.js](../../frontend/src/app.js)
- [feed.js](../../frontend/src/feed.js)
- [screen.js](../../frontend/src/screen.js)
- [dom.js](../../frontend/src/dom.js)
- [person-basic-info.js](../../frontend/src/person-basic-info.js)
- [feed.test.js](../../tests/feed.test.js)
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
