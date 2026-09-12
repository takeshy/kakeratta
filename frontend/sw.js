// The service worker exists for one reason: the share target.
//
// Android posts the shared image to /api/capture as a form. The worker takes
// that request, forwards it with the session's credentials, and then opens the
// page - so a screenshot shared from Kindle lands in the timeline without the
// page ever having been open.
//
// It deliberately does not cache the app. The timeline is the point, and a
// stale one is worse than waiting a moment for the real one.
const VERSION = "kakeratta-1";

self.addEventListener("install", (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== "POST" || url.pathname !== "/api/capture") return;

  event.respondWith(
    (async () => {
      try {
        const form = await request.formData();
        const response = await fetch("/api/capture", {
          method: "POST",
          body: form,
          credentials: "include",
        });
        // Whether it worked or not, the user is sent to the timeline: that is
        // where they will see it landed, or see that it did not.
        const ok = response.ok;
        return Response.redirect(new URL(ok ? "/?shared=1" : "/?shared=0", url.origin).href, 303);
      } catch {
        return Response.redirect(new URL("/?shared=0", url.origin).href, 303);
      }
    })(),
  );
});

self.addEventListener("message", (event) => {
  if (event.data === "version") event.source?.postMessage({ type: "version", version: VERSION });
});
