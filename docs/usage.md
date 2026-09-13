# Usage and configuration

[README](../README.md) · [日本語](usage_ja.md)

## Running it

```sh
task build      # build (bin/kakeratta)
task run        # start (http://127.0.0.1:8787)
task test       # go test ./internal/... and node --test tests/*.test.js
```

| Variable | Default | |
| --- | --- | --- |
| `KAKERATTA_ADDR` | `:8787` | Listen address |
| `KAKERATTA_HOME` | OS config directory | Where posts, notes and images are kept |
| `KAKERATTA_TOKEN` | empty | Empty means anyone can walk in. **Set it whenever the app is reachable from outside** — this process is the one holding the inference keys |
| `KAKERATTA_FIREBASE_PROJECT_ID` | empty | Enable Google sign-in and per-user workspaces |
| `KAKERATTA_FIREBASE_API_KEY` | empty | Firebase Web API key |
| `KAKERATTA_FIREBASE_AUTH_DOMAIN` | empty | Firebase Auth domain |

With a token set, open `/?token=<URL-encoded token>` once in each browser.
The server sets an HttpOnly session cookie and redirects to a URL without the
token. Subsequent API requests and Android shares use that cookie.
Images are limited to 20 MiB per post, including paste and multipart shares;
oversized shares are rejected rather than saved as truncated images.

When all Firebase variables are set, Google sign-in replaces shared-token
authentication. The server exchanges a Firebase ID token for a signed,
HttpOnly session cookie and gives every Firebase `uid` a separate store,
reaction queue, event stream, and speech service.

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

**Keeping a position and using one are separate decisions.** Needing your
current position to react is not the same as leaving it on the record
afterwards, and the second can be switched off (reactions still use it).

## Timeline

Your posts and lines, the personas' remarks, and your replies to them, in the
order they happened. A post that got a reaction looks exactly like one that did
not. No unread count, and no "an AI is thinking".

**Read-aloud mode** reads the backlog while you are moving (the browser's own
Web Speech API).

## Models and deployment

| For | Setting | Default |
| --- | --- | --- |
| The low tier | `lowModel` | Nemotron Nano |
| The middle tier | `middleModel` | Nemotron Super |
| The high tier | `highModel` | unset (falls back to middle, so two tiers by default) |
| Reading images / OCR | `visionModel` | Nemotron Nano (vision) |
| Routing | `routeModel` | Nemotron Nano |
| Speech recognition | dictation settings | whisper.cpp / OpenAI / Gemini … |
| Read-aloud | — | The browser's own |

**Which model sits in which tier is the difference in experience.** This is not
cost optimisation. The low one is allowed a shallow reaction, allowed to stop at
"I'm losing the thread of this", and the tiers above come along afterwards and
pick it up. Being told clever things one-sidedly is dull, so what matters is
that **the tiers really differ**.

**There are three tiers, but you do not need three models.** An empty tier falls
back to the one below, so the default runs on two (low and middle). Put
something in high and you have three; make them all the same and you have one.
More tiers, more contrast; one tier, none.

- Inference runs on Nebius Token Factory (OpenAI-compatible, so the endpoint is
  interchangeable)
- **The client never calls it directly; a relay sits in between.** Reactions are
  generated asynchronously and server-side, so both the keys and the work belong
  there. Nebius Serverless Endpoints is the intended target
- Reactions are asynchronous (the queue in `internal/persona`). Nebius
  Serverless Jobs is the intended target for the background work
- Posts live server-side because they cross devices. For now that is the
  process's own disk — the blob half of `Store` is the one place to swap for
  Nebius Object Storage
- Web search is intended to be Tavily

## Data

```
<workspace>/settings.json          endpoints, models, tool keys, who is in the room
<workspace>/timeline.jsonl         posts and remarks (append only)
<workspace>/blobs/<id>.png         captures
<workspace>/memory/<id>/           each persona's notes (an OKF bundle; the app does not read inside)
<workspace>/memory/<id>.meta.json  how many times it woke, and so on
<workspace>/skills/<id>/skill.json added personas
```

The timeline is **append only**. An entry is never rewritten: a change (the
image finished being read, it was deleted) is another line with the same id
further down, and the later one wins. Appending costs the same however long the
log gets, and a crash mid-write costs the last line rather than the file.
