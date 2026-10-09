// The captures in the README, the manual and the landing page, taken from the
// stories.
//
// A story is the shipped modules over fixtures, so a capture of one is the app
// as it looks with nobody's real timeline in it - and it can be retaken after
// any change with `npm run captures`. One set per display language, because
// the README is in two.
//
// They are taken as a phone sees the app: a 390px-wide viewport at 2x, so the
// text stays crisp when the image is shown at phone size. A screen is captured
// to the bottom of its content; a sheet or a dialog is captured as just that
// element, so the picture is the thing and not the space around it. Chromium is
// driven over its DevTools protocol, which is what makes both possible.
import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "docs", "captures");
const port = Number(process.env.CAPTURE_PORT || 6018);
const debugPort = Number(process.env.CAPTURE_DEBUG_PORT || 9333);
const browser = process.env.CHROMIUM || "chromium";
// A sandboxed (snap) Chromium can only write under the home directory, so its
// profile lives there.
const profile = process.env.CAPTURE_SCRATCH || join(homedir(), ".cache", "kakeratta-captures", "profile");

export const WIDTH = 390;
const SCALE = 2;
// A screen is captured to the bottom of its content, but not without end.
const MAX_PAGE = 2400;
// A sheet or dialog is measured in a viewport tall enough that it never has to
// scroll inside itself, then clipped to its own box.
const TALL = 4000;
const MARGIN = 12;

// clip: the element that is the picture; without it, the whole screen. ready:
// something that appears once the story has finished arriving.
const shots = [
  { name: "search-period", id: "search--date-range", clip: "#sheet", ready: ".search-hit" },
  { name: "speaker-period", id: "timeline--speaker-filters", clip: "#sheet", ready: ".speaker-view .thread" },
  { name: "favorites", id: "timeline--favorites-only", clip: "#app", ready: ".entry-filters" },
  { name: "search-favorites", id: "search--favorites-only", clip: "#sheet", ready: ".search-hit" },
  { name: "bluesky-connect", id: "bluesky--connect", clip: "#sheet", ready: ".bluesky-connection" },
  { name: "bluesky-selection", id: "bluesky--selection", clip: ".social-dialog", ready: ".social-selection" },
  { name: "bluesky-answer-only", id: "bluesky--answer-only", clip: ".social-dialog", ready: ".social-preview-post" },
  { name: "bluesky-preview", id: "bluesky--preview", clip: ".social-dialog", ready: ".social-preview" },
  { name: "bluesky-posted", id: "bluesky--posted", clip: ".social-dialog", ready: ".social-preview" },
  { name: "memory", id: "personas--wine-memory", clip: ".memory-dialog", ready: ".memory-card" },
  { name: "search-keyword", id: "search--keyword", clip: "#sheet" },
  { name: "search-semantic", id: "search--semantic", clip: "#sheet" },
  { name: "search-thread", id: "search--thread", clip: "#sheet" },
  { name: "timeline-speaker", id: "timeline--speaker", clip: "#sheet", ready: ".speaker-view .thread" },
  { name: "persona-create", id: "personas--ai-create-start", clip: ".dialog" },
  { name: "persona-draft", id: "personas--ai-create-draft", clip: ".dialog" },
  { name: "timeline", id: "screen--hero" },
  { name: "offline", id: "screen--offline", ready: ".sync-status" },
  { name: "nobody", id: "screen--nobody-in-the-room" },
  { name: "personas", id: "personas--all-present", clip: "#sheet" },
  { name: "persona-usage", id: "personas--with-usage", clip: '.persona[data-id="book"]', ready: ".usage-table" },
  { name: "persona-planner", id: "personas--external-planner", clip: '.persona[data-id="book"]', ready: '.persona[data-id="book"] .planner-option input[type="checkbox"]' },
  { name: "settings", id: "settings--default", clip: "#sheet", open: ".settings-advanced" },
  { name: "settings-usage", id: "settings--with-usage", clip: ".settings-usage", ready: ".usage-table" },
  { name: "profile-emoji", id: "settings--default", clip: ".avatar-field", ready: ".avatar-emoji-grid .avatar-emoji", open: ".avatar-emoji-picker" },
  { name: "profile-photo", id: "settings--profile-photo", clip: ".avatar-field", ready: ".avatar-preview .avatar-photo img" },
  { name: "hermes-settings", id: "settings--external-agent", clip: '[data-category="外部連携"]', ready: "#sheet", open: [".settings-advanced", '[data-category="外部連携"]'] },
  { name: "routing", id: "timeline--routing-opened", clip: ".routing-dialog", ready: ".routing-row" },
  { name: "condition-fix", id: "timeline--condition-proposal", clip: ".condition-fix", ready: ".condition-new" },
  { name: "reply-fix", id: "timeline--reply-fix-proposal", clip: ".dialog.reply-fix", ready: ".reply-instructions" },
  { name: "reply-trial", id: "timeline--reply-fix-trial", clip: ".dialog.reply-fix", ready: ".reply-accept" },
  { name: "person-edit", id: "personas--person-edit", clip: ".persona-editor", ready: ".persona-editor" },
  { name: "person-card", id: "personas--person-card", clip: '.persona[data-id="sato"]' },
  { name: "people", id: "personas--people-by-name", clip: "#sheet", ready: '.persona[data-id="mother"]' },
  { name: "llm-connect-hub", id: "timeline--external-agent-vault", clip: ".thread", ready: ".model-badge" },
  { name: "month-summary", id: "timeline--month-summary", clip: ".thread", ready: ".post.summary" },
  { name: "person-draft", id: "personas--person-create-draft", clip: ".dialog", ready: ".persona-builder .builder-detail" },
  { name: "person-routing", id: "timeline--person-routing", clip: ".routing-dialog", ready: ".routing-row" },
  { name: "person-keywords", id: "timeline--person-keyword-proposal", clip: ".condition-fix", ready: ".condition-new" },
  { name: "person-words", id: "timeline--person-words-remove", clip: ".condition-fix", ready: ".condition-word" },
];

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitFor(check, what, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await check().catch(() => false)) return true;
    await sleep(250);
  }
  throw new Error(`${what} did not come up`);
}

// A small DevTools client: one socket, numbered requests, events by name.
class Devtools {
  constructor(socket) {
    this.socket = socket;
    this.next = 1;
    this.pending = new Map();
    this.listeners = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (message.id && this.pending.has(message.id)) {
        const { resolve, reject, timer } = this.pending.get(message.id);
        clearTimeout(timer);
        this.pending.delete(message.id);
        if (message.error) reject(new Error(message.error.message));
        else resolve(message.result);
        return;
      }
      for (const listener of this.listeners.get(message.method) || []) listener(message.params, message.sessionId);
    });
  }
  static async connect(url) {
    const socket = new WebSocket(url);
    await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
    return new Devtools(socket);
  }
  send(method, params = {}, sessionId) {
    const id = this.next++;
    this.socket.send(JSON.stringify({ id, method, params, sessionId }));
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`${method} timed out`)); }, 30000);
      this.pending.set(id, { resolve, reject, timer });
    });
  }
  on(method, listener) {
    if (!this.listeners.has(method)) this.listeners.set(method, []);
    this.listeners.get(method).push(listener);
  }
  close() { this.socket.close(); }
}

async function main() {
  const wanted = process.env.CAPTURE_ONLY ? process.env.CAPTURE_ONLY.split(",") : null;
  const buildDir = mkdtempSync(join(tmpdir(), "kakeratta-storybook-captures-"));
  const storybook = createServer(async (request, response) => {
    const pathname = new URL(request.url, "http://127.0.0.1").pathname;
    const file = resolve(buildDir, pathname === "/" ? "index.html" : decodeURIComponent(pathname).slice(1));
    if (!file.startsWith(buildDir + sep)) { response.writeHead(404); response.end(); return; }
    try {
      const data = await readFile(file);
      const type = file.endsWith(".js") ? "text/javascript" : file.endsWith(".css") ? "text/css" : file.endsWith(".json") ? "application/json" : file.endsWith(".png") ? "image/png" : file.endsWith(".svg") ? "image/svg+xml" : "text/html";
      response.setHeader("Content-Type", type);
      response.end(data);
    } catch { response.writeHead(404); response.end(); }
  });
  let chromium;
  let devtools;
  try {
    const build = spawn(join(here, "node_modules", ".bin", "storybook"),
      ["build", "--output-dir", buildDir, "--disable-telemetry"], { cwd: here, stdio: "ignore" });
    await new Promise((resolve, reject) => {
      build.once("error", reject);
      build.once("exit", code => code === 0 ? resolve() : reject(new Error(`Storybook build failed (${code})`)));
    });
    await new Promise((resolve, reject) => { storybook.once("error", reject); storybook.listen(port, "127.0.0.1", resolve); });
    mkdirSync(profile, { recursive: true });
    chromium = spawn(browser, [
      "--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--hide-scrollbars",
      `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profile}`, "about:blank",
    ], { stdio: "ignore" });
    await waitFor(() => fetch(`http://127.0.0.1:${port}/index.json`).then((r) => r.ok), "Storybook", 60000);
    let version;
    await waitFor(async () => { version = await (await fetch(`http://127.0.0.1:${debugPort}/json/version`)).json(); return true; }, "Chromium");
    devtools = await Devtools.connect(version.webSocketDebuggerUrl);
    for (const lang of ["ja", "en"]) {
      mkdirSync(join(out, lang), { recursive: true });
      for (const shot of shots.filter((shot) => !wanted || wanted.includes(shot.name))) {
        // A fresh tab avoids stale lifecycle state between stories and languages.
        const { targetId } = await devtools.send("Target.createTarget", { url: "about:blank" });
        const { sessionId } = await devtools.send("Target.attachToTarget", { targetId, flatten: true });
        const page = (method, params) => devtools.send(method, params, sessionId);
        const evaluate = async (expression) => (await page("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true })).result.value;
        await page("Page.enable");
        await page("Runtime.enable");
        await page("Page.bringToFront");
        try {
          await page("Emulation.setDeviceMetricsOverride", {
            width: WIDTH, height: shot.clip ? TALL : 844, deviceScaleFactor: SCALE, mobile: true,
          });
          await page("Page.navigate", { url: `http://127.0.0.1:${port}/iframe.html?id=${shot.id}&viewMode=story&globals=lang:${lang}` });
          const ready = shot.ready || shot.clip || "#app";
          try { await waitFor(() => evaluate(`!!document.querySelector(${JSON.stringify(ready)})`), `${shot.name}: ${ready}`, 15000); }
          catch (error) {
            const text = await evaluate("document.body.innerText.slice(0, 3000)").catch(() => "Page did not respond");
            throw new Error(`${lang}/${shot.name}: ${error.message}; ${text}`, { cause: error });
          }
          await evaluate("document.fonts.ready.then(() => true)");
          // Fixtures arrive over a stubbed fetch and images decode; a beat for both.
          await sleep(700);
          if (shot.open) {
            for (const selector of [shot.open].flat()) {
              await evaluate(`document.querySelector(${JSON.stringify(selector)})?.setAttribute("open", "")`);
              await sleep(100);
            }
            // A section that grows can carry the page with it, and the clip is
            // measured against the viewport: start from the top either way.
            await evaluate("window.scrollTo(0, 0)");
            await sleep(100);
          }
          let clip;
          if (shot.clip) {
            // Bottom-anchored sheets can extend a screenshot past the viewport.
            // Move only their capture container; the shipped content keeps its layout.
            await evaluate(`(() => {
              const sheet = document.querySelector(${JSON.stringify(shot.clip)})?.closest("#sheet");
              if (sheet) { sheet.style.top = "12px"; sheet.style.bottom = "auto"; }
              return true;
            })()`);
            await sleep(150);
            const rect = await evaluate(`(() => { const r = document.querySelector(${JSON.stringify(shot.clip)}).getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; })()`);
            const x = Math.max(0, Math.floor(rect.x - MARGIN));
            const y = Math.max(0, Math.floor(rect.y - MARGIN));
            clip = {
              x, y, width: Math.min(WIDTH - x, Math.ceil(rect.width + 2 * MARGIN)),
              height: Math.min(TALL - y, Math.ceil(rect.height + 2 * MARGIN)), scale: 1,
            };
          } else {
            // The screen ends where #app ends - its own bottom padding included -
            // rather than at the bottom of a viewport it did not fill.
            const height = await evaluate("Math.ceil(Math.max(document.querySelector('#app')?.getBoundingClientRect().bottom || 0, ...[...document.querySelectorAll('#app > *')].map((n) => n.getBoundingClientRect().bottom)) + 24)");
            clip = { x: 0, y: 0, width: WIDTH, height: Math.min(height, MAX_PAGE), scale: 1 };
          }
          let data;
          if (!shot.clip) {
            await page("Emulation.setDeviceMetricsOverride", {
              width: WIDTH, height: clip.height, deviceScaleFactor: SCALE, mobile: true,
            });
            await sleep(150);
          }
          try { ({ data } = await page("Page.captureScreenshot", { format: "png", clip, captureBeyondViewport: false })); }
          catch (error) { throw new Error(`${lang}/${shot.name}: ${error.message}; clip=${JSON.stringify(clip)}`, { cause: error }); }
          writeFileSync(join(out, lang, `${shot.name}.png`), Buffer.from(data, "base64"));
          console.log(`${lang}/${shot.name}.png ${Math.round(clip.width)}x${Math.round(clip.height)}`);
        } finally { await devtools.send("Target.closeTarget", { targetId }); }
      }
    }
  } finally {
    devtools?.close();
    chromium?.kill("SIGTERM");
    storybook.closeAllConnections();
    await new Promise(resolve => storybook.close(resolve));
    rmSync(buildDir, { recursive: true, force: true });
  }
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
