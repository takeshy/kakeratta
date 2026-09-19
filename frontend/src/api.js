import { verifyIdentity } from "./auth.js";
import { localIdentity, localRecord, localList } from "./local-store.js";
import { createOutbox } from "./outbox.js";
// The only place that knows how to reach the relay.
//
// Everything goes through the relay: the keys are there, the reactions are
// generated there, and the page never talks to a model directly.
const listeners = new Set();
let stream = null;
let backoff = 1000;

async function call(method, path, body) {
  const user = localIdentity()?.key;
  if (user) await verifyIdentity();
  // Publishing and a persona's trial turn are the two calls that legitimately
  // take minutes; everything else should be back well inside 45 seconds.
  const slow = path === "/api/social/publish" || path.endsWith("/reply/preview");
  const options = { method, headers: {}, signal: AbortSignal.timeout(slow ? 250000 : 45000) };
  if (user) options.headers["X-Kakeratta-Workspace"] = user;
  if (body !== undefined) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  const response = await fetch(path, options);
  const text = await response.text();
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    payload = null;
  }
  if (!response.ok) {
    const error = new Error(payload?.error || `${response.status} ${response.statusText}`);
    error.status = response.status; throw error;
  }
  if (user && localIdentity()?.key !== user) throw Error("Account changed");
  return payload;
}

// The live channel. Reactions arrive late by design, so the page is told rather
// than asking: it reconnects on its own and reloads when it comes back.
let connecting = false;
async function connect() {
  if (stream || connecting) return;
  connecting = true;
  try {
    if (localIdentity()) await verifyIdentity();
    const key = localIdentity()?.key;
    stream = new EventSource(key ? `/api/events?workspace=${encodeURIComponent(key)}` : "/api/events");
  } catch { setTimeout(connect, 10000); return; }
  finally { connecting = false; }
  stream.onopen = () => {
    backoff = 1000;
    emit("open", null);
  };
  stream.onmessage = (event) => {
    try {
      const payload = JSON.parse(event.data);
      emit(payload.name, payload.data);
    } catch {
      /* a malformed frame is not worth breaking the stream over */
    }
  };
  stream.onerror = () => {
    stream?.close();
    stream = null;
    emit("closed", null);
    setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, 30000);
  };
}

function emit(name, data) {
  for (const listener of listeners) listener(name, data);
}

export const api = {
  mcpConnection: () => call("GET", "/api/mcp/connection"),
  issueMCPKey: () => call("POST", "/api/mcp/connection"),
  revokeMCPKey: () => call("DELETE", "/api/mcp/connection"),
  blueskyConnection: () => call("GET", "/api/social/bluesky/connection"),
  connectBluesky: (body) => call("POST", "/api/social/bluesky/connect", body),
  finishBluesky: (body) => call("POST", "/api/social/bluesky/finish", body),
  disconnectBluesky: () => call("DELETE", "/api/social/bluesky/connection"),
  previewSocial: (body) => call("POST", "/api/social/preview", body),
  publishSocial: (body) => call("POST", "/api/social/publish", body),
  timeline: ({ limit = 20, before } = {}) => {
    const query = new URLSearchParams({ limit: String(limit) });
    if (before) query.set("before", before);
    return call("GET", `/api/timeline?${query}`);
  },
  post: (body) => localIdentity() ? outbox.enqueue("/api/post", body) : call("POST", "/api/post", body),
  searchTimeline: (body) => call("POST", "/api/timeline/search", body),
  thread: (id) => call("GET", `/api/thread/${encodeURIComponent(id)}`),
  reply: async (replyTo, text, speechAutoSend = false) => {
    const body = { replyTo, text, speechAutoSend };
    if (!localIdentity()) return call("POST", "/api/reply", body);
    const local = [...(await localRecord("timeline").catch(() => [] ) || []), ...await localList("ack:"), ...(await outbox.entries()).map(item => item.entry)];
    const parent = local.find(e => e.id === replyTo) || (await call("GET", `/api/thread/${encodeURIComponent(replyTo)}`)).entries?.find(e => e.id === replyTo);
    if (!parent) throw Error("返事の宛先が見つかりません");
    return outbox.enqueue("/api/reply", body, parent);
  },
  remove: (id) => call("DELETE", `/api/entry/${encodeURIComponent(id)}`),
  edit: (id, text) => call("PATCH", `/api/entry/${encodeURIComponent(id)}`, { text }),
  routing: (id) => call("GET", `/api/entry/${encodeURIComponent(id)}/routing`),
  checkRouting: (id, persona) => call("POST", `/api/entry/${encodeURIComponent(id)}/routing/check`, { persona }),
  previewReply: (id, persona) => call("POST", `/api/entry/${encodeURIComponent(id)}/reply/preview`, { persona }),
  fetchURL: (url) => call("POST", "/api/fetch", { url }),

  buildPersona: (body) => call("POST", "/api/personas/build", body),
  savePersonaDraft: (skill, id) => call(id ? "PUT" : "POST", id ? `/api/personas/${encodeURIComponent(id)}` : "/api/personas/draft", skill),
  personas: () => call("GET", "/api/personas"),
  setDailyCheckIn: (id, schedule) => call("PUT", `/api/personas/${encodeURIComponent(id)}/daily`, schedule),
  setActive: (active) => call("PUT", "/api/personas", { active }),
  installPersona: async (definition) => {
    const response = await fetch("/api/personas/install", { method: "POST", body: definition });
    const payload = await response.json().catch(() => null);
    if (!response.ok) throw new Error(payload?.error || "追加できませんでした");
    return payload;
  },
  uninstallPersona: (id) => call("DELETE", `/api/personas/${encodeURIComponent(id)}`),
  memory: (id) => call("GET", `/api/personas/${encodeURIComponent(id)}/memory`),
  forgetNote: (id, path) => call("DELETE", `/api/personas/${encodeURIComponent(id)}/memory/note?${new URLSearchParams({ path })}`),
  editNote: (id, path, text) => call("PUT", `/api/personas/${encodeURIComponent(id)}/memory/note?${new URLSearchParams({ path })}`, { text }),
  forget: (id) => call("DELETE", `/api/personas/${encodeURIComponent(id)}/memory`),

  checkSearch: () => call("POST", "/api/search/check"),
  settings: () => call("GET", "/api/settings"),
  startExport: () => call("POST", "/api/export"),
  saveSettings: (settings) => call("PUT", "/api/settings", settings),
  models: () => call("GET", "/api/models"),
  openRouterModels: () => call("GET", "/api/openrouter/models"),
  about: () => call("GET", "/api/about"),

  on(listener) {
    listeners.add(listener);
    connect();
    return () => listeners.delete(listener);
  },
};

export const blobURL = (id) => `/api/blob/${encodeURIComponent(id)}`;

export const outbox = createOutbox({
  verify: () => verifyIdentity(true),
  send: (path, body) => call("POST", path, body),
  changed: async (saved) => { emit("outbox", saved); },
});
export async function pendingEntries() {
  return [...await localList("ack:"), ...(await outbox.entries()).map(item => item.entry)];
}
