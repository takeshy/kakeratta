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
post / reply
 ↓ the image is read once                  (GLM-5.3-Flash)
 ↓ routing           Nemotron Super (or optional Jev) matches it against each persona's routing condition
 ↓ every recipient, in parallel, one turn   (depth from explicit phrases; Super reads, Ultra answers deep requests or AlwaysUltra)
     read     → field summaries and facts, latest records, older records, timeline search, web search, places, routes
     answer   → final text and sources, plus whether to remember the post and whether a schedule is needed
     plan     → propose schedule changes; validate structure, ownership, and times in code
     review   → Nemotron Super checks the plan only when it contains schedules
     execute  → stop at the first failed change; publish the answer after successful execution
     memory   → the note keeper runs separately after publication when the answer asked to remember
 ↓ once a day: the app summarises the day's posts, then every remembering persona checks in reading that summary
 ↓ one structured log line per routing decision and per persona turn; the routing decision is kept per post
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

**Fix:** routing delivers only where a post belongs, resolves names as well as ids, ignores a
name that matches nobody instead of guessing, and delivers to nobody when it
fails (a persona named with `@` is still always reached). Every decision is
logged with a one-line reason per persona. The first such log line showed
why a post had gone to all six personas: the router had answered `plana`.

Routing was moved to Nemotron Ultra while this was being debugged. That made
the problems easier to see, but routing runs on every post, and a personal
notebook should not make anyone think twice before saving a small thought.
What the router sees was cut down to a short routing condition per persona -
what posts it should get, not what it is meant to achieve - and the examples
that pulled the model toward particular cases were removed. After that, a
replay of 20 production posts put Super at 1.91 seconds and about 0.098 yen a
route against Ultra's 2.28 seconds and 0.449 yen, and Super became the default
again ([2026-09-19](routing-model-comparison-2026-09-19.md)).

**Lesson:** if a large model gets a routing task right and a smaller one gets
it wrong, the task may be too ambiguous. Change the task before paying for the
model.

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

A review that judges taste becomes a second persona. Earlier versions also
asked the reviewer to judge answer content, language, and memory writes. In one
live run, a persona's remark was its own plan — "search the web for lunch near
Kamiyacho", word for word its summary — and the review passed it by describing
a remark that listed shops. At that stage, the fix rejected equal summary and
remark text in code and asked the reviewer to quote the remark before judging it.

The current pipeline separates the final answer, schedule planning,
execution, publication, and note keeping. Code checks types, counts,
permissions, arguments, and references to existing schedules first. Model
review runs only if the plan contains schedules, even when the
persona has scheduling permission. It checks that changes match the user's
request and promises in the answer, and that operations do not conflict or
leave dependencies unmet. It does not review answer quality, length, research
results, or memory updates. The note keeper runs separately after publication.
See [the current turn sequence](system/personas.md#from-research-to-answer-publication).

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

## 10. Model tiers gave way to research planning and final-answer depth

Personas used to carry a tier. Low ran on Nemotron 3.5 Lightning with notes,
web search and a remark called directly, because the read-then-workflow
structure was where the smallest model stumbled most; middle and high ran the
full workflow on Super and Ultra. That meant two ways of running a turn, a
rule refusing tools on a low persona, and the person writing a persona having
to guess how hard its posts would be.

Built-in personas normally answer on Super, with recipients running in parallel.
**Always think deeply** selects Ultra for final answers to ordinary requests too. All built-in research and schedule planning use Super. Research reports its live Web-search allowance each round and removes exhausted or unavailable reads from the schema; Web exhaustion still permits other reads or `finish_read`. Plans with
schedules or actions pass through model review; plans without either skip it.
The first Super research call now decides deep and a short research plan from the user's request and required answer quality, alongside the first read. A short "詳しく" reply requests detail about its parent answer; comparison, synthesis or careful verification can also select deep without explicit depth words. Thanks and simple follow-ups stay ordinary. Only final-answer composition uses Ultra for deep or AlwaysUltra. Expanding the normal 10-read/2-search allowance to 20/4 preserves spent counts; later research receives the plan and actual remaining allowance.

The former standalone Lightning check judged 15 synthetic replies correctly in 0.67 seconds on average on 2026-09-22; Super also got 15/15 but took 1.30 seconds at about five times the cost. Real use later exposed a false result for a short "詳しく" reply. The separate check has been removed. Each remark still records the final-answer model slot, and the timeline marks Ultra answers.

**Lesson:** depth selection depends on context and required answer quality. Combining it with the first Super research plan removes a classification-only call.

## 11. Small things that broke real data

- Prompts and notes carried UTC from the server; the user's time zone is now
  what every persona is told.
- Notes arrived with escaped line breaks (`\n` as two characters) and with the
  frontmatter indented, which made them unreadable and untyped. Both are
  repaired on write, without touching what the persona meant.
- A persona woken only to tidy up after the user deleted a post published a new
  top-level post ("Watching the drama?"). Tidy-up wakes are now silent turns,
  and are not offered `speak` at all.
- Every wake handed a persona a listing of all its notes, so the prompt grew
  with the memory. A wake now hands over `index.md` and a note count; the index
  is the way in, and `notes_list` pages 100 at a time, newest first.
- Routing records showed every persona as "called, but stayed silent" the
  moment a post was routed - only in production. Firestore returns an empty
  array as a nil slice, and nil was how the app recognised an old record with
  no turns tracked. Local development stores JSON, where the two differ, so no
  test saw it. The record now says for itself that it tracks turns.

## 12. The persona's own answers were becoming its memory

A persona that speaks on a post has to leave a note behind; otherwise it has
opinions and no memory of having had them. That rule was right and had a
side effect. Asked "where should I have lunch on the way to Soka", the food
persona searched, proposed three places, and, needing a note, filed the three
places. Nothing in that note was about the user. The more thoroughly a persona
answered, the more of its memory was its own output, and every later wake
read it back as if it were something learned.

The fix was not to judge better what to keep. Notes now have lifetimes. A
note a persona made from its own searching carries `expires`, the next day,
and the app deletes it then; the user's reply is what turns it into memory,
by having the persona rewrite it without the line. A note about something the
user said can carry `review`, a date the persona picks from the content -
weeks for a shop to try, months for a topic - and when it passes the persona
asks, in a post of its own, whether to keep it. The app never deletes those.
A wrong guess about what to keep now heals on its own, and expiry became the
last moment a thing is brought back to the user rather than the moment it is
thrown out.

Two smaller things came out of it. A reply under a post a persona started on
its own had gone through the router like any other and often reached nobody;
it now always reaches the author. And "forget it" answered with `forget_note`
and a word had failed the note-required rule, since forgetting was not
counted as leaving something behind; it is now.

## 13. One note per thing, written by the persona, did not stay readable

Memory began as an OKF bundle the persona kept itself: one note per thing,
an `index.md` to find them by, a keeper that chose which notes to read and
proposed writes and forgets. Much of what the app grew to check was there to
hold that together - one title, one note; types from the layout; every note
under an index heading; an index repaired after every turn; links carried
over when notes were merged. It still drifted: notes split one thing across
paths, the index grew into a second memory, and a persona read the index and
a few notes and missed the rest.

It is now two things, both written by the app. One summary per field the
skill names, at most 2,000 characters, rewritten whole when a fragment
changes it; and one line per fragment - a sentence, its tags, and the post to
go back to: the persona's reply when it replied, the fragment when it did
not. The keeper is a single call that hands in the sentence, the tags and the
summaries that change; it cannot read more and cannot choose a path. A
record is what a line of the index was, and the lifetimes of lesson 12
moved onto it: `expires` for a fragment that served its moment ("where for
lunch today?"), `review` for something the user said whose lifetime is
unknown, both extended when the persona reads the record again for a post.

The old notes were not migrated by the running app. A one-off command had the
persona's model draft the summaries, a person read the draft - the first one
kept figures and explanations the persona had researched, and dropped a gift
the user had sent - and only the edited draft was written. A model asked to
fold its own old notes keeps what it wrote; a person has to look.

That form did not last either. A summary rewritten whole with every
fragment loses a detail at a time, unnoticed, and a deleted fragment had to be
taken out of prose by a model. Fields are now facts: one line each, with the
fragment it rests on, changed by `add`, `update`, `invalidate` and `merge`, so
nothing goes except when the keeper says so, and the past keeps what was
replaced. Deleting a fragment takes out the facts resting on it alone, by
code. The prose stayed - the user wanted the persona's own reading of a field
- but it is rewritten once a day, at the persona's check-in, from what
changed among the facts, rather than with every fragment.

## 14. What made any of this findable

Every routing decision and every persona turn is written as one structured log
entry: who was woken and why, what was read, each workflow draft with its
problems and review, each step as it ran, and what was said. Most of the fixes
above started from one of those lines. Before them, "routing chose everyone"
and "routing failed" were indistinguishable.

The routing decision is also kept per post and shown from the timeline: whom
the router chose, whom it passed over and why. When the user says a persona
should or should not have been called, that correction rewrites the persona's
routing condition, and the router tries the same post again before it is
saved. The condition is the only thing that changes; nothing is learned
silently.

Tests were held to one rule: a test for a fix has to fail when the fix is
removed. Twice a new test passed for the wrong reason - once because a test
script's JSON was malformed and the case it was meant to exercise never ran -
and was rewritten until it failed against the old code.
