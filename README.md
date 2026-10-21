<img src="frontend/public/logo.svg" alt="kakeratta" width="304" height="80">

English | [日本語](README_ja.md)

**Ka-ke-ratta.** Japanese for the act of tossing in a *kakera* — a fragment.

**You cannot search for what you forgot exists.** kakeratta is a personal AI
timeline that brings it back to you. Toss in fragments of your day as text,
photos, URLs or speech. Personas, one for each person or interest you care
about, remember what matters to them and speak up when it matters.

- **A separate memory for each persona.** Each person or interest keeps its
  own memory, so none is diluted by the others. Person personas are reached
  by name and keyword matching, not by an LLM's guess.
- **Silence is the default.** A persona replies only when it has something
  worth saying. A quiet timeline means the app is working.
- **Memory you can trace and review.** Facts link back to your posts.
  Temporary records can expire; records marked for review come back with
  a question: keep, postpone or forget? Your original posts stay intact.

[Try it](https://kakeratta.net/lp/?lang=en) ·
[Watch the demo](https://youtu.be/fdUYDI6Sd70) ·
[Devpost](https://devpost.com/software/kakeratta) ·
[Manual](https://kakeratta.net/manual/?lang=en) ·
[Design](docs/design/technical.md)

[![Watch the kakeratta demo on YouTube](https://img.youtube.com/vi/fdUYDI6Sd70/hqdefault.jpg)](https://youtu.be/fdUYDI6Sd70)

<img src="docs/captures/en/timeline.png" alt="Your fragments and the personas' replies on the timeline" width="390">

From production and testing:
[AI cost estimate — Oct 2026](docs/reports/production-ai-cost-2026-10-11.md) ·
[routing model comparison — Sep 2026](docs/reports/routing-model-comparison-2026-09-19.md) ·
[lessons from what the AI got wrong](docs/design/ai-lessons.md)

## What you can do

- **Get timely follow-ups.** Remembering personas speak when they have something
  worth sharing. For people, choose “Look back often” or “Look back occasionally”. Birthdays, appointments and preparation checks run on
  the server; supported devices can receive reminder push notifications.
- **Inspect and correct memory.** Each remembering persona keeps facts with
  source links, field summaries and monthly kakera records. Edit or forget
  individual facts; search your timeline by words or meaning.
- **Create personas in plain language.** Describe a person or interest, then
  adjust its instructions, routing and replies as you use it.
- **Let personas grow with your interests.** When a new topic builds up in a
  persona's memory, it suggests a dedicated persona at check-in. One tap on
  **Create it** drafts the persona, moves the related memories to it, and
  narrows the original persona's routing so those posts reach only the new one.
- **Save answers as favorites.** Keep posts and persona replies in a list,
  most recently added first. Filter by persona or search only your favorites.
- **Turn an answer into a diagram.** Reply to it with
  `@infographic turn this answer into an infographic`. The Markdown diagram uses emoji
  and Mermaid in the display language selected when you send the request.
- **Keep posting offline.** Pending posts and replies send when the app is
  open and connectivity returns. Preview and share selected conversations to
  your own Bluesky account.

A direct `@` mention or reply to an active persona reaches that persona alone;
otherwise, interests receive posts by routing conditions and people by names
and keywords. A mention takes precedence over the persona being replied to.

Five personas ship with the app:

| Persona | Role | Active in new workspaces |
| --- | --- | --- |
| **tabe** | Food and dining | No |
| **shiori** | Reading and books | No |
| **sona** | Preparation, belongings, weather and small follow-ups | Yes |
| **support** | kakeratta questions, answered from the system specification | Yes |
| **infographic** | Markdown diagrams with emoji and Mermaid for text, expanded articles, interpreted images and answers | Yes |

Creation and editing offer **Remember** and **Always think deeply**.
**Remember** is on by default (off for support and infographic); turning it
off stops memory updates and periodic check-ins. **Always think deeply**
uses a stronger model for final answers (on by default for infographic).
A request that says "in detail" uses the strongest answer model in the default configuration.
See [Design and integrations](#design-and-integrations) for which model does what.

## Gmail and Google Calendar

[Google Bridge](tools/google-bridge/README.md) is a separate personal Web app and
MCP server you run on your device or server. It uses your Google Cloud project
and OAuth client, encrypts Google credentials in its own data directory, and
can run without the kakeratta server.

| Feature | How it works |
| --- | --- |
| Important Gmail | Incremental sync selects mail by labels and senders, with optional Hermes model triage; Hermes delivers the selected candidates. Model-selected mail is summarized without signatures or quoted history. |
| Today and tomorrow's events | Daily Calendar intake uses your chosen timezone, including all-day and recurring occurrences. Intake never changes Google Calendar. |
| Review before importing | Hermes sends selected mail and events to pending Timeline cards. Choose Import or Discard; only Import starts normal posting, persona routing and memory processing. Pending cards stay out of timeline search and persona processing. |
| Adjustable exclusions | Discard can also exclude a mail type or exact sender, or a single event or recurring series. Remove rules in Bridge; sender and event exclusions run before model triage. |
| Ask Hermes directly | Search Gmail, read messages and threads, or look up events over MCP. With extra Google scopes, an explicit owner request can send/reply/save a draft or create/change/delete an event. Direct-request writes run without a second Web approval and are separate from intake. |

```mermaid
flowchart LR
  Google[Gmail / Google Calendar] -->|OAuth: read| Bridge[Your Google Bridge]
  Cron[Your Hermes cron] --> Worker[Hermes worker: code]
  Worker <-->|Sync / candidates / decisions| Bridge
  Worker <-->|Select and summarize only| Model[Hermes model]
  Worker -->|Candidates over kakeratta MCP| Pending[Timeline: pending review]
  Pending -->|Actual receipts via worker code| Worker
  Owner[You] -->|Import / Discard| Pending
  Pending -->|Import| Posts[Posts → personas → memory]
  Owner -->|Explicit direct request| Direct[Your Hermes agent]
  Direct -->|Write tool with extra scopes| Bridge
  Bridge -->|Requested send / event change| Google
```

The Hermes worker owns both MCP connections. Like persona-answer submission,
Python code fetches candidates, submits classification results, delivers cards,
and polls and returns actual review receipts. The LLM only selects and summarizes;
its isolated session has no MCP connections or delivery tools. Bridge never stores
a kakeratta URL/key or contacts kakeratta. Existing Hermes cron runs the gate with
`--no-agent`; delivery-only and idle runs use no model tokens.
Content passed to Hermes also reaches its configured model. Imported events
are snapshots: later Google edits do not update existing cards or posts.
Direct-request writes rely on Hermes following the owner's instructions; the
server cannot verify the request's origin, so triage jobs should exclude write tools.

<img src="docs/captures/google-bridge/gmail.png" alt="Google Bridge showing a selected reservation email waiting for Hermes to relay" width="620">
<img src="docs/captures/google-bridge/calendar.png" alt="Google Bridge showing events waiting for Hermes to relay" width="620">

Screenshots show the shipped Japanese UI after sync with sample API responses,
not a live Google account. See the [advanced setup guide](https://kakeratta.net/manual/?guide=advanced&lang=en#en-google)
and [detailed setup and recovery (Japanese)](docs/guides/google_ja.md).
Real Google OAuth and Hermes integration remain unverified; the captures demonstrate the UI flow.

## Run locally

Requires Go 1.26+, Node.js and [Task](https://taskfile.dev), plus a Nebius Token
Factory API key. With [mise](https://mise.jdx.dev), `mise install` installs the
tools declared in [mise.toml](mise.toml).

```sh
task setup  # install frontend dependencies once
task dev    # API on :8787 and frontend on :15173
```

When running in a container, publish or forward port `15173` (for Docker, `-p 15173:15173`). Vite listens on `0.0.0.0`; open `http://localhost:15173` on the host. If the port is occupied, startup fails instead of choosing another port.

Open http://127.0.0.1:15173 and enter your API key in Settings. Local runs need
no Firebase account or external database; data is stored on disk and the API
listens on loopback. See [usage and configuration](docs/guides/usage.md) for commands
and [self-hosting](docs/design/technical.md#running-it) for environment variables and
hosted storage. Use `task test` to run the Go and browser tests.

## Architecture

The diagram shows the preferred Cloud Run deployment. Production currently
runs on a shared Compute Engine VM to reduce cost. It keeps the same
Web/worker separation and durable queues; see
[current operations](infra/shared-vm/README.md).

```mermaid
flowchart LR
  Browser[Browser] --> Hosting[Firebase Hosting]
  Hosting --> Web[Cloud Run: Go Web API]
  Tasks[Cloud Tasks: scheduled wakes] --> Web
  Web -->|Save posts and jobs| State[(Firestore: per-account state)]
  Web -->|Publish saved jobs| Queue[Pub/Sub]
  Queue --> Workers[Cloud Run: Go workers]
  Workers -->|Read jobs and save replies| State
  Workers --> AI[Nebius Token Factory: NVIDIA Nemotron]
  Workers -->|Set next wake| Tasks
  State -->|Account events| Web
  Web -->|Live updates| Browser
  Scheduler[Cloud Scheduler] --> Repair[Web: retry unpublished jobs]
  State --> Repair
  Repair --> Queue
```

In production, Web saves a post and its processing request together, then
publishes the job to Pub/Sub. An available worker processes it using the user's
AI settings and credentials, persists the result, and acknowledges completion.
Cloud Tasks triggers scheduled work through the same path. Unpublished requests
remain in Firestore for retry; Pub/Sub redelivers unacknowledged jobs.

Data, execution claims and conversation ordering belong to each account.
Web and worker instances can be added independently: accounts need no assigned
server, and independent accounts and conversations can run in parallel while
each conversation keeps its processing order. In the Cloud Run configuration,
workers can scale to zero when idle. Capacity grows through additional instances
and measured tuning of shared infrastructure; worker sizes are deployment
settings. The temporary shared VM has fixed capacity and a running worker.
Local development uses the same persona engine with file storage and an in-process dispatcher.

See [architecture and scaling](docs/system/architecture.md) for the full
execution model and [deployment](infra/README.md) for infrastructure settings.

## Design and integrations

The backend is Go and the browser app is JavaScript. NVIDIA Nemotron models
through Nebius Token Factory provide routing, answers and workflow checks.
The default model configuration is:

| Model | Role |
| --- | --- |
| **Nemotron 3.5 Lightning** | Optional dictation polishing |
| **Nemotron 3 Super 120B A12B** | Persona routing, reads and ordinary answers, and the note keeper |
| **Nemotron 3.5 Super VL 120B A12B** | Research for deep requests and Always think deeply, schedule planning and workflow checks, answers for personas with Always think deeply enabled, daily and monthly check-ins, the daily memory pass, and the day's and month's summaries |
| **Nemotron 3 Ultra 550B A55B** | Answers for deep requests (such as "in detail") |

OpenRouter is an optional alternative. Models are chosen by task to benefit
from smaller models' lower cost and faster responses, using stronger models
where deeper reasoning is needed. Each persona has its own memory and
permitted tools. Export your timeline, images, settings, personas and memory
from Settings; see [data portability](docs/guides/usage.md#taking-it-with-you).

External agents such as Hermes or Obsidian's LLM Connect Hub can research and
submit answers over MCP. kakeratta owns the timeline, schedules and
memory updates. Address `@agent` to hand the agent itself a longer task -
sending an email, for instance - past the personas, and carry on by replying. The tested NemoClaw/OpenShell sandbox setup is documented in
the integration report below.

Built for the [Nebius x NVIDIA Global AI Hackathon](https://nebiusglobalaihackathon.devpost.com/).
Read the [Devpost project page](https://devpost.com/software/kakeratta) for the
inspiration and project story.
The [project background](docs/design/background.md) explains why remembering includes
bringing something back at the right time.

## Documentation

Browse the [documentation index](docs/README.md) for all guides, design notes, and reports.

| Document | Use it for |
| --- | --- |
| [Manual](https://kakeratta.net/manual/?lang=en) | Illustrated instructions for everyday use |
| [Usage and configuration](docs/guides/usage.md) | Local commands, settings, data and portability |
| [Under the hood](docs/design/technical.md) | Design, routing, models, memory, tools and self-hosting |
| [System implementation](docs/system/index.md) | Source-linked subsystem specifications, also used by support |
| [AI lessons](docs/design/ai-lessons.md) | Failures observed in development and how they were addressed |
| [External agents](docs/guides/hermes.md) | Hermes and LLM Connect Hub setup, MCP permissions, fallback and direct `@agent` requests |
| [Gmail and Calendar setup](docs/guides/google_ja.md) (Japanese) | Personal Google Bridge, OAuth, reviewed imports and Hermes cron |
| [NemoClaw integration report](docs/reports/nemoclaw-hermes-integration.md) | Sandbox setup and verification, with protocol-version context |
| [Google Cloud deployment](infra/README.md) | Cloud Run configuration and temporary shared VM operations |

[MIT licensed](LICENSE).
