---
okf_version: "0.2"
---

# kakeratta system implementation

This **OKF v0.2 bundle** documents the implementation in six subsystem chapters. Each chapter covers a complete area of responsibility; individual functions and execution stages are sections within it. The implementation reference date is 2026-10-24.

# Chapters

- [Architecture and runtime](./architecture.md) - System composition diagram, separate Web/worker processes, temporary shared VM production, dependency assembly, Pub/Sub push workers, multiple instances, and shutdown.
- [API and persistent state](./backend.md) - Authentication, English judge sample workspaces, entry handling, timeline data, persistence, settings, model usage logging, monthly persona costs and Web search counts, and completion finish reasons, schedule editing, reminder push delivery, profile and avatar validation, favorites, search, and owner-reviewed Gmail and Calendar intake.
- [Persona execution and memory](./personas.md) - Persona definitions, background output language, identifying person call names and shared topic keywords, support through the direct built-in AI and Markdown infographics with emoji and Mermaid, explicit depth string matching, combined reads and answers, aggregate Super routing, per-job models on Nebius (Super VL deep reads, planning, review, AlwaysUltra answers and the daily run), live research budgets and read capabilities, output truncation recovery, reviewed schedule changes, optional memory kept as one file of records and facts per persona - a turn appends a tagged record, and a daily pass folds new records into facts, deletes folded records, hands stale ones to review, enriches grounded facts, and may suggest a new persona instead of splitting memory, which a button under the check-in creates, moving the related facts and records to it and leaving the subject out of the suggesting persona's condition - retained past-event review decisions, conditional schedules, the account's daily run with a shared internal day's summary, and corrections.
- [Browser application and device state](./frontend.md) - Frontend composition, English judge registration and progress, requests, live updates, monthly AI usage on persona cards and settings, OpenRouter image-model selection, the schedule overview and editing, reminder push notifications, smartphone Home Screen installation prompts, profile names and photo avatars, offline delivery, favorites, saved-entry search, speech input, read-aloud, and Gmail/Calendar review cards.
- [External agents, tools, and sharing](./integrations.md) - Japanese and English Alexa confirmed voice posts and threaded replies, reviewer account linking and read-aloud, external-agent answers, shared judge Hermes registration and authenticated discovery, Hermes Super VL parent configuration, direct requests with separately selectable external and built-in AI destinations, posts as a persona and the connected Timeline import over MCP, permitted tools and Geo MCP availability, the shipped specification, weather forecasts, selected publication to Bluesky, and the separate personal Google Bridge with owner-direct-request writes, code-driven Hermes Gmail and daily Calendar review delivery, and Google setup guidance.
- [Development and workspace operations](./operations.md) - Development, validation, model usage inspection, Hermes on NemoClaw setup, remote Terraform state and variables, temporary shared VM deployment, memory migration, location backfill, portable exports, workspace deletion, and personal Google Bridge startup, cron polling and code-driven Hermes review delivery.

# Reading the bundle

Start with architecture, then follow the backend and persona chapters to trace a post from input to storage, routing, an answer, and memory. Backend also defines stable workspace identity; personas explains how it fixes monthly summary days and check-in times across process restarts. The frontend chapter explains browser state and delivery. Integrations and operations cover external boundaries and running the application.

- The bundle root is `docs/system/`; its index declares `okf_version: "0.2"`.
- OKF supports both bundle-relative links beginning with `/` and ordinary file-relative links. This developer bundle uses file-relative links such as `./personas.md` and `../../main.go` so they can be followed on GitHub.
- Chapter frontmatter provides `type`, `title`, `description`, `tags`, and `updated`, the last implementation-check date.
- Source and test links within chapters are ordinary relative links to repository files. Code is the source of implementation facts; tests identify verification points.

# Updating the documentation

Update the relevant section when implementation changes and revise the chapter's `updated` date. Keep the index description and chapter metadata aligned. Add a separate chapter when there is a distinct subsystem, and describe its operations as sections. `index.md` and optional `log.md` are reserved names without concept frontmatter; only the bundle-root index may declare `okf_version`. The `updated` field is a project extension recording the implementation review date, not a generation timestamp.

The [OKF skill](../../internal/persona/skills/okf/SKILL.md) defines this bundle's format. Support reads this specification through `kakeratta_help`. Personas do not receive the OKF memory-writing skill: their runtime memory is plain facts and kakera records, described in the persona chapter. Existing [technical documentation](../design/technical.md), the [usage guide](../guides/usage.md), and the [Hermes guide](../guides/hermes.md) provide additional context.

The [official OKF v0.2 specification](https://github.com/GoogleCloudPlatform/open-knowledge-format/blob/main/SPEC.md) defines conformance, optional metadata, and link resolution.
