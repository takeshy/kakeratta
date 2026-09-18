# Design and personas

[README](../README.md) · [日本語](architecture_ja.md)

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

**Not speaking and not remembering are different things.** A post it said
nothing about still goes into its notes, if it is its subject.

**Name it with @ and it always answers.** Because the subjects are separate,
there is something to choose between.

**Asynchronous by default.** Reactions arrive late.

## What the app does

Four things, and the app's own judgement in them is made by **the best model
configured** (the high tier; Nemotron Ultra on Nebius).

1. Take the post
2. **Route it to the personas it belongs to**
3. **Review each persona's workflow, and run it**
4. Put remarks on the timeline

Whether to summarise and whether to speak are not the app's decisions.

**Routing delivers only where a post belongs** (`internal/persona/route.go`).
A place, a time or a food named in passing is not a reason: "明日9:10に神谷町に
着きたい" goes to the travel persona and not to the wine one. Nobody is an
ordinary answer, and so is a routing call that fails. Every decision is logged
with a one-line reason per persona.

**The decision is kept, and can be corrected.** What the router said about a
post - whom it chose, whom it passed over and why - is stored per post
(`routing.json`, or a `routing` collection on Firestore) and shown from the
timeline. A persona reached by an @ mention is left out, since that was not
the router's call, and a router that had no answer leaves no record. From the
timeline a person can dispute a row: the persona builder rewrites the
persona's condition from their reason, they edit the text, and saving has the
router try the same post again without delivering it (`Runner.CheckRouting`).
The condition is the only thing that changes; nothing is learned silently.

**A remark can be disputed the same way**, for the persona's tone and
instructions. After they are rewritten and saved, the persona takes the same
turn again (`Runner.PreviewReply`): the ordinary `run`, over a workspace
wrapper that reads from the real one and writes nowhere. The persona reads its
notes and may look things up, but no remark is appended, no note or schedule
is written and no wake is counted. What it would have said comes back for the
person to judge.

### A turn: read, then a workflow

A persona does not call tools one at a time any more (`internal/persona/workflow.go`).

1. **Read.** Its `index.md` is handed over whole at the start of every turn.
   It may then ask, up to ten times, to look at things: its note list,
   notes, a search, the original image. Reading writes nothing, so the app just
   does it and hands the results back.
2. **Workflow.** It hands in what it has decided, as steps in order, with every
   text written out in full:

   | Step | Does |
   |---|---|
   | `write_note` / `forget_note` | Write or discard one note |
   | `write_schedule` | Place a wake **and** write the note it rests on, as one step. If the note cannot be written, the wake is taken back |
   | `cancel_schedule` | Cancel a wake by an id from the list the persona was given |
   | `tool` | Call a declared tool (not `schedule`) |
   | `speak` | Say something. At most one, and only as the last step |

3. **Checked by the app.** Mechanically first — known step types, declared
   tools, no invented schedule ids, no remark on a turn that cannot speak, an
   answer when named. Then the app's model reviews it against a narrow list:
   something written as fact that did not happen, an invented id, a time
   promised with no schedule, a remark in another language than the post, a
   note rewritten without being read, a persona's own instructions plainly
   broken. A rejected workflow goes back once with the reasons. The review uses
   Nemotron Super. A workflow that only writes notes and speaks,
   or does nothing, is not reviewed; schedules, forgetting and tools are.
4. **Run.** In order; **the first step that fails stops the rest**, so a remark
   never goes out after what it rests on did not happen.

What a persona is told about workflows is assembled for the tools it holds
(`internal/persona/workflow_spec.go`): every step with its fields, a sample
that is right, and a mistake a live run actually made. A persona without
schedule is not taught write_schedule; one with web search is shown a search
call it can copy.

Each turn is logged as one structured entry: what was read, every draft with
its problems and review, each step as it ran, and what was said.

The app never reads a note. It carries the text a persona wrote to where it is
stored, and shows the reviewer only what the persona read this turn.

**An @ mention skips the routing.** A named persona always gets the post and
always answers. Routing runs as usual for everyone else.

### Order

Each persona is set to **low / middle / high**. They are **not run in parallel:
low goes first and the order runs upwards.**

**The tier also decides how a persona works.** A persona that uses any tool
other than web search (routes, places, schedules) is middle or high, and works
through a reviewed workflow. A low persona holds no such tools: it reads its
notes, may search the web, writes its notes and speaks, calling these directly
with no workflow and no review (`internal/persona/light.go`). Web search counts
as reading on both paths — on the workflow path it is used before the workflow,
never inside one.

The exchange only works because what came earlier is in the later input. Run
together, the larger models cannot see what the smaller one said, so they have
nothing to pick up on.

```
post
 ↓ the image is read once (OCR, what is in it, where it seems to come from)
 ↓ routing
low     runs ──→ anything it said is added to the context
 ↓
middle  runs (with low's remark in the input)
 ↓
high    runs (with low's and middle's remarks in the input)
 ↓
the tiers that already ran are handed what came after them
(**they cannot speak**; notes only)
```

**What came later is handed to the tiers that went earlier.** Not speaking and
not remembering are different things, so it is passed once, to be remembered.
But **they cannot speak on that turn** — otherwise they would keep answering
each other unprompted and the timeline would fill with a conversation that is
not yours. When the higher tiers all stayed quiet there is nothing to pass on,
so that extra turn does not happen at all (staying quiet is the default, so
this is the normal case).

Nothing waits on a lower tier that said nothing. Personas in the same tier run
together; the order only matters between tiers. The higher tiers answer later
still, which is acceptable because asynchronous is the default.

### Reading the image

When a post arrives the app reads the image once (`internal/capture`). Handing
the picture to every persona would mean paying for vision once per persona for
the same screenshot, so it is turned into text and shared. A persona that
decides it needs the detail calls `image` and gets the original.

## Personas

### What each one is given

- The post it was routed, and what the app read out of it
- **The remarks under that post** (other personas' and your own replies)
- The list of its own notes (contents are read through tools as needed)

**Not the whole timeline.** Hand that over and every persona sees everything
again, which is exactly what dividing by subject was for. The routing sits in
the middle to avoid it.

**The past exists only in its own notes.** There is no path back to fetch it. If
something is not there, that persona decided it was not worth keeping.

The remarks under the same post are shared so that the exchange, and your
replies, work at all.

### Notes

**Each persona grows its own.** What to write, what to keep and throw away, when
to compress — all of it is left to what the skill says. The food one may care
about tendencies; another may care about individual events.

**The app holds no threshold.** A persona uses the list it receives and the contents it reads through tools
to decide when its notes need reorganising. "See notes" in the UI shows
them as they are.

### The format is handed over: OKF

The [OKF (Open Knowledge Format)](https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf)
skill is handed to each persona, and **it reads and writes its own notes** as a
bundle in that format: one concept per file, YAML frontmatter and a markdown
body.

```
=== index.md ===
# Tendencies
* [Leans tonkotsu](/tastes/tonkotsu.md) - Picks tonkotsu when there is a choice.

=== tastes/tonkotsu.md ===
---
type: Preference
title: Leans tonkotsu
description: Picks tonkotsu when there is a choice.
---
# Evidence
* 09-08 chose tonkotsu over shoyu when both were there
```

**The persona does the reading and writing itself, through tools.** When it is
woken it is handed **the list only** — never the contents.

| Tool | |
| --- | --- |
| `notes_read` | Read contents (only what it wants) |
| `notes_write` | Write one file. **Only that file** is replaced; the rest are untouched |
| `notes_forget` | Throw one file away |
| `notes_list` | Fetch the list again |

They need no declaration. What a skill declares are the tools that reach
**outside**, and its own notes are not outside.

This is what keeps a grown memory from travelling in full on every wake.
Rewriting everything to change one line sends the whole memory out and back, and
anything not copied across disappears without a trace.

**The app does not read the notes.** It puts bytes at a name — it does not parse
frontmatter, does not build the index, does not check that the format holds.
From the app's side there is no telling whether the contents are OKF at all. If
a persona does not follow it, so be it.

The files are plain files on disk, so you can `cat` them, and **they can be
pointed at as an OKF knowledge source elsewhere**. Something that accumulated
nothing but six months of your meals becomes an asset you can take with you.

The skill is `internal/persona/skills/okf/SKILL.md`. The format is OKF as it is,
but the steps are written against this app's tools. It rides along in every
persona's prompt, so it has to stay short.

Which `type` values to use and when to merge differ by subject, so they go in
each skill's `instructions` — `Preference` and `Shop` for the food one, `Book`
and `Theme` for the reading one. **The format is handed over; the policy belongs
to the skill.**

### Skills

| Field | |
| --- | --- |
| Name, icon, tone | How it shows up and how it talks |
| Subject | What it reacts to and remembers. **The router reads this** |
| Instructions | How to keep notes, when to compress, what is worth saying. **Only it reads these** |
| Model | `low` / `middle` / `high` |
| Tools | Only what is declared can be called |

Four ship with the app (tabe food / michi travel / shiori reading / plana routines). A
`skill.json` can be added from the UI.

```json
{
  "id": "wine",
  "name": "ぶどう",
  "icon": "🍷",
  "domain": "Wine. Labels, regions, photos of bottles, what was drunk. Does not react to food in general or to technical talk.",
  "tone": "Understated. Never pushes a recommendation.",
  "instructions": "Concepts are type: Wine and type: Preference. When the same preference shows up three times, merge it into a Preference and delete the bottles behind it.",
  "model": "high",
  "tools": ["web_search", "place_search"]
}
```

**There is no ceiling on how many.** Add as many as you have subjects. Because
the routing is central, only the few that matter wake for a given post, and the
routing judgement happens once per post.
**How many speak at once is a separate problem**, though: if a post straddles
subjects and everyone answers, it is a chorus rather than a conversation. That
part gets tuned once it is actually in use.

**Without separate subjects, @ means nothing.** If there is nothing to choose
between, naming someone is pointless. A difference in model size makes the
exchange work, but it is not a reason to pick one over another.

### Tools

Only what is declared can be called (`internal/tools`). The app makes the call
and the keys stay server-side, which is also how swapping providers and
insisting on a source link stay the app's guarantees rather than each skill's.
Where the tools go is the operator's configuration (`tools.Config`, from
environment variables): one set of map endpoints and one list of MCP servers
for the whole deployment. The Tavily key is the user's, from their settings.

| Name | What it is | Key |
| --- | --- | --- |
| `web_search` | Tavily | Yes (the user's) |
| `place_search` | Places nearby, with **opening hours** (OpenStreetMap / Overpass) | No |
| `route` | Travel time from where you are (OSRM; roads only, no public transport) | No |
| `schedule` | Wake times a persona asks the app for (nothing leaves the app; kept in `schedules.json` / Firestore) | No |
| `server.tool` | Any tool of a configured MCP server (`internal/mcp`, Streamable HTTP) | Operator's |

MCP tools are listed once per server and cached; a persona is offered them
exactly like the built-in tools, each carrying the server's own input schema,
and calls them the same way. The result's text goes back as the answer to that
call and its resource links become sources.
