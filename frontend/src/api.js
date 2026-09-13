// The only place that knows how to reach the relay.
//
// Everything goes through the relay: the keys are there, the reactions are
// generated there, and the page never talks to a model directly.
const listeners = new Set();
let stream = null;
let backoff = 1000;

async function call(method, path, body) {
  const options = { method, headers: {} };
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
    throw new Error(payload?.error || `${response.status} ${response.statusText}`);
  }
  return payload;
}

// The live channel. Reactions arrive late by design, so the page is told rather
// than asking: it reconnects on its own and reloads when it comes back.
function connect() {
  if (stream) return;
  stream = new EventSource("/api/events");
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
  timeline: ({ limit = 20, before } = {}) => {
    const query = new URLSearchParams({ limit: String(limit) });
    if (before) query.set("before", before);
    return call("GET", `/api/timeline?${query}`);
  },
  post: (body) => call("POST", "/api/post", body),
  searchTimeline: (body) => call("POST", "/api/timeline/search", body),
  thread: (id) => call("GET", `/api/thread/${encodeURIComponent(id)}`),
  reply: (replyTo, text, speechAutoSend = false) => call("POST", "/api/reply", { replyTo, text, speechAutoSend }),
  remove: (id) => call("DELETE", `/api/entry/${encodeURIComponent(id)}`),
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
  forget: (id) => call("DELETE", `/api/personas/${encodeURIComponent(id)}/memory`),

  settings: () => call("GET", "/api/settings"),
  saveSettings: (settings) => call("PUT", "/api/settings", settings),
  models: () => call("GET", "/api/models"),
  checkSearch: () => call("POST", "/api/search/check"),
  about: () => call("GET", "/api/about"),

  on(listener) {
    listeners.add(listener);
    connect();
    return () => listeners.delete(listener);
  },
};

export const blobURL = (id) => `/api/blob/${encodeURIComponent(id)}`;
