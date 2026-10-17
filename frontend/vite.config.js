// The page stands on its own: Vite serves it in development and Firebase
// Hosting serves it in production. The Go binary is only what /api is, so that
// is the only thing proxied - the same split the deployed setup has.
import { createReadStream, existsSync, statSync } from "node:fs";
import { join, normalize, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

// /lp/ and /manual/ are directories in the project root, which Vite serves as
// is. Two things Hosting does for them that Vite does not: send /lp to /lp/,
// and serve the screenshots, which live in docs/captures and are copied to
// dist/captures by build.mjs.
const capturesDir = fileURLToPath(new URL("../docs/captures/", import.meta.url));
function sitePages() {
  return {
    name: "kakeratta-site-pages",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const url = new URL(req.url, "http://localhost");
        if (["/lp", "/manual", "/privacy", "/terms"].includes(url.pathname)) {
          res.statusCode = 301;
          res.setHeader("Location", `${url.pathname}/${url.search}`);
          res.end();
          return;
        }
        if (url.pathname.startsWith("/captures/")) {
          const relative = normalize(decodeURIComponent(url.pathname.slice("/captures/".length)));
          const file = join(capturesDir, relative);
          if (!relative.startsWith("..") && file.startsWith(capturesDir.slice(0, -1) + sep) && existsSync(file) && statSync(file).isFile()) {
            res.setHeader("Content-Type", "image/png");
            createReadStream(file).pipe(res);
            return;
          }
        }
        next();
      });
    },
  };
}

export default defineConfig({
  plugins: [sitePages()],
  server: {
    host: "0.0.0.0",
    port: 15173,
    strictPort: true,
    // dist/ is the Hosting copy that build.mjs writes; rebuilding it is not a
    // change to the page being served here.
    watch: { ignored: ["**/dist/**", "**/storybook-static/**"] },
    proxy: {
      "/api": {
        target: process.env.KAKERATTA_API || "http://127.0.0.1:8787",
        // Reactions arrive over SSE, which must not be buffered on the way.
        configure: (proxy) => {
          proxy.on("proxyRes", (proxyRes) => {
            if (proxyRes.headers["content-type"]?.includes("text/event-stream")) {
              proxyRes.headers["cache-control"] = "no-cache";
            }
          });
        },
      },
    },
  },
  // `npm run build` stays build.mjs: what ships is the same unbundled ES
  // modules the browser loads here.
  build: { outDir: "dist", emptyOutDir: false },
});
