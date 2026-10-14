---
okf_version: "0.2"
---

# kakeratta system implementation

This **OKF v0.2 bundle** documents the implementation in six subsystem chapters. Each chapter covers a complete area of responsibility; individual functions and execution stages are sections within it. The implementation reference date is 2026-10-14.

# Chapters

- [Architecture and runtime](./architecture.md) - System composition diagram, separate Web/worker processes, temporary shared VM production, dependency assembly, Pub/Sub push workers, multiple instances, and shutdown.
- [API and persistent state](./backend.md) - Authentication, entry handling, timeline data, persistence, settings, model usage logging, monthly persona costs and Web search counts, and completion finish reasons, schedule editing, reminder push delivery, profile and avatar validation, favorites, and search.
- [Persona execution and memory](./personas.md) - Persona definitions, identifying person call names and shared topic keywords, built-in support and Markdown infographics with emoji and Mermaid, explicit depth string matching, combined reads and answers, aggregate Super routing, live research budgets and read capabilities, output truncation recovery, reviewed actions, optional memory judged by the post-publication note keeper, retained past-event review decisions and deduplicated prompt context, conditional schedules, the account's daily run with a shared internal day's summary, and corrections.
- [Browser application and device state](./frontend.md) - Frontend composition, requests, live updates, monthly AI usage on persona cards and settings, the schedule overview and editing, reminder push notifications, smartphone Home Screen installation prompts, profile names and photo avatars, offline delivery, favorites, saved-entry search, speech input, and read-aloud.
- [External agents, tools, and sharing](./integrations.md) - Japanese and English Alexa confirmed voice posts and threaded replies, reviewer account linking and read-aloud, external-agent answers, direct requests, posts as a persona and the connected Timeline import over MCP, permitted tools and Geo MCP availability, the shipped specification, weather forecasts, and selected publication to Bluesky.
- [Development and workspace operations](./operations.md) - Development, validation, model usage inspection, remote Terraform state and variables, temporary shared VM deployment, memory migration, location backfill, portable exports, and workspace deletion.

# Reading the bundle

Start with architecture, then follow the backend and persona chapters to trace a post from input to storage, routing, an answer, and memory. The frontend chapter explains browser state and delivery. Integrations and operations cover external boundaries and running the application.

- The bundle root is `docs/system/`; its index declares `okf_version: "0.2"`.
- OKF supports both bundle-relative links beginning with `/` and ordinary file-relative links. This developer bundle uses file-relative links such as `./personas.md` and `../../main.go` so they can be followed on GitHub.
- Chapter frontmatter provides `type`, `title`, `description`, `tags`, and `updated`, the last implementation-check date.
- Source and test links within chapters are ordinary relative links to repository files. Code is the source of implementation facts; tests identify verification points.

# Updating the documentation

Update the relevant section when implementation changes and revise the chapter's `updated` date. Keep the index description and chapter metadata aligned. Add a separate chapter when there is a distinct subsystem, and describe its operations as sections. `index.md` and optional `log.md` are reserved names without concept frontmatter; only the bundle-root index may declare `okf_version`. The `updated` field is a project extension recording the implementation review date, not a generation timestamp.

The [OKF skill](../../internal/persona/skills/okf/SKILL.md) defines this bundle's format. Support reads this specification through `kakeratta_help`. Personas do not receive the OKF memory-writing skill: their runtime memory is plain field summaries and kakera records, described in the persona chapter. Existing [technical documentation](../technical.md), the [usage guide](../usage.md), and the [Hermes guide](../hermes.md) provide additional context.

The [official OKF v0.2 specification](https://github.com/GoogleCloudPlatform/open-knowledge-format/blob/main/SPEC.md) defines conformance, optional metadata, and link resolution.
