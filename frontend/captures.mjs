// The captures in the README, taken from the stories.
//
// A story is the shipped modules over fixtures, so a capture of one is the app
// as it looks with nobody's real timeline in it - and it can be retaken after
// any change with `npm run captures`. One set per display language, because
// the README is in two.
import { execFileSync, spawn } from "node:child_process";
import { copyFileSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "docs", "captures");
const port = Number(process.env.CAPTURE_PORT || 6018);
const browser = process.env.CHROMIUM || "chromium";
// A sandboxed (snap) Chromium can only write under the home directory, so the
// shot lands there first and is copied into the repository.
const scratch = join(homedir(), ".cache", "kakeratta-captures");

const shots = [
  { name: "timeline", id: "screen--hero", height: 900 },
  { name: "nobody", id: "screen--nobody-in-the-room", height: 520 },
  { name: "personas", id: "personas--all-present", height: 1000 },
  { name: "settings", id: "settings--default", height: 1000 },
];

const storybook = spawn(join(here, "node_modules", ".bin", "storybook"),
  ["dev", "-p", String(port), "--ci", "--no-open"], { cwd: here, stdio: "ignore" });
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

try {
  let up = false;
  for (let i = 0; i < 120 && !up; i++) {
    up = await fetch(`http://127.0.0.1:${port}/index.json`).then((r) => r.ok).catch(() => false);
    if (!up) await sleep(500);
  }
  if (!up) throw new Error(`Storybook did not come up on :${port}`);
  mkdirSync(scratch, { recursive: true });
  for (const lang of ["ja", "en"]) {
    mkdirSync(join(out, lang), { recursive: true });
    for (const shot of shots) {
      const temp = join(scratch, `${lang}-${shot.name}.png`);
      execFileSync(browser, [
        "--headless", "--disable-gpu", "--no-sandbox", "--hide-scrollbars",
        "--virtual-time-budget=5000", `--window-size=760,${shot.height}`, `--screenshot=${temp}`,
        `http://127.0.0.1:${port}/iframe.html?id=${shot.id}&viewMode=story&globals=lang:${lang}`,
      ], { stdio: "ignore" });
      copyFileSync(temp, join(out, lang, `${shot.name}.png`));
      console.log(`${lang}/${shot.name}.png`);
    }
  }
} finally {
  storybook.kill("SIGTERM");
}
