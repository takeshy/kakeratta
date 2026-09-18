# Usage and configuration

[README](../README.md) · [日本語](usage_ja.md)

## Running it

```sh
task setup      # once: npm ci for the page's dev tools
task dev        # API (http://127.0.0.1:8787) and page (http://127.0.0.1:5173) together
task run        # API only
task build      # API only (bin/kakeratta)
task stories    # Storybook (http://127.0.0.1:6006)
task test       # go test ./internal/... and node --test tests/*.test.js
```

| Variable | Default | |
| --- | --- | --- |
| `KAKERATTA_PUBLIC_URL` | empty | Public HTTPS origin for Bluesky OAuth |
| `KAKERATTA_BLUESKY_KEY` | empty | Secret for Bluesky signing and encryption (64 hexadecimal characters); see [operator setup](social-sharing-en.md) |
| `KAKERATTA_ADDR` | `:8787` | Listen address |
| `KAKERATTA_HOME` | OS config directory | Where posts, notes and images are kept |
| `KAKERATTA_TOKEN` | empty | Empty means anyone can walk in. **Set it whenever the app is reachable from outside** — this process is the one holding the inference keys |
| `KAKERATTA_FIREBASE_PROJECT_ID` | empty | Enable Google sign-in and per-user workspaces |
| `KAKERATTA_FIREBASE_API_KEY` | empty | Firebase Web API key |
| `KAKERATTA_FIREBASE_AUTH_DOMAIN` | empty | Firebase Auth domain |
| `KAKERATTA_MCP_SERVERS` | empty | JSON list of MCP servers: `[{"name":"geo","url":"https://…/mcp","headers":{…}}]`. Their tools appear as `name.tool` |
| `KAKERATTA_NOMINATIM_URL`, `KAKERATTA_OVERPASS_URL`, `KAKERATTA_OSRM_CAR_URL`, `KAKERATTA_OSRM_FOOT_URL`, `KAKERATTA_OSRM_BIKE_URL` | public OpenStreetMap services | Where `place_search` and `route` go |

Tool endpoints and MCP servers are the operator's configuration and the same
for every user. The Tavily key for `web_search` is the user's own, in Settings.

With a token set, open `/?token=<URL-encoded token>` once in each browser.
The server sets an HttpOnly session cookie and redirects to a URL without the
token. Subsequent API requests and Android shares use that cookie.
Images are limited to 20 MiB per post, including paste and multipart shares;
oversized shares are rejected rather than saved as truncated images.

When all Firebase variables are set, Google sign-in replaces shared-token
authentication. The server exchanges a Firebase ID token for a signed,
HttpOnly session cookie and gives every Firebase `uid` a separate store,
reaction queue and event stream.

## Landing page and manual

Two pages sit beside the app on the same host: `/lp/` introduces it and
`/manual/` explains how to use it, with the README's screenshots. Both carry
Japanese and English in one document and a switch in the header; the choice is
kept under the same browser key as the app's display language, so the app, the
landing page and the manual agree. A `?lang=ja` or `?lang=en` in the URL wins,
so a link can carry a language. The **?** icon in the app header opens the
manual in a new tab, in the app's current language.

The pages are plain HTML in `frontend/lp/`, `frontend/manual/` and
`frontend/site/` (shared stylesheet and language switch). `node build.mjs`
copies them to `frontend/dist` along with `docs/captures` as `/captures/`;
in development Vite serves the captures from `docs/` directly.

## Ways in

A thought is fastest written; something on screen is fastest shot. They all go
through the same single box.

| Way in | What it is |
| --- | --- |
| Write | A line. For a thought this is the quickest |
| Shoot / paste | An image (paste with Ctrl+V, hand it over from the share sheet, pick a file) |
| URL | The body of a public page is fetched |
| Speak | A transcript. The send phrase commits it |

What makes an image strong is that it **does not care whether you were signed in
or which app it was**. Kindle, a paywalled article, a social feed — if it is on
screen it can be shot. No API and no DRM in the way. Shooting a passage that
caught your eye mid-read and adding a line only works this way.

Only the shoot-and-hand-over route from a phone needs anything set up.

| | Shoot and hand over | Needs |
| --- | --- | --- |
| PC | The screenshot is already on the clipboard → paste it (Ctrl+V) | Nothing (a browser) |
| Android | PWA + Web Share Target puts it in the share sheet | Nothing (PWA) |
| iOS | A shortcut (takes an image from the share sheet and POSTs it) | Nothing |

### Android

Add it to the home screen and the `share_target` in `manifest.webmanifest` makes
the OS treat it as somewhere to share to. It appears in the share sheet like a
native app, and an image handed over lands in the timeline. `sw.js` takes the
shared POST and forwards it to `/api/capture`.

### iOS

Web Share Target is unavailable, so a shortcut stands in for it. No native code.

1. Create a shortcut, turn on "Show in Share Sheet", and accept images
2. "Get contents of URL" — method `POST`, body `Form`
   - `image` … the shortcut's input
   - `text` … optional; the result of "Ask for input" if you want to write there and then
   - Header `Authorization: Bearer <KAKERATTA_TOKEN>`

While reading you can throw things in silently and add the lines later, in one go.

`/api/capture` takes multipart, so it has to be on a server. Static hosting
cannot receive it.

## What travels with a post

**Only where you are and what time it is.** Where you are going and when you
want to be there is in what you wrote ("I want to be in Shibuya by 8"), and the
persona reads that off the text.

**The place is optional.** A post from a desktop has no meaningful position. It
has to work without one.

When a position is available, it is kept with the post and used for reactions.

## Timeline

Your posts and lines, the personas' remarks, and your replies to them, in the
order they happened. A post that got a reaction looks exactly like one that did
not. No unread count, and no "an AI is thinking".

It starts with the latest 20 posts and loads 20 more as you scroll. Remarks and
replies fold under **N replies**; **Add a comment** or **Reply** opens that
thread. Reactions normally arrive over the live channel, but in case one is
missed, the timeline is re-read every 10 seconds for five minutes after you post
or reply (not while the tab is in the background).

**Read-aloud mode** reads the backlog while you are moving (the browser's own
Web Speech API).

**⚖ Router's call**, on your own posts and replies, opens a dialog with what the
app decided about that post: the personas it chose, and for each one it passed over, the
router's one-line reason. A persona you mentioned with `@` is not listed. A
post the router could not judge (no model configured, the call failed) reads as
"no record", which is different from a post it sent to nobody. For each
persona that was called you see whether its turn has finished (`done` in
`routing.json`), whether it replied, and whether one of its notes cites this
post. **A reply was wanted** opens the reply dialog
below; the trial then runs from the post itself and also lists the notes it
would write.

From each row you can correct the persona's condition. **Should have been
called** and **Should not have been called** open the same dialog: say why, the
persona builder rewrites the condition (only the condition), and the result is
shown as text you can edit. **Ask the AI to change it** sends a new request
that works from whatever is in the box. **OK** saves the condition and then has
the router try the same post again without waking anyone. If it lands as you
meant, the dialog closes; if not, the router's reason is shown and you are
asked again. The save is not rolled back.

**Fix this reply**, on a persona's remark, does the same for the persona's
tone and instructions. Say how the reply missed; the persona builder rewrites
the tone and the instructions and nothing else, both shown as text you can
edit. **OK** saves them and then has the persona take the same turn again on
the same post (`POST /api/entry/{id}/reply/preview`). The turn is the real
thing - it reads its notes and may use its lookup tools - but runs over a
workspace that keeps nothing: no remark is appended, no note or schedule is
written, no wake is counted. The reply it would give is shown; **That's it**
closes and **Still not it** asks again with that reply quoted. This call can
take a few minutes, since a persona's turn may be planned and reviewed.

## Fast startup and offline posts

The browser saves the latest 20 original posts and their replies per user, displays them immediately on the next visit, and refreshes in the background. After opening the app online once, you can save text, images, URLs, and replies while offline. Unsent posts are kept separately from the 20-post limit. They send automatically when connectivity and authentication return while the app is open. Persona reactions and URL/image analysis start after the server receives the post.

“Saved on this device · waiting to send” means the server has not received the post yet. Use “Reconnect and retry” after an error. Retries reuse the same post ID to prevent duplicates after a lost response. Clearing browser data removes unsent posts. Signing out clears this device’s saved data and asks for confirmation if posts are still waiting to send.

[Illustrated instructions](https://kakeratta.net/manual/?lang=en#en-offline)

## Share a conversation to Bluesky

In Settings, open Bluesky connection, enter your handle, and authorize on Bluesky. Each user connects their own account. Choose Share to Bluesky on a post, select the messages, preview, and publish. Persona replies start unchecked, and selected replies require their parents to be selected too. Published replies preserve their parents and include persona attribution.

[Illustrated instructions](https://kakeratta.net/manual/?lang=en#en-bluesky) · [Operator setup and publication recovery](social-sharing-en.md). Disconnecting or deleting a post in kakeratta does not delete posts on Bluesky.

Already published messages are not sent again. Newly selected replies can be added to their existing parent posts. Publishing is disabled while sending or when all selected messages are already posted. The published conversation has a single link to its root post.

## Search

**Search** in the header and the personas' `timeline_search` tool share one
implementation. It covers text, URLs, image readings, place labels, and replies;
deleted posts and their replies never appear.

Enter a name such as “Pinot” or a description such as “a light red I enjoyed” in
one search field. Short terms use keyword search without waiting for AI. Questions
and sentences (for example, a question mark or “find” / “tell me”) use meaning
search. After keyword search, “Search similar meanings too” broadens the search,
even with zero results. Use “Search exact words only” to switch back when the
automatic choice does not fit.

- Keyword search normalizes case, character width, and hiragana/katakana variants and requires all space-separated terms to occur in the post. It makes no embedding request.
- Meaning search and the personas’ default search also combine lexical and semantic rankings, merging duplicate posts. Their lexical branch splits Japanese script runs,
  and uses Han substrings. More matches and rarer terms rank higher. This is a
  lightweight tokenizer, not morphological analysis. It reads all posts on each
  search, so reads grow with the timeline.
- Meaning search uses `Qwen/Qwen3-Embedding-8B` (1024 dimensions) at the configured
  AI endpoint. Candidates below cosine similarity 0.5 are excluded from this
  branch; matching words can still retrieve those posts. Personas can adjust
  this floor with `min_score` (0–1). The default is provisional and needs real-data
  evaluation.
- If meaning search is unconfigured, fails, is busy, or takes more than eight
  seconds, word matches are returned with a notice. An empty fallback result
  does not mean all records were successfully checked for semantic relevance.

Personas receive complete matching chunks, authors, dates, and sources. Word
matches use chunks of up to 2,000 characters; semantic chunks are up to 2,000
locally or 8,000 in Firestore. Both are supplied if they contain different evidence.
Ranking scores are not confidence. Personas must verify evidence in the text and
refine the query when evidence is missing.

Document embeddings are registered in the background after posts and replies are saved, and again when image analysis completes. OCR text, image descriptions, and source hints are included. Saving a post does not wait for embeddings. Startup and AI connection settings changes also schedule missing or updated documents; failures retry after 30 seconds. Search-time backfill remains a recovery path. One indexing pass runs at a time, reusing saved vectors. Word matching also treats “サザエ”, “さざえ”, and “ｻｻﾞｴ” alike.

Where vectors live:

- `KAKERATTA_STORE=firestore`: in each user's `searchPassages` collection.
  Firestore vector search (`FindNearest`, cosine distance) returns the nearest
  100 passages, grouped by post. Requires the vector index in
  `firestore.indexes.json` ([deployment](../infra/README.md#firestore-indexes)).
- `KAKERATTA_STORE=file`: in `<workspace>/social-state/          encrypted Bluesky connections and OAuth state
<workspace>/publications/          external publication history per account and entry
<workspace>/search-embeddings/`, with similarity computed in Go.

## Voice input

Dictation uses browser speech recognition. “Polish text with AI before auto-send” is OFF by default. When enabled, only a spoken send phrase triggers polishing by the low model (Nemotron 3.5 Lightning by default), before posting or replying. Manual sends and silence stops do not trigger polishing. If polishing fails, the draft remains without being posted.

## Models and deployment

Kakeratta uses NVIDIA Nemotron models through Nebius Token Factory.

- Nemotron 3.5 Lightning — routing, low-tier personas, and optional dictation polishing
- Nemotron Super — middle-tier personas and workflow review
- Nemotron Ultra — high-tier personas and persona drafting
- Nebius Token Factory — inference backend

Image reading uses GLM-5.3-Flash and meaning search uses
Qwen3-Embedding-8B on the same backend.

![Settings: display language, model, web search, Bluesky connection, voice input, read-aloud](captures/en/settings.png)

| For | Setting | Default |
| --- | --- | --- |
| The low tier | `lowModel` | Nemotron 3.5 Lightning |
| The middle tier | `middleModel` | Nemotron Super |
| The high tier | `highModel` | Nemotron Ultra |
| Reading images / OCR | `visionModel` | GLM-5.3-Flash |
| Routing | fixed | Nemotron 3.5 Lightning |
| Workflow review | fixed | Nemotron Super |
| Persona drafting and editing | high tier (falling back to middle, then low) | Nemotron Ultra |
| Meaning-search embeddings | fixed | Qwen3-Embedding-8B |
| Speech recognition | dictation settings | Browser speech recognition |
| Read-aloud | — | The browser's own |

The page has no model picker: users enter an API key and the tiers keep these
defaults. To change a model for a workspace, edit the keys above in its
`settings.json` (or `PUT /api/settings`); the page keeps working with whatever
is stored.

**Which model sits in which tier is the difference in experience.** This is not
cost optimisation. The low one is allowed a shallow reaction, allowed to stop at
"I'm losing the thread of this", and the tiers above come along afterwards and
pick it up. Being told clever things one-sidedly is dull, so what matters is
that **the tiers really differ**.

**The default has three distinct tiers: Lightning → Super → Ultra.** On Token Factory,
an empty high setting also uses Ultra. Each persona chooses its tier; a high-tier
persona receives earlier reactions as context for more complex reasoning.

- Nebius Token Factory provides Nemotron inference, GLM image reading, and Qwen embeddings.
- The browser sends requests to the Go API, which holds the keys and runs reactions asynchronously.
- In production, Cloud Run hosts the API, Firebase Hosting serves the page, and Firestore and Cloud Storage persist user data. Local mode stores data on disk.
- Tavily is the web search provider; each user enters their own key.

## Data

```
<workspace>/settings.json          endpoints, models, the web search key, who is in the room
<workspace>/timeline.jsonl         posts and remarks (append only)
<workspace>/blobs/<id>.png         captures
<workspace>/memory/<id>/           each persona's notes (an OKF bundle; the app does not read inside)
<workspace>/memory/<id>.meta.json  how many times it woke, and so on
<workspace>/routing.json           the router's call per post: who was chosen, who was passed over and why
<workspace>/skills/<id>/skill.json added personas
<workspace>/search-embeddings/     semantic search vectors (rebuilt on the next search if removed)
```

The timeline is **append only**. An entry is never rewritten: a change (the
image finished being read, it was deleted) is another line with the same id
further down, and the later one wins. Appending costs the same however long the
log gets, and a crash mid-write costs the last line rather than the file.
