# System documentation

The system implementation is documented as an English OKF v0.2 bundle in
`docs/system/`. Start with `docs/system/index.md` and read the relevant subsystem
chapters before changing implementation. The format is defined in
`internal/persona/skills/okf/SKILL.md`.

## Before committing

- Review whether the implementation changes affect the system documentation.
- If behavior, architecture, APIs, persistence, configuration, or operations
  described in the bundle have changed, update the relevant sections in
  `docs/system/` before committing. Include those updates in the same commit.
- Check the documentation against the actual implementation. Update source and
  test references, chapter metadata, and the index when affected. Set the
  affected chapter's `updated` date to the date of this review.
- Preserve the existing subsystem chapter structure and OKF format. Describe
  individual functions and execution stages as sections within a chapter.
- Changes that do not affect the documented implementation do not require a
  documentation edit. Do not add unrelated edits just to update documentation.
