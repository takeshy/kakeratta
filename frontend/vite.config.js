// The page stands on its own: Vite serves it in development and Firebase
// Hosting serves it in production. The Go binary is only what /api is, so that
// is the only thing proxied - the same split the deployed setup has.
import { defineConfig } from "vite";

export default defineConfig({
  server: {
    port: 5173,
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
