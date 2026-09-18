# Under the hood

[README](../README.md) · [日本語](technical_ja.md)

You do not need this document to use kakeratta. The aim is that a user does two
things only: post what they are thinking, and tell a persona in plain words what
they would like it to do. This page collects what runs underneath to make that
possible, and what the person running the server decides.

## The flow

When a post arrives, a lightweight routing step wakes only the personas that
seem relevant. Each of them decides for itself whether to speak, to remember,
or to do nothing.

Long-term memory uses [OKF (Open Knowledge Format)](https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf),
split per persona and then per topic. Instead of reading one huge context every
time, a persona reads and writes only the notes it needs.

Everything stays in the timeline, including what no persona remembered. Keyword
search and meaning search find a past fragment when it is needed.

Inference runs on NVIDIA Nemotron through Nebius Token Factory, with light,
middle, and high models for different jobs. Personas can also use tools such as
web search and MCP servers.

A note has to be writable the moment it comes to mind, so a Service Worker lets
the timeline be read and written offline.

Put together, the shape is this:

**A mixed timeline → personas by subject → structured memory per persona**

Inside there are MCP, RAG, OKF, skills, meaning search, and model routing. None
of it needs to be understood to use the app.

## Stopping AIs from talking to each other

Several AIs reacting to one another burn tokens and fill the timeline with a
conversation among themselves.

So personas run in the order light → middle → high, and each later model can see
the reactions so far. A later judgement can be fed back to an earlier persona so
it updates its memory, but that persona cannot speak again in the same turn.
This keeps AI-to-AI chatter from continuing while still making use of the
stronger model's judgement.

The order is described in detail in [Design and personas](architecture.md#order).

## Routing is matching against conditions, not judgement

At first, Lightning was asked to judge who a post should go to. Each persona's
description included what we wanted from it and what it would actually do, and
posts were misrouted, sometimes missing the persona that should have reacted.

So routing moved to Ultra. Ultra also misbehaved at first, but responded
clearly to prompt fixes, and after some tuning it ran steadily.

Then the actual spend showed up in Usage, and the setup was reconsidered. Ultra
by itself is not extreme, but one post in kakeratta triggers several AI steps,
and in some cases the total came to one or two yen per post. A personal AI
should never make the user ask "is this worth sending to an AI?"; the small
events of a day should go in freely. So the cost of the router, which runs on
every single post, mattered.

Comparing the six production personas over the latest twenty posts, at 150 yen
to the dollar, one routing call cost about 0.069 yen on Lightning and about
0.456 yen on Ultra. Simply switching to Lightning at that point agreed with
Ultra on 17 of 20 posts, but the three disagreements were two personas that
should have reacted and were missed, and one broken response that ran to 8,192
tokens without a decision. Close on paper, but a router that misses personas
and breaks could not go to production. Agreement here is between models, not a
human-checked accuracy.

If a high-end model gets it right and a low-end one gets it wrong, the routing
conditions are probably too complex. So what the router reads about each
persona changed from "what it is responsible for achieving" to a *routing
condition*, and the job became a plain check of whether the post matches. That
helped, but personas were still overlooked, so `reasons` for the chosen ones
was dropped in favour of **a required `excluded_reasons` for every persona not
chosen**. Concrete examples were also removed from the router prompt, because
they pulled the judgement towards themselves.

Through the intermediate runs, agreement with Ultra stayed at 15 to 17 of 20,
and what improved was the substance: broken responses disappeared, and the
food photo and the book question that had been missed were picked up. The
final production configuration reached 18/20. The two remaining disagreements
were posts like "did I bathe today, maybe again", too immediate to be worth a
schedule: Ultra ignored them and Lightning took them as plans, and either
reading is defensible. Both were checked by hand and judged acceptable. The
method and every run's figures are in
[Routing model comparison](routing-cost-experiment.md).

The good thing about this design was that showing the exclusion reasons to the
user suggested a feature: the user can improve the routing themselves. Not even
Ultra routed perfectly from the start, so when a routing feels wrong, the user
says why with "I wanted it called", and that grew into the way personas are
trained.

## Persona actions as a workflow, with constraints and a review

Apart from the router, there is execution control so that what a persona
generates is not run as is. A persona hands in a **workflow**, its writes and
its remark in order, and the app checks it before running it.

One thing Nemotron (and LLMs in general) is poor at is doing two similar things
in the same flow: it prefers to skip the second. The core of this app is that a
persona remembers a fragment and replies when there is a reason to. When the
note and the remark would say the same thing, it tends to write the note and
forget to speak, or the other way round. So a remark now requires a note, and
the `speak` step is always required even when only a note is wanted, with an
empty text meaning silence, which forces the question of whether to speak.

For schedules, it does no harm for the schedule and the note to say the same
thing, so the opposite was done: one step writes the note and places the
schedule together.

A wrong schedule costs trust, so for a persona that holds the schedule tool the
workflow goes to Super for review, including the note's contents. If they do
not agree, or the persona spoke without placing the schedule, the workflow is
sent back to the persona.

## Models

Kakeratta uses NVIDIA Nemotron models through Nebius Token Factory. Persona
execution, routing, and workflow review use native tool calling
(`tool_choice: "required"`); image reading, persona drafting, dictation
polishing, and embeddings use the provider's structured-output or embedding
APIs instead.

| Model (Token Factory) | Role |
|---|---|
| Nemotron 3 Ultra | High-tier personas and persona drafting |
| Nemotron 3 Super | Middle-tier personas; reviews workflows that reach beyond notes and remarks |
| Nemotron 3.5 Lightning | Routes posts; low-tier personas; optional polishing of dictated text |
| GLM-5.3-Flash | Reading screenshots and photos, once per post |
| Qwen3-Embedding-8B | Meaning search over past posts |

Web search uses Tavily. What was hard about making this dependable — and what
was done about it — is written up in [What was hard about the AI](ai-lessons.md).

## Running it

Kakeratta is a browser app in two pieces: a static page and a Go API. Vite
serves the page while developing and Firebase Hosting serves it deployed; the
Go binary is only the API, on Cloud Run or on your own machine.

Requirements: Go 1.26 or later, Node.js, and Task (the `task` command).
Run from the repository root:

```sh
task setup  # once: the page's dev dependencies (Vite, Storybook)
task dev    # the API on :8787 and the page on :5173, together
```

Open <http://127.0.0.1:5173>. A new workspace has nobody in the room yet, and the
screen leads with that until you call someone in:

<img src="captures/en/nobody.png" alt="Nobody in the room yet: call in a built-in persona with one tap" width="390">

Then enter your Nebius Token Factory API key in Settings. Lightning, Super, and Ultra are configured for the three tiers by default. The page proxies `/api` to the binary, the same split the
deployed setup has. `task run` starts only the API, `task build` builds only the
binary (`bin/kakeratta`), `task stories` opens Storybook, and `task test` runs
the tests.

| Variable | Default | Purpose |
| --- | --- | --- |
| `KAKERATTA_PUBLIC_URL` | empty | Public HTTPS origin for Bluesky OAuth |
| `KAKERATTA_BLUESKY_KEY` | empty | Secret for Bluesky signing and encryption (64 hexadecimal characters); see [operator setup](social-sharing-en.md) |
| `KAKERATTA_ADDR` | `:8787` | Listen address |
| `KAKERATTA_HOME` | OS config directory | Storage for posts, notes, and images |
| `KAKERATTA_TOKEN` | empty (no authentication) | Access token |
| `KAKERATTA_FIREBASE_PROJECT_ID` | empty | Enables multi-user Firebase Authentication |
| `KAKERATTA_FIREBASE_API_KEY` | empty | Public Firebase Web API key |
| `KAKERATTA_FIREBASE_AUTH_DOMAIN` | empty | Firebase Auth domain |
| `KAKERATTA_GCS_BUCKET` | empty | Private Cloud Storage bucket for images |
| `KAKERATTA_STORE` | `file` | Set to `firestore` for Cloud Run |
| `KAKERATTA_FIRESTORE_PROJECT_ID` | Firebase project | Firestore project |
| `KAKERATTA_MCP_SERVERS` | empty | JSON list of MCP servers whose tools personas may use |
| `KAKERATTA_NOMINATIM_URL` etc. | public OpenStreetMap services | Place and route endpoints (see below) |

**Set `KAKERATTA_TOKEN` whenever the app is reachable from outside.** Open the
page as `/?token=<URL-encoded token>` once in each browser; it claims the token
through the API and keeps a session cookie from then on.

For a public multi-user deployment, set all three Firebase variables instead.
Users sign in with Google and each gets isolated data. Firebase authentication
takes precedence over the shared `KAKERATTA_TOKEN` mode. Where the data lives:

- `KAKERATTA_STORE=firestore` (for Cloud Run): posts, settings, persona notes, and persona definitions go to Firestore under `users/<uid-hash>`.
- `KAKERATTA_STORE=file` (default): the same data goes to disk under `KAKERATTA_HOME/users/<uid-hash>/`.
- With `KAKERATTA_GCS_BUCKET` set, images go to the private bucket under `users/<uid-hash>/blobs`, with either store.

## Check-ins and timers

Each active persona can opt into **Daily check-in** in the Personas panel, with
one preferred time per day (off by default). The browser's time zone is saved with that time, including
daylight-saving rules; it stays saved when you travel and is not limited to JST.
There is currently no time-zone picker. A persona can read its memory, search
fragments, and use its usual tools; it starts a timeline thread only when it has
something worth sharing. Silence leaves no system post or empty thread.

A persona that declares the `schedule` tool asks the app to wake it at a given
minute: up to 20 pending schedules per persona, 30 days ahead, once or every
day. The app holds the schedule the way it holds the endpoint and the active
personas; the persona decides its contents and asks for them as it writes the
note they belong to. The time comes from what the persona recorded rather than
from a question to you, and it is always shortly *after* the moment - plana asks
"did you get the flowers?" rather than nudging beforehand. A schedule is claimed
before the model runs, so a silent or failed wake is not repeated, and one more
than 10 minutes late is dropped rather than replayed. Deleting a note wakes the
persona so it can revisit the schedules that went with it; forgetting a
persona's whole memory, pausing it, or removing it drops its schedules.

Deleting a post works the same way. The app removes the post and tells the
personas it had woken for it, so each can drop or correct what rested on it. Who
to tell comes from the app's own record of who it woke - it never reads a note
to find out - and a persona that finds nothing of its memory resting on the post
says nothing. The app never edits a note itself.

The server checks once per minute and uses the existing execution queue, so a
busy queue can delay a wake. The server must be running; the browser can be
closed. Configured workspaces are restored after server restarts, including
Firestore users. The provided Cloud Run configuration already keeps one
instance running with CPU allocated outside requests.

A persisted claim limits each persona to one check-in attempt per local
calendar day, including silent or failed attempts. Firestore claims are
transactional across instances; local storage assumes one server process per
workspace. Changing the time or forgetting memory does not reset the claim.
After downtime, only today's due check-in runs; missed days are not replayed. A
crash after claiming can skip that day's check-in rather than repeat it.

## Tools are the operator's

[Tool guide: purposes, setup, examples, and limits](https://kakeratta.web.app/manual/?lang=en#en-tools)

Where a persona's tools go is configured by whoever runs the server, through
environment variables, and is the same for every user: the map endpoints and
the MCP servers. The one thing a user sets is their own Tavily API key for
`web_search`, in Settings. A skill declares tool names; the app makes every
call and holds the keys.

**MCP servers** extend the tool list. `KAKERATTA_MCP_SERVERS` is a JSON array:

```json
[{"name":"geo","url":"https://geo.example.com/mcp","headers":{"Authorization":"Bearer …"}}]
```

Each server's tools appear as `name.tool` (for example `geo.compute_commute`),
next to the built-in ones, and a skill declares them by that name. Only remote
servers over Streamable HTTP are supported. Tool lists are cached for a few
minutes; a server that stops answering keeps its last known list, and a call
that fails is reported to the persona rather than dropped.

## Place and route tools

`route` needs a location attached to the post. Driving, walking, and cycling use
separate OSRM endpoints: `KAKERATTA_OSRM_CAR_URL`, `KAKERATTA_OSRM_FOOT_URL`, and
`KAKERATTA_OSRM_BIKE_URL`. The defaults are `https://router.project-osrm.org`,
`https://routing.openstreetmap.de/routed-foot`, and
`https://routing.openstreetmap.de/routed-bike`, respectively. Changing a URL's
profile token alone does not select a travel mode: custom servers must prepare
the corresponding routing data. These are road estimates, without public
transport schedules or live traffic. Unsupported modes return an error.

`place_search` uses Overpass (`KAKERATTA_OVERPASS_URL`) for nearby places when a
post has a location, or Nominatim (`KAKERATTA_NOMINATIM_URL`) for a name lookup
otherwise. Opening hours are returned only when
recorded in OpenStreetMap; they do not confirm that a shop is open now. Temporary
Overpass failures are retried once, then reported as unavailable rather than
“no places found”. Public services can still be busy or unavailable.

When an MCP server offers a tool with the same name that takes the position
(`lat` and `lng`), as geo-home-mcp does, `route` and `place_search` go to that
server with the post's position filled in. If the server cannot be reached, the
built-in OpenStreetMap calls above answer instead; an error the server itself
returns is passed on to the persona.

Check-ins do not acquire a current location: `route` is unavailable and
`place_search` can only do a name lookup during these wakes. Personas can still
use their memory, timeline search, and declared web search.

## Read more

| Document | Contents |
| --- | --- |
| [Usage and configuration](usage.md) | Android and iOS sharing, location, speech, model settings, and stored data |
| [Design and personas](architecture.md) | Principles, routing, execution order, OKF notes, skills, and tools |
| [What was hard about the AI](ai-lessons.md) | What went wrong with Nemotron personas in live runs and production, and how each was fixed |
| [Development and status](development.md) | Tests, source layout, implemented features, and unverified items |
| [Google Cloud deployment](../infra/README.md) | Terraform deployment: Firebase Hosting for the page, Cloud Run for the API |
