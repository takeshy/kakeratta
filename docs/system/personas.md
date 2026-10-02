---
type: "Component"
title: "Persona execution and memory"
description: "Persona definitions, built-in support and infographics, routing, research, reviewed actions, optional memory, schedules, and corrections."
tags: ["persona", "skill", "support", "infographic", "html", "ultra", "routing", "answer", "workflow", "memory", "records", "schedule", "daily", "preview", "trial"]
updated: "2026-10-02"
---

Persona definitions, built-in support and infographics, routing, research, reviewed actions, optional memory, schedules, and corrections.

# Persona definitions and the Catalogue

## Skill structure

`Skill` contains an ID, name, icon, tone, `Domain`, `Instructions`, `Memory`, and `Tools`. `Domain` informs routing, `Instructions` describe the persona's behavior, and `Memory` names its memory fields (分野) - the capitalised words in it, such as `Preference` and `Shop` (`allowedTypes`) - and says what each field's summary keeps. A persona whose `Memory` names none keeps one field, `Memo`.

Subject personas cover topics. Person personas use `Kind: person` and have `Aliases` and `Keywords` that can match text. The Catalogue combines built-in personas with user-installed or edited definitions. `Settings.Active` selects active personas.

The Catalogue keeps nothing itself. It reads the installed definitions through `Workspace.Skills` - `skills/{id}/skill.json` files, or the Firestore `skills` collection - so a definition changed by another process serving the same user is seen at once. `Install`, `Update`, and `Uninstall` write only through `PersistSkill` and `DeleteSkill`; there is no second copy to keep in step.

A piece of work reads once. `Catalogue.Snapshot` returns a read-only `Skills` (`All`, `Find`, `Active`) that does not change afterwards. A reaction takes one when a worker claims it (`Runner.withSkills`) and keeps it on its context, so the router, every persona's turn, the note keeper, and the periodic checks it runs all see one set; the entry points reached from HTTP (`CheckRouting`, `ApplyRouting`, `ApplyReply`, `PreviewReply`) take one on entry, and handlers that look up several personas take one at the top. Code without a snapshot on its context reads afresh through `Runner.skills`. Prepared entry deliveries persist the routing snapshot as `SkillsJSON`, together with the active persona order, and restore it when claimed, including after restart. A definition changed while a long turn runs takes effect from the next piece of work.

## Shipped personas and retired travel persona

The app ships `tabe` (`gourmet`), `shiori` (`book`), `sona` (`routine`), `support` (`support`), and `インフォグラフィック` (`infographic`). The former built-in `michi` (`move`) is no longer provided. Without an installed definition, saved `move` activation IDs do not resolve in `Skills.Active`, so it is absent from the persona list and receives no new turns. Existing timeline entries and private memory are retained. A previously customized `move` definition remains available as a user-installed persona and can be paused or removed through normal management. Users can create their own travel persona and declare the existing `route` and `place_search` tools.

Verification: [skill_test.go](../../internal/persona/skill_test.go) checks the retired built-in and preservation of a user-installed travel definition. Cross-persona execution tests use [travel_fixture_test.go](../../internal/persona/travel_fixture_test.go) as a custom persona.

## Built-in kakeratta support

The subject persona `support` (name `support`, icon 🛟) answers questions about kakeratta's usage, specifications, settings, personas, memory, notifications, search, and integrations. Its domain excludes general technical questions and posts that merely mention the app. Users can call it directly with `@support`; ordinary questions reach it through the subject router.

It declares only the read-only `kakeratta_help` tool, reads the OKF bundle's index and relevant chapters before answering, and explains confirmed behavior in the user's language. The specification is the same `docs/system/` bundle developers maintain, embedded in the binary at build time; updating it takes effect in support after rebuilding. It is shared product knowledge. The tool is also available to an external planner through the ordinary permitted research path.

Support defaults to `memoryEnabled: false`: it answers without keeping private notes or checking in. Its definition can be edited and it uses the same optional external-agent preference as other personas, off by default. It distinguishes documented behavior from the user's actual configuration and runtime state, asks for missing error/operation details, and never claims to have changed settings or repaired the app. New workspaces activate it alongside sona; saved activation choices remain authoritative.

## Built-in infographics

`infographic` (name `インフォグラフィック`, icon 📊) handles explicit requests to turn a post or conversation into an infographic. Its definition defaults to `memoryEnabled: false` and `alwaysUltra: true`; it does not save notes or wake for periodic check-ins, and ordinary requests use the high (Ultra) model. Its external-agent preference follows the ordinary rules and defaults off. New workspaces activate it together with sona and support; saved activation selections remain authoritative.

The persona submits a complete HTML document in `Answer.Text`, beginning with `<!DOCTYPE html>`, with inline CSS and SVG, responsive layout, and no scripts or external assets. The host persists the text unchanged; the browser displays complete HTML answers in an isolated frame. Normal prose remains available for clarification when source material is missing. URL requests use the page title and body already attached by `readLink`, and image requests use the description and extracted text already produced by `readCapture`. A reply's thread carries the original material. Failed retrieval or interpretation must not be replaced with invented content.

`infographicRules` overrides the normal post-language rule with the request's `Entry.Language` (`ja` or `en`), captured from the browser display language. Headings, explanations, and diagram labels are translated while retaining facts and proper names. Older requests without this field use the latest user language in their thread, or the ordinary post-language rule if none exists. The rules reach both the built-in answer prompt and the external ask. Verification: [infographic_test.go](../../internal/persona/infographic_test.go) checks interpreted image/expanded URL context, both display languages, intact HTML persistence, and Ultra selection.

## Always thinking deeply

`Skill.AlwaysUltra` (`alwaysUltra`, default false) forces `Runner.run` onto `ultraModel`, the `ultra` display slot, and the deeper research budget for any wake. The high model falls back to the configured middle/low model when absent, as with an explicit deeper reply. A normal request with this setting uses a length instruction that does not claim the user requested a revision of an earlier answer. It does not enable or disable external delegation: a preferred external agent still answers, with the built-in Ultra path used on fallback. The creation and editing forms expose this option, and an AI revision that omits it retains the saved value.

## Optional persona memory

`Skill.MemoryEnabled` (`memoryEnabled` in JSON) is an optional boolean. Omitted or null values retain the historical default of remembering; support's built-in default is false, also inherited by saved built-in overrides that omit the option. The creation and editing forms expose **記憶する** (Remember). AI revisions preserve the existing choice when they omit the field.

When false, turns receive no private memory, record queries return no memory (explicit timeline entry reads remain available), and the host skips the note keeper and its retry writes, summary rewrites, and memory migration. Neither daily/monthly check-ins nor their alarms are scheduled for that persona. A memoryless persona with only read tools also skips post-answer schedule/action planning and review model calls. The check-in settings endpoint rejects changes while memory is off. Ordinary routing, replies, persona editing, tool permissions, explicit notification schedules, and external-agent planning work as usual. Existing notes remain stored so enabling memory again can resume them; disabling does not erase user data. Cards and room menus hide memory actions and check-in controls, while retaining editing and external-agent preference.

Implementation: [skill.go](../../internal/persona/skill.go), [kakeratta_help.go](../../internal/tools/kakeratta_help.go), [bundle.go](./bundle.go), and [settings.go](../../internal/store/settings.go). Verification: [skill_test.go](../../internal/persona/skill_test.go), [kakeratta_help_test.go](../../internal/tools/kakeratta_help_test.go), and [settings_test.go](../../internal/store/settings_test.go).

## Management

Installing a definition from the frontend validates it, saves it through the Catalogue, and activates the new persona. AI-assisted definition creation is handled by the persona builder. Pausing a persona retains memory and removes schedules. Removing a persona removes its definition, memory, and schedules.

For person personas, `Skill.BasicInfo` (`basicInfo` in JSON) stores initial personal information, such as birthdays and preferences, with the definition. The builder returns it in the draft, and users can edit or clear it in the creation and editing forms. Saving never posts this information to the timeline or creates memory notes. Date-bearing information is separately normalized into notification schedules before saving; dates that cannot be established are omitted. The field is limited to 8,000 characters and cleared for subject personas.

A person's persona is not the person: it looks after the user's dealings with them. Every prompt it is given opens with `Skill.intro` - "you look after the user and 「name」, and are not name" - rather than "you are 「name」", which made it answer as the person ("ありがとう、届いたよ" to a gift the user said they had sent). `personIdentityRules` at the head of its instructions say the same in rules: it does not speak as the person or write their replies or feelings, it talks to the user, and when the user reports what they did for the person it answers the user with what it noted and what it can check later. The answer, phase, and note-keeper prompts all use `intro`, so the log keyed by the opening line stays whole.

Each person turn receives the current basic information through `forTurn`, alongside the shared person rules. Personas use it for answers, research, and scheduling, without copying it into notes; the current definition takes precedence over outdated notes. AI edits receive the current draft and preserve existing basic information when the response omits the field.

A persona's declared tools are selected from the Registry. A person persona's fields are always `Fact` and `Event` (`personMemory`).

## sona: preparation and timely follow-ups

The built-in `sona` replaces `plana`, retaining the storage ID `routine` so existing memory, schedules, and activation choices stay attached. Its default definition answers to `@sona` or `@routine`, not `@plana`. New workspaces start with `routine` active; saved active lists, including an empty list, take precedence.

sona covers upcoming plans, everyday belongings, habits, forgotten items, and weather relevant to an outing. `Routine` holds user-grounded belongings, timing, places and past omissions, without turning one failure into a permanent habit; `Errand` holds one-off plans and preparation until completed. Generic suggestions are not stored as the user's habits, and forecasts are fetched when needed rather than kept in fields. Existing `Routine` and `Errand` fields remain usable.

Its permitted tools are `schedule` and `weather_forecast`, alongside shared timeline and record reads. With a grounded departure time, it may schedule a preparation check shortly before leaving; chore-completion checks remain shortly afterwards. A sharing check-in can find upcoming plans in posts addressed to other personas and schedule a later preparation turn, staying silent until there is a reason to speak. The daily check-in time remains the app-selected minute between 07:00 and 21:00; the scheduled preparation turn supplies timely delivery. The system does not detect departure independently of posts, memory, or schedules.

Preparation wakes compare known plans, belongings and past omissions with a fresh forecast for the relevant region and outing period, distinguishing sunshine now from rain later. Unknown locations or times and unavailable forecasts are not invented; grounded belongings checks can still proceed without weather. Completed, cancelled, departed or already-mentioned plans stay quiet. Model instructions guide these decisions; schedule claims prevent repeating the same due wake. Existing person reminders can be read to avoid proposing duplicate notifications. User-customized definitions continue to override built-in defaults.

Verification: [sona_test.go](../../internal/persona/sona_test.go) exercises a scheduled weather lookup, weather failure with a belongings-only answer, and a silent sharing check-in that schedules preparation; [settings_test.go](../../internal/store/settings_test.go) checks initial activation and preservation of saved choices.

## Implementation and verification

- [skill.go](../../internal/persona/skill.go): `Catalogue`, `Snapshot`, `Skills`
- [queue_test.go](../../internal/persona/queue_test.go): a reaction reads the definitions once
- [people.go](../../internal/persona/people.go)
- [personas.go](../../internal/server/personas.go)
- [persona_builder.go](../../internal/server/persona_builder.go)
- [types.go](../../internal/persona/types.go)
- [skill_test.go](../../internal/persona/skill_test.go)
- [types_test.go](../../internal/persona/types_test.go): the fields each built-in and person persona keeps
- [persona_builder_test.go](../../internal/server/persona_builder_test.go): basic-information persistence and editing without posts or notes
- [people_test.go](../../internal/persona/people_test.go): basic information in turn instructions, and a person's persona not introduced as the person

# Delivering posts to personas

## Building the recipient set

After image analysis, Runner loads active personas and the thread. An explicit `@` mention selects its recipient; the HTTP API rejects mentions of multiple distinct personas. Without a mention, a reply to an active persona selects that persona. A mention takes precedence over a different reply target. For these directed entries, Runner skips both subject routing and person alias/keyword matching, and records the recipient as called by mention or reply. Replies to user entries or inactive/deleted personas use ordinary routing when there is no active mentioned recipient.

For other entries, Runner combines subject personas selected by the router and person personas whose aliases or keywords match the entry's text and associated content. Post edits also notify prior recipients to correct their existing memories, including when the edited entry has an explicit addressee.

Person personas are not sent to the subject router; their matches are recorded in `Heard`. Subject routing uses the configured model or the configured Jev Decisions API.

Edits refresh prior recipients again when their ordered delivery starts, so a preceding turn that recorded its wake after routing still receives the correction. Existing recipients receive edit instructions; newly routed recipients receive an ordinary first delivery. Failures reading recipients or the delivery thread retain the reaction for retry.

## Failures and records

Missing configuration, request failure, or malformed output leaves the router with no subject recipients and `Answered: false` plus an error. A valid explicit empty array means a completed decision with no recipients. Unknown IDs are ignored. Explicit mentions, reply targets, person matching, and other recipient paths operate separately from that model decision.

Routing records retain recipients, exclusion reasons, explicit-call reasons, completed personas, and personas that spoke. Completions change them through `UpdateRouting` transactions, so turns finishing at the same time - in one process or several - do not drop each other's state. The frontend receives `routed` and `turn` events.

Check and correction APIs distinguish a trial after changing conditions from applying the result. Merely checking a trial does not wake the actual persona.

## Implementation and verification

- [runner.go](../../internal/persona/runner.go): `prepareEntry`, `deliverEntry`, `routeSubjects`, `routingRecord`, `turnsDone`
- [route.go](../../internal/persona/route.go)
- [runner_test.go](../../internal/persona/runner_test.go): directed entries skip automatic routing and person matching; mention precedence and reply model escalation
- [people.go](../../internal/persona/people.go)
- [queue_test.go](../../internal/persona/queue_test.go): edits refresh recipients recorded after routing, including prepared delivery recovery
- [routing.go](../../internal/server/routing.go)
- [route_production_test.go](../../internal/persona/route_production_test.go)

# From research to answer publication

## Turn sequence

```mermaid
flowchart TD
  Input[Entry, thread, fields and latest records] --> Read[Research with permitted reads]
  Read --> Answer[Finalize Answer]
  Answer --> Phases[Propose schedules and actions]
  Phases --> Review[Validate structure, permissions, consistency]
  Review --> Execute[Execute schedules and actions]
  Execute --> Publish[Publish answer to timeline]
  Publish --> Keeper[Keep the record and fields with the note keeper]
```

The built-in model researches through `research`, calls `finish_read` to move to answering, and submits an `Answer` with `submit_answer`. When the answer is asked of the external agent, the turn opens an ask and ends; once the ask closes, a reaction carries the turn on with the agent's answer and research, which enter the same subsequent processing, or with the built-in planner from the start.

`Answer` contains text and source entry IDs. Sources must be IDs found through timeline search in this turn. Web evidence is linked in the answer body. Silence uses empty text, with `Unrelated` distinguishing an out-of-domain turn.

## Conditions for publication

`planPhases` proposes schedules and actions without changing the answer. A schedule carries only its time, recurrence, and a note of what it is for; it no longer writes a background memory note. Deterministic checks cover types, counts, permissions, and arguments; a review checks consistency with promises in the answer. `Workflow.batch` converts to an internal validation and execution form. It is not a submission format for external agents.

If `execute` fails, the answer is not published as though its promised operations succeeded. Execution of all side effects is not a transaction with rollback, however. Reading and planning have limits to prevent indefinite generation loops.

Failure in the note keeper after publication does not retract an already published answer. A turn that stays silent because it is unrelated skips memory updates.

Research reads are `records` steps (older kakera records by `tag`, `query`, and `before` month, or by `ids` - up to ten in one step, `maxEntryReads` - the entries records point to, each with the kakera it answered when it is a reply; an entry read that way may be cited in `sources`) and read-use tool steps (`readAll`).

## Implementation and verification

- [runner.go](../../internal/persona/runner.go): `run`
- [answer.go](../../internal/persona/answer.go): `research`, `composeAnswer`
- [phases.go](../../internal/persona/phases.go): `planPhases`
- [workflow.go](../../internal/persona/workflow.go)
- [answer_test.go](../../internal/persona/answer_test.go)

# Memory: facts, summaries, and kakera records

## Memory layout

A persona's memory is plain text, not OKF, and the host writes all of it ([facts.go](../../internal/persona/facts.go), [memory.go](../../internal/persona/memory.go)):

- **Facts, by field** - one file per field, `{Field}.md` (for example `Preference.md`, `Shop.md`). Each fact is one line: `- [f_ab12cd] 三田の二郎に行きたい from:e_a,e_d since:2026-09-08` - an id, at most 200 characters (`maxFactRunes`), the kakera it rests on, and the day it began to hold. The facts that hold now sit under `## いまの事実`; those that no longer do move under `## 以前のこと` with `until:` and, if a newer fact replaced them, `by:` (at most 50 per field, `maxPastFacts`). The facts that hold now are at most 2,000 characters per field together (`maxFieldRunes`).
- **A summary, by field** - at the top of the same file, `## まとめ（2026-10-02 更新）` and the persona's prose over the facts (the day is the last day it was looked over - rewritten, or handed to the rewrite with nothing to change), at most 2,000 characters. It is not rewritten with every kakera; see [Field summaries](#field-summaries).
- **Kakera records** - one line per kakera of the user's the persona took in, in monthly files `records/YYYY-MM.md`: `- 2026-10-01 12:34 [e_x](/?entry=e_x) one sentence #tag #tag`, then any dates: `expires:2026-10-02/1d`, `review:2026-11-01/31d` (the day and the span it was given, in days), `asked:e_y` (the post that asked about it under review), and `of:e_k` (the kakera, when the id is a reply). A record is what a line of the old `index.md` was. The id is the persona's reply when it replied, and the kakera itself when it did not; a reply leads back to its kakera through `ReplyTo`. The time is the kakera's, in the workspace time zone. A sentence is at most 140 characters (`maxSummaryRunes`) with one to five tags (`maxTags`); a `#` in it is made full-width so it cannot read back as a tag.

Every turn is handed each field's summary and the facts that hold now, with their ids, and the 15 latest records (`memoryView`, `renderFacts`, `recentRecordsLimit`). A field file whose field the persona's layout no longer names is still shown, marked so, and its facts can be moved into a named field by `update` (`memoryFacts.Orphans`). Older records are read on demand (`readRecords`): by `tag`, by `before` month, and by `query` - words in the record, or a meaning search of the timeline (`similarKakera`, `search.Unified` with score 0.5) whose hits match records by their id or `of:` - newest first, 30 at a time; or by `ids`, up to ten posts records point to. The external agent gets the same text as `memory` in its ask context. There is no `index.md`, no per-thing note, and no `notes_*` tool.

## Facts change only as asked

The keeper never rewrites a field. It hands in operations (`factOp`), and `planFacts` applies exactly those, in memory first so a submission that cannot be applied is sent back with its reasons:

- `add` - a new fact in a named field, resting on this kakera.
- `update` - a fact that holds now, replaced by a new one resting on its kakera and this one; the old one moves to the past with `by:` the new one. The same text with a `field` only moves it (a field the layout no longer names must be moved this way).
- `invalidate` - a fact that holds now moves to the past.
- `merge` - two or more facts that hold now become one, resting on all their kakera, held since the earliest of them; the merged ones are dropped, not kept in the past.

An id used twice, an id that does not hold now, an unknown field, an empty or over-long text, and a field whose facts would run over its limit are sent back. Nothing is lost except by `invalidate` or `merge`, and the past keeps what was replaced and when.

## Facts put right by the user

`Runner.EditFact` and `Runner.ForgetFact` ([facts.go](../../internal/persona/facts.go)), behind `PUT` and `DELETE /api/personas/{id}/memory/fact?field=&fact=`, change one fact under the keeper lock. An edit is a correction: the fact keeps its id, its kakera and its day, and nothing moves to the past; the field's limit still holds. A fact forgotten by the user is dropped, from the facts that hold or from the past, and a field left with nothing is deleted. The persona is not woken; the summary takes the change in at the next check-in.

## Field summaries

[summaries.go](../../internal/persona/summaries.go). A field's summary is rewritten at the persona's check-in - daily, or monthly for a person looked back on monthly, minimal or sharing alike - right after `dailyAt` claims the check-in and before the persona is woken for it, so the check-in reads it fresh (`rewriteSummaries`). Only the fields with something new since the summary's day are rewritten (`staleFields`, `changedSince`: facts held since, or ended, on or after that day; a summary never written takes everything in), and a field is not rewritten twice in a day. One `write_summaries` call on the persona model is handed, per field, the summary as it is, the facts added or changed, those that stopped holding, and all that hold now, and returns the new prose (sent back up to three times if a field is unknown, empty, or over 2,000 characters). Every field handed is marked with today's day, written or not, so it is not asked about again until more changes. A failed rewrite is tried at the next check-in. Facts are dated by day, so a fact added on a summary's own day is taken in again the next time.

The same call retires plans whose day has passed. A fact that holds now and names only days before today - `2026-10-03`, `2026年10月3日`, `10/3`, `10月3日`, a day without a year read in the year the fact began (`datedPast`, `pastDated`) - is handed with its id, and its field is handed even if nothing else in it changed. The model returns `invalidate` with the ids of those that are no longer ahead - a reservation, an appointment, a deadline - and leaves events that happened, which stay true; those ids move to the past (`planFacts`) before the summaries are written, and the summary may be rewritten for them. With no changed field and no past-dated fact, no model is called.

## Record dates, extension, and review

The keeper dates each new record from what it is ([review.go](../../internal/persona/review.go), `keeperDates`): `expires` - the next day, say - for a kakera that served its moment ("where for lunch today?", "how do I get to Shibuya?"), and `review` - weeks for a shop the user wants to try, months for a topic - for something the user said whose lifetime is unknown; preferences and habits get neither. A day must be tomorrow or later (`checkKept`). Built-in layouts say which of their records get which.

- **Expiry.** The due pass deletes records whose `expires` day has come (`expireRecords`, under the keeper lock), takes their kakera out of the facts' sources (`kakeraOf`, `dropFactSourcesHeld`) - a fact that rested on it alone goes - and returns the next such day for `nextDue`. Nobody is woken.
- **Extension.** Records a turn answering a post reads through `records` - shown by a search, or opened by id - have both dates moved so the span they were given starts again today (`extendRecords`, `recordDate.extended`); a date never moves closer. Records only handed over in the latest 15, and reads during check-ins or tidy-ups, do not count. An external agent's reads are kept on the ask (`askResearch.Referenced`) and count the same way.
- **Review.** A daily check-in is handed up to three records whose `review` day has passed, oldest first (`reviewsDue`; one that also expires is left to the sweep), with `reviewInstructions`: the persona asks about one in its post. The keeper is handed the same records and submits `records` decisions; it puts the asked one off a week, and the host marks it `asked:` with the check-in post. When the user replies to that post, the persona is told which records it asked about (`reviewReply`, wake `review-reply`), and the keeper - recording the reply as a kakera too - keeps the record (review cleared), moves it, or forgets it. A forgotten record's kakera is taken out of the facts' sources as on expiry, so what the user let go of is let go of in the facts too. A date restated unchanged keeps its span; a new one counts from now (`changeRecords`, `redated`).

## The note keeper after publication

`keepNotes` ([notekeeper.go](../../internal/persona/notekeeper.go)) runs once the remark is out, for a kakera of the user's (`keepsNotes`: a post, reply, or edit by the user, including listening turns) and for a check-in handed records due for review. It makes one `keep_memory` call on the persona model, in the persona's voice, with the summaries and facts, the latest records, the thread, the trigger, and the published answer, and no way to read more. The submission is `summary`, `tags`, `expires`, and `review` for the record; `facts`, the fact operations (above); and `records`, decisions on the records it was handed for review (`handedRecords`). `checkKept` and `planFacts` send a submission back with their reasons (no or long sentence, wrong tag count, a date that does not parse or is not after today, a record it was not handed, an operation that cannot be applied, two or more of the answer's own sentences copied into a fact - `copiesAnswer`), up to three attempts (`maxKeeperAttempts`). The facts rest on the kakera itself, never on a reply to it. The keeper writes the changed fields (`writeFacts`) and appends the record (`appendRecord`), replacing any record already kept for the same kakera - under its own id, or under a reply of the persona's to it - so an edit or a retry leaves one record. The remark lists the fields written in its `Notes` (`pointAtNotes`).

The keeper runs under the named lock `keeper:{id}`, one at a time across processes; if the lock is lost it stops writing. Its input is persisted as a `NoteJob` (`keeperSnapshot`). Failures retry with increasing delays from one minute up to one hour, on their due time (see [Times and wakes](./architecture.md#times-and-wakes)); a job whose kakera was deleted or edited meanwhile, or a job left at the old `index` stage, is dropped instead. Scheduled wakes, unrelated silences, deletion tidy-ups, and check-ins without a review keep nothing.

## Deleted kakera

`EntriesForgotten` ([forgotten.go](../../internal/persona/forgotten.go)) takes the deleted entries out of the memory of the personas woken for the post before anyone is woken - a paused persona included: their records (`dropRecords`, in the months of the deleted entries), and the deleted kakera from the facts' sources (`dropFactSources`), so a fact that rested on nothing else goes, now or in the past, while one that rests on another kakera, or that never had a source, stays. No model is asked. A persona that had a record removed and is active gets one `deletedInstructions` wake for the whole deletion: it cannot speak and may cancel the schedules it set for that kakera; its keeper does not run. A persona's reply deleted on its own leaves the kakera it answered: its record is re-keyed to the kakera and nobody is woken. The summary is not rewritten then; it takes the change in at the next check-in. Deleting a field or a month of records from the memory viewer wakes nobody.

## Splitting prose into facts

Fields were once prose, rewritten whole by the keeper. A field-shaped file with text and no fact line or section heading is prose (`proseText`, `proseFields`); the running app reads it as the field's summary, so a fact the keeper adds before it is split keeps the prose instead of writing over it. `Runner.MigrateMemory` ([migrate.go](../../internal/persona/migrate.go)), run from [cmd/migrate-memory](../../cmd/migrate-memory/main.go) after the deploy (see [Memory migration](./operations.md#memory-migration)), hands one persona's prose fields to the persona model, which returns facts through `keep_memory` (`add` only, checked by `planFacts`). A dry run only reports them and saves a plan; `Runner.ApplyMigration` writes the plan as it is, or as a person edited it: the facts, with no source, and the prose kept as each named field's summary, dated today. A prose field the layout no longer names has its facts moved into a named one and its file deleted. It runs under the keeper lock and refuses when the prose changed since the dry run - the files, or their text (`Migration.Prose`). Every defined persona is run, paused ones included; one with no prose costs no model call.

## Implementation and verification

- [facts.go](../../internal/persona/facts.go): fact lines, `planFacts`, `writeFacts`, `dropFactSources`, `renderFacts`
- [summaries.go](../../internal/persona/summaries.go): `rewriteSummaries`, `staleFields`, `datedPast`
- [memory.go](../../internal/persona/memory.go): record lines, `memoryView`, `findRecords`
- [notekeeper.go](../../internal/persona/notekeeper.go): `keepNotes`, `checkKept`, `appendRecord`, `dropRecords`
- [notes_tools.go](../../internal/persona/notes_tools.go): `readRecords`, `similarKakera`, `recordEntry`, `RecordsTagged`
- [migrate.go](../../internal/persona/migrate.go): `MigrateMemory`, `ApplyMigration`
- [review.go](../../internal/persona/review.go): `reviewsDue`, `reviewReply`, `handedRecords`, `expireRecords`
- [note_retry.go](../../internal/persona/note_retry.go)
- [facts_test.go](../../internal/persona/facts_test.go): fact operations and their history, the field limit, records found through their kakera, summaries rewritten at the check-in, past plans retired there and past events kept, a fact put right or forgotten by the user, facts going with a record forgotten or expired
- [notekeeper_test.go](../../internal/persona/notekeeper_test.go): records under the reply or the kakera, edits replacing a record, sent-back submissions, record dates and their round trip, extension on reading, the review asked and settled, the turn's memory view, reading recorded posts together, splitting prose into facts
- [due_test.go](../../internal/persona/due_test.go): expired records and the next expiry
- [forgotten_test.go](../../internal/persona/forgotten_test.go): records dropped and re-keyed, facts losing their sources on deletion, the tidy-up wake
- [note_retry_test.go](../../internal/persona/note_retry_test.go)
- [answer_test.go](../../internal/persona/answer_test.go): an answer copied into a fact is sent back

# Schedules, check-ins, and monthly summaries

## Scheduled wakes

Creating an ordinary persona schedule stores its time, recurrence, and wake note. Personas with the schedule tool receive only their own schedules in the initial turn context (also handed to external agents). The same permission automatically provides the read-only `schedules_list` tool during research. It reads current workspace schedules on demand, including other personas' schedules, and accepts owner ID (`skill`), substring (`query`, matching wake notes or owner names/IDs), and notification-time (`from`/`to`, in the workspace timezone unless an offset is supplied) filters. Results are chronological, default to 20 items, and are capped at 30. The response includes `total` and a `next_after` cursor; pass it as `after` with the same filters to fetch the next page. If the cursor was removed, restart without it. Recurring schedules are matched by their stored next notification time, not expanded into future occurrences.

Each item displays its owner as `Name（ID）` using the persona definitions for the turn, falling back to the ID when no name is available. Tool listings preserve `skill` as the ID and add the display label in `owner`, plus `daily` and `annual` recurrence flags. The read tool returns no memory; memory remains private. Cancellation is validated and executed only for schedules owned by the requesting persona. Planning instructions ask personas to check other owners' reminders through the read tool when duplication is possible; this does not atomically prevent concurrent creations. The legacy `schedule list` internal action remains available but is not offered during research.

Person calendars are also synchronized when a person is created, installed, reactivated, or its basic information is edited, and when it receives a user post or reply containing date or plan information (including analyzed image text). A dedicated model request extracts confirmed birthdays, anniversaries, meetings, appointments, and cancellations using the current time, timezone, and existing schedule keys. Basic-information synchronization replaces schedules sourced from that field; posts add or correct only the specified events. Unrelated schedules are preserved. These schedules need nothing in memory.

Birthdays and anniversaries have two annual notifications: seven days before and on the date, at 09:00 in the saved timezone. Newly learned imminent dates notify at the next minute when the usual time has passed. February 29 is observed on February 28 in non-leap years. Annual schedules retain the original calendar date and advance to the following occurrence when claimed; repeating the same information preserves that occurrence. Appointments are one-time notifications, respecting explicitly requested notification times; otherwise the default is 09:00 on the event date, or one hour before an event earlier than 09:00. All schedules accept any future date without a scheduling horizon, within the existing per-persona count limit.

Calendar reconciliation for one persona runs under the named lock `calendar:{id}`, and stops writing if the lock is lost. Due person events use the normal Runner notification path, with instructions to notify even when memory says nothing about it. Model or storage failures are surfaced as API errors during basic-information saving or live error events during post processing. Runner claims due schedules before executing the persona. Claiming is part of the persistence contract and prevents duplicate execution of the same schedule. Pausing or removing a persona removes its schedules.

A successful scheduled utterance emits an internal `reminder` event immediately after its timeline post is persisted. The schedule instruction prefix identifies the wake even when person-event instructions are appended or an external-agent answer resumes later. Silent and failed wakes emit no reminder. Ordinary replies and daily check-ins do not use this event. See [Reminder push delivery](./backend.md#reminder-push-delivery) and [schedule_test.go](../../internal/persona/schedule_test.go).

Past schedules are not all replayed unconditionally. Execution windows and persona state determine how late schedules are handled.

## Periodic checks

Daily checks use the user's time zone and times derived from the workspace and persona. The previous actual execution time determines the lookback window, and execution is claimed first. A sharing check-in reads the timeline since the previous one and the persona's records by tag, also checking upcoming plans and preparation against memory and relevant external information. A persona permitted to schedule can arrange a later wake and stay silent now, checking other personas' schedules to avoid duplicates. It speaks only when there is something useful now; a minimal one speaks only about what needs the user's decision - records due for review. Either is handed the records due for review (see [Record dates, extension, and review](#record-dates-extension-and-review)). Before the persona is woken for its check-in, its field summaries are rewritten from what changed (see [Field summaries](#field-summaries)). Person personas can check in monthly instead.

The app itself builds monthly summaries from the timeline as topic-based posts with source links. Person personas' monthly checks use the summary and relevant records. Both read only the lookback window through `TimelineWindow`, and summaries have an input length limit.

## Implementation and verification

- [schedule.go](../../internal/persona/schedule.go)
- [person_schedules.go](../../internal/persona/person_schedules.go): event extraction and calendar reconciliation
- [person_schedule.go](../../internal/store/person_schedule.go): annual dates and recurrence
- [person_schedules_test.go](../../internal/persona/person_schedules_test.go)
- [person_schedule_test.go](../../internal/store/person_schedule_test.go)
- [workflow.go](../../internal/persona/workflow.go): own schedule context and ownership validation
- [schedule.go](../../internal/tools/schedule.go): filtered, paginated read-only listing and owner-scoped cancellation
- [schedule_test.go](../../internal/tools/schedule_test.go): cross-persona visibility and cancellation ownership
- [schedule.go](../../internal/store/schedule.go)
- [schedule_test.go](../../internal/store/schedule_test.go): future dates without a horizon, ownership, caps, and claims
- [daily.go](../../internal/persona/daily.go)
- [summary.go](../../internal/persona/summary.go)
- [daily.go](../../internal/store/daily.go)
- [schedule_test.go](../../internal/persona/schedule_test.go)

# Routing checks and reply previews

## Routing checks

`CheckRouting` evaluates a target entry against the persona's current conditions and returns the delivery decision and reason. Person personas return alias and keyword matches. A token records the entry text, persona, and decision at check time.

`ApplyRouting` uses that decision without calling the router again and updates the routing record. A newly selected persona gets the post as a `Deliver` reaction in the conversation's lane and answers like any other reaction; the request does not wait for its turn. A later trial cannot undo delivery that already happened.

## Reply previews

`PreviewReply` reconstructs the thread before a persona spoke, or through the target user entry, and runs a built-in model turn using the current instructions. `dryWorkspace` reads the real workspace but holds remarks and memory changes inside the preview; old notes are not folded in a preview. Schedule and wake-count writes likewise do not persist to the host workspace. Model and search requests still occur.

`ApplyReply` persists the previewed remark and field changes without regenerating them. From the records file the preview wrote it takes only the record it made, keeps it under the remark that is kept, and appends it under the keeper lock (`keepRecord`), so records written since the preview are not overwritten. Replacing a persona remark preserves its ID, timestamp, and conversation position. Applying a preview does not create the schedules proposed during that preview.

## Token constraints

Trials are `trials` runtime records kept for 15 minutes, so a check and its application may reach different processes and survive a restart. Application marks a trial as being applied in a transaction, so two requests cannot apply it at once. Application checks the token and entry ID, changed or deleted text, and whether the persona remains active. A completed application is recorded so repeating the same token is treated as already applied.

## Implementation and verification

- [trial.go](../../internal/persona/trial.go): `trialRecord`, `takeTrial`, `ApplyRouting`, `ApplyReply`
- [queue.go](../../internal/persona/queue.go): `deliver`
- [preview.go](../../internal/persona/preview.go): `PreviewReply`, `dryWorkspace`
- [routing.go](../../internal/server/routing.go)
- [preview.go](../../internal/server/preview.go)
- [condition-fix.js](../../frontend/src/condition-fix.js)
- [reply-fix.js](../../frontend/src/reply-fix.js)
- [trial_test.go](../../internal/persona/trial_test.go)

# Related chapters

- [Architecture and runtime](./architecture.md)
- [API and persistent state](./backend.md)
- [Browser application and device state](./frontend.md)
- [External agents, tools, and sharing](./integrations.md)
