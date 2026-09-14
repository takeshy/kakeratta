# Development and status

[README](../README.md) · [日本語](development_ja.md)

## Validation

```sh
task test  # Go and JavaScript tests
```

## Where it stands

Done: the share-sheet entry point (PWA share_target / iOS shortcut); posting
(capture, paste, URL, speech); reading the image once; the timeline and replies;
routing; personas' notes and remarks; the low→middle→high exchange; place and
time; tools (web search, places, routes); read-aloud.

**The entry point was built first**, because it is the only part that either
works or does not. The notes machinery and the reaction threshold degrade
gracefully if they disappoint; if the entry point does not work, the design
changes. That it works is pinned by `internal/server/server_test.go`, and the
order of the exchange by `internal/persona/runner_test.go`.

GLM-5.3-Flash image reading and JSON output were verified through the Token Factory API with a Japanese screenshot.

Unverified:

- Whether the iOS shortcut route — take an image, POST it — actually holds up on
  a real device
- Whether Android's Web Share Target really appears when sharing a screenshot
  from the Kindle app
- The quality of Nano, Super, and Ultra decisions across different persona domains
- OCR quality, particularly Japanese and Kindle's vertical text
- The reaction threshold. Too low is meddling; too high looks dead

## Layout

```
internal/timeline   one list of posts and remarks (cutting out a thread, writing it into a prompt)
internal/capture    reading an image once, and fetching a public page
internal/persona    skills, routing, the reaction loop (low→middle→high), the notes format handed over (skills/okf)
internal/tools      the tools only a declaration can reach
internal/websearch  search providers
internal/llm        Nebius Token Factory inference client (images and file input included)
internal/speech     legacy dictation code (not exposed by the server)
internal/store      the append log, images, notes, settings
internal/server     the HTTP surface (the page, the API, SSE)
frontend/src        ES modules, no bundler in what ships (builtins.js glosses the Japanese-named personas for the English interface)
frontend/public     what sits at the site root: manifest, service worker, icons
frontend/lp         the landing page (/lp/), Japanese and English in one document
frontend/manual     the manual (/manual/), which the app's header opens in a new tab
frontend/site       what the two pages share: a stylesheet and the language switch
frontend/stories    Storybook stories, built from the same modules and fixtures shaped like the API
frontend/dist       what Firebase Hosting deploys (a copy of the above)
```

## The frontend

The page is a static site of its own; the Go binary does not embed or serve it.
`frontend/src` is plain ES modules and one stylesheet, and that is also what
ships: `npm run build` (`node build.mjs`) copies them to `frontend/dist` for
Firebase Hosting, with no bundler in between.

Development uses Vite only as a server. `npm run dev` (or `task dev`, which also
starts the API) serves the page on :5173 and proxies `/api` to the binary on
:8787 - the same split Hosting makes with its `/api/**` rewrite, so nothing has
to differ between the two. A page-only change ships without touching the image.

`npm run storybook` (or `task stories`) opens Storybook. The stories in
`frontend/stories` render the real modules - `createRoom`, `createCompose`,
`createFeed`, `createPersonas`, `createSettings`, the screen assembled by
`screenNodes` and the sheet by `sheetNodes` - against fixtures with the same
shape the API returns. The panels load themselves from `/api`, so their stories
hand them a `fetch` that answers from those fixtures (`stories/api.js`). What is
reviewed there is the thing that ships. A toolbar switches the display language, since both
come out of the same source strings. Vite and Storybook are dev dependencies
only and never reach `dist/`.

The captures in the README (`docs/captures/{en,ja}/`) are taken from those
stories by `npm run captures` (`task captures`): a Storybook is started, each
listed story is screenshotted in both languages with headless Chromium, and the
result is copied into `docs/captures/`. Retake them after a visible change.

Dictation uses the browser recogniser adapted from speech-popup. Recorded and live API engines are no longer exposed. Automatic sends can optionally polish text with the low model before persistence; manual sends bypass polishing. Regression tests run with `node --test tests/*.test.js` and `go test ./...`.

## If a native app were built

Not needed for now. If it ever moves that way, iOS would be a Share Extension
(a separate target, App Group or a direct POST) and Android an `ACTION_SEND`
intent-filter. Wails v3 could host both, but its mobile support is officially
experimental and outside the desktop beta's compatibility guarantee. It is not
in scope.

## Bluesky integration and captures

Go 1.26 or later is required. `internal/social` handles OAuth, publication planning, and publishing; `frontend/src/bluesky.js` and `social.js` provide connection, selection, and preview screens. Tests use a simulated API. Authorization and publishing with a real account remain unverified.

`frontend/stories/Bluesky.stories.js` renders the real connection, selection, and preview screens with sample data. It does not authorize or publish externally. To retake just the relevant images in both languages:

```sh
CAPTURE_ONLY=bluesky-connect,bluesky-selection,bluesky-preview,settings,timeline,search-thread npm --prefix frontend run captures
npm --prefix frontend run build
```

## Browser storage and offline validation

`local-store.js` provides per-user IndexedDB storage; `outbox.js` manages pending submissions. The cache holds the latest 20 original posts and their replies; unsent posts are separate. The server’s atomic `CreateEntry` allows each post ID to be created only once. The service worker caches public application assets, never API responses.

The browser check below uses a local fixture API to verify offline startup, posting, reload, and one-time publication after reconnecting. It also updates `docs/captures/*/offline.png` in both languages. Requires Node.js 22 or later and Chromium (override its executable with `CHROMIUM`).

```sh
npm --prefix frontend run build
npm --prefix frontend run test:offline
npm --prefix frontend run build
```
