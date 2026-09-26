# Under the hood

[README](../README.md) · [日本語](technical_ja.md)

You do not need this document to use kakeratta.

The user-facing idea is deliberately small: post what you are thinking, and
tell a persona in plain words what you would like it to do.

This page describes what runs underneath to make that possible: routing,
persona execution, memory, model selection, scheduled wakes, tools, storage,
offline behavior, external agents, and the choices made by whoever runs the
server.

## The flow

A post moves through three main layers:

**mixed timeline → routing → subject-specific personas → structured memory**

When a fragment arrives, the router checks it against each active persona's
routing condition and wakes only the ones that match.

Each selected persona then decides what to do:

* read the parts of its memory that are relevant
* inspect the current thread or older fragments
* call one of the tools allowed for that persona
* write or update a note
* schedule a later follow-up
* speak in the timeline
* or stay silent

Long-term memory uses
[OKF (Open Knowledge Format)](https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf),
split first by persona and then by topic.

A persona therefore does not receive one ever-growing personal-history prompt.
It reads and writes the notes needed for the task at hand.

Everything else still remains in the timeline, including fragments that no
persona chose to remember. Keyword and embedding-based meaning search can bring
those fragments back later.

Inference is primarily provided through **Nebius Token Factory**, with different
models assigned according to task difficulty and frequency.

Personas can also use tools such as:

* Tavily web search
* place lookup
* walking, cycling, and driving routes
* remote MCP servers

The browser keeps recent state locally and supports offline posting, while
server-side check-ins and schedules can continue when the browser is closed.

The complexity stays below the surface. None of MCP, RAG, OKF, model selection,
or workflow validation needs to be understood by the person using the app.

## Design principles

**Memory is divided by subject.** The point of the split is not to hide
anything — it is to **keep the concentration**.

**The app routes, reviews and runs.** Who gets a post is decided centrally.
Whether to speak, what to remember and when to compress it are each persona's
own — but a persona says so as a workflow, and the app checks that workflow
before running it. The app does not decide what a persona says; it stops a
persona saying what did not happen, or promising what it did not arrange.

**Silence is fine.** Not everything gets a reaction. This is not an app for
having things explained to you, and being reacted to every time you just wanted
to look at something is an imposition. A post nobody answers is a normal post.

**Not speaking and not remembering are different things.** A post a persona
said nothing about still goes into its notes, if it is its subject.

**Name it with @ and it always answers.** Because the subjects are separate,
there is something to choose between.

**Asynchronous by default.** A post is stored and shown immediately; reactions
arrive afterwards.

## Routing is condition matching, not open-ended judgement

Routing happens on every post, so it needs to be both inexpensive and
predictable.

Each persona has a short **routing condition** describing the fragments it
should receive. The router's job is not to decide what that persona ought to do
with the post. It only decides whether the post matches that condition.

That separation was learned the hard way.

Early versions gave the router broader persona descriptions containing both the
subject and what the persona was expected to accomplish. Smaller models missed
personas that should have reacted, and some prompts became too open-ended.

Routing was moved to a larger model while the prompt was being debugged. That
made the problems easier to see, but usage data exposed a more important design
issue: routing runs for every fragment, while one post may already trigger
several later AI calls.

A personal AI should not make the user hesitate before saving a small thought
because every fragment is expensive.

The useful lesson was:

**If a high-end model gets a routing task right and a smaller model gets it
wrong, the routing task may be too ambiguous.**

So the problem was changed instead of simply keeping the larger model.

What the router sees became a short routing condition rather than a description
of what the persona is responsible for achieving. Concrete examples that pulled
the model toward particular cases were removed.

For models that support explanations, the output also requires an
`excluded_reasons` entry for every persona that was not selected.

This did two things at once:

1. it forced the router to explicitly consider every persona;
2. it created a reason that could be shown to the user.

That second effect became a product feature.

When the user says **Should have been called** or that a persona should not have
been called, kakeratta can use the correction to rewrite the persona's routing
condition and try the same post again before saving it.

### How routing behaves

**A post goes only where it belongs** (`internal/persona/route.go`). A place, a
time or a food named in passing is not a reason: "明日9:10に神谷町に着きたい"
goes to the travel persona and not to the wine one. Nobody is an ordinary
answer, and so is a routing call that fails. Every decision is logged with a
one-line reason per persona.

**The decision is kept.** What the router said about a post — whom it chose,
whom it passed over and why — is stored per post (`routing.json`, or a
`routing` collection on Firestore) and shown from the timeline. A persona
reached by an @ mention is left out, since that was not the router's call, and
a router that had no answer leaves no record. Disputing a row rewrites the
persona's condition as above, and saving has the router try the same post
again without delivering it (`Runner.CheckRouting`). The condition is the only
thing that changes; nothing is learned silently.

**A remark can be disputed the same way**, for the persona's tone and
instructions. After they are rewritten and saved, the persona takes the same
turn again (`Runner.PreviewReply`): the ordinary run, over a workspace wrapper
that reads from the real one and writes nowhere. No remark is appended, no note
or schedule is written and no wake is counted. What it would have said comes
back for the person to judge.

**An @ mention skips the routing.** A named persona always gets the post and
always answers. It was asked directly, so it is not told to keep it short: it
answers at the length the question needs. Routing runs as usual for everyone
else.

**A reply goes through the same routing as a post.** It is routed against the
whole thread. Whoever the router picks answers it, plus anyone @mentioned.

The current default router is **Nemotron 3 Super through Nebius Token Factory**.

Advanced settings can instead use **Jev** for routing alone. Jev is a
decision-oriented model and can be faster and cheaper for that narrow task, but
it does not provide the exclusion reasons used by the normal routing-inspection
UI.

### Measurements behind the routing design

The measurements are kept in separate experiment documents because they are
snapshots of particular prompts, personas, models, and production states rather
than specifications of the current architecture.

The first controlled comparison used 12 synthetic routing cases with expected
recipients fixed before execution. Both Lightning and Ultra matched all 12
cases. At the prices and exchange rate used for that experiment, the measured
routing call averaged about **0.0465 yen with Lightning** and **0.3385 yen with
Ultra**. Lightning was cheaper but slower in that run.

See
[Routing model comparison — 2026-09-17](routing-cost-experiment.md)
for the full method, token counts, latency, cost assumptions, and reproduction
command.

A later read-only replay used the latest 20 production posts and the six active
production persona conditions at that time. It compared Lightning, Nano, Super,
Ultra, Jev, and GPT Luna without waking personas or writing anything back to
production.

Among the Nemotron models in that run, **Super averaged 1.91 seconds and about
0.098 yen per route**, while Lightning averaged 6.55 seconds / 0.070 yen and
Ultra 2.28 seconds / 0.449 yen. The optional Jev Decisions path averaged
0.37 seconds / 0.026 yen.

That replay did **not** have a pre-established human ground-truth label for all
20 posts, so those figures should not be read as an accuracy leaderboard.

See
[Routing model comparison — 2026-09-19](routing-model-comparison-2026-09-19.md)
*(Japanese)* for the complete measurements and limitations.

The architecture described in this document remains the source of truth for the
current configuration; the experiment documents explain why that configuration
changed.

## Who answers, and on which model

**Every persona a post reaches runs at once, on Nemotron 3 Super.** Each takes
one turn. On the timeline, only the user's own posts and replies wake anyone;
a persona's remark never wakes another persona, so the timeline cannot fill
with AI-to-AI conversation. (Check-ins and schedules wake a persona on their
own clock, described below.) The remarks already under a post are part of what each persona
is given, so a later reply can pick up on them.

**A reply that asks for a better answer is answered on Nemotron 3 Ultra.** When
the user replies to a persona's remark, Nemotron 3.5 Lightning is shown that
remark and the reply and answers one question
(`internal/persona/escalate.go`): is the user explicitly asking for a
correction, a rethink, more detail or a deeper look ("違う", "本当？",
"もう一度考えて", "もっと詳しく", "深掘りして")?

* If so, the persona whose remark was replied to answers on Ultra. It is added
  to the recipients even when routing did not choose it, and it runs once. Its
  prompt drops "keep it short" and asks for a thorough answer in Markdown.
* Anyone else the reply reaches answers on Super as usual.
* Thanks, ordinary follow-up questions and new topics stay on Super, as does a
  judgement Lightning fails to give.
* A reply to the user's own post, or to a persona no longer in the room, is not
  judged at all.

The judgement runs after routing, so it adds its own time to such a reply. In
a live measurement on 2026-09-22 (15 synthetic replies,
`TestEscalateLiveLatency`), Lightning judged all 15 correctly in 0.67 seconds
on average (1.52 at most, about 0.006 yen per call); Super was also 15/15 but
took 1.30 seconds on average at about five times the cost.

Lightning is not asked whether Super would be good enough — only whether the
user said, in so many words, that they want more. That keeps the small model's
task narrow, and it makes the difference between the models visible: Super is
the everyday voice, Ultra is what the user reaches for.

**Each remark records which model answered** (`model: "super"` or `"ultra"`;
nothing for a turn that failed), and the timeline marks an Ultra answer with a
badge beside the time.

```text
post / reply
 ↓ the image is read once
 ↓ routing
 ↓ a reply to a persona's remark: Lightning → {"escalate": true|false}
every recipient runs in parallel on Super — the replied-to persona on Ultra when escalated
```

## Persona actions are workflows, not raw model output

A persona does not directly mutate application state from whatever text the
model happens to produce.

Instead, it submits a **workflow** describing the actions it wants kakeratta to
perform.

The application validates that workflow before executing it.

This matters because several operations are deceptively similar from a model's
point of view.

For example, the core behavior of a persona is often:

1. remember something;
2. possibly say something about it.

LLMs tend to collapse two similar actions into one. If a note and a remark
contain related content, a model may write the note and omit the reply, or
reply and forget the note.

kakeratta makes that distinction explicit.

A `speak` step is always present in the workflow, even when silence is intended.
An empty text means "do not speak". This forces the model to answer the question
of whether it should say something instead of simply forgetting the action.

When a remark is produced, the workflow also requires the corresponding memory
write expected by that persona.

Schedules use a different rule. A scheduled follow-up and its note often
describe the same future event, so note storage and scheduling are handled
together instead of forcing the model to repeat nearly identical steps.

### A turn: read, then a workflow

Every persona works this way, whatever tools it holds
(`internal/persona/workflow.go`). The reading and deciding can be done by the
built-in model or, for a persona whose planner is set to it, by an external
agent over MCP (see [External agents through MCP](#external-agents-through-mcp));
the check, review and execution of the workflow are the same either way.

1. **Read.** Its `index.md` is handed over whole at the start of every turn.
   It may then ask, up to ten times (twenty in an Ultra turn for a deeper
   answer), to look at things: its note list, notes, a timeline search, a web
   search, a place or route lookup. Reading writes
   nothing, so the app just does it and hands the results back.
2. **Workflow.** It hands in what it has decided, as steps in order, with every
   text written out in full:

   | Step | Does |
   |---|---|
   | `write_note` / `forget_note` | Write or discard one note |
   | `write_schedule` | Place a wake **and** write the note it rests on, as one step. If the note cannot be written, the wake is taken back |
   | `cancel_schedule` | Cancel a wake by an id from the list the persona was given |
   | `tool` | Call a declared tool that acts rather than looks (not `schedule`) |
   | `speak` | Say something. **Exactly one, and last.** Silence is a speak with an empty text. A remark answering a post or a reply — an Ultra answer included — also needs a `write_note` (a remark alone is sent back; a note alone is fine). Check-ins, schedules and deletions are exempt |

3. **Checked by the app.** Mechanically first: known step types, declared
   tools, no invented schedule ids, a non-empty speak when named, one speak at
   the end, and no second note under a title that already exists — one thing,
   one note. Then Nemotron 3 Super reviews it against a narrow list: something
   written as fact that did not happen, a fact asserted that nothing read
   supports, a time promised with no schedule, a remark in another language
   than the post, a note rewritten without being read, a persona's own
   instructions plainly broken, a remark that describes work instead of
   answering. A rejected workflow goes back once with the reasons.

   **What is reviewed depends on the kinds of step, not how many there are.**
   A workflow holding any step other than `write_note` and `speak` —
   schedules, forgetting, tools — is always reviewed. One that only writes
   notes and speaks is not, except from a persona that holds `schedule`: a
   time promised with nothing set to wake appears in exactly that shape. A
   workflow that does nothing is not reviewed.

   A bad conversational reply is annoying; a bad schedule is worse, because it
   comes back later as if it were something the user asked to be reminded of.
   The review sees the thread, the persona's instructions, and what the
   persona read this turn — nothing else from its notes.
4. **Run.** In order; **the first step that fails stops the rest**, so a remark
   never goes out after what it rests on did not happen.

What a persona is told about workflows is assembled for the tools it holds
(`internal/persona/workflow_spec.go`): every step with its fields, a sample
that is right, and a mistake a live run actually made. A persona without
`schedule` is not taught `write_schedule`; one with web search is shown a
search it can copy.

Each turn is logged as one structured entry: what was read, every draft with
its problems and review, each step as it ran, and what was said.

## Models

kakeratta uses multiple models through **Nebius Token Factory** rather than
sending all work to one large model.

| Model                      | Role                                                                  |
| -------------------------- | --------------------------------------------------------------------- |
| **Nemotron 3 Super**       | Every persona's answers, routing, and workflow review                 |
| **Nemotron 3 Ultra**       | The replied-to persona, when a reply asks for a better or deeper answer; persona drafting |
| **Nemotron 3.5 Lightning** | Judging whether a reply asks for a better answer; dictation polishing |
| **GLM-5.3-Flash**          | Reading screenshots and photos, once per post                         |
| **Qwen3-Embedding-8B**     | Meaning search over past fragments                                    |

The settings keep three slots — `lowModel` (Lightning), `middleModel` (Super)
and `highModel` (Ultra) — and any OpenAI-compatible model name can go in each.

Persona execution, routing, and workflow review use native tool calling where
appropriate.

Image reading, persona drafting, dictation cleanup, structured generation, and
embeddings use the provider's structured-output or embedding interfaces rather
than pretending every operation is the same kind of chat request.

One of the advantages of Token Factory during development was that the model
roles could be changed without rebuilding the surrounding provider integration.

Usage data also made the cost of those choices visible while the routing design
was being changed.

Advanced settings can:

* move routing alone to Jev's Decisions API;
* move the high, middle, and low slots to OpenRouter-selected models.

OpenRouter tool calls use Exacto routing.

## Image understanding happens once per post

A photo or screenshot is sent to **GLM-5.3-Flash once when the post is
processed**.

The resulting textual interpretation is attached to the fragment and shared
with the relevant personas (`internal/capture`): what is in it, the text on
it, where it seems to come from, and how it looks — colour, shape, count,
layout, the things a persona might otherwise want to ask about.

The picture itself is never handed to a persona. None of the persona models
take image input, and a live run lost a wine label's remark to a picture sent
to one. A persona that asks for the original is told to use the reading it
already has.

The image is not independently sent through vision once for every persona.

This is deliberate.

If three personas receive the same photo, repeated vision calls would multiply
both latency and cost while often extracting the same basic facts. A single
interpretation also gives the personas a consistent description of what was
seen.

There is a trade-off.

Different personas can care about different visual details. A food persona may
want the ingredients, while a travel persona may care about the restaurant sign
in the background.

If the initial image interpretation did not extract a detail, a later persona
cannot recover it simply by asking a different question: the original vision
model is not run again from that persona's point of view.

For kakeratta, the current choice is:

**one predictable image-understanding step per post → shared textual context for
all relevant personas**

rather than:

**one vision request per persona → potentially richer persona-specific details
at multiplied cost and latency**.

That limit is intentional and documented rather than hidden as if every persona
had independently seen the original pixels.

## Memory and search

Each persona owns its own memory.

A food persona cannot silently rewrite the travel persona's notes, and an
external agent cannot write notes at all except through a persona's reviewed
workflow.

Notes are stored as structured Markdown under OKF-style topics rather than as
one conversation transcript.

The user can inspect those notes directly from the interface.

Each note can also retain provenance back to the fragments and conversations
that changed it. That is why the UI can show **Posts that updated this memory**
instead of presenting long-term memory as an unexplained model state.

Individual notes can be edited or forgotten without deleting the source post.

Deleting a source post is different.

When a post is deleted, kakeratta uses a reverse index to find notes that cite
it and removes the app-managed source links. Notes left with no source are
deleted automatically, except the index and update log. Personas that were
woken for the post or cited it are then asked to clean up surviving notes and
references to deleted notes. Each persona decides how to revise the meaning of
the surviving text; the app does not rewrite that text.

### What a persona is given

* the post it was routed, and what the app read out of its image;
* **the remarks under that post** — other personas' and the user's replies;
* its own `index.md` and a note count (other notes are read as needed);
* its own pending schedules, if it can schedule.

**Not the whole timeline.** Hand that over and every persona sees everything
again, which is exactly what dividing by subject was for. Anything older is in
its own notes, or found with a timeline search.

### Notes are OKF, and the persona writes them

The [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf)
skill (`internal/persona/skills/okf/SKILL.md`) is handed to each persona, and
**it reads and writes its own notes** as a bundle in that format: one concept
per file, YAML frontmatter and a Markdown body.

```text
=== index.md ===
# Tendencies
* [Leans tonkotsu](/tastes/tonkotsu.md) - Picks tonkotsu when there is a choice.

=== tastes/tonkotsu.md ===
---
type: Preference
title: Leans tonkotsu
description: Picks tonkotsu when there is a choice.
tags: [ramen, tonkotsu]
---
# Evidence
* 09-08 chose tonkotsu over shoyu when both were there
```

Notes are read in the read phase (`notes_list`, which can narrow by tag, and
`notes_read`) and written by workflow steps (`write_note`, `forget_note`). A
write replaces **only that file**, so a grown memory never travels in full on
every wake, and nothing is lost by being left out of a rewrite. These need no
declaration: a persona's own notes are not outside.

A wake hands over `index.md` and how many notes there are, never a listing of
every note. The index is the usual way in; `notes_list` finds notes beyond it,
100 at a time, newest first, and can narrow by tag. The duplicate check reads
notes in the same directory as the proposed note. The memory reader on the
page loads 50 notes at a time.

Which `type` values to use and when to merge them differ by subject, so they go
in each skill's `memory` field — `Preference` and `Shop` for the food persona,
`Book` and `Theme` for the reading one. Only the types named there may be used.
**The format is handed over; the policy belongs to the skill.** The app holds
no threshold for when to compress.

**The app does not write the meaning of the notes or build `index.md`.** It
parses `type` and `tags` to validate writes, detect duplicates, and filter the
reader; it also maintains source links to posts. The Markdown body remains
the persona's to write. In local storage the notes are plain files that can
be read with `cat` or used as an OKF knowledge source elsewhere.

**Memory comes from the user; what the persona made up has a deadline.** A
persona that speaks must leave a note behind, and when the remark was a list
of lunch candidates it found by searching, that note is the persona's own
output, not something known about the user. The note-writing skill draws the
line: a candidate list, search result, or route the persona produced gets an
`expires` line in its front matter, dated the next day; what the user said
does not. The app deletes a
note once that date has passed - the one time it removes a note on its own
schedule - and wakes the persona to take it out of `index.md`, told plainly
that this was its own arrangement rather than the user's deletion. A note
without `expires` is never aged out by the app. The user's reaction is what
promotes a candidate into memory: when they reply, choose, or go, the persona
rewrites the note without the line. A day is long enough for that reply to
arrive; anything the persona invented and nobody answered is gone by then,
so a wrong guess about what to keep heals itself instead of accumulating.

**A note in use keeps longer.** When a persona reads a note with `notes_read`
while answering a post, the app moves both its `expires` and `review` dates:
the span the persona gave it, from when it was written to the date, starts
again from the read. A next-day candidate read again today lasts until
tomorrow; a shop given a few weeks gets a few weeks from the read. A date
still ahead moves too when the restarted span ends later; one that would end
sooner is left alone, so reading never shortens a note's life. A note the
turn itself rewrote or forgot is left as the persona left it. Reading at a
check-in, a schedule or any other wake that is not a post does not count, so a
note read in order to ask about it is not put off again and again.

**What the user said can carry a `review` date the persona chooses, and the
app never deletes it.** A shop they wanted to try gets a few weeks, a topic
they were into a few months, a preference none; the persona decides from the
content. When the day has passed, the persona's next daily check-in is
handed at most three due notes, oldest first. The persona asks about one of them in a short post
and moves that note's `review` a week ahead so it does not ask again
tomorrow. A reply under that post always reaches its author, which then
drops the line to keep the note for good, moves the date to extend it, or
forgets it. Expiry is the last moment a thing is brought back to the user's
attention, not merely when it is discarded.

### Meaning search

A fragment does not need to have been intentionally remembered by a persona to
remain useful.

Timeline text, persona conversations, and text extracted from images can be
searched later.

Meaning search uses **Qwen3-Embedding-8B** embeddings. A query such as "a light
red that was easy to drink" can therefore find a past post without requiring
the user to remember the exact word "Pinot".

This is deliberately separate from long-term persona memory:

* memory is what a persona chose to retain;
* search can recover what remained only in the timeline.

## Skills

A persona is a `skill.json`:

| Field | |
| --- | --- |
| `name`, `icon`, `tone` | How it shows up and how it talks |
| `domain` | The routing condition: what it reacts to and remembers. **The router reads this** |
| `memory` | Its note kinds (2–4 types such as `Wine`, `Preference`), what goes in each and when they merge. A note of any other type is sent back |
| `instructions` | How to keep notes, when to speak. **Only the persona reads these** |
| `tools` | Only what is declared can be called |

Four ship with the app — tabe (food), michi (travel), shiori (reading), plana
(routines). More can be
added from the UI, written by hand or drafted with the persona builder
(Nemotron 3 Ultra).

```json
{
  "id": "wine",
  "name": "ぶどう",
  "icon": "🍷",
  "domain": "ワイン。銘柄、産地、ラベルの写真、飲んだ記録。",
  "tone": "控えめ。おすすめを押しつけない。",
  "memory": "型は Wine（銘柄）と Preference（好み）の2つ。同じ好みが3回出たら Preference に統合する。",
  "instructions": "引用と本人の意見を分け、推測は推測と書く。",
  "tools": ["web_search", "place_search"]
}
```

**There is no ceiling on how many.** Because routing is central, only the few
that matter wake for a given post, and routing runs once per post. **Without
separate subjects, @ means nothing** — if there is nothing to choose between,
naming someone is pointless.

## Tools belong to the persona and the operator

A persona does not automatically receive every tool available to the server.

Its skill declares the tool names it may use.

The server operator determines where those tool names actually point.

Built-in integrations include:

* `web_search`
* `place_search`
* `route`

* `schedule` — wake times a persona asks the app for; nothing leaves the app
  (`schedules.json` / Firestore)

Every persona also gets `timeline_search` — keyword and meaning search over the
user's own timeline — without declaring it.

Remote MCP servers can extend the tool list.

The application performs the actual calls and holds the relevant credentials.

### Web search

`web_search` uses **Tavily**.

The Tavily API key is supplied by the user in Settings rather than embedded in
the application.

A persona receives web search only when its configured skill declares that
tool.

### Place search

`place_search` uses OpenStreetMap services.

When a post carries a location, nearby-place search uses Overpass through
`KAKERATTA_OVERPASS_URL`.

When no current position is available and the persona searches by place name,
Nominatim is used through `KAKERATTA_NOMINATIM_URL`.

Opening hours are returned only when OpenStreetMap contains them. They are not a
live guarantee that a business is open now.

Temporary Overpass failure is retried once and then reported as unavailable
rather than being converted into a false "no places found" result.

### Routes

`route` requires a position attached to the post.

Driving, walking, and cycling use separate OSRM-compatible endpoints:

* `KAKERATTA_OSRM_CAR_URL`
* `KAKERATTA_OSRM_FOOT_URL`
* `KAKERATTA_OSRM_BIKE_URL`

The defaults use public OpenStreetMap-based routing services.

These are road-network estimates. They do not contain live traffic or public
transport timetables.

Changing only an OSRM URL's profile token is not sufficient to change travel
mode; a custom routing server must be built with the corresponding routing
data.

### MCP tools

`KAKERATTA_MCP_SERVERS` is a JSON array of remote Streamable HTTP MCP servers.

For example:

```json
[
  {
    "name": "geo",
    "url": "https://geo.example.com/mcp",
    "headers": {
      "Authorization": "Bearer …"
    }
  }
]
```

Their tools appear alongside built-in tools using a qualified name such as:

```text
geo.compute_commute
```

A persona's skill can then declare that exact tool.

Tool discovery is cached for a few minutes. If a remote server temporarily
stops answering, its last known tool list can remain available, while an actual
tool call failure is surfaced to the persona instead of silently disappearing.

When an MCP server provides `route` or `place_search`-equivalent functionality
that accepts `lat` and `lng`, kakeratta can route those operations through that
server and fill in the post's position automatically.

If the remote server itself cannot be reached, the built-in OpenStreetMap
implementation can be used as fallback.

An explicit error returned by the remote server is not hidden as a fallback
success.

An MCP tool's text result goes back as the answer to that call, and its
resource links become sources under the remark. A timeline search is the
exception: what it finds is offered for citing, the persona names in its
`speak` the posts its remark rests on, and only those are linked.

## Check-ins and scheduled wakes

Every active persona has a **Daily check-in** once a day. It cannot be turned
off, and only how much it says can be chosen.

When it runs is the app's choice: a minute between 07:00 and 21:00 in the
user's time zone (the one the page last saved) that drifts by at most an hour
from one day to the next. The window is a ring - after 21:00 comes 07:00. Every 7
days a fixed point is chosen by a hash of the workspace, the persona and the
point, and the days between move the shorter way round from one point to the
next in equal steps; two points are at most half the ring (7 hours) apart, so
that is at most 60 minutes a day. Walking straight between points would crowd
the minutes toward midday; the ring uses the edges of the window as much as the
middle. A day on which the minute moves forward past 21:00 and wraps to
the morning (20:50 yesterday, 07:10 today) has no check-in, so that two do not
come about ten hours apart; the day after a skip never wraps again, so no two
days in a row are skipped. The minute is computed, not remembered: the same day always gives the same
minute, so a restart does not move it, and check-ins spread across the day
rather than every persona of every user waking at once. A check-in not run by 21:00 - the
server was down - is skipped rather than run at night.

With **At check-in, speak only when needed** on (it is off by default), the persona may speak only
about what needs the user's decision; today that is a note that has reached its
review date (whether it can be forgotten). It does not bring discoveries,
suggestions, news or greetings. It may tidy its notes quietly. On a day with
nothing to ask it still wakes, and ends silent.

With it off (the default), the persona also reads the timeline since the
previous check-in actually ran - recorded with the day's claim - and older notes
sharing tags with recent ones, and speaks when it finds a connection worth
bringing back. "The last 24 hours" would leave stretches unread when the minute
moves later, or a day is skipped or missed; reading from the previous run makes
the spans meet end to end. With no previous run on record it reads a day, and
it reads a week at most.

Silence produces no empty system post and no empty conversation thread.

A persisted claim limits a persona to one check-in attempt per local calendar
day, including silent or failed attempts.

Firestore claims are transactional across server instances.

Local file storage assumes one server process for a workspace.

Forgetting memory does not reset an already-used daily claim.

After downtime, only today's due check-in can run. Missed previous days are not
replayed.

A crash after claiming a check-in can therefore skip that day's run rather
than risk duplicating it.

### Reviews ride on the same claim

A note whose `review` date has passed is appended to the check-in trigger, at
most three at a time, oldest first. The persona asks about one of them and
posts once.

A reply under a post a persona started on its own - a check-in, a schedule,
a review - always reaches that persona, whatever the router decides. The
persona is told which notes it had asked about and which others are due.

### Schedules created by personas

A persona that has the `schedule` tool can ask kakeratta to wake it later.

Current limits are:

* up to 20 pending schedules per persona;
* up to 30 days ahead;
* one-time or daily recurrence.

Schedules are attached to the note that caused them to exist.

For example, plana may write a note about buying flowers and schedule a later
wake to ask whether they were bought.

The design favors a check shortly *after* the relevant moment rather than a
traditional calendar alarm beforehand.

A schedule is claimed before the model executes so a failed or silent wake is
not run repeatedly.

A wake more than 10 minutes late is dropped rather than replayed at an
unexpected time.

Deleting the note gives the persona a chance to revisit the schedules that
depended on it.

Forgetting all of a persona's memory, pausing it, or removing it drops its
pending schedules.

### The browser does not need to stay open

Check-ins and schedules are server-side.

The browser can be closed.

The server itself must still be running.

The provided Cloud Run deployment keeps one instance available and allocates
CPU outside request handling so the server can perform these periodic checks.

Configured workspaces are restored after a restart, including Firestore-backed
users.

The scheduler checks once per minute and uses the same execution queue as
ordinary persona work, so a busy queue can delay a wake.

## Offline posting

A passing thought should not depend on having a network connection at that
moment.

The browser therefore keeps recent conversations locally for fast startup and
registers a **Service Worker** after the application has been used online.

Text, images, URLs, and replies created while offline are stored as pending
operations in **IndexedDB**.

They appear locally as pending rather than being discarded.

When connectivity returns, they are sent to the server and enter the same
routing and persona-execution path as ordinary posts.

Offline support therefore changes when a post reaches the server, not what the
post means once it arrives.

The browser cache is not the authoritative long-term data store.

## Storage, privacy, and account deletion

kakeratta can run as a local single-user application or as a hosted multi-user
service.

### Local mode

Local runs:

* require no login;
* listen on loopback by default;
* use filesystem storage by default.

`KAKERATTA_STORE=file` stores user state under `KAKERATTA_HOME`.

This includes posts, settings, persona definitions, and persona notes.

Images are stored locally unless a Cloud Storage bucket is configured.

### Hosted mode

Public deployments can enable Firebase Authentication by setting:

* `KAKERATTA_FIREBASE_PROJECT_ID`
* `KAKERATTA_FIREBASE_API_KEY`
* `KAKERATTA_FIREBASE_AUTH_DOMAIN`

Authenticated user data is isolated under a user-specific hashed namespace.

With:

```text
KAKERATTA_STORE=firestore
```

posts, settings, persona definitions, and persona notes are stored in Firestore
under:

```text
users/<uid-hash>/
```

With `KAKERATTA_GCS_BUCKET` configured, images are stored privately under:

```text
users/<uid-hash>/blobs/
```

### Backup and portability

Settings → **Backup** creates a zip containing the user's:

* posts
* personas
* notes
* images

The layout is deliberately usable by a self-hosted kakeratta instance.

Provider API keys and external-service connections are excluded.

### Account deletion

The hosted application provides a complete account-deletion path.

The user is directed to Backup first if they want to keep a copy.

Deleting the account removes the user's:

* posts and conversations;
* persona definitions and persona memories;
* uploaded images;
* settings;
* stored credentials;
* Bluesky and other external connection state;
* MCP credentials and connection state;
* server-side backup data;
* kakeratta login account itself.

The goal is not to leave a personal-memory archive behind after the user has
left the service.

## Running it

kakeratta is a browser application in two pieces:

1. a static web application;
2. a Go API server.

During development, Vite serves the page.

The deployed reference setup uses Firebase Hosting for the static site and
Cloud Run for the Go API.

The Go binary itself is only the API and can also run on a user's own machine.

### Requirements

* Go 1.26 or later
* Node.js
* Task (`task`)
* Nebius Token Factory API key

With [mise](https://mise.jdx.dev):

```sh
mise install
```

uses `mise.toml` to install Go and Task.

Otherwise install Task manually:

```sh
go install github.com/go-task/task/v3/cmd/task@latest
```

Then from the repository root:

```sh
task setup  # once: install the page's development dependencies
task dev    # API on :8787 and Vite page on :5173
```

Open:

```text
http://127.0.0.1:5173
```

A new workspace starts with no active personas and leads the user through
adding one.

<img src="captures/en/nobody.png" alt="Nobody in the room yet: call in a built-in persona with one tap" width="390">

Enter a Nebius Token Factory API key in Settings.

Lightning, Super, and Ultra are configured for the low, middle, and high model
slots by default.

Other development commands include:

```sh
task run      # API only
task build    # build bin/kakeratta
task stories  # Storybook
task test     # tests
```

To refresh README screenshots in both languages, run `task captures` after a
visible change. This starts Storybook and saves the images under
`docs/captures/`. Run `python3 frontend/capture-sizes.py` if their display
sizes have changed, then `task frontend` to rebuild `frontend/dist`.

To check offline startup, posting, and delivery after reconnecting in a real
browser, run `npm --prefix frontend run test:offline` after `task frontend`.
This check also refreshes the offline screenshots. It requires Node.js 22 or
later and Chromium (`CHROMIUM` can select its executable). Rebuild the frontend
afterward to include the refreshed images.

### Environment variables

| Variable                         | Default                                                         | Purpose                                                                 |
| -------------------------------- | --------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `KAKERATTA_PUBLIC_URL`           | empty                                                           | Public HTTPS origin used by features such as Bluesky OAuth              |
| `KAKERATTA_BLUESKY_KEY`          | empty                                                           | Secret used for Bluesky signing and encryption                          |
| `KAKERATTA_ADDR`                 | `127.0.0.1:8787` (`:8787` in the reference Firebase deployment) | API listen address                                                      |
| `KAKERATTA_HOME`                 | OS config directory                                             | Base directory for local state                                          |
| `KAKERATTA_FIREBASE_PROJECT_ID`  | empty                                                           | Enables multi-user Firebase Authentication                              |
| `KAKERATTA_FIREBASE_API_KEY`     | empty                                                           | Firebase Web API key                                                    |
| `KAKERATTA_FIREBASE_AUTH_DOMAIN` | empty                                                           | Firebase Auth domain                                                    |
| `KAKERATTA_GCS_BUCKET`           | empty                                                           | Private Cloud Storage bucket for images                                 |
| `KAKERATTA_STORE`                | `file`                                                          | Storage backend; use `firestore` for the reference Cloud Run deployment |
| `KAKERATTA_FIRESTORE_PROJECT_ID` | Firebase project                                                | Firestore project                                                       |
| `KAKERATTA_MCP_SERVERS`          | empty                                                           | Remote MCP servers whose tools may be assigned to personas              |
| `KAKERATTA_NOMINATIM_URL`        | public service                                                  | Nominatim endpoint                                                      |
| `KAKERATTA_OVERPASS_URL`         | public service                                                  | Overpass endpoint                                                       |
| `KAKERATTA_OSRM_CAR_URL`         | public service                                                  | Driving route endpoint                                                  |
| `KAKERATTA_OSRM_FOOT_URL`        | public service                                                  | Walking route endpoint                                                  |
| `KAKERATTA_OSRM_BIKE_URL`        | public service                                                  | Cycling route endpoint                                                  |

For Bluesky operator setup, see
[social-sharing.md](social-sharing.md).

## External agents through MCP

Of a persona's turn, only the part that thinks — reading the context, deciding
what to do next, and writing the workflow — can be handed to an external agent
such as **Hermes Agent**. Each persona chooses its planner: the built-in model
(the default) or the external agent. kakeratta exposes the user's workspace as
an authenticated **Streamable HTTP MCP server** for this.

When a persona planned externally wakes — for a post, a check-in, a schedule, a
note review — kakeratta builds exactly the context the built-in model would get
and opens an **ask**. The agent claims it, reads the context, may look things up
with the persona's own notes and tools, and submits a workflow. kakeratta checks
and reviews it like any other and, once accepted, runs it as the persona. The
agent never writes a note or a remark itself.

```text
persona wakes
→ ask opened (pending, claimBy)
→ agent: claim_ask → context
→ agent: read_for_ask (optional) → submit_workflow
→ kakeratta: check, review → accepted
→ kakeratta runs the workflow as the persona
```

Waiting on an agent must not stall everything else, so an externally planned
turn runs apart from the reaction queue and reports to the routing sheet when it
has really ended.

### Fall-through

The built-in model takes the turn back when:

* no agent claims the ask within the claim wait (3 minutes by default);
* no workflow is accepted within the plan wait after the claim (15 minutes by
  default);
* the agent runs out of drafts, or the review fails;
* the agent calls `release_ask`;
* the agent has made no MCP call at all for the claim wait (away). No ask is
  opened then, and the agent comes back by itself on its next call.

Both waits are set under Settings → Advanced options → Integrations. An ask
lives in memory with the turn waiting on it; a restart ends both, and a late
workflow is refused. A preview a person is waiting on is always planned
in-process.

### MCP credential

The user issues an MCP key from Settings.

The raw key is shown only at creation time.

The server stores a hash rather than the original credential.

Reissuing the key invalidates the previous one.

Disconnecting MCP removes the stored hash and makes the credential unusable.

The key can access only the workspace for which it was issued.

It can read:

* the timeline;
* persona definitions;
* persona memories.

Its only write is a workflow submitted for a claimed ask, which kakeratta checks
and reviews before running it as that persona.

Revoking the key cannot erase data the agent already retrieved into its own
external memory. That boundary is made explicit rather than pretending an
external agent can be remotely un-told what it already received.

### Exposed MCP tools

```text
list_personas
get_timeline
get_thread
list_notes
read_note
list_asks
claim_ask
read_for_ask
submit_workflow
release_ask
```

`claim_ask` takes an `askId` and a caller-generated `claimId`. Repeated calls
with the same `claimId` are retries of the same claim; a different claim ID
cannot take a claimed ask.

The context a claim returns holds the persona's identity, instructions and
note-writing skill, why it woke, the trigger and its thread, the persona's
`index.md`, its schedules, the workflow spec and JSON Schema, its tools and
where each may be used, the turn's limits, and the built-in model's own system
and user messages.

`list_tasks` remains as a compatibility tool returning only pending asks, for
Wake Gate scripts installed before asks replaced tasks.

Detailed semantics are in
[Hermes Agent setup](hermes.md)
([日本語](hermes_ja.md)).

## NemoClaw and OpenShell

For the hackathon integration, Hermes was also tested inside an
**NVIDIA NemoClaw / OpenShell** environment.

The tested stack was:

```text
NVIDIA NemoClaw
    ↓
OpenShell sandbox
    ↓
Hermes Agent v0.20.6
    ↓
Nebius Token Factory
    ↓
kakeratta production MCP server
```

NemoClaw provides the higher-level setup, orchestration, lifecycle, and policy
configuration for the agent runtime.

OpenShell provides the sandbox and policy-enforcement boundary.

The tested `hermes-sandbox` used filesystem and network restrictions including
Landlock-based filesystem isolation and controlled outbound network access.

Inference credentials were kept outside Hermes's own configuration. Requests
left the sandbox through the OpenShell gateway and were proxied to Nebius Token
Factory.

Hermes-4-405B and Nemotron 3.5 Lightning were tested during development. The
final operational configuration used Nemotron 3 Super 120B after Lightning
produced unreliable tool calls in multistep tasks.

The sandboxed Hermes instance successfully connected to:

```text
https://kakeratta.net/api/mcp/...
```

over Streamable HTTP MCP and discovered all 11 production MCP tools of the
earlier, task-based integration in which Hermes was a persona of its own; the
planning tools above have since replaced them.

This was an integration verification of the execution path and tool boundary.
It does not mean kakeratta itself depends on NemoClaw.

A Hermes installation can connect directly to the same MCP interface, while
NemoClaw/OpenShell provides the tested sandboxed configuration used for the
hackathon work.

## Why the external agent only plans

A normal kakeratta persona is intentionally constrained: it acts through a
generated workflow that kakeratta validates before execution.

Handing the planning to an external agent keeps that constraint. The agent may
think with any tools and skills it has, but what reaches the user's timeline and
memory is still a workflow the app has checked and reviewed and runs itself.
"External agent" does not become an invisible bypass of kakeratta's ownership
model, and a persona whose agent is asleep still answers, only later.

## Design trade-offs

Several choices in kakeratta deliberately favor understandable behavior over
maximum automation.

### Silence is a valid result

A persona can write memory without speaking.

A check-in can finish without creating a thread.

An external agent planning a turn can end its workflow with an empty `speak`.

The system is not optimized for producing a visible AI response to every event.

### Memory is inspectable

Long-term memory is represented as notes the user can read and correct.

It is not only a hidden vector store or conversation summary.

Embeddings help retrieve fragments, but they are not treated as the source of
truth for what the system "remembers".

A note's lifetime is visible too: the reader shows the day the app will
delete a note the persona made up, and the day the persona will ask whether
to keep a note about something the user said.

### Stronger models are not used everywhere

The most capable model is not automatically the best model for a frequent,
narrow task.

The routing redesign is the clearest example.

Making the task simpler reduced both model requirements and ambiguity.

The measured routing experiments are kept alongside the repository rather than
embedded as timeless claims in this architecture document, because prices,
prompts, active personas, and model behavior can all change.

### Vision is shared, not repeated per persona

The original image is interpreted once and the resulting text is shared.

That keeps vision cost and latency bounded as the number of personas grows, but
it also means a persona cannot later ask the vision model to recover a detail
that was omitted from the original interpretation.

That is an explicit trade-off rather than an assumption that all personas need
the same visual information.

### External autonomy gets a harder boundary

An external agent only plans. What it plans still goes through the same
workflow validation and review as the built-in model's, and kakeratta alone
executes it. Authentication, the MCP tool surface, and sandbox policy bound what
the agent can read and do on its own side.

## Read more

| Document                                                                                     | Contents                                                                                                                          |
| -------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| [README](../README.md)                                                                       | What kakeratta is, what the user can do, privacy, always-on behavior, Nemotron/Token Factory, and the tested NemoClaw integration |
| [Routing model comparison — 2026-09-17](routing-cost-experiment.md)                          | Controlled Lightning vs Ultra routing experiment with expected recipients, token counts, cost, and latency                        |
| [Routing model comparison — 2026-09-19](routing-model-comparison-2026-09-19.md) *(Japanese)* | Read-only replay of 20 production posts across Nemotron models, Jev, and GPT Luna                                                 |
| [Usage and configuration](usage.md)                                                          | Android and iOS sharing, location, speech, model settings, and stored data                                                        |
| [What was hard about the AI](ai-lessons.md)                                                  | Failures seen with Nemotron personas and what changed to make them dependable                                                     |
| [Hermes Agent](hermes.md) ([日本語](hermes_ja.md))                                              | MCP authentication, the ask lifecycle, the planning context, and fall-through                                                     |
| [Google Cloud deployment](../infra/README.md)                                                | Terraform deployment with Firebase Hosting, Cloud Run, Firestore, and related infrastructure                                      |

[MIT licensed](../LICENSE).
