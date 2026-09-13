// Wiring. One screen: a passage to drop something through, and the timeline
// under it.
//
// Everything else is a panel you can open and close. There is no inbox, no
// unread badge and no place where the AI lives - the reactions turn up in the
// timeline beside your own posts, or they do not turn up at all.
import { $, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { createCompose } from "./compose.js";
import { createFeed } from "./feed.js";
import { createPersonas } from "./personas.js";
import { createRoom } from "./room.js";
import { screenNodes, sheetNodes } from "./screen.js";
import { aiConfigured, createSettings } from "./settings.js";
import { createSpeaker } from "./speak.js";
import { getLanguage } from "./i18n.js";
import { authenticationEnabled, logout } from "./auth.js";

document.documentElement.lang = getLanguage();

const root = $("#app");
let settings = null;
let personaRows = [];
let panel = null;
let settingsRequired = false;

const speaker = createSpeaker();
const feed = createFeed({
  getPersona: (id) => personaRows.find((row) => row.id === id),
  onReply: reload,
});
const compose = createCompose({
  getSettings: () => settings,
  onPosted: (entry) => {
    feed.upsert(entry);
    window.scrollTo({ top: 0, behavior: "smooth" });
  },
});
const personas = createPersonas({
  onChange: (rows) => {
    personaRows = rows;
    feed.draw();
    room.set(rows);
  },
});
// Naming one is how you get an answer for certain, so the row of names writes
// the mention for you rather than opening a panel about itself.
const room = createRoom({
  onOpen: () => open("personas"),
  onActivate: (id) => void personas.activate(id),
  onMention: (row) => compose.mention(row.name),
  onSpeak: () => speaker.toggle(feed.spoken()),
});
const settingsPanel = createSettings({
  speaker,
  onSettings: (next) => {
    settings = next;
    const wasRequired = settingsRequired;
    settingsRequired = !aiConfigured(next);
    speaker.setRate(next.readAloudRate || 1.2);
    speaker.setVoice(next.readAloudVoice || "");
    if (panel === "settings" && wasRequired !== settingsRequired) showPanel();
  },
});

speaker.onState((on) => room.setSpeaking(on));
speaker.onLine((line) => {
  document.querySelector(`[data-id="${line.id}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" });
});

function showPanel() {
  const body = panel === "personas" ? personas.element : settingsPanel.element;
  const host = $("#sheet");
  host.classList.toggle("required", panel === "settings" && settingsRequired);
  host.replaceChildren(sheetNodes({
    title: panel === "personas" ? "担当" : "設定",
    body,
    onClose: panel === "settings" && settingsRequired ? null : close,
  }));
}

function open(which) {
  if (panel === which) {
    if (!(which === "settings" && settingsRequired)) close();
    return;
  }
  panel = which;
  showPanel();
  if (which === "personas") void personas.load();
  else void settingsPanel.load();
}

function close() {
  panel = null;
  const host = $("#sheet");
  host.classList.remove("required");
  clear(host);
}

async function reload() {
  try {
    const payload = await api.timeline();
    feed.set(payload.entries || []);
  } catch (error) {
    toast(String(error.message || error), "error");
  }
}

// A reaction arriving is the app working, not an event to celebrate: it lands
// in place and nothing else moves.
api.on((name, data) => {
  if (name === "entry") {
    feed.upsert(data);
    return;
  }
  if (name === "open") void reload();
});

clear(root).append(
  ...screenNodes({
    compose: compose.element, room, feed: feed.element, onOpen: open,
    onLogout: authenticationEnabled() ? () => void logout() : null,
  }),
);

// The share target lands here with the image already in the request. The
// service worker hands it over, so the page opens with it in the box.
navigator.serviceWorker?.addEventListener("message", (event) => {
  if (event.data?.type !== "shared") return;
  if (event.data.file) compose.accept(event.data.file, event.data.text || "");
});

async function boot() {
  if (new URLSearchParams(location.search).get("shared") === "0") {
    toast("共有した内容を保存できませんでした。接続と認証を確認し、もう一度共有してください。", "error");
  }
  await reload();
  try {
    settings = await api.settings();
    settingsRequired = !aiConfigured(settings);
    speaker.setRate(settings.readAloudRate || 1.2);
    speaker.setVoice(settings.readAloudVoice || "");
    if (settingsRequired) open("settings");
  } catch {
    /* the timeline still reads without settings */
  }
  // Who is in the room decides what the screen leads with, so it is loaded
  // before the page settles rather than when the panel is first opened.
  await personas.load();
  if ("serviceWorker" in navigator) {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  }
  compose.focus();
}

void boot();
