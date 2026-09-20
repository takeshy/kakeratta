# kakeratta

English | [日本語](README_ja.md)

**Ka-ke-ratta.** Japanese for the act of tossing in a *kakera* — a fragment.

Put thoughts and things that catch your eye into one timeline as text, images,
URLs, or speech, without deciding first whether they matter or where they
belong. AIs with separate memories for their own subjects react only when they
have something to say.

Built for the [Nebius Global AI Hackathon](https://nebiusglobalaihackathon.devpost.com/).
Try it at [kakeratta.net](https://kakeratta.net/lp/?lang=en); how it works inside is in [Under the hood](docs/technical.md).

<img src="docs/captures/en/timeline.png" alt="The timeline: your fragments, with what the personas said underneath" width="390">

## Why it exists

I talk to myself a lot. Saying out loud what a meal was like, or an idea that
just came, feels like leaving a hook to catch it by later. Then I forget anyway.

I take notes too, but hardly ever read them back. The notes I do read are the
ones I already know are there, like passwords and API keys.

Even if an AI files every note neatly, information whose existence I have
forgotten cannot be searched for. And what seemed unimportant when I wrote it
down sometimes matters later.

Talking to myself is also a way to sort out a thought, or a wish to tell
someone about a small discovery. But a conversation with myself never leaves my
own biases.

So there should be a place where the fragments of a day can be tossed in as
they are, without deciding whether they matter or where they belong. Something
that remembers them, looks back through the past or the web when that helps,
and speaks up at a good moment, in a good way.

That is where kakeratta began.

## What it does

kakeratta is a timeline for talking to yourself. Text, photos, URLs, and speech
go in without worrying about mistakes or categories.

Living there are AIs called *personas*, one for each subject: food, travel,
reading, follow-ups. Each finds the posts that concern it, reacts when there is
a reason to, and keeps what is worth keeping in a memory of its own. Replying to
everything would be tiresome, so a persona stays quiet when it has nothing to
say. It can still take a note without speaking.

Personas are created and changed in plain words. "Remember the wines I drink and
help me choose next time" is enough to make one, and it grows to your taste as
you use it.

You can see what a persona remembers, trace a memory back to the post it came
from, and make it forget just the one that is wrong.

Once a day, a persona looks back over its memory and the recent timeline, and
searches the web if that helps. It may bring you news related to your past, or
something you had forgotten. If there is nothing, it stays silent.

With speech, a send phrase of your choosing posts what you just said, so a
thought is kept the moment it arrives.

What you have kept is yours to take. Settings → Backup writes your posts,
personas, notes and images as a zip laid out for a kakeratta running on your
own machine. No API keys or connections travel with it.

## How it works

1. Write a thought, paste a screenshot, send a URL, or speak.
2. The relevant personas receive the post and decide what to note and whether to reply.
3. Reactions arrive in the timeline a little later. Mention a persona with `@` when you want an answer.

Four personas ship with the app: **tabe (food), michi (travel), shiori
(reading), plana (follow-ups)**. Their names are Japanese words in Latin letters
so that `@tabe` can be typed on any keyboard. They are examples of how to divide
things up, not a complete set; make your own for other subjects.

plana keeps what is too small for a calendar but bad to forget: medicine,
feeding the goldfish, laundry, the bins. Write "flowers on the way home" and it
asks "did you get the flowers?" around the evening, working out the time from
its own records.

## Find what you tossed in before

"What was that wine I liked?" Open **Search** in the header. A name like
"Pinot" and a vague memory like "a light red that was easy to drink" go in the
same box. Search covers your notes, text read from photos, and conversations
with personas. A result opens the original post and its conversation.

<img src="docs/captures/en/search-semantic.png" alt="Search from memory: records close to 'a light, easy-drinking red wine' (sample)" width="390">

**Personas look back too.** When a new label arrives, a wine persona may say
"this is close to the Pinot you liked before", with a link to that post.

## See what a persona remembers

Open **What it remembers** in the Personas panel to read its saved notes by
kind: tastes, wines, places, books. Evidence and exceptions show as written.
Filter by the tags the persona gave them, and open the post and conversation a
note came from under **Posts that updated this memory**.

<img src="docs/captures/en/memory.png" alt="What the wine persona remembers: tastes and wines, with tags, links to the posts, and ✎ to edit (sample)" width="382">

Fix a misspelling or a wrong fact with **✎** on the note. Remove a wrong note
with **Forget this note**, one at a time; the post and the other notes stay. To
change how it remembers, use **Edit with AI** and say, for example, "don't call
something a favourite after one glass". Its memory is not a black box.

## See why a persona was called, or not

The app decides who receives a fragment, and it keeps the reason. Press **⚖
(the router's call)** on a post to see the personas that were called and, for
each one that was not, a one-line reason.

<img src="docs/captures/en/routing.png" alt="The router's call on a tantanmen post: tabe was called, and why michi, shiori and plana were not (sample)" width="382">

When it got it wrong, press **Should have been called** and say why in your own
words. The AI rewrites the persona's condition and tries the same post again to
make sure. The new condition is shown as text and can be edited before saving.

## Fix a reply where it went wrong

Press **Fix the reply** on a persona's remark and say how it missed, such as
"no recommendations, just one line on how it differs from last time". The AI
rewrites only the tone and instructions; the condition, name, and tools stay.

<img src="docs/captures/en/reply-trial.png" alt="A trial: how tabe would answer the same post now. 'Good' or 'Still off' (sample)" width="382">

On save, the same persona is stood up on the same post again for a trial reply.
Nothing lands in the timeline and no note or schedule is written. You judge the
result. Nothing is learned behind your back.

## Describe a persona. Start using it.

Answer "What would you like your persona to help with?" in your own words.

> When I send a photo of a wine label I liked, tell me the wine and its grape varieties so I can order it or buy it.

Once the subject is clear, the AI fills in the name, tone, and what to remember,
and shows a complete draft. A single word like "wine" is enough to start.

<img src="docs/captures/en/persona-draft.png" alt="Review and save a sample wine persona drafted by AI" width="382">

**Start using it, then shape it.** "Reply more briefly", "remember why I liked a
wine, not just its name": tell it through **Edit with AI**. Built-in personas
can be edited the same way.

## Share the conversation you choose to Bluesky

Connect your own Bluesky account and choose which messages to publish from a
post, the personas' replies, and your follow-ups. Persona replies start
unchecked. Preview the text, reply structure, and account before publishing.
Persona messages carry an attribution such as `🍚 tabeからの返信:` (reply from tabe).

<img src="docs/captures/en/bluesky-preview.png" alt="Bluesky preview with tabe's reply and your follow-up selected, leaving out gout's reply (sample)" width="382">

[Connect and share a conversation](https://kakeratta.net/manual/?lang=en#en-bluesky)

## Fast startup and offline posts

Recent conversations stay in the browser and show first on the next visit. On a
device that has been online once, text, images, URLs, and replies can be saved
offline as pending, and they send when the connection returns.
[Using offline posts](https://kakeratta.net/manual/?lang=en#en-offline)

## What we learned

An AI that organizes your information is not enough.

A room someone else has tidied can be harder to find things in than before. AI
organizing your notes is similar: perfectly filed, they are still out of reach
if you do not know where they are. More fundamentally, **you cannot search for
what you forgot exists.**

So a personal AI cannot stop at remembering and organizing. It has to bring
things to you when they are needed, and sometimes say "this might matter to you
now" without being asked. We learned that **when to remind** matters as much as
what to remember.

The inside grew complex, but what the user does became simpler for it. Post
what you are thinking. Tell a persona in plain words what you would like. That
is all.

## Get started

Requirements: Go 1.26 or later, Node.js, Task (the `task` command), and a
Nebius Token Factory API key. With [mise](https://mise.jdx.dev), `mise install`
in the repository root gets Go and Task from `mise.toml`; otherwise install
Task with `go install github.com/go-task/task/v3/cmd/task@latest`.

```sh
task setup  # once
task dev    # the API on :8787 and the page on :5173, together
```

Open <http://127.0.0.1:5173> and enter the API key in Settings. Anything past
that — another provider for the models, an external agent, running the server
yourself — is under [Advanced](#advanced).

## Advanced

None of this is needed to use the app. It is here for when the defaults are not
what you want.

**Another provider for the models.** Settings → **Use OpenRouter** takes an
OpenRouter API key, and once the connection check passes it unlocks two things.
Routing alone can move to **Jev**, a decision-only model that is fast and cheap
but returns no reasons, so the router's call shows who was chosen without
saying why. **OpenRouter-only mode** moves the high, middle and low tiers to
models you pick, with tool calls routed through Exacto. Left alone, everything
runs on Nemotron through Nebius Token Factory. The measurements behind Jev are
in [the routing model comparison](docs/routing-model-comparison-2026-09-19.md)
(Japanese).

**An external agent as a persona.** Hermes Agent joins the room like anyone
else. Kakeratta keeps your requests as an MCP server; Hermes connects to it,
runs them with its own models, tools and long-term memory, and reports back
into the timeline — from **Ask Hermes**, from `@Hermes` in a post, from a daily
check-in, or from Hermes's own cron. Kakeratta never starts the Hermes process;
it only needs a URL Hermes can reach. Issue the key in Settings, under **MCP
連携**, and follow [Setup and API guide](docs/hermes.md) (Japanese).

**Running the server yourself.** Environment variables, authentication, where
the data lives, and the tools an operator provides are in
[Under the hood](docs/technical.md#running-it).

## Read more

| Document | Contents |
| --- | --- |
| [Under the hood](docs/technical.md) | The flow, why routing and workflows are shaped as they are, models, environment variables and storage, how check-ins work, tools and routes |
| Landing page and manual ([/lp/](https://kakeratta.net/lp/?lang=en), [/manual/](https://kakeratta.net/manual/?lang=en)) | Bilingual pages served beside the app; the **?** icon in the app header opens the manual in a new tab |
| [Usage and configuration](docs/usage.md) | Android and iOS sharing, location, speech, model settings, and stored data |
| [Design and personas](docs/architecture.md) | Principles, routing, execution order, OKF notes, skills, and tools |
| [What was hard about the AI](docs/ai-lessons.md) | What went wrong with Nemotron personas in live runs and production, and how each was fixed |
| [Hermes Agent](docs/hermes.md) (Japanese) | Connecting an external agent as a persona: the MCP key, requests, and reports |
| [Development and status](docs/development.md) | Tests, source layout, implemented features, and unverified items |
| [Google Cloud deployment](infra/README.md) | Terraform deployment: Firebase Hosting for the page, Cloud Run for the API |

[MIT licensed](LICENSE).
