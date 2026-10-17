# Hackathon image gallery

Fifteen English presentation images for the kakeratta Hackathon entry.
Each upload image is a 1800 × 1200 PNG (3:2), under 5 MB.

Upload the numbered PNGs in [`images/`](images/) in filename order, or extract
[`kakeratta-hackathon-gallery.zip`](kakeratta-hackathon-gallery.zip) first.
The ZIP contains only the fifteen upload images.

- [Browse full-size images and captions](preview.html)
- [See the contact sheet](overview.png) (review only; not an upload image)
- [Copy the titles and captions](captions.json)

| Order | Image | Subject |
| --- | --- | --- |
| 1 | `01-remember-what-matters.png` | Personal AI timeline |
| 2 | `02-the-right-moment.png` | Bringing a forgotten plan back |
| 3 | `03-personas-for-your-life.png` | People and interest personas |
| 4 | `04-memory-you-control.png` | Inspecting and correcting memory |
| 5 | `05-routing-and-feedback.png` | Routing and reply feedback |
| 6 | `06-find-the-fragment.png` | Search by meaning |
| 7 | `07-connected-agents.png` | Obsidian and Hermes over MCP |
| 8 | `08-powered-by-nemotron.png` | Models and execution flow |
| 9 | `09-monthly-reflection.png` | A month of fragments, connected |
| 10 | `10-save-useful-answers.png` | Keep useful posts and answers in favorites |
| 11 | `11-offline-fragments.png` | Capture a thought even while offline |
| 12 | `12-share-your-selection.png` | Preview exactly what you share to Bluesky |
| 13 | `13-understand-ai-usage.png` | Persona costs and activity, alongside the overall total |
| 14 | `14-turn-notes-into-infographics.png` | Turn notes and conversations into infographics |
| 15 | `15-remember-your-people.png` | Keep the small details about your people |

## Sources and regeneration

The layouts use existing English UI captures from `docs/captures/en/`, produced
by the app's Storybook stories. Source images are listed in `captions.json`.
Image 14 uses a curated English travel example rendered by the application’s actual
`renderInfographic` and Markdown/Mermaid renderer; its reproducible source is
`infographic-example.html`. It is illustrative content, not a captured production
AI response.
CSS crops and scales those images without changing their UI text. The memory
section headings follow the English display language.
All added titles and explanatory copy are English. No demo-data label appears
on the gallery images.

The execution diagram was checked against `docs/system/personas.md` and
`docs/system/architecture.md`; the agent description follows
`docs/system/integrations.md`. These are presentation assets and do not change
application behavior.

Requires Node.js, Python 3 with Pillow, Chromium, Lato, and Noto Sans CJK JP. On Debian/Ubuntu, install `fonts-lato`, `fonts-noto-cjk`, and `python3-pil` before capturing. The application font stack explicitly includes Noto Sans CJK JP so Japanese captures use sans-serif glyphs. Retake the UI sources with `mise exec -- npm --prefix frontend run captures` after installing fonts or changing the UI. From the repository root:

```sh
bash docs/hackathon/gallery/render.sh
```

Set `CHROMIUM` to use a different Chromium binary. Generated HTML and the
browser profile stay in the ignored `tmp/hackathon-gallery/` directory.
`build.mjs` controls the layout and copy; `render.sh` exports it;
`package.py` checks dimensions, upload count and file sizes, builds the contact sheet with Pillow, and packages the upload images.
The contact sheet uses the rendered slide PNGs, keeping its memory use lower than rendering all fifteen full layouts together.
