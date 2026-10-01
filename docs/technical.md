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

**fragments of the day → personas for people and interests → a separate memory for each → recall when it matters**

In code both kinds are personas (`skill.json`); the user sees them as **People**
and **Interests**. When a fragment arrives, a person's persona is reached if
the fragment contains one of their names or keywords, and the router checks it
against each interest persona's routing condition and wakes only the ones that
match.

Each selected persona then decides what to do:

* read its memory - a short summary per field, and its latest records
* look further back through its records, the current thread or older fragments
* call one of the tools allowed for that persona
* schedule a later follow-up
* speak in the timeline
* or stay silent

Afterwards the app keeps what the fragment leaves behind: the persona sums the
fragment up in one sentence with tags, and rewrites the summaries the fragment
changes. Memory is split by persona, then by the fields its skill names.

A persona therefore does not receive one ever-growing personal-history prompt.
Its summaries stay under 2,000 characters each; the details live in one-line
records it can search, and in the timeline itself.

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

The complexity stays below the surface. None of MCP, RAG, model selection,
or workflow validation needs to be understood by the person using the app.

## Design principles

**Memory is divided by subject.** The point of the split is not to hide
anything — it is to **keep the concentration**.

**The app routes, reviews and runs.** Who gets a post is decided centrally.
Whether to speak, what to remember and when to compress it are each persona's
own — but a persona hands in its answer and each kind of change apart, the app
assembles them into a workflow, and checks that workflow before running it and
publishing the answer. The app does not decide what a persona says; it stops a
persona saying what did not happen, or promising what it did not arrange.

**Silence is fine.** Not everything gets a reaction. This is not an app for
having things explained to you, and being reacted to every time you just wanted
to look at something is an imposition. A post nobody answers is a normal post.

**Not speaking and not remembering are different things.** A post a persona
said nothing about is still recorded in its memory, if it is its subject.

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
A reply to a persona's remark or post always reaches that persona as well,
whether or not the router picked it.

The current default router is **Nemotron 3 Super through Nebius Token Factory**.

Advanced settings can instead use **Jev** for routing alone. Jev is a
decision-oriented model and can be faster and cheaper for that narrow task, but
it does not provide the exclusion reasons used by the normal routing-inspection
UI.

### People are matched by words, not routed

A persona can keep one person instead of a subject (`"kind": "person"`). A
person is never put to the router: asked about 佐藤さん, it would hand over
every post with a 佐藤 in it, a politician's included. A post reaches a person
when it contains the persona's name, one of its `aliases` or one of its
`keywords` (`internal/persona/people.go`). Width, case, hiragana against
katakana, spacing and a trailing honorific (さん, くん, 様...) are folded away,
so 佐藤 matches 佐藤くん and さとうさん; near words are listed as keywords of
their own. Names also answer an `@`. Text read from a photo counts.

A word can match by accident - 母 is in 母校. The persona is told that a word
is all it was, and one whose post is not about the person ends its turn with
no reply and no note. The word matched is kept on the routing record and shown
in ⚖. There, **Should have been called** has the persona builder propose
keywords from the post and their near words, and the saved words are checked
against the same post without the router; **Should not have been called**
lists every one of the person's words found in the post and lets the user
remove the ones they choose (never the persona's own name). A person heard in
a post is delivered even when the subjects' router fails.

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

* If so, the persona whose remark was replied to answers on Ultra, once. Its
  prompt drops "keep it short" and asks for a thorough answer in Markdown.
* Anyone else the reply reaches answers on Super as usual.
* Thanks, ordinary follow-up questions and new topics stay on Super, as does a
  judgement Lightning fails to give. The persona replied to still answers.
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

## A workflow assembled phase by phase, not written in one call

A persona does not directly mutate application state from whatever text the
model happens to produce, and it does not write its answer and its changes in
one breath either.

An earlier shape asked one model call for everything at once: the remark, the
notes to keep, the schedules and the tool calls, as one workflow. It carried a
cost the answer paid for. The model spent its attention on the note format and
the step schema, shortened the remark to leave room for a note it was required
to write, and a reviewer that could not see what had been researched sent good
answers back. When the answering was handed to an external agent, the quality
dropped visibly.

The workflow stays - `Workflow{answer, schedules, actions}` is still what
kakeratta checks, reviews and runs - but no model writes it whole any more, and
memory is kept apart, after the answer is out.
A turn is split by responsibility, each phase fills one typed part, and the
host assembles the workflow:

```text
Built-in: Read → Answer        External agent: Read + Answer → submit_answer
                     ↓
            Schedule phase → schedules
                     ↓
            Action phase   → actions      (only if the persona holds one)
                     ↓
            Code checks (every time)
                     ↓
            Review (only when there is a schedule or an action)
                     ↓
            Execute → publish the answer
                     ↓
            Note keeper    → record of the fragment, field summaries
```

### A turn, phase by phase

Every persona works this way, whatever tools it holds
(`internal/persona/answer.go`, `phases.go`, `workflow.go`). The reading and the
answer can be done by the built-in model or, for a persona whose planner is set
to it, by an external agent over MCP (see
[External agents through MCP](#external-agents-through-mcp)); everything after
the answer is the same either way.

1. **Read.** Its field summaries, whole, and its latest 15 records are handed
   over at the start of every turn. It may then ask, up to ten times (twenty in
   an Ultra turn for a deeper answer), to look at things: older records by tag,
   words or month, the post a record points to, a timeline search, a web
   search, a place or route lookup. Only read-only tools run here; an MCP tool
   counts as read-only only when its server marks it `readOnlyHint`. Reading
   writes nothing, so the app just does it and hands the results back.
2. **Answer.** With the reading done, the persona hands in only
   `{text, sources}`. Its prompt holds no note format, memory layout or
   change schema, and it is not told to keep notes. It is told what the later
   phases can do for it (`capabilities`), so it does not promise a reminder or
   an action the persona cannot make. An empty text is silence; a named turn
   must answer, a listening turn must not. The answer is fixed from here on:
   no later phase can change it.
3. **Schedule phase.** A separate call, on the same model, sees the finished
   answer and the schedules already placed, and hands in `submit_schedule`:
   create and cancel operations (a change is a cancel and a create), each
   create with a short note of what it is for. It also states whether every promise the
   answer makes about time is backed by an operation or an existing schedule
   (`consistent`, with a `reason`). "I cancelled it" with no cancel, or "I'll
   ask you at 22:30" with nothing placed, is sent back; if it cannot be made
   consistent, the turn stops and the answer is not published.
4. **Action phase.** Only for a persona that holds a tool that acts. It hands
   in `submit_action` — the tool calls and their arguments — with the same
   consistency statement about what the answer promised.
5. **Memory comes after the answer.** It is not a phase of the turn: memory
   that fails to come out must not take a finished answer with it. Once the
   remark is out, the persona's **note keeper** keeps it (see
   [The note keeper](#the-note-keeper)).
6. **Checked by code, every time.** Known operation types, declared tools and no
   lookup used as an action, schedule ids that exist and are the persona's own
   to cancel, times that parse. A proposal that fails is sent back to its own
   phase with the reasons.
7. **Reviewed, only when it reaches further.** A plan holding a schedule or an
   action is reviewed by Nemotron 3 Super against a narrow list: the answer's
   promises and the schedules' times, creates and cancels agree; schedules and
   actions do not contradict or depend on each other unmet; an action's target
   and arguments fit the user's request and the persona's permissions; an
   answer that says a change is done has something behind it. The answer's
   quality, its length and its research are not reviewed, and a missing
   internal read is never a reason to reject research done elsewhere. A plain
   answer costs no review. A failed review
   regenerates only the phases it blamed (`schedule`, `action` or `both`); the
   answer stays as it was. After two reviews, the turn stops.
8. **Run, then publish.** Schedules and actions run in order; **the first step
   that fails stops the rest**, and the answer is published only when
   everything ran. An external action is not a transaction: what already ran is recorded, and
   an answer promising success is withheld.

Each turn is logged as one structured entry: what was read, the answer, each
phase with its rounds, reads and what it had sent back and why, each review and
which phases it regenerated, the assembled workflow, each step as it ran, and what
was said. The note keeper logs its own entry (`notes <persona>`): what it handed
in and how many submissions it took.

### The note keeper

`internal/persona/notekeeper.go`. After the remark is published, the persona
keeps what the turn should leave behind, on the persona model (Super, even in
an Ultra turn), in its own voice. It runs for a fragment of the user's - a post,
a reply, an edit, also when the persona stayed quiet or could not speak - for a
check-in handed records due for review, and for the tidy-up after a fragment was
deleted. A schedule wake does not run it, and neither does a persona that
stayed quiet because the fragment was not its business.

It is one call, `keep_memory`, with the current summaries, the latest records,
the thread and the published answer in front of it, and **no way to read
more**. It hands in:

* `summary` - the fragment in one sentence, in the user's words (140 characters at most);
* `tags` - one to five words to find it by later;
* `expires` / `review` - the record's dates, if any (see below);
* `fields` - only the summaries the fragment changes, each rewritten whole,
  2,000 characters at most;
* `records` - when it was handed records due for review, what becomes of them.

The app checks it and sends it back with the reasons, up to three times: no
sentence, too many tags, a date that is not after today, a field the skill
does not name, a body too long, two or more of the answer's own sentences
copied. Then **the app writes it**: the changed summaries (`{Field}.md`), and
one record line in the month's file (`records/YYYY-MM.md`):

```text
- 2026-10-01 12:34 [e_x](/?entry=e_x) Wants to try Jiro in Mita #ramen #Mita review:2026-10-22/21d
```

The id is the persona's reply when it replied - which leads back to the
fragment it answered - and the fragment itself when it did not. An edited
fragment, or a retry, replaces its record rather than adding a second.

If the keeper cannot finish, the answer is already out. Its input is saved as a
note job before it runs, and failed memory work is retried after one minute,
with backoff up to one hour, on the workspace's due passes. A deleted or edited
fragment's old input is not replayed. Two turns of one persona finishing
together take turns under a lock, so they do not rewrite the same summary or
month from what each saw. The remark is updated afterwards to point at the
summaries that were written. If the job itself cannot be saved, the keeper
still makes one immediate attempt; that attempt has no durable retry.

## Models

kakeratta uses multiple models through **Nebius Token Factory** rather than
sending all work to one large model.

| Model                      | Role                                                                  |
| -------------------------- | --------------------------------------------------------------------- |
| **Nemotron 3 Super**       | Every persona's answers and phases, routing, and workflow review      |
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

Each persona owns its own memory, and **only the app writes it**. A food
persona cannot touch the travel persona's memory, and no persona - nor an
external agent - writes files of its own: the persona's note keeper hands in
what to keep, and the app checks and stores it.

A persona keeps two things:

* **One summary per field.** The fields are the capitalised words of its
  skill's `memory` - `Preference` and `Shop` for the food persona, `Book` and
  `Theme` for the reading one; a persona that names none keeps `Memo`. Each
  summary holds what is known in that field so far, at most 2,000 characters,
  rewritten whole as fragments change it. When it would run over, the
  tendency is kept and the single events are left to the records.
* **One record per fragment.** A sentence, its tags, the post to go back to,
  and possibly a date. Records are kept by month and only grow; a persona is
  handed the latest 15 and searches the rest.

```text
=== Preference.md ===
Leans tonkotsu when there is a choice; finds strong sesame too much in tantanmen.

=== records/2026-09.md ===
# 2026-09 のかけら

- 2026-09-01 20:14 [e_a](/?entry=e_a) Said the tantanmen had too much sesame #tantanmen #sesame
- 2026-09-08 12:30 [e_b](/?entry=e_b) Chose tonkotsu over shoyu #ramen #tonkotsu
- 2026-09-12 12:02 [e_c](/?entry=e_c) Asked where to eat lunch near Kanda today #lunch #Kanda expires:2026-09-13/1d
```

The user can read both directly from the interface: the summaries first, then
the records month by month. A record's id opens the post, and a tag narrows the
view to the months holding it. A summary or a month of records can be edited or
deleted without deleting any post; nothing is woken for it. In local storage
they are plain files that can be read with `cat`.

**Records are what a line of an index used to be, and some are set to go.**
The keeper dates a record from what it is. A fragment that served its moment -
"where for lunch today?", "how do I get to Shibuya?" - gets `expires`, the next
day; the app deletes the record when the day comes, without waking anyone. A
thing the user said whose lifetime nobody knows - a shop they want to try, a
topic they are into - gets `review`, weeks or months ahead; preferences and
habits get neither, and a record without dates is never aged out.

**A record in use keeps longer.** When a persona reads a record through a
search or by its id while answering a post, both its dates move: the span it
was given starts again from that day. Reading never brings a date closer, and
reading at a check-in or any other wake that is not a post does not count, so
a record read in order to ask about it is not put off again and again.

**What has come up for review is asked about, and the app never deletes it.**
When the review day has passed, the persona's next daily check-in is handed at
most three due records, oldest first. The persona asks about one in a short
post; its keeper puts that record off a week and marks it with the post that
asked. A reply under that post reaches the persona with the records it asked
about, and its keeper keeps the record for good, moves the date, or forgets it.

Deleting a post is different. The app takes out the records kept under it - the
fragment's own, or the reply that answered it - and wakes each persona that had
one, which cannot speak and has nothing to answer: it may cancel the schedules
it set for that fragment, and its keeper takes out of the summaries what rested
on it. If only a persona's reply is deleted, its record is kept under the
fragment instead.

### What a persona is given

* the post it was routed, and what the app read out of its image;
* **the remarks under that post** - other personas' and the user's replies;
* its field summaries, whole, and its latest records;
* its own pending schedules, if it can schedule.

**Not the whole timeline.** Hand that over and every persona sees everything
again, which is exactly what dividing by subject was for. Anything older is in
its records, or found with a timeline search.

**Memory comes from the user.** The keeper is told to keep what the user said
and did - facts, preferences, plans, changes, what they asked to be remembered
- and not what an answer researched or recommended: a list of candidates, a
figure looked up, an explanation. Those can be looked up again when needed,
and kept as memory they would be read back as something known about the user
long after they went stale. How an answer went may be kept as a word or two.

**The format is the app's; the policy belongs to the skill.** Which fields a
persona keeps, what each summary prefers, which records get which date - those
are in each skill's `memory`, written in plain words. The app holds no
threshold for when to compress; it only holds the sizes.

Memory kept the old way - one OKF note per thing with an `index.md` - is not
read any more. It was folded into the summaries once by hand
(`cmd/migrate-memory`): the persona's model drafts the summaries, a person
checks and edits the draft, and only then are they written and the old notes
deleted.

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
| `memory` | Its fields (1–4 capitalised words such as `Wine`, `Preference`), what each summary keeps, which records get which date. Each field is one summary |
| `instructions` | What to separate and mark as a guess, when to speak. **Only the persona reads these** |
| `tools` | Only what is declared can be called |

Four ship with the app — tabe (food), michi (travel), shiori (reading), sona
(preparation, weather and small follow-ups). sona is active by default in new workspaces; saved activation choices are preserved. More can be
added from the UI, written by hand or drafted with the persona builder
(Nemotron 3 Ultra).

```json
{
  "id": "wine",
  "name": "ぶどう",
  "icon": "🍷",
  "domain": "ワイン。銘柄、産地、ラベルの写真、飲んだ記録。",
  "tone": "控えめ。おすすめを押しつけない。",
  "memory": "分野は Wine（飲んだ銘柄）と Preference（好みの傾向）の2つ。好みは重なったらまとめる。",
  "instructions": "引用と本人の意見を分け、推測は推測と書く。",
  "tools": ["web_search", "place_search"]
}
```

**There is no ceiling on how many.** Because routing is central, only the few
that matter wake for a given post, and routing runs once per post. **Without
separate subjects, @ means nothing** — if there is nothing to choose between,
naming someone is pointless.

### A person's persona

A person's `skill.json` adds `kind`, `aliases` and `keywords`, and its
`domain` is the relationship (恋人, 母, a friend from university) rather than a
routing condition. It has no `tone` or `memory` of its own, and its
`instructions` are only what is particular to this one person, if anything.
How a person's persona speaks, its two fields (Fact and Event) and its
rules - keep loans and promises until settled, wake on the morning of a
meeting, wake a week before and on a birthday or an anniversary - are held by the app
and given to every person at the start of a turn (`forTurn` in
`internal/persona/people.go`), so a better rule reaches every person at once.

Birthdays, preferences and other details supplied when creating a person or using **Edit with AI** are saved as editable **Basic information** in the persona definition. Saving does not post a fragment or copy them into notes. You can also change them in the regular editor.

The definition stores `basicInfo` (up to 8,000 characters), supplied on each `forTurn`. `internal/persona/person_schedules.go` extracts calendar events before saving basic information, then reconciles schedules after persistence. Fragments use the same extraction and reconciliation.

Saving or editing basic information, and fragments delivered to the person, automatically register birthdays, anniversaries and appointments with known dates. Birthdays and anniversaries notify annually, one week before and on the day at 09:00 in the saved timezone. Appointments use an explicitly requested notification time, otherwise 09:00 on the day (one hour before appointments earlier than 09:00). Notifications arrive as persona posts. Future dates have no scheduling horizon; unknown dates are not guessed. Changes and cancellations are reconciled.

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
answer's `sources` the posts it rests on, and only those are linked.

## Check-ins and scheduled wakes

Every active persona has a **Daily check-in** once a day. It cannot be turned
off, and only how much it says can be chosen - and, for a person, whether it is
looked back on monthly instead (see the month's summary below).

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
about what needs the user's decision; today that is a record that has reached its
review date (whether it can be forgotten). It does not bring discoveries,
suggestions, news or greetings. On a day with nothing to ask it still wakes,
and ends silent.

With it off (the default), the persona also reads the timeline since the
previous check-in actually ran - recorded with the day's claim - and older records
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

A record whose `review` date has passed is appended to the check-in trigger, at
most three at a time, oldest first. The persona asks about one of them and
posts once; its keeper puts that record off a week and marks it with the post.

A reply under a post a persona started on its own - a check-in, a schedule,
a review - always reaches that persona, whatever the router decides. The
persona is told which records it had asked about, and its keeper settles them.

### The month's summary, and people looked back on monthly

Once a month the app reads the timeline since its previous summary (32 days at
most, the newest 120,000 characters of it) and posts **This month** under its
own name (`timeline.App`, `internal/persona/summary.go`): headings by topic,
one line per item, each ending in a link to the post it came from. People's
events, plans and anything still open are always kept. The day is picked by a
hash of the workspace (1st to 28th, so every month has it) and the minute like
a check-in's; the run is claimed like one, so it is written once. A month with
no posts gets no summary. The user can edit the summary like their own post;
editing it routes nothing.

A person's persona can be looked back on **monthly** instead of daily (a
subject always checks in daily). Such a person checks in on the summary's day,
after it, and is handed the summary - as the user may have corrected it - and
the posts since its previous check-in that contain its names or keywords (the
newest 40), instead of reading the whole timeline. However many people there
are, the month is read once. Calendar notifications execute from saved schedules independently of check-in frequency.

### Schedules created by personas

A persona that has the `schedule` tool can ask kakeratta to wake it later.

Current limits are:

* up to 20 pending schedules per persona;
* any future time, without a scheduling horizon;
* one-time or daily recurrence.

A schedule carries a short note of what it is for; nothing is written to memory for it. Host-managed person events need nothing in memory either; birthdays and anniversaries recur annually and share the 20-schedule limit.

For example, sona may hear about buying flowers and schedule a later wake to
ask whether they were bought.

Preparation checks can run shortly before departure, using current forecasts when the outing region and date are known. Chore follow-ups run shortly after the relevant moment. Both require grounded timing and suppress repeated or completed checks.

A schedule is claimed before the model executes so a failed or silent wake is
not run repeatedly.

A wake more than 10 minutes late is dropped rather than replayed at an
unexpected time.

Deleting the fragment gives the persona a chance to revisit the schedules it
set for it.

Forgetting all of a persona's memory, pausing it, or removing it drops its
pending schedules.

### The browser does not need to stay open

Check-ins and schedules are server-side.

The browser can be closed.

The server itself must still be running.

The provided Cloud Run deployment keeps one instance available and allocates
CPU outside request handling so the server can perform these periodic checks.
It may scale to more instances; checks, schedules and reactions are taken up
by whichever instance claims them in Firestore first, and work an instance
left when it stopped is taken up by another.

Configured workspaces are restored after a restart, including Firestore-backed
users.

There is no clock going round every workspace. A workspace asks to be woken at
the next time anything of its comes due - a schedule, a check-in, a retry, an
`expires` date - through a Cloud Tasks task (a timer where there is no queue),
and the wake uses the same execution queue as ordinary persona work, so a busy
queue can delay it.

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

This includes posts, settings, persona definitions, and persona memory
(field summaries and monthly records).

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

posts, settings, persona definitions, and persona memory are stored in Firestore
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
* persona memory
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

Of a persona's turn, the research and the answer — reading the context, looking
things up, and writing what the persona says — can be handed to an external
agent such as **Hermes Agent**. Each persona chooses its planner: the built-in model
(the default) or the external agent. kakeratta exposes the user's workspace as
an authenticated **Streamable HTTP MCP server** for this.

When a persona planned externally wakes — for a post, a check-in, a schedule, a
reply to a review question — kakeratta builds the context the built-in model would get to answer
and opens an **ask**. The agent claims it, reads the context, may look things up
with the persona's own records and read-only tools or with its own, and submits
only the answer. kakeratta checks its shape and the turn's rules, then plans the
schedule and action phases on the persona's model, checks and reviews
them as for any turn, runs them as the persona, publishes the answer, and then
has the persona's note keeper keep the memory. The
agent never writes memory, a schedule or a remark itself, and its answer is not
reviewed: the app's reviewer cannot see what the agent researched.

```text
persona wakes
→ ask opened (pending, claimBy)
→ agent: claim_ask → context
→ agent: read_for_ask / own research (optional) → submit_answer
→ kakeratta: check the answer → accepted
→ kakeratta: schedule → action phases, checks, review if needed
→ kakeratta runs the changes, publishes the answer as the persona, then keeps memory
```

Waiting on an agent must not stall everything else, so an externally planned
turn runs apart from the reaction queue and reports to the routing sheet when it
has really ended.

### Fall-through

The built-in model takes the turn back when:

* no agent claims the ask within the claim wait (3 minutes by default);
* no answer is accepted within the plan wait after the claim (15 minutes by
  default);
* the agent's answer is sent back more than the allowed number of times;
* the agent calls `release_ask`;
* the agent has made no MCP call at all for the claim wait (away). No ask is
  opened then, and the agent comes back by itself on its next call.

Both waits are set under Settings → Advanced options → Integrations. An ask
is kept in the workspace and survives a restart; the turn that opened it ends
and is carried on once the ask closes, and a late answer is refused. An accepted answer is not taken back: if the later phases
cannot back up what it promised, or a change fails, it is withheld. A preview a person is waiting on is always planned
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

Its only write is an answer submitted for a claimed ask. kakeratta decides,
checks and runs any change itself before publishing it as that persona.

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
submit_answer
release_ask
```

`claim_ask` takes an `askId` and a caller-generated `claimId`. Repeated calls
with the same `claimId` are retries of the same claim; a different claim ID
cannot take a claimed ask.

The context a claim returns holds the persona's identity and instructions,
what it can change (`capabilities`), why it woke, the trigger and its thread,
the persona's memory (field summaries and latest records), its schedules, the
answer's JSON Schema, its read-only tools (`records` among them), the turn's
read limits and answering rules, current time, post location, and whether the
turn requires an answer or silence. Full system/user prompts and a second
rendering of the thread are omitted to avoid repeating the same context, and
how memory is kept and the change schemas are left out: they belong to the later phases, not the answer.

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

## Why the external agent only answers

A normal kakeratta persona is intentionally constrained: its changes go through
phases that kakeratta checks before execution.

Handing the answer to an external agent keeps that constraint. The agent may
research with any tools and skills it has, but every change that reaches the
user's memory is decided by the app's own phases, checked, reviewed where it
reaches further, and run by the app itself.
"External agent" does not become an invisible bypass of kakeratta's ownership
model, and a persona whose agent is asleep still answers, only later.

## Design trade-offs

Several choices in kakeratta deliberately favor understandable behavior over
maximum automation.

### Silence is a valid result

A persona can write memory without speaking.

A check-in can finish without creating a thread.

An external agent answering a turn can hand in an empty answer.

The system is not optimized for producing a visible AI response to every event.

### Memory is inspectable

Long-term memory is represented as short summaries and one-line records the
user can read and correct, each record leading back to its post.

It is not only a hidden vector store or conversation summary.

Embeddings help retrieve fragments, but they are not treated as the source of
truth for what the system "remembers".

A record's lifetime is visible too: the reader shows the day the app will
delete a record of a fragment that served its moment, and the day the persona
will ask whether to keep a record of something the user said.

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

An external agent only answers. Every change still goes through the same
phases, checks and review as for the built-in model, and kakeratta alone
executes it. Authentication, the MCP tool surface, and sandbox policy bound what
the agent can read and do on its own side.

## Read more

| Document                                                                                     | Contents                                                                                                                          |
| -------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| [README](../README.md)                                                                       | What kakeratta is, what the user can do, privacy, always-on behavior, Nemotron/Token Factory, and the tested NemoClaw integration |
| [Routing model comparison — 2026-09-17](routing-cost-experiment.md)                          | Controlled Lightning vs Ultra routing experiment with expected recipients, token counts, cost, and latency                        |
| [Routing model comparison — 2026-09-19](routing-model-comparison-2026-09-19.md) | Read-only replay of 20 production posts across Nemotron models, Jev, and GPT Luna                                                 |
| [Usage and configuration](usage.md)                                                          | Android and iOS sharing, location, speech, model settings, and stored data                                                        |
| [What was hard about the AI](ai-lessons.md)                                                  | Failures seen with Nemotron personas and what changed to make them dependable                                                     |
| [Hermes Agent](hermes.md) ([日本語](hermes_ja.md))                                              | MCP authentication, the ask lifecycle, the planning context, and fall-through                                                     |
| [Google Cloud deployment](../infra/README.md)                                                | Terraform deployment with Firebase Hosting, Cloud Run, Firestore, and related infrastructure                                      |

[MIT licensed](../LICENSE).
