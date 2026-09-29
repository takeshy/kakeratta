# kakeratta system implementation

This **OKF v0.1 bundle** documents the implementation in six subsystem chapters. Each chapter covers a complete area of responsibility; individual functions and execution stages are sections within it. The implementation reference date is 2026-09-30.

# Chapters

- [Architecture and runtime](/architecture.md) - System boundaries, dependency assembly, background workers, and shutdown.
- [API and persistent state](/backend.md) - Authentication, entry handling, timeline data, persistence, settings, and search.
- [Persona execution and memory](/personas.md) - Persona definitions, routing, research, reviewed actions, memory maintenance, schedules, and corrections.
- [Browser application and device state](/frontend.md) - Frontend composition, requests, live updates, offline delivery, speech input, and read-aloud.
- [External agents, tools, and sharing](/integrations.md) - External-agent answers over MCP, permitted external tools, and selected publication to Bluesky.
- [Development and workspace operations](/operations.md) - Development, validation, deployment, portable exports, and workspace deletion.

# Reading the bundle

Start with architecture, then follow the backend and persona chapters to trace a post from input to storage, routing, an answer, and memory. The frontend chapter explains browser state and delivery. Integrations and operations cover external boundaries and running the application.

- The bundle root is `docs/system/`. A leading `/` in a link such as `/personas.md` refers to this root.
- GitHub resolves a leading `/` against the website root. Open the matching file in the directory tree or use an OKF viewer that supports bundle-relative links.
- Chapter frontmatter provides `type`, `title`, `description`, `tags`, and `updated`, the last implementation-check date.
- Source and test links within chapters are ordinary relative links to repository files. Code is the source of implementation facts; tests identify verification points.

# Updating the documentation

Update the relevant section when implementation changes and revise the chapter's `updated` date. Keep the index description and chapter metadata aligned. Add a separate chapter when there is a distinct subsystem, and describe its operations as sections. `index.md` and optional `log.md` are reserved names without frontmatter.

The [OKF skill](../../internal/persona/skills/okf/SKILL.md) defines the format given to personas. This developer bundle is separate from runtime persona memory. Existing [technical documentation](../technical.md), the [usage guide](../usage.md), and the [Hermes guide](../hermes.md) provide additional context.
