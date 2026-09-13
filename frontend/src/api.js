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
  timeline: (limit) => call("GET", `/api/timeline${limit ? `?limit=${limit}` : ""}`),
  post: (body) => call("POST", "/api/post", body),
  reply: (replyTo, text) => call("POST", "/api/reply", { replyTo, text }),
  remove: (id) => call("DELETE", `/api/entry/${encodeURIComponent(id)}`),
  fetchURL: (url) => call("POST", "/api/fetch", { url }),

  personas: () => call("GET", "/api/personas"),
  setActive: (active) => call("PUT", "/api/personas", { active }),
  installPersona: async (definition) => {
    const response = await fetch("/api/personas/install", { method: "POST", body: definition });
    const payload = await response.json().catch(() => null);
    if (!response.ok) throw new Error(payload?.error || "追加できませんでした");
    return payload;
  },
  uninstallPersona: (id) => call("DELETE", `/api/personas/${encodeURIComponent(id)}`),
  memory: (id) => call("GET", `/api/personas/${encodeURIComponent(id)}/memory`),
  forget: (id) => call("DELETE", `/api/personas/${encodeURIComponent(id)}/memory`),

  settings: () => call("GET", "/api/settings"),
  saveSettings: (settings) => call("PUT", "/api/settings", settings),
  models: () => call("GET", "/api/models"),
  checkSearch: () => call("POST", "/api/search/check"),
  about: () => call("GET", "/api/about"),

  // The dictation stack reaches its backends through here so credentials stay
  // in the relay rather than in the browser.
  speechRequest: (request) => call("POST", "/api/speech/request", request),
  speechVertex: (request) => call("POST", "/api/speech/vertex", request),
  liveStart: () => call("POST", "/api/speech/live/start"),
  liveAudio: (sessionId, audio) => call("POST", "/api/speech/live/audio", { sessionId, audio }),
  liveFinish: (sessionId) => call("POST", "/api/speech/live/finish", { sessionId }),
  liveStop: () => call("POST", "/api/speech/live/stop"),

  on(listener) {
    listeners.add(listener);
    connect();
    return () => listeners.delete(listener);
  },
};

export const blobURL = (id) => `/api/blob/${encodeURIComponent(id)}`;
