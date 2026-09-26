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
import { mkdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
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
  { name: "bluesky-connect", id: "bluesky--connect", clip: "#sheet", ready: ".bluesky-connection" },
  { name: "bluesky-selection", id: "bluesky--selection", clip: ".social-dialog", ready: ".social-selection" },
  { name: "bluesky-preview", id: "bluesky--preview", clip: ".social-dialog", ready: ".social-preview" },
  { name: "bluesky-posted", id: "bluesky--posted", clip: ".social-dialog", ready: ".social-preview" },
  { name: "memory", id: "personas--wine-memory", clip: ".memory-dialog", ready: ".memory-card" },
  { name: "search-keyword", id: "search--keyword", clip: "#sheet" },
  { name: "search-semantic", id: "search--semantic", clip: "#sheet" },
  { name: "search-thread", id: "search--thread", clip: "#sheet" },
  { name: "persona-create", id: "personas--ai-create-start", clip: ".dialog" },
  { name: "persona-draft", id: "personas--ai-create-draft", clip: ".dialog" },
  { name: "timeline", id: "screen--hero" },
  { name: "nobody", id: "screen--nobody-in-the-room" },
  { name: "personas", id: "personas--all-present", clip: "#sheet" },
  { name: "persona-planner", id: "personas--external-planner", clip: '.persona[data-id="book"]', ready: '.persona[data-id="book"] .switch input[type="checkbox"]' },
  { name: "settings", id: "settings--default", clip: "#sheet", open: ".settings-advanced" },
  { name: "hermes-settings", id: "settings--external-agent", clip: '[data-category="外部連携"]', ready: "#sheet", open: [".settings-advanced", '[data-category="外部連携"]'] },
  { name: "routing", id: "timeline--routing-opened", clip: ".routing-dialog", ready: ".routing-row" },
  { name: "condition-fix", id: "timeline--condition-proposal", clip: ".condition-fix", ready: ".condition-new" },
  { name: "reply-fix", id: "timeline--reply-fix-proposal", clip: ".dialog.reply-fix", ready: ".reply-instructions" },
  { name: "reply-trial", id: "timeline--reply-fix-trial", clip: ".dialog.reply-fix", ready: ".reply-accept" },
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
        const { resolve, reject } = this.pending.get(message.id);
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
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }
  on(method, listener) {
    if (!this.listeners.has(method)) this.listeners.set(method, []);
    this.listeners.get(method).push(listener);
  }
  close() { this.socket.close(); }
}

async function main() {
  const wanted = process.env.CAPTURE_ONLY ? process.env.CAPTURE_ONLY.split(",") : null;
  const storybook = spawn(join(here, "node_modules", ".bin", "storybook"),
    ["dev", "-p", String(port), "--ci", "--no-open"], { cwd: here, stdio: "ignore" });
  mkdirSync(profile, { recursive: true });
  const chromium = spawn(browser, [
    "--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
    `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profile}`, "about:blank",
  ], { stdio: "ignore" });
  let devtools;
  try {
    await waitFor(() => fetch(`http://127.0.0.1:${port}/index.json`).then((r) => r.ok), "Storybook", 60000);
    let version;
    await waitFor(async () => { version = await (await fetch(`http://127.0.0.1:${debugPort}/json/version`)).json(); return true; }, "Chromium");
    devtools = await Devtools.connect(version.webSocketDebuggerUrl);
    const { targetId } = await devtools.send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await devtools.send("Target.attachToTarget", { targetId, flatten: true });
    const page = (method, params) => devtools.send(method, params, sessionId);
    const evaluate = async (expression) => (await page("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true })).result.value;
    await page("Page.enable");
    await page("Runtime.enable");
    let loaded = null;
    devtools.on("Page.loadEventFired", (_, session) => { if (session === sessionId) loaded?.(); });

    for (const lang of ["ja", "en"]) {
      mkdirSync(join(out, lang), { recursive: true });
      for (const shot of shots.filter((shot) => !wanted || wanted.includes(shot.name))) {
        await page("Emulation.setDeviceMetricsOverride", {
          width: WIDTH, height: shot.clip ? TALL : 844, deviceScaleFactor: SCALE, mobile: true,
        });
        const arrived = new Promise((resolve) => { loaded = resolve; });
        await page("Page.navigate", { url: `http://127.0.0.1:${port}/iframe.html?id=${shot.id}&viewMode=story&globals=lang:${lang}` });
        await arrived;
        const ready = shot.ready || shot.clip || "#app";
        await waitFor(() => evaluate(`!!document.querySelector(${JSON.stringify(ready)})`), `${shot.name}: ${ready}`, 15000);
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
          const rect = await evaluate(`(() => { const r = document.querySelector(${JSON.stringify(shot.clip)}).getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; })()`);
          clip = {
            x: Math.max(0, rect.x - MARGIN), y: Math.max(0, rect.y - MARGIN),
            width: Math.min(WIDTH, rect.width + 2 * MARGIN), height: rect.height + 2 * MARGIN, scale: 1,
          };
        } else {
          // The screen ends where #app ends - its own bottom padding included -
          // rather than at the bottom of a viewport it did not fill.
          const height = await evaluate("Math.ceil(Math.max(document.querySelector('#app')?.getBoundingClientRect().bottom || 0, ...[...document.querySelectorAll('#app > *')].map((n) => n.getBoundingClientRect().bottom)) + 24)");
          clip = { x: 0, y: 0, width: WIDTH, height: Math.min(height, MAX_PAGE), scale: 1 };
        }
        const { data } = await page("Page.captureScreenshot", { format: "png", clip, captureBeyondViewport: true });
        writeFileSync(join(out, lang, `${shot.name}.png`), Buffer.from(data, "base64"));
        console.log(`${lang}/${shot.name}.png ${Math.round(clip.width)}x${Math.round(clip.height)}`);
      }
    }
  } finally {
    devtools?.close();
    chromium.kill("SIGTERM");
    storybook.kill("SIGTERM");
  }
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
