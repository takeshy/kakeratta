import { createHash } from "node:crypto";
// Copies the static frontend into dist/ for go:embed. No bundler: the sources
// are plain ES modules and CSS, and the WebView loads them directly.
import { cpSync, mkdirSync, readdirSync, rmSync, statSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const frontendDir = dirname(fileURLToPath(import.meta.url));
const distDir = join(frontendDir, "dist");

mkdirSync(distDir, { recursive: true });
for (const entry of readdirSync(distDir)) {
  if (entry === ".gitkeep") continue;
  rmSync(join(distDir, entry), { recursive: true, force: true });
}

// public/ holds what has to sit at the site root - the manifest, the worker,
// the icons - because that is where the browser looks for them: a service
// worker only controls the scope it is served from. Vite (and so Storybook) serve the same directory at /.
cpSync(join(frontendDir, "index.html"), join(distDir, "index.html"));
cpSync(join(frontendDir, "public"), distDir, { recursive: true });
cpSync(join(frontendDir, "src"), join(distDir, "src"), { recursive: true });

// The landing page and the manual are pages beside the app, not part of it:
// plain HTML at /lp/ and /manual/, sharing one stylesheet and the language
// switch under /site/. Their screenshots are the README's, so they are copied
// from docs/ rather than kept twice.
for (const dir of ["lp", "manual", "privacy", "terms", "site"]) {
  cpSync(join(frontendDir, dir), join(distDir, dir), { recursive: true });
}
cpSync(join(frontendDir, "..", "docs", "captures"), join(distDir, "captures"), { recursive: true });

let totalBytes = 0;
const walk = (dir) => {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) walk(p);
    else totalBytes += statSync(p).size;
  }
};
walk(distDir);
console.log(`Copied frontend to ${distDir} (${totalBytes} bytes)`);

// Precache one complete, content-versioned application shell for offline starts.
const assets = ["/", "/manifest.webmanifest", "/icon.svg", "/icon.png", "/icon-maskable.png", ...readdirSync(join(distDir, "src")).filter(name => /\.(js|css)$/.test(name)).map(name => `/src/${name}`)];
const digest = createHash("sha256");
for (const asset of assets) digest.update(readFileSync(join(distDir, asset === "/" ? "index.html" : asset.slice(1))));
const workerPath = join(distDir, "sw.js");
digest.update(readFileSync(workerPath));
const version = `kakeratta-shell-${digest.digest("hex").slice(0,16)}`;
writeFileSync(workerPath, readFileSync(workerPath, "utf8").replace('"kakeratta-shell-dev"', JSON.stringify(version)).replace(/const ASSETS = .*?;/, `const ASSETS = ${JSON.stringify(assets)};`));
