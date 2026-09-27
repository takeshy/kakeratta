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
 ↓ a reply to a persona's remark: Nemotron 3.5 Lightning answers one question - does it ask for a better answer?
 ↓ every recipient, in parallel, one turn   (Nemotron Super; the replied-to persona on Ultra when it does)
     read     → index.md, notes, timeline search, web search, places, routes
     workflow → the writes and the remark, every text final
     checked by code, then reviewed by Nemotron Super unless it only writes notes and speaks
     (and the persona cannot schedule); run in order, stopping at the first step that fails
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

A review that judges taste becomes a second persona. The review here only looks
for things that go wrong: something stated that did not happen or was not in
what the persona read, a promised time with no schedule, a remark in a
different language from the post, a note overwritten without being read, the
persona's own instructions broken, a remark that describes work instead of
answering. An invented schedule id used to be on this
list; the app now catches it before the review, by matching every id in the
workflow against the persona's own schedules.

It still made mistakes. In one live run a persona's remark was its own plan -
"search the web for lunch near Kamiyacho", word for word its summary - and the
review passed it by describing a remark that listed shops. Now a remark equal
to the summary is rejected by code, and the review is told to quote the remark
before judging it.

What gets reviewed depends on the kinds of step, not how many there are. A
workflow with anything beyond `write_note` and `speak` - schedules,
forgetting, tools - is always reviewed. One that only writes notes and speaks
is reviewed only when the persona holds `schedule`, because a time promised
with nothing set to wake has exactly that shape.

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

## 10. Model tiers per persona gave way to one model and a narrow question

Personas used to carry a tier. Low ran on Nemotron 3.5 Lightning with notes,
web search and a remark called directly, because the read-then-workflow
structure was where the smallest model stumbled most; middle and high ran the
full workflow on Super and Ultra. That meant two ways of running a turn, a
rule refusing tools on a low persona, and the person writing a persona having
to guess how hard its posts would be.

Now every persona a post reaches runs once, in parallel, on Super, always
through the reviewed workflow. Ultra is used when the user asks for it: when
someone replies to a persona's remark, Lightning is shown the remark and the
reply and answers only whether the reply explicitly asks for a correction, a
rethink, more detail or a deeper look ("違う", "本当？", "もっと詳しく"). If it
does, the persona that was replied to answers on Ultra - added even when
routing did not pick it - with "keep it short" replaced by a request for a
thorough Markdown answer. Anyone else the reply reaches stays on Super, and so
does a judgement that fails.

Lightning is not asked whether Super would be good enough; only whether the
user said, in so many words, that they want more. On 15 synthetic replies
(2026-09-22) it judged all 15 correctly in 0.67 seconds on average; Super also
got 15/15 but took 1.30 seconds at about five times the cost. Each remark
records which model answered, and the timeline marks an Ultra answer.

**Lesson:** the smallest model did badly at a whole turn and well at one
narrow yes/no question. Give it the question.

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

## 13. What made any of this findable

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
