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

Unverified:

- Whether the iOS shortcut route — take an image, POST it — actually holds up on
  a real device
- Whether Android's Web Share Target really appears when sharing a screenshot
  from the Kindle app
- Whether Token Factory serves vision, and what each Nemotron size can do
- OCR quality, particularly Japanese and Kindle's vertical text
- The reaction threshold. Too low is meddling; too high looks dead

## Layout

```
internal/timeline   one list of posts and remarks (cutting out a thread, writing it into a prompt)
internal/capture    reading an image once, and fetching a public page
internal/persona    skills, routing, the reaction loop (low→middle→high), the notes format handed over (skills/okf)
internal/tools      the tools only a declaration can reach
internal/websearch  search providers
internal/llm        an OpenAI-compatible client (images and file input included)
internal/speech     dictation backends (the relay that keeps the keys server-side)
internal/store      the append log, images, notes, settings
internal/server     the HTTP surface (the page, the API, SSE)
frontend/src        ES modules, no bundler
```

Dictation is the port from speech-popup (gemihub-desktop's speech feature),
carried over as it was. Three engines: the browser's own recogniser, a recording
sent to a speech API, and live transcription. The original tests are here too
(`node --test tests/*.test.js`), alongside regression tests for the app.

## If a native app were built

Not needed for now. If it ever moves that way, iOS would be a Share Extension
(a separate target, App Group or a direct POST) and Android an `ACTION_SEND`
intent-filter. Wails v3 could host both, but its mobile support is officially
experimental and outside the desktop beta's compatibility guarantee. It is not
in scope.
