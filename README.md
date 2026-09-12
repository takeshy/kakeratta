# kakeratta

English | [日本語](README_ja.md)

**Ka-ke-ratta.** Japanese for the act of tossing in a *kakera* — a fragment.

**Keep tossing in whatever you thought of or noticed, and AIs that each grew a
separate memory for their own subject react — but only when it is theirs.**

Write it, shoot it, paste it, or say it. It all lands in one timeline, and
reactions arrive late. Sometimes none arrive at all.

The food one remembers only food. Technical articles and holiday photos never
enter its context, and because they never enter, they never dilute it. Something
that has accumulated nothing but six months of your meals can produce "that
place you liked before" at once, and it has a sense of what you tend to choose.

Throw everything at one general-purpose AI and the history grows while the
concentration drops. Splitting it is what keeps it concentrated.

There is no work to teach it your taste. No questionnaire, no history to
connect. Keep putting things in and things accumulate.

One Go binary with the page embedded. No native app. MIT licensed.

## Design principles

**Memory is divided by subject.** The point of the split is not to hide
anything — it is to **keep the concentration**.

**The app only routes.** Who gets a post is decided centrally. Whether to speak,
what to remember and when to compress it are each persona's own. The split
exists so that adding personas does not wake all of them every time; the app
takes no part in what comes back.

**Silence is fine.** Not everything gets a reaction. This is not an app for
having things explained to you, and being reacted to every time you just wanted
to look at something is an imposition. A post nobody answers is a normal post.

**Not speaking and not remembering are different things.** A post it said
nothing about still goes into its notes, if it is its subject.

**Name it with @ and it always answers.** Because the subjects are separate,
there is something to choose between.

**Asynchronous by default.** Reactions arrive late.

## What it is for

**Bringing back something you liked.** A capture from six months ago is still
there, if it is that persona's subject.

**Knowing what you tend to choose.** Without being told.

**Where you are and what time it is.** Write "I want to be in Shibuya by 8" and
the travel one works out the route from where you are now. Write "I'm hungry"
and the food one picks somewhere open nearby, taking what you usually go for
into account.

**A different subject means silence.** The food one says nothing about a
technical article.

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

With a token set, open `/?token=<URL-encoded token>` once in each browser.
The server sets an HttpOnly session cookie and redirects to a URL without the
token. Subsequent API requests and Android shares use that cookie.
Images are limited to 20 MiB per post, including paste and multipart shares;
oversized shares are rejected rather than saved as truncated images.

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

### If a native app were built

Not needed for now. If it ever moves that way, iOS would be a Share Extension
(a separate target, App Group or a direct POST) and Android an `ACTION_SEND`
intent-filter. Wails v3 could host both, but its mobile support is officially
experimental and outside the desktop beta's compatibility guarantee. It is not
in scope.

## What the app does

Three things.

1. Take the post
2. **Route it to the personas it belongs to**
3. Put remarks on the timeline

Whether to summarise and whether to speak are not the app's decisions.

Notes are not on that list. Reading and writing them is done by the persona
through its tools, so the app only provides the path — it puts bytes somewhere
when `notes_write` is called, and nothing more than that.

**Routing is a light judgement** (`internal/persona/route.go`). It reads the
subjects and drops what is plainly none of a persona's business. Being handed a
post is not a promise to speak; staying quiet is still the persona's call. When
the judgement fails, everyone gets it — routing is an optimisation that lets the
number of personas grow, not a way to swallow a post.

**An @ mention skips the routing.** A named persona always gets the post and
always answers. Routing runs as usual for everyone else.

### Order

Each persona is set to **low / middle / high**. They are **not run in parallel:
low goes first and the order runs upwards.**

The exchange only works because what came earlier is in the later input. Run
together, the larger models cannot see what the smaller one said, so they have
nothing to pick up on.

```
post
 ↓ the image is read once (OCR, what is in it, where it seems to come from)
 ↓ routing
low     runs ──→ anything it said is added to the context
 ↓
middle  runs (with low's remark in the input)
 ↓
high    runs (with low's and middle's remarks in the input)
 ↓
the tiers that already ran are handed what came after them
(**they cannot speak**; notes only)
```

**What came later is handed to the tiers that went earlier.** Not speaking and
not remembering are different things, so it is passed once, to be remembered.
But **they cannot speak on that turn** — otherwise they would keep answering
each other unprompted and the timeline would fill with a conversation that is
not yours. When the higher tiers all stayed quiet there is nothing to pass on,
so that extra turn does not happen at all (staying quiet is the default, so
this is the normal case).

Nothing waits on a lower tier that said nothing. Personas in the same tier run
together; the order only matters between tiers. The higher tiers answer later
still, which is acceptable because asynchronous is the default.

### Reading the image

When a post arrives the app reads the image once (`internal/capture`). Handing
the picture to every persona would mean paying for vision once per persona for
the same screenshot, so it is turned into text and shared. A persona that
decides it needs the detail returns `{"action":"image"}` and gets the original.

## Personas

### What each one is given

- The post it was routed, and what the app read out of it
- **The remarks under that post** (other personas' and your own replies)
- Its own notes

**Not the whole timeline.** Hand that over and every persona sees everything
again, which is exactly what dividing by subject was for. The routing sits in
the middle to avoid it.

**The past exists only in its own notes.** There is no path back to fetch it. If
something is not there, that persona decided it was not worth keeping.

The remarks under the same post are shared so that the exchange, and your
replies, work at all.

### What each one returns

Speak, or stay quiet. Either way it updates its own notes.

- **Silence is fine.** Not everything gets a reaction. A post nobody answers is
  a normal post.
- **Not speaking and not remembering are different things.** A post it said
  nothing about still goes into its notes, if it is its subject.

### Notes

**Each persona grows its own.** What to write, what to keep and throw away, when
to compress — all of it is left to what the skill says. The food one may care
about tendencies; another may care about individual events.

**The app holds no threshold.** A persona sees its own notes every time, so it
can tell for itself when they have grown unwieldy. "See notes" in the UI shows
them as they are.

### The format is handed over: OKF

The [OKF (Open Knowledge Format)](https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf)
skill is handed to each persona, and **it reads and writes its own notes** as a
bundle in that format: one concept per file, YAML frontmatter and a markdown
body.

```
=== index.md ===
# Tendencies
* [Leans tonkotsu](/tastes/tonkotsu.md) - Picks tonkotsu when there is a choice.

=== tastes/tonkotsu.md ===
---
type: Preference
title: Leans tonkotsu
description: Picks tonkotsu when there is a choice.
---
# Evidence
* 09-08 chose tonkotsu over shoyu when both were there
```

**The persona does the reading and writing itself, through tools.** When it is
woken it is handed **the list only** — never the contents.

| Tool | |
| --- | --- |
| `notes_read` | Read contents (only what it wants) |
| `notes_write` | Write one file. **Only that file** is replaced; the rest are untouched |
| `notes_forget` | Throw one file away |
| `notes_list` | Fetch the list again |

They need no declaration. What a skill declares are the tools that reach
**outside**, and its own notes are not outside.

This is what keeps a grown memory from travelling in full on every wake.
Rewriting everything to change one line sends the whole memory out and back, and
anything not copied across disappears without a trace.

**The app does not read the notes.** It puts bytes at a name — it does not parse
frontmatter, does not build the index, does not check that the format holds.
From the app's side there is no telling whether the contents are OKF at all. If
a persona does not follow it, so be it.

The files are plain files on disk, so you can `cat` them, and **they can be
pointed at as an OKF knowledge source elsewhere**. Something that accumulated
nothing but six months of your meals becomes an asset you can take with you.

The skill is `internal/persona/skills/okf/SKILL.md`. The format is OKF as it is,
but the steps are written against this app's tools. It rides along in every
persona's prompt, so it has to stay short.

Which `type` values to use and when to merge differ by subject, so they go in
each skill's `instructions` — `Preference` and `Shop` for the food one, `Book`
and `Theme` for the reading one. **The format is handed over; the policy belongs
to the skill.**

### Skills

| Field | |
| --- | --- |
| Name, icon, tone | How it shows up and how it talks |
| Subject | What it reacts to and remembers. **The router reads this** |
| Instructions | How to keep notes, when to compress, what is worth saying. **Only it reads these** |
| Model | `low` / `middle` / `high` |
| Tools | Only what is declared can be called |

Four ship with the app (たべ food / みち travel / くだ tech / しおり reading). A
`skill.json` can be added from the UI.

```json
{
  "id": "wine",
  "name": "ぶどう",
  "icon": "🍷",
  "domain": "Wine. Labels, regions, photos of bottles, what was drunk. Does not react to food in general or to technical talk.",
  "tone": "Understated. Never pushes a recommendation.",
  "instructions": "Concepts are type: Wine and type: Preference. When the same preference shows up three times, merge it into a Preference and delete the bottles behind it.",
  "model": "high",
  "tools": ["web_search", "place_search"]
}
```

**There is no ceiling on how many.** Add as many as you have subjects. Because
the routing is central, only the few that matter wake for a given post, and the
routing judgement happens once per post.
**How many speak at once is a separate problem**, though: if a post straddles
subjects and everyone answers, it is a chorus rather than a conversation. That
part gets tuned once it is actually in use.

**Without separate subjects, @ means nothing.** If there is nothing to choose
between, naming someone is pointless. A difference in model size makes the
exchange work, but it is not a reason to pick one over another.

### Tools

Only what is declared can be called (`internal/tools`). The app makes the call
and the keys stay server-side, which is also how swapping providers and
insisting on a source link stay the app's guarantees rather than each skill's.

| Name | What it is | Key |
| --- | --- | --- |
| `web_search` | Tavily / Brave / SearXNG | Yes (not for SearXNG) |
| `place_search` | Places nearby, with **opening hours** (OpenStreetMap / Overpass) | No |
| `route` | Travel time from where you are (OSRM; roads only, no public transport) | No |

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

## Where it stands

Done: the share-sheet entry point (PWA share_target / iOS shortcut); posting
(capture, paste, URL, speech); reading the image once; the timeline and replies;
routing; personas' notes and remarks; the low→middle→high exchange; place and
time; tools (web search, places, routes); read-aloud.

**The entry point was built first**, because it is the only part that either
works or does not. The notes machinery and the reaction threshold degrade
gracefully if they disappoint; if the entry point does not work, the design
changes. That it works is pinned by `internal/server/server_test.go`, and the
order of the exchange by `internal/persona/runner_test.go`.

Unverified:

- Whether the iOS shortcut route — take an image, POST it — actually holds up on
  a real device
- Whether Android's Web Share Target really appears when sharing a screenshot
  from the Kindle app
- Whether Token Factory serves vision, and what each Nemotron size can do
- OCR quality, particularly Japanese and Kindle's vertical text
- The reaction threshold. Too low is meddling; too high looks dead

## Layout

```
internal/timeline   one list of posts and remarks (cutting out a thread, writing it into a prompt)
internal/capture    reading an image once, and fetching a public page
internal/persona    skills, routing, the reaction loop (low→middle→high), the notes format handed over (skills/okf)
internal/tools      the tools only a declaration can reach
internal/websearch  search providers
internal/llm        an OpenAI-compatible client (images and file input included)
internal/speech     dictation backends (the relay that keeps the keys server-side)
internal/store      the append log, images, notes, settings
internal/server     the HTTP surface (the page, the API, SSE)
frontend/src        ES modules, no bundler
```

Dictation is the port from speech-popup (gemihub-desktop's speech feature),
carried over as it was. Three engines: the browser's own recogniser, a recording
sent to a speech API, and live transcription. The original tests are here too
(`node --test tests/*.test.js`), alongside regression tests for the app.
