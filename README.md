# kakeratta

English | [日本語](README_ja.md)

**Ka-ke-ratta.** Japanese for the act of tossing in a *kakera* — a fragment.

Put thoughts and things that catch your eye into one timeline as text, images,
URLs, or speech. AIs with separate memories for their own subjects react when
they have something to say.

The food persona learns your tastes from meals; the reading persona remembers
passages that caught your eye. Keeping memory separate by subject prevents
unrelated information from diluting it.

A browser app served by one Go binary with the page embedded. [MIT licensed](LICENSE).

## How it works

1. Write a thought, paste a screenshot, send a URL, or speak.
2. Relevant personas receive the post and decide what to remember and whether to reply.
3. Reactions arrive later in the timeline. Mention a persona with `@` when you want an answer.

Some posts get no reaction. A persona can remember information in its subject
without speaking. Four personas ship with the app: **たべ (food), みち (travel),
くだ (tech), and しおり (reading)**. Add more by importing a `skill.json` from the UI.

## Get started

Requirements: Go 1.25 or later, Node.js, and Task (the `task` command).
Run from the repository root:

```sh
KAKERATTA_ADDR=127.0.0.1:8787 task run  # build and start
```

Open <http://127.0.0.1:8787> and configure the inference endpoint, API key, and
models in Settings. Use `task build` to build only (output: `bin/kakeratta`),
and `task test` to run the tests.

| Variable | Default | Purpose |
| --- | --- | --- |
| `KAKERATTA_ADDR` | `:8787` | Listen address |
| `KAKERATTA_HOME` | OS config directory | Storage for posts, notes, and images |
| `KAKERATTA_TOKEN` | empty (no authentication) | Access token |

**Set `KAKERATTA_TOKEN` whenever the app is reachable from outside.** Open
`/?token=<URL-encoded token>` once in each browser to authenticate subsequent
requests with a session cookie.

## Read more

| Document | Contents |
| --- | --- |
| [Usage and configuration](docs/usage.md) | Android and iOS sharing, location, speech, model settings, and stored data |
| [Design and personas](docs/architecture.md) | Principles, routing, execution order, OKF notes, skills, and tools |
| [Development and status](docs/development.md) | Tests, source layout, implemented features, and unverified items |

Posting and share intake, routing, notes, replies, search and route tools, and
read-aloud are implemented. Sharing on real phones, model vision support, OCR
quality, and reaction frequency still need verification.
