# Hackathon image gallery

Fifteen English presentation images, rebuilt as one product story:
**capture → remember → reconnect → act → refine → extend**.
Each upload is a 3600 × 2400 PNG (3:2; laid out at 1800 × 1200 and written at 2×), under 5 MB.

Upload the numbered PNGs in [`images/`](images/) in order, or extract
[`kakeratta-hackathon-gallery.zip`](kakeratta-hackathon-gallery.zip).
The ZIP contains only the fifteen upload images.

- [Full-size preview and captions](preview.html)
- [Titles, captions and source references](captions.json)
- [Contact sheet](overview.png) — review aid, not an upload image

| Order | Image | What the viewer learns |
| --- | --- | --- |
| 1 | `01-a-life-in-fragments.png` | A fragment can become personal context and a useful nudge |
| 2 | `02-people-and-interests.png` | People and interests have their own personas |
| 3 | `03-fragments-become-facts.png` | At check-in, wine records become a sourced preference |
| 4 | `04-you-decide-what-stays.png` | Inspect, correct, forget, or answer a memory-review question |
| 5 | `05-remember-at-the-right-time.png` | A forgotten restaurant returns before a new trip |
| 6 | `06-ready-before-you-leave.png` | sona connects plans, known belongings and fresh weather |
| 7 | `07-review-mail-and-calendar.png` | Gmail and Calendar cards wait for an import decision |
| 8 | `08-plans-you-can-manage.png` | Manage tasks across personas and mark a reminder done |
| 9 | `09-room-for-a-new-interest.png` | A growing subject prompts a suggestion; one tap adds a persona with its profile and memory |
| 10 | `10-find-and-reflect.png` | Search finds details; monthly summaries connect themes |
| 11 | `11-teach-the-response.png` | Inspect routing and preview a revised reply |
| 12 | `12-bring-your-agents.png` | Choose built-in @ai or external @agent; bring Obsidian knowledge into replies |
| 13 | `13-speak-and-listen.png` | Browser: Speak → say over → automatic post → Read aloud; Alexa: save notes, hear replies, check schedules |
| 14 | `14-make-and-share-a-visual.png` | Turn a conversation into a visual and preview selected sharing |
| 15 | `15-purposeful-models-visible-usage.png` | Understand model roles and inspect monthly persona usage |

## Editorial choices

The gallery keeps a fifteen-image ceiling while combining related features:
Gmail + Calendar, memory controls + forgetting review, search + monthly reflection,
infographics + Bluesky sharing, and model roles + usage. The added space shows
memory consolidation, preparation, persona suggestions and voice access.
Routing/reply feedback returns as its own slide because it explains how users
can improve the system's behavior. Each slide has one main point; screenshots
are cropped around that point rather than used as full-screen filler.
Every excerpt is shown at one zoom (1.35 layout pixels per app pixel; the denser
slides 7 and 8 use 1.15), so UI text reads at a consistent size across slides.
Crops end on element or line boundaries; the one deliberately cut answer
(slide 12) fades out.

## Sources and limits

Slide 12 introduces built-in `@ai` questions and external `@agent` requests. The built-in entry also handles app-help questions through the shipped specification; it only reads and answers. Slide 15 includes ordinary direct `@ai` questions in the Super VL role. Presentation copy describes these capabilities; the Vault screenshot remains a local fictional external-planner example, not a live test of direct requests.

The screenshots use shipped application components over local fictional data.
They are reproducible UI examples, not proof of production model output or
real-account Google, Hermes, Alexa, speech or notification delivery.
No screenshot text is repainted or replaced. Presentation copy is English; source files are recorded per slide in `captions.json`.

- `frontend/stories/Personas.stories.js` supplies the memory before/after states.
  Related records become facts at check-in, not immediately after posting.
  Slide 3 shows matching excerpts of the three wine records and derived facts.
- `frontend/stories/Gallery.stories.js` supplies curated preparation, persona
  suggestion with the persona it creates (card, drafted profile and moved
  memory), completed-reminder, voice-composer, persona-creation-input and
  Shizuoka share-dialog examples using the real feed, compose and social modules. The weather statement is fictional, not a live forecast.
  Preparation depends on a known plan; it does not detect departure. A persona
  suggestion changes nothing by itself. Only when the user presses **Create it**
  does the app add the drafted persona, move the related facts and records to
  it, and narrow the original persona's routing condition; **Not now** leaves
  everything as it is.
- `frontend/stories/Imports.stories.js` uses the shipped `gmail-import.js`
  cards/dialogs. Actual Google intake requires the separate personal Google Bridge
  and Hermes. Pending cards are outside persona processing, memory and search.
  Slide 7 shows both source cards and the Gmail decision dialog.
- Slide 13 separates browser and Alexa operations with numbered steps. The
  browser example shows the real composer: Speak starts browser recognition.
  With the English default send phrase, a final trailing over stops recognition,
  is removed from the note and calls the automatic posting path
  (`browser_speech.js`, `send_phrases.js`, `dictation.js`, `compose.js`).
  Saved custom or disabled send phrases are preserved. Manual Send remains
  available. The timeline's Read aloud control speaks
  the root post plus its conversation replies (`compose.js`, `feed.js` and
  `app.js`). The Alexa examples use the English invocation name `memory timeline` with the take-a-note,
  read-my-replies and read-my-schedule samples from `integrations/alexa/en-US.json`.
  Schedule reading covers saved kakeratta tasks, not a direct Google Calendar
  query. Opening the skill is shown as an optional entry point; the Yes intent reaches
  `confirmAlexaPost` in `internal/server/alexa.go`. It is an explanatory exchange,
  not an Alexa-device capture or a recorded transcript. Optional AI punctuation
  and line-break formatting is restricted to configured speech auto-send
  (`internal/server/dictation.go`); manual Send does not invoke that formatting.
  Browser speech support varies. Alexa requires its own operator configuration,
  account linking and device setup; proactive Alexa announcements are not shipped.
  The skill has passed Amazon certification. The developer has confirmed device invocation
  with the updated Japanese and English names.
- Slide 14's travel infographic is illustrative content from
  `infographic-example.html`, rendered with the real `renderInfographic` and
  Markdown/Mermaid renderer. It is not a captured live AI answer. The share
  dialog beside it uses the same Shizuoka conversation as slide 5.
- Slide 15's model roles follow the shipped Nebius configuration in
  `docs/system/personas.md`. Custom endpoints and model selections can differ.
  Costs and counts are synthetic examples, not production bills or benchmarks.
- Web Push requires support, device registration and task notification settings;
  iPhone/iPad requires Home Screen installation. The gallery shows the task UI,
  not an operating-system notification capture.

No application behavior changes are introduced by the gallery stories.

## Regeneration and checks

Requires Node.js, Python 3 with Pillow, Chromium, Lato, and Noto Sans CJK JP.
On Debian/Ubuntu the fonts/Pillow packages are `fonts-lato`, `fonts-noto-cjk`,
and `python3-pil`. The existing frontend dependencies must be installed.
From the repository root, take the gallery's English UI sources at 3×, apart
from the 2× documentation captures, then render:

```sh
CAPTURE_OUT="$PWD/tmp/hackathon-gallery/captures" CAPTURE_SCALE=3 CAPTURE_LANGS=en \
CAPTURE_ONLY=calendar-card,gallery-hero,gallery-month-summary,gallery-reply-trial,gallery-routing,gallery-search-form,gallery-search-results,gallery-share,gallery-trip-daily,gallery-trip-missed,gallery-trip-return,gallery-usage,gallery-vault,gmail-card,gmail-review,memory-after,memory-before,person-card,persona-suggestion,preparation-check,preparation-plan,reminder-done,review-question,schedules,suggested-definition,suggested-memory,suggested-persona,voice-compose,wine-intent \
npm --prefix frontend run captures
bash docs/hackathon/gallery/render.sh
```

`build.mjs` warns and falls back to `docs/captures/en` (2×) when the 3× set is
missing; the slides are then softer.

Use `CHROMIUM` for a different browser binary. `CAPTURE_SCRATCH` selects the
Storybook capture profile. Generated gallery HTML and temporary browser profiles
stay in the ignored `tmp/hackathon-gallery/` directory.

`build.mjs` defines the story, copy, source references and layouts. Crops are
given in app (CSS) pixels of each capture; by default a crop drops the
capture's 12px margin and keeps just the captured element. `render.mjs`
reuses a single headless browser, waits for fonts and images, rejects content
outside the slide or overlapping layout blocks, then writes each PNG. `render.sh`
first refreshes the illustrative infographic, runs the builder and renderer, then
calls `package.py` to check the fifteen-image limit, file set, dimensions and size,
compose the contact sheet and validate the ZIP.

For a layout-only change, keep the infographic source and render just selected
slide numbers:

```sh
GALLERY_ONLY=03,06 bash docs/hackathon/gallery/render.sh
```

If slide filenames are changed, retire the superseded upload PNGs before
packaging. The file-set check deliberately rejects stale images.
