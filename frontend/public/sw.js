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

// Check the active account before displaying or opening a reminder. A queued
// push from a previous login must not appear in another account's session.
async function reminderAccount(workspace) {
  try {
    const response = await fetch("/api/auth/identity", { cache: "no-store", signal: AbortSignal.timeout(10000) });
    return response.ok && (await response.json()).key === workspace;
  } catch { return false; }
}
self.addEventListener("push", event => {
  event.waitUntil((async () => {
    let data;
    try { data = event.data?.json(); } catch { return; }
    if (!data?.entry || !data.workspace || !await reminderAccount(data.workspace)) return;
    await self.registration.showNotification(data.title || "kakeratta", {
      ...(data.body ? { body: data.body } : {}), icon: "/icon.png", badge: "/icon.png",
      tag: `reminder:${data.entry}`, data: { entry: data.entry, workspace: data.workspace },
    });
  })());
});
self.addEventListener("notificationclick", event => {
  event.notification.close();
  event.waitUntil((async () => {
    const data = event.notification.data;
    if (!data?.entry || !await reminderAccount(data.workspace)) return;
    const target = new URL("/", self.location.origin);
    target.searchParams.set("entry", data.entry);
    const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    const app = windows.find(client => new URL(client.url).origin === target.origin && new URL(client.url).pathname === "/");
    if (app) { await app.navigate(target.href); await app.focus(); }
    else await self.clients.openWindow(target.href);
  })());
});
