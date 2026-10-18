# Judge sample personas and data

This document describes the English starting workspace prepared for new hackathon judge registrations. It is adapted from the owner's October 2026 production timeline with permission. Posts, replies and memories are edited English examples, not fresh AI responses for the judge. Mei is a fictional demo friend. The welcome post makes this distinction visible in the app.

The content is defined in [judge_seed.json](../internal/server/judge_seed.json), with one selected production photo in [judge_seed_assets/wine.jpg](../internal/server/judge_seed_assets/wine.jpg). Registration uses the bundled data, not a live connection to the owner's account. This feature requires deployment of the corresponding server build.

## Personas

| Name | ID | Initially active | Answer engine at setup | Purpose | Initial memory |
| --- | --- | --- | --- | --- | --- |
| tabe 🍜 | `gourmet` | Yes | Built-in | Food preferences and places to eat; excludes wine and cooking instructions | Wanted to visit Sawayaka in Shizuoka |
| Wine 🍷 | `wine-assistant` | Yes | External agent when shared Hermes is configured | Wine labels, tasting preferences, pairings and recommendations | User's full-bodied red preference and the photographed bottle |
| Kitchen 🍳 | `recipe` | Yes | Built-in | Recipes, substitutions and leftovers; excludes restaurant searches | Recent nikujaga and turnip questions |
| Mei 👩 | `mei` | Yes | Built-in | Remember the fictional friend's preferences and plans, without impersonating her | Mei's light, fruity red preference |
| Dad 👨 | `dad` | Yes | Built-in | Gift history, interests and unfinished plans concerning Dad | Previous gifts and a proposed whisky gift |
| sona 🎒 | `routine` | Yes | Built-in | Outing preparation and requested follow-ups; checks forecasts only when relevant | None |
| support 🛟 | `support` | Yes | Built-in | App help grounded in the shipped system specification | Memory disabled |
| infographic 📊 | `infographic` | Yes | Built-in | Explicit requests to visualize existing material with Markdown, emoji and Mermaid | Memory disabled |
| shiori 📚 | `book` | No | Built-in | Books, reading impressions and literary questions | None |
| Goldfish 🐟 | `goldfish-care` | No | Built-in | Tank observations, feeding, water quality and care questions | None |

All names, descriptions, instructions and memory content are English. The internal memory file retains the parser's fixed headings; the English memory view localizes those headings.

Only **Wine** initially prefers the external agent. Shared Hermes registration enables the connection without changing the other personas' answer engines. Re-registering preserves the judge's per-persona engine choices. Judges can change the choices themselves, and direct `@agent` requests are available independently of persona selection when the shared agent is connected and running.

## Timeline

The workspace contains twelve entries: eight user sample posts, three edited sample replies and one welcome post. Times are relative to the first initialization, rather than fixed to the production dates.

| Age at initialization | Content | What it demonstrates |
| --- | --- | --- |
| 7 days | Wanted to try Sawayaka's Genkotsu hamburger in Shizuoka, but ran out of time | An unfinished interest that can resurface on a return trip |
| 6 days | Mei likes light, fruity reds and avoids heavy, oaky reds | A friend's preferences belong in her own memory |
| 5 days | The user likes full-bodied, dry reds | Different tastes can coexist without being merged |
| 4 days, 14 hours | Photo of Réserve Mouton Cadet Margaux 2022, saved for a friend's birthday | A real photo post with English interpretation and visible-label OCR |
| 4 days | Bought running shoes; nothing to plan | An ordinary update without an unnecessary sample reply |
| 3 days | Asked how to turn leftover nikujaga into curry | A cooking question with a short sample reply |
| 2 days | Asked for turnip recipes | A follow-up cooking topic with three sample options |
| 1 day | Sent Dad cured ham and soup last month; considering whisky this month | Gift history separated from an unconfirmed plan, with a sample reply |
| 1 hour | Welcome, sample-data disclosure and suggested prompts | A starting point for the judge's own exploration |

The three replies belong to the curry, turnip and Dad posts. Thread roots and reply targets are preserved inside the sample workspace. The photo is copied into each judge's own blob storage, with a new blob ID; it does not link to the owner's private image URL. Its interpretation is prepared English sample data, so registration does not run image inference.

## Memories

Five personas have a `Memo.md` linked to the sample posts:

- **tabe:** a current fact about the unvisited Shizuoka restaurant.
- **Mei:** a current fact about her wine preferences, explicitly separate from the user's.
- **Wine:** a current fact about the user's wine preferences and a recent record of the photographed bottle. The record does not claim the wine has been tasted or liked.
- **Kitchen:** recent records of the curry and turnip questions, without claiming the dishes were made.
- **Dad:** a current fact distinguishing gifts already sent from whisky merely being considered.

Facts and records link to original posts inside the judge workspace, so the judge can inspect sources, correct a fact or delete a sample. No initial reminders, birthdays or Push subscriptions are installed.

## Suggested checks

| Action | Expected route or context |
| --- | --- |
| Post “Going to Shizuoka tomorrow.” | tabe can connect the new trip to the unvisited restaurant |
| Post “What wine would suit Mei?” | Mei and Wine can use the distinct preferences; Wine prefers the external agent |
| Reply to the bottle photo with “@Wine What can you tell from this label?” | Wine receives the photo's thread and prepared English interpretation |
| Post “@Kitchen What can I make with turnips and eggs?” | Kitchen receives the direct request and can use its recent cooking records |
| Post “What did I send Dad last month?” | Dad has source-linked gift history |
| Reply to a useful answer with “@infographic Turn this into a diagram.” | The built-in infographic persona visualizes that answer |
| Post “@agent Research three gift ideas for someone who enjoys whisky.” | A direct external request, independently of persona engine preferences |

Responses to these new requests are generated live; the table describes the supplied context and intended route, not guaranteed wording. Scheduled reminders and actual device notifications can be tested by explicitly requesting a future reminder and enabling notifications on the judge's device.

## Initialization and preservation

Only untouched invitation workspaces are seeded. Existing posts or installed personas cause initialization to be skipped. A durable marker, stable entry IDs and recorded image blob IDs let interrupted initialization resume without duplicate posts or moving timestamps. Completed initialization never restores examples the judge deleted or resets persona activation or engine choices.

The seed makes no AI calls, consumes no judge AI-action quota and queues no reactions. It copies no API keys, external connection credentials, Push devices, production jobs or live schedules. Shared Hermes is configured separately through the existing registration flow. See [judge access](judge-access.md) for connection requirements and live acceptance checks, and [the backend specification](system/backend.md#http-api-and-authentication) for implementation and tests.
