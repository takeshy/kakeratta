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

function localStore(operation) {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open("kakeratta-local", 1);
    request.onupgradeneeded = () => request.result.createObjectStore("records");
    request.onerror = () => reject(request.error);
    request.onsuccess = () => {
      const db = request.result;
      const tx = db.transaction("records", "readwrite");
      let result;
      operation(tx.objectStore("records"), value => { result = value; });
      tx.oncomplete = () => { db.close(); resolve(result); };
      tx.onabort = tx.onerror = () => { db.close(); reject(tx.error); };
    };
  });
}
async function saveShare(form) {
  const identity = await localStore((store, done) => { const q = store.get("device:identity"); q.onsuccess = () => done(q.result); });
  if (!identity?.key) return false;
  const file = form.get("image");
  let image = "";
  if (file?.size) {
    if (file.size > 20 * 1024 * 1024) throw Error("Image too large");
    const bytes = new Uint8Array(await file.arrayBuffer());
    let binary = "";
    for (let i = 0; i < bytes.length; i += 8192) binary += String.fromCharCode(...bytes.subarray(i, i + 8192));
    image = `data:${file.type || "image/png"};base64,${btoa(binary)}`;
  }
  const text = [form.get("title"), form.get("text")].filter(value => typeof value === "string" && value).join("\n");
  // Keep the shared URL as the post's link so the server reads the page.
  const link = typeof form.get("url") === "string" ? form.get("url").trim() : "";
  if (!text && !link && !image) throw Error("Empty share");
  const id = `local-${crypto.randomUUID()}`, at = new Date().toISOString();
  const item = { path: "/api/post", body: { text, link, image, clientId: id, clientAt: at },
    entry: { id, at, root: id, speaker: "user", kind: "post", text, link, localImage: image, pending: true } };
  await localStore((store) => {
    const q = store.get("device:identity");
    q.onsuccess = () => {
      if (q.result?.key !== identity.key) { store.transaction.abort(); return; }
      store.put(item, `${identity.key}:outbox:${id}`);
    };
  });
  return true;
}
self.addEventListener("fetch", event => {
  const request = event.request;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (request.method === "POST" && url.pathname === "/api/capture") {
    event.respondWith((async () => {
      try {
        const form = await request.formData();
        if (typeof indexedDB !== "undefined" && await saveShare(form)) return Response.redirect(new URL("/?shared=1", url.origin).href, 303);
        const response = await fetch("/api/capture", { method: "POST", body: form, credentials: "include" });
        return Response.redirect(new URL(response.ok ? "/?shared=1" : "/?shared=0", url.origin).href, 303);
      } catch { return Response.redirect(new URL("/?shared=0", url.origin).href, 303); }
    })());
    return;
  }
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
