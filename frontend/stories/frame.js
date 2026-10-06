import { el } from "../src/dom.js";
import { t } from "../src/i18n.js";
import { createEntryFilters, filterConversations } from "../src/entry-filters.js";
import { watchFavorites } from "../src/favorite-state.js";
// Builds the real pieces into the real container, so a story is the app with
// fixtures instead of a server behind it.
import { createCompose } from "../src/compose.js";
import { createFeed } from "../src/feed.js";
import { createRoom } from "../src/room.js";
import { screenNodes, sheetNodes } from "../src/screen.js";
import { personas as personasBehind, timeline } from "./fixtures.js";

// The timeline renders captures as <img src="/api/blob/...">. There is no
// server here, so the story draws its own rather than showing broken images.
const capture = () =>
  "data:image/svg+xml;utf8," +
  encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 300">
       <rect width="640" height="300" fill="#efe8dd"/>
       <rect x="40" y="44" width="420" height="14" rx="7" fill="#cdbfa9"/>
       <rect x="40" y="82" width="540" height="14" rx="7" fill="#cdbfa9"/>
       <rect x="40" y="120" width="360" height="14" rx="7" fill="#cdbfa9"/>
       <rect x="40" y="186" width="500" height="14" rx="7" fill="#cdbfa9"/>
       <rect x="40" y="224" width="300" height="14" rx="7" fill="#cdbfa9"/>
     </svg>`,
  );

function drawCaptures(node) {
  for (const img of node.querySelectorAll('img[src^="/api/blob/"]')) {
    img.src = capture();
    img.closest("a")?.removeAttribute("href");
  }
  return node;
}

export function room(rows) {
  const view = createRoom({
    onOpen: () => {},
    onActivate: () => {},
    onMention: () => {},
  });
  view.set(rows);
  return view;
}

export function feed(entries, rows = [], { onFixCondition, onFixReply, onOpenNote = () => {}, onSpeaker, getSettings } = {}) {
  const view = createFeed({
    getSettings,
    getPersona: (id) => rows.find((row) => row.id === id),
    onReply: () => {},
    onSpeak: () => {},
    onFixCondition,
    onFixReply,
    onOpenNote,
    onSpeaker,
  });
  view.set(entries);
  return view;
}

export function compose({ text = "", settings = {} } = {}) {
  const view = createCompose({ getSettings: () => settings, onPosted: () => {} });
  const area = view.element.querySelector(".compose-text");
  if (text) {
    area.value = text;
    // The box sizes itself from scrollHeight, which is zero until it is in the
    // document. Storybook appends on this tick, so measure on the next one.
    requestAnimationFrame(() => area.dispatchEvent(new Event("input")));
  }
  return view;
}

// One story, one screen. Everything below `#app` is what app.js assembles.
export function screen({ rows = [], entries = [], text = "", speaking = false, signedIn = false, pending = 0 } = {}) {
  const host = document.createElement("div");
  host.id = "app";
  const timeline = feed(entries, rows);
  let filters = {};
  const controls = createEntryFilters(next => { filters = next; timeline.set(filterConversations(entries, filters)); });
  const content = document.createElement("div");
  if (pending) content.append(el("div", { class: "sync-status" },
    el("span", { class: "hint", role: "status", text: t("端末に保存済み・送信待ち {0}件", pending) }),
    el("button", { class: "chip", type: "button", label: "接続して再送" })));
  content.append(controls.element, timeline.element);
  watchFavorites(() => { if (host.isConnected) timeline.set(filterConversations(entries, filters)); });
  host.append(...screenNodes({
    compose: compose({ text }).element,
    room: room(rows, { speaking }),
    feed: content,
    onOpen: () => {},
    onLogout: signedIn ? () => {} : null,
  }));
  return drawCaptures(host);
}

export function panel(node) {
  const host = document.createElement("div");
  host.id = "app";
  host.append(node);
  return drawCaptures(host);
}

// A panel is a sheet over the screen, so the story shows it that way: the real
// screen behind, the real sheet in front, and the panel inside it.
export function sheet(title, body) {
  const host = document.createElement("div");
  const behind = screen({ rows: personasBehind, entries: timeline().answered });
  const sheetHost = document.createElement("div");
  sheetHost.id = "sheet";
  sheetHost.append(sheetNodes({ title, body, onClose: () => {} }));
  host.append(behind, sheetHost);
  return host;
}
