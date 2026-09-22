// Only public application assets are cached here. Private data lives in the
// account-scoped IndexedDB store; API responses never enter a shared cache.
const VERSION = "kakeratta-shell-dev";
const ASSETS = ["/", "/src/bootstrap.js", "/src/styles.css"];
self.addEventListener("install", event => {
  event.waitUntil(caches.open(VERSION).then(cache => cache.addAll(ASSETS)).then(() => self.skipWaiting()));
});
self.addEventListener("activate", event => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) if (key.startsWith("kakeratta-shell-") && key !== VERSION) await caches.delete(key);
    await self.clients.claim();
  })());
});
self.addEventListener("fetch", event => {
  const request = event.request;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (request.method !== "GET" || url.pathname.startsWith("/api/")) return;
  const path = url.pathname === "/index.html" ? "/" : url.pathname;
  if (!ASSETS.includes(path)) return;
  event.respondWith((async () => {
    const cache = await caches.open(VERSION);
    return await cache.match(path) || fetch(request);
  })());
});
self.addEventListener("message", event => {
  if (event.data === "version") event.source?.postMessage({ type: "version", version: VERSION });
});
