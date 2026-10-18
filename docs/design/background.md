# Why kakeratta exists

[README](../../README.md) · [日本語](background_ja.md)

## Why it exists

With smartphones, jotting down notes and snapping photos has become effortless,
leading to an overwhelming volume of data. Naturally, the more data piles up,
the less often we revisit it. We forget we even took a note, or look back at
one and have no idea what it was about.

I tried building an app that uses AI to automatically categorize and organize
notes. But much like when someone cleans up your room for you and suddenly
nothing is where you expect it to be, it only made things harder to find. When
things were cluttered, it may have been messy, but because they were always in
sight or being shuffled around, I still had a rough sense of where everything
was. That said, with the sheer volume of data I now had, leaving it scattered
meant I would never find anything.

Then it hit me: what if the organizer could tell you where things are,
anticipate that you might forget something even exists, proactively bring it
up when relevant topics arise, and ask, "Can I forget this?" for things left
untouched for far too long? Wouldn't that fulfill the true purpose of taking
notes?

One of my biggest personal regrets is losing touch with someone who always
cared about me - simply because I was too busy, forgot to send a thank-you
message, and before I realized it, the moment had passed, letting us drift
apart.

That is how kakeratta was born.

## What we learned

An AI that organizes your information is not enough.

A room someone else has tidied can be harder to find things in than before. AI
organizing your notes is similar: perfectly filed, they are still out of reach
if you do not know where they are.

More fundamentally, **you cannot search for what you forgot exists.**

So a personal AI cannot stop at remembering and organizing. It has to bring
things to you when they are needed, and sometimes say "this might matter to you
now" without being asked.

We learned that **when to remind matters as much as what to remember.**

We also learned that making the AI more capable does not always mean giving
every decision to a larger model.

If a high-end model gets a task right and a smaller model gets it wrong, the
task may be too ambiguous.

That is what happened with routing. Instead of continuing to spend a larger
model on every post, the job was changed from a broad judgement into explicit
condition matching.

The smaller router became much more dependable, the work on every post became
cheaper, and the routing conditions became something the user could inspect and
correct.

Smaller models can also answer faster. Making narrow tasks reliable on them
helps reduce both inference cost and the time the user waits for a response.

The same principle shaped the rest of the system: strong models are useful, but
clear boundaries are useful too — between routing and execution, between a
persona and its memory, between a persona and the tools it is allowed to use,
and between kakeratta and an external autonomous agent.

The inside grew complex, but what the user does became simpler.

**Post what you are thinking. Tell a persona in plain words what you would like.**

That is all.
