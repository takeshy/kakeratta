import { verifyIdentity } from "./auth.js";
import { localIdentity, localRecord, localList } from "./local-store.js";
import { createAPIRequest } from "./api-request.js";
import { createOutbox } from "./outbox.js";
import { getLanguage, t } from "./i18n.js";
// The only place that knows how to reach the relay.
//
// Everything goes through the relay: the keys are there, the reactions are
// generated there, and the page never talks to a model directly.
const listeners = new Set();
let stream = null;
let backoff = 1000;

const call = createAPIRequest({ identity: localIdentity, verify: verifyIdentity });

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
 decideGmailImport: (id, accept, excludeSimilar = false, excludeSender = false, excludeEvent = false) => call("POST", `/api/imports/${encodeURIComponent(id)}/decision`, { accept, excludeSimilar, excludeSender, excludeEvent }),
 usage: (month = "", persona = "") => call("GET", `/api/usage?${new URLSearchParams({ month, persona })}`),
  favorites: ({ speaker = "", limit = 20, offset = 0 } = {}) => call("GET", `/api/favorites?${new URLSearchParams({ speaker, limit, offset })}`),
  setDone: (id, done) => call(done ? "PUT" : "DELETE", `/api/entry/${encodeURIComponent(id)}/done`),
  answerSuggestion: (id, choice, language) => call("POST", `/api/entry/${encodeURIComponent(id)}/suggestion`, { choice, language }),
  answerReview: (id, choice) => call("POST", `/api/entry/${encodeURIComponent(id)}/review`, { choice }),
  setFavorite: (id, saved) => call(saved ? "PUT" : "DELETE", `/api/entry/${encodeURIComponent(id)}/favorite`),
  pushConfig: endpoint => call("GET", "/api/push" + (endpoint ? `?endpoint=${encodeURIComponent(endpoint)}` : "")),
  savePush: body => call("PUT", "/api/push", body),
  deletePush: body => call("DELETE", "/api/push", body),
  mcpConnection: () => call("GET", "/api/mcp/connection"),
  issueMCPKey: (connection) => call("POST", "/api/mcp/connection", connection),
  selectMCPConnection: (id) => call("PUT", "/api/mcp/connection", { id }),
  setMCPConnectionWaits: (id, waits) => call("PATCH", "/api/mcp/connection", { id, ...waits }),
  revokeMCPKey: (id) => call("DELETE", "/api/mcp/connection" + (id ? `?id=${encodeURIComponent(id)}` : "")),
  blueskyConnection: () => call("GET", "/api/social/bluesky/connection"),
  connectBluesky: (body) => call("POST", "/api/social/bluesky/connect", body),
  finishBluesky: (body) => call("POST", "/api/social/bluesky/finish", body),
  disconnectBluesky: () => call("DELETE", "/api/social/bluesky/connection"),
  previewSocial: (body) => call("POST", "/api/social/preview", body),
  publishSocial: (body) => call("POST", "/api/social/publish", body),
  timeline: ({ limit = 20, before, ...filters } = {}) => {
    const query = new URLSearchParams({ limit: String(limit), ...filters });
    if (before) query.set("before", before);
    return call("GET", `/api/timeline?${query}`);
  },
  post: (body) => localIdentity() ? outbox.enqueue("/api/post", body) : call("POST", "/api/post", { ...body, language: getLanguage() }),
  searchTimeline: (body) => call("POST", "/api/timeline/search", body),
  thread: (id) => call("GET", `/api/thread/${encodeURIComponent(id)}`),
  // What one speaker said, newest first: the list a name on the timeline opens.
  speaker: (id, { limit = 20, offset = 0, ...filters } = {}) => call("GET", `/api/speaker/${encodeURIComponent(id)}?${new URLSearchParams({ limit, offset, ...filters })}`),
  reply: async (replyTo, text, speechAutoSend = false) => {
    const body = { replyTo, text, speechAutoSend, language: getLanguage() };
    if (!localIdentity()) return call("POST", "/api/reply", body);
    const local = [...(await localRecord("timeline").catch(() => [] ) || []), ...await localList("ack:"), ...(await outbox.entries()).map(item => item.entry)];
    const parent = local.find(e => e.id === replyTo) || (await call("GET", `/api/thread/${encodeURIComponent(replyTo)}`)).entries?.find(e => e.id === replyTo);
    if (!parent) throw Error(t("返事の宛先が見つかりません"));
    return outbox.enqueue("/api/reply", body, parent);
  },
  remove: (id) => call("DELETE", `/api/entry/${encodeURIComponent(id)}`),
  edit: (id, text) => call("PATCH", `/api/entry/${encodeURIComponent(id)}`, { text }),
  routing: (id) => call("GET", `/api/entry/${encodeURIComponent(id)}/routing`),
  checkRouting: (id, persona) => call("POST", `/api/entry/${encodeURIComponent(id)}/routing/check`, { persona }),
  applyRouting: (id, token) => call("POST", `/api/entry/${encodeURIComponent(id)}/routing/apply`, { token }),
  previewReply: (id, persona) => call("POST", `/api/entry/${encodeURIComponent(id)}/reply/preview`, { persona }),
  applyReply: (id, token) => call("POST", `/api/entry/${encodeURIComponent(id)}/reply/apply`, { token }),
  fetchURL: (url) => call("POST", "/api/fetch", { url }),

  buildPersona: (body) => call("POST", "/api/personas/build", body),
  savePersonaDraft: (skill, id) => call(id ? "PUT" : "POST", id ? `/api/personas/${encodeURIComponent(id)}` : "/api/personas/draft", skill),
  personas: () => call("GET", "/api/personas"),
  setDailyCheckIn: (id, schedule) => call("PUT", `/api/personas/${encodeURIComponent(id)}/daily`, schedule),
  setPlanner: (id, planner) => call("PUT", `/api/personas/${encodeURIComponent(id)}/planner`, { planner }),
  setActive: (active) => call("PUT", "/api/personas", { active }),
  installPersona: (definition) => call("POST", "/api/personas/install", definition, { raw: true }),
  updateSchedule: (id, schedule, patch) => call("PUT", `/api/personas/${encodeURIComponent(id)}/schedules/${encodeURIComponent(schedule)}`, patch),
  deleteSchedule: (id, schedule) => call("DELETE", `/api/personas/${encodeURIComponent(id)}/schedules/${encodeURIComponent(schedule)}`),
  uninstallPersona: (id) => call("DELETE", `/api/personas/${encodeURIComponent(id)}`),
  // One page of notes, the most recently written first; `next` continues.
  memory: (id, { after = "", tag = "", type = "" } = {}) => {
    const query = new URLSearchParams();
    if (after) query.set("after", after);
    if (tag) query.set("tag", tag);
    if (type) query.set("type", type);
    return call("GET", `/api/personas/${encodeURIComponent(id)}/memory${query.size ? `?${query}` : ""}`);
  },
  note: (id, path) => call("GET", `/api/personas/${encodeURIComponent(id)}/memory/note?${new URLSearchParams({ path })}`),
  forgetNote: (id, path) => call("DELETE", `/api/personas/${encodeURIComponent(id)}/memory/note?${new URLSearchParams({ path })}`),
  editNote: (id, path, text) => call("PUT", `/api/personas/${encodeURIComponent(id)}/memory/note?${new URLSearchParams({ path })}`, { text }),
  editFact: (id, fact, text) => call("PUT", `/api/personas/${encodeURIComponent(id)}/memory/fact?${new URLSearchParams({ fact })}`, { text }),
  forgetFact: (id, fact) => call("DELETE", `/api/personas/${encodeURIComponent(id)}/memory/fact?${new URLSearchParams({ fact })}`),

  checkSearch: () => call("POST", "/api/search/check"),
  settings: () => call("GET", "/api/settings"),
  requests: () => call("GET", "/api/requests"),
  setRequestNotify: (id, notify) => call("PUT", `/api/requests/${encodeURIComponent(id)}/notify`, { notify }),
  startExport: () => call("POST", "/api/export"),
  closeAccount: () => call("DELETE", "/api/workspace", { confirm: true }),
  saveSettings: (settings) => call("PUT", "/api/settings", settings),
  models: () => call("GET", "/api/models"),
  openRouterModels: () => call("GET", "/api/openrouter/models"),

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
