# kakeratta

English | [日本語](README_ja.md)

**Ka-ke-ratta.** Japanese for the act of tossing in a *kakera* — a fragment.

A personal AI timeline: post fragments of your day as text, photos, URLs or
speech, and personas for people and interests remember what matters to them
and bring it back when it is useful.

[Try it](https://kakeratta.net/lp/?lang=en) ·
[Manual](https://kakeratta.net/manual/?lang=en) ·
[Design](docs/technical.md)

<img src="docs/captures/en/timeline.png" alt="Your fragments and the personas' replies on the timeline" width="390">

## What you can do

- **Create personas in plain language.** Describe a person or interest, then
  adjust its instructions, routing and replies as you use it.
- **Inspect and correct memory.** Each remembering persona keeps facts with
  source links, field summaries and monthly kakera records. Edit or forget
  individual facts; search your timeline by words or meaning.
- **Get timely follow-ups.** Remembering personas speak when they have something
  worth sharing. For people, choose “Look back often” or “Look back occasionally”. Birthdays, appointments and preparation checks run on
  the server; supported devices can receive reminder push notifications.
- **Turn an answer into a diagram.** Reply to it with
  `@infographic turn this answer into an infographic`. The HTML diagram uses
  the display language selected when you send the request.
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
| **infographic** | HTML diagrams of text, expanded articles, interpreted images and answers | Yes |

Creation and editing offer **Remember** and **Always think deeply**. Memory
is on by default; support and infographic default to off. Turning it off stops
memory updates and periodic check-ins while retaining existing notes. Always
thinking deeply uses the high (Ultra) model for ordinary requests; infographic
defaults to on. Existing workspaces keep their saved activation choices.

## Run locally

Requires Go 1.26+, Node.js and [Task](https://taskfile.dev), plus a Nebius Token
Factory API key. With [mise](https://mise.jdx.dev), `mise install` installs the
tools declared in [mise.toml](mise.toml).

```sh
task setup  # install frontend dependencies once
task dev    # API on :8787 and frontend on :5173
```

Open http://127.0.0.1:5173 and enter your API key in Settings. Local runs need
no Firebase account or external database; data is stored on disk and the API
listens on loopback. See [usage and configuration](docs/usage.md) for commands
and [self-hosting](docs/technical.md#running-it) for environment variables and
hosted storage. Use `task test` to run the Go and browser tests.

## Design and integrations

The backend is Go and the browser app is JavaScript. NVIDIA Nemotron models
through Nebius Token Factory provide routing, answers and workflow checks;
OpenRouter is an optional alternative. Models are chosen by task to benefit
from smaller models' lower cost and faster responses, using stronger models
where deeper reasoning is needed. Each persona has its own memory and
permitted tools. Export your timeline, images, settings, personas and memory
from Settings; see [data portability](docs/usage.md#taking-it-with-you).

External agents such as Hermes or Obsidian's LLM Connect Hub can research and
submit answers over MCP. kakeratta owns the timeline, schedules, actions and
memory updates. The tested NemoClaw/OpenShell sandbox setup is documented in
the integration report below.

Built for the [Nebius x NVIDIA Global AI Hackathon](https://nebiusglobalaihackathon.devpost.com/).
The [project background](docs/background.md) explains why remembering includes
bringing something back at the right time.

## Documentation

| Document | Use it for |
| --- | --- |
| [Manual](https://kakeratta.net/manual/?lang=en) | Illustrated instructions for everyday use |
| [Usage and configuration](docs/usage.md) | Local commands, settings, data and portability |
| [Under the hood](docs/technical.md) | Design, routing, models, memory, tools and self-hosting |
| [System implementation](docs/system/index.md) | Source-linked subsystem specifications, also used by support |
| [AI lessons](docs/ai-lessons.md) | Failures observed in development and how they were addressed |
| [External agents](docs/hermes.md) | Hermes and LLM Connect Hub setup, MCP permissions and fallback |
| [NemoClaw integration report](docs/nemoclaw-hermes-integration.md) | Sandbox setup and verification, with protocol-version context |
| [Google Cloud deployment](infra/README.md) | Terraform, Firebase Hosting and Cloud Run |

[MIT licensed](LICENSE).
