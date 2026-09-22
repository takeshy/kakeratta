# What was hard about the AI

[README](../README.md) · [日本語](ai-lessons_ja.md) · [Under the hood](technical.md)

Kakeratta hands each post to small AI personas that remember things in their
own notes and, now and then, say something. The models are NVIDIA Nemotron on
Nebius Token Factory. This page is the record of what went wrong on the way to
making that dependable, and what was done about each. Every problem below was
seen in a live run or in production data. Each fix is covered by a test, and
the key ones were confirmed to fail when the fix is removed.

The short version: **almost nothing that went wrong was the model being
unable to think.** It was the model doing what the prompt, the schema or an
example showed it, in a place where that was wrong - and the app having no way
to see it happen.

## The shape it ended up in

```
post
 ↓ routing           Nemotron Super (or optional Jev) matches the post against each persona's conditions
 ↓ each persona:
     low tier         notes, web search, remark — called directly, no review   (Nemotron 3.5 Lightning)
     middle / high    read   → notes, web search, places, routes, the image     (Nemotron Super / Ultra)
                      workflow → the writes and the remark, every text final
                      checked by code, then reviewed by Nemotron Super unless it only writes notes and speaks
                      run in order, stopping at the first step that fails
 ↓ one structured log line per routing decision and per persona turn
```

## 1. A protocol in prose was followed in prose

Personas first answered by writing a JSON object into their reply -
`{"action":"tool","tool":"schedule",...}` - which the app parsed. plana, the
persona that keeps small errands, was meant to set itself a wake-up for posts
like "I'll watch the drama tonight". It wrote the time into its note as text
instead (`予定: 22:30`), and the schedules collection stayed empty. From the model's side, a line in a note
and a tool call were both just text it had written.

**Fix:** native tool calling on Token Factory, with `tool_choice: "required"`.
Both Nemotron tiers were verified to return real `tool_calls` and to honour
`required` before the migration. Speaking became a tool too, so staying quiet
is something a persona does rather than something the app infers from a
missing field.

## 2. The tool that could not run

Even with tool calling, plana never placed a schedule from a post. The tool set
for reacting to a post was built without the schedule store; the wake-up paths
had it. So the tool was offered, called, and answered "schedule is not
configured" - and plana did the only thing left to it, writing a made-up id
(`s_xxx`) into its note.

**Lesson:** a model working around a broken tool looks exactly like a model
misbehaving. The fix was one line; finding it took reading production data.

## 3. Routing threw away correct answers

- The router answered `plana`, the name a person sees. The id is `routine`. An
  unknown id meant "routing failed, deliver to everyone", so a food persona kept
  answering posts about medicine.
- The routing prompt told the model to deliver when in doubt, because a wrong
  "no" once swallowed a post. In practice "I want to be at Kamiyacho by 9:10
  tomorrow" reached the wine persona.
- A failed routing call delivered to everyone - the exact noise routing exists
  to prevent - and nothing recorded whether "everyone" was a choice or a
  fallback.

**Fix:** routing is now made by the best model configured (Nemotron Ultra),
delivers only where a post belongs, resolves names as well as ids, ignores a
name that matches nobody instead of guessing, and delivers to nobody when it
fails (a persona named with `@` is still always reached). Every decision is
logged with a one-line reason per persona. The first such log line showed
why a post had gone to all six personas: the router had answered `plana`.

## 4. Deciding and acting in the same breath

A persona calling tools one at a time had nothing between "write that the
drama was watched" and the note that says so. Live runs produced
`2026-09-16 22:21 済` ("done") for a drama that had not started, and
"22:30ごろに聞くね" ("I'll ask around 22:30") with nothing set to wake at 22:30.

**Fix:** a persona now hands in a **workflow** - the writes and the remark, in
order - and the app checks it before anything happens. An earlier version let
a `compose` step write the remark *after* the review; the first live run used
exactly that gap to promise a time with no schedule behind it. Now every text
in a workflow is final when it is reviewed, and reading (notes, search) happens
before the workflow rather than inside it.

The app's side is mechanical: run in order, stop at the first step that fails,
and keep `speak` as the last step so a remark never goes out after what it
rests on did not happen.

## 5. One step for two things that must not come apart

The failure the whole workflow started from was a note that says 22:30 with
nothing set to wake at 22:30 - two writes in two places. `write_schedule` now
places the wake-up **and** writes the note it rests on as one step; if the note
cannot be written, the wake-up is taken back. A test proves the rollback fails
when removed.

## 6. Structured output left out everything optional

With only `type` required in a step's schema, live runs handed in steps that
were nothing but `{"type":"notes_write"}` - output constrained to the schema
simply omitted optional fields. A free-form `args` object tends to come back as
`{}` for the same reason.

**Fix:** every field of a step is required, with empty values for the ones not
used, and tool arguments travel as a JSON object written into a string.

## 7. Models use what you show them

Three separate failures had the same cause:

- The route sample in the spec said `"mode":"foot"`. A live travel persona
  copied it into every call, and production answered "about 259 minutes on
  foot". The sample no longer names a mode, and the persona picks one from the
  post and the distance (walking within 30 minutes, driving past an hour, both
  in between).
- The shared prompt had an example about "1時間後。" ("in an hour"). A persona
  asked for lunch ideas wrote "arriving in an hour" into its note and its
  remark. The review caught it.
- The workflow schema offered a `tool` step to every persona and the spec listed
  web search under it. A persona running on Ultra put a web search inside its
  workflow.

**Fix:** the workflow schema and the written spec are built per persona from
the tools it holds. A persona that cannot schedule is never shown
`write_schedule` or an `at` field; lookups (web search, places, routes,
timeline search) exist only in the reading phase, so no persona is offered a
lookup as a workflow step. Each step in the spec has a correct sample and the
mistake a live run actually made.

## 8. The review needs to be narrow, and checked

A review that judges taste becomes a second persona. The review here only looks
for things that go wrong: something stated that did not happen or was not in
what the persona read, a promised time with no schedule, a remark in a
different language from the post, a note overwritten without being read, the
persona's own instructions broken. An invented schedule id used to be on this
list; the app now catches it before the review, by matching every id in the
workflow against the persona's own schedules.

It still made mistakes. In one live run a persona's remark was its own plan -
"search the web for lunch near Kamiyacho", word for word its summary - and the
review passed it by describing a remark that listed shops. Now a remark equal
to the summary is rejected by code, and the review is told to quote the remark
before judging it.

**Lesson:** anything code can check, code checks first. The model reviews only
what code cannot.

## 9. Budgets that ended turns for the wrong reason

- A workflow sent back for its shape (a misplaced web search) used up the draft
  the review needed, and the review then rejected the second draft with no
  chance left. Shape rejections are now counted apart from review drafts.
- A persona spent all its reads retrying one failing map search and never tried
  the web search it also held. Each read result now ends with how many reads are
  left and which other tools there are, and the same call failing the same way
  is said outright. The next live run switched to web search after two
  failures.

## 10. Not every tier should carry the same structure

The read-then-workflow structure was where the smallest model stumbled most.
The tiers now mean different ways of working:

| Tier | Model | How it works |
|---|---|---|
| low | Nemotron 3.5 Lightning | Notes, web search and a remark, called directly. No workflow, no review |
| middle | Nemotron Super | Reads, hands in a workflow, reviewed, run |
| high | Nemotron Ultra | Same as middle |
| the app | Nemotron Super / optional Jev | Routing (Super or Jev) / reviewing workflows beyond notes and remarks (Super) |

Any tool that does more than look things up needs a workflow, so a low persona
holding one is refused on save; one saved earlier runs a tier up rather than
quietly losing its tools.

## 11. Small things that broke real data

- Prompts and notes carried UTC from the server; the user's time zone is now
  what every persona is told.
- Notes arrived with escaped line breaks (`\n` as two characters) and with the
  frontmatter indented, which made them unreadable and untyped. Both are
  repaired on write, without touching what the persona meant.
- A persona woken only to tidy up after the user deleted a post published a new
  top-level post ("Watching the drama?"). Tidy-up wakes are now silent turns,
  and are not offered `speak` at all.

## 12. What made any of this findable

Every routing decision and every persona turn is written as one structured log
entry: who was woken and why, what was read, each workflow draft with its
problems and review, each step as it ran, and what was said. Most of the fixes
above started from one of those lines. Before them, "routing chose everyone"
and "routing failed" were indistinguishable.

Tests were held to one rule: a test for a fix has to fail when the fix is
removed. Twice a new test passed for the wrong reason - once because a test
script's JSON was malformed and the case it was meant to exercise never ran -
and was rewritten until it failed against the old code.

## Still open

- Place-name lookup through the geo MCP server does not prefer nearby matches:
  "Tokyo Tower by car" once resolved to a place 150 km away. The fix belongs in
  that server, not here.
- Only a handful of live runs back any of this. A fixed set of posts run against
  each tier would say more about which model suits which persona than the
  anecdotes above.
