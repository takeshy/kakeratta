// Copies the static frontend into dist/ for go:embed. No bundler: the sources
// are plain ES modules and CSS, and the WebView loads them directly.
import { cpSync, mkdirSync, readdirSync, rmSync, statSync } from "node:fs";
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
// worker only controls the scope it is served from, and the share target has
// to be in scope. Vite (and so Storybook) serve the same directory at /.
cpSync(join(frontendDir, "index.html"), join(distDir, "index.html"));
cpSync(join(frontendDir, "public"), distDir, { recursive: true });
cpSync(join(frontendDir, "src"), join(distDir, "src"), { recursive: true });

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
