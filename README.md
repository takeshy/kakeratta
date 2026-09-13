# kakeratta

English | [日本語](README_ja.md)

**Ka-ke-ratta.** Japanese for the act of tossing in a *kakera* — a fragment.

Put thoughts and things that catch your eye into one timeline as text, images,
URLs, or speech. AIs with separate memories for their own subjects react when
they have something to say.

The food persona learns your tastes from meals; the reading persona remembers
passages that caught your eye. Keeping memory separate by subject prevents
unrelated information from diluting it.

**Create your own personas with AI.** Say “Remember the wines I try and help me
find bottles I would enjoy,” and AI drafts a persona for you. Start using it,
then refine its tone and what it remembers through conversation.

Kakeratta uses NVIDIA Nemotron models through Nebius Token Factory.

- Nemotron Nano — lightweight routing and everyday persona decisions
- Nemotron Super — richer domain reasoning
- Nemotron Ultra — complex reasoning when a persona needs it
- Nebius Token Factory — inference backend

Image reading uses GLM-5.3-Flash on the same backend.

A browser app in two pieces: a static page and a Go API. Vite serves the page
while developing and Firebase Hosting serves it deployed; the Go binary is only
the API, on Cloud Run or on your own machine. [MIT licensed](LICENSE).

## How it works

The timeline starts with the latest 20 posts and loads 20 more as you scroll. Replies stay with their parent posts.

1. Write a thought, paste a screenshot, send a URL, or speak.
2. Relevant personas receive the post and decide what to remember and whether to reply.
3. Reactions arrive later in the timeline. Mention a persona with `@` when you want an answer.

![The timeline: your fragments, with what the personas said about them underneath](docs/captures/en/timeline.png)

Some posts get no reaction. A persona can remember information in its subject
without speaking. Four personas ship with the app: **tabe (food), michi
(travel), kuda (tech), and shiori (reading)** — Japanese words in Latin letters,
lowercase, so `@tabe` can be typed on any keyboard. A persona answers in the language the
post was written in.

## Describe a persona. Start using it.

Answer “What would you like your persona to help with?” in your own words.

> A persona that remembers wines I have tried and helps me find wines I would enjoy.

Once the subject is clear, AI fills in a name, tone, and memory rules and gives
you a draft right away. Even “wine” or “reading” is enough to start; there is no
long questionnaire. Review the draft in plain language: what it helps with,
how it talks, what it remembers, and when it replies.

![Create a persona with AI: describe what you want in your own words](docs/captures/en/persona-create.png)

![Review and save a sample wine persona drafted by AI](docs/captures/en/persona-draft.png)

1. Open Personas and choose **Create a persona with AI**. Describe what you want.
2. Review the draft and select **Save this persona**.
3. **Enable** it and start posting as usual.

**Try it first, then make it yours.** Use **Edit with AI** to say “Give shorter
replies” or “Remember why I liked a wine, as well as its name.” Built-in personas
can be edited too. Their existing memories and enabled status carry over.

Creation and editing use your configured AI connection. Advanced users can also
import a `skill.json` from the UI.

![The Personas panel: each one's subject, model tier, tools, and notes](docs/captures/en/personas.png)

## Get started

Requirements: Go 1.25 or later, Node.js, and Task (the `task` command).
Run from the repository root:

```sh
task setup  # once: the page's dev dependencies (Vite, Storybook)
task dev    # the API on :8787 and the page on :5173, together
```

Open <http://127.0.0.1:5173>. A new workspace has nobody in the room yet, and the
screen leads with that until you call someone in:

![Nobody in the room yet: call in a built-in persona with one tap](docs/captures/en/nobody.png)

Then enter your Nebius Token Factory API key in Settings. Nano, Super, and Ultra are configured for the three tiers by default. The page proxies `/api` to the binary, the same split the
deployed setup has. `task run` starts only the API, `task build` builds only the
binary (`bin/kakeratta`), `task stories` opens Storybook, and `task test` runs
the tests.

| Variable | Default | Purpose |
| --- | --- | --- |
| `KAKERATTA_ADDR` | `:8787` | Listen address |
| `KAKERATTA_HOME` | OS config directory | Storage for posts, notes, and images |
| `KAKERATTA_TOKEN` | empty (no authentication) | Access token |
| `KAKERATTA_FIREBASE_PROJECT_ID` | empty | Enables multi-user Firebase Authentication |
| `KAKERATTA_FIREBASE_API_KEY` | empty | Public Firebase Web API key |
| `KAKERATTA_FIREBASE_AUTH_DOMAIN` | empty | Firebase Auth domain |
| `KAKERATTA_GCS_BUCKET` | empty | Private Cloud Storage bucket for images |
| `KAKERATTA_STORE` | `file` | Set to `firestore` for Cloud Run |
| `KAKERATTA_FIRESTORE_PROJECT_ID` | Firebase project | Firestore project |

**Set `KAKERATTA_TOKEN` whenever the app is reachable from outside.** Open the
page as `/?token=<URL-encoded token>` once in each browser; it claims the token
through the API and keeps a session cookie from then on.

For a public multi-user deployment, set all three Firebase variables instead.
Users sign in with Google and receive isolated stores under
`KAKERATTA_HOME/users/<uid-hash>/`. Firebase authentication takes precedence
over the shared `KAKERATTA_TOKEN` mode.
When `KAKERATTA_GCS_BUCKET` is set, captures are stored as private objects;
timeline metadata, settings, and persona notes remain on the workspace disk.

## Read more

| Document | Contents |
| --- | --- |
| [Usage and configuration](docs/usage.md) | Android and iOS sharing, location, speech, model settings, and stored data |
| [Design and personas](docs/architecture.md) | Principles, routing, execution order, OKF notes, skills, and tools |
| [Development and status](docs/development.md) | Tests, source layout, implemented features, and unverified items |
| [Google Cloud deployment](infra/README.md) | Terraform deployment: Firebase Hosting for the page, Cloud Run for the API |

Conversational AI persona creation and editing, posting and share intake,
routing, notes, replies, search and route tools, and read-aloud are implemented. Sharing on real phones, OCR quality across different images and reaction frequency still need verification.
