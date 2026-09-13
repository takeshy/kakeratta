// Wiring. One screen: a passage to drop something through, and the timeline
// under it.
//
// Everything else is a panel you can open and close. There is no inbox, no
// unread badge and no place where the AI lives - the reactions turn up in the
// timeline beside your own posts, or they do not turn up at all.
import { $, el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { createCompose } from "./compose.js";
import { createFeed } from "./feed.js";
import { createPersonas } from "./personas.js";
import { createRoom } from "./room.js";
import { screenNodes, sheetNodes } from "./screen.js";
import { aiConfigured, createSettings } from "./settings.js";
import { createSpeaker } from "./speak.js";
import { createTimelinePages } from "./timeline-pages.js";
import { createSearch } from "./search.js";
import { getLanguage, t } from "./i18n.js";
import { authenticationEnabled, logout } from "./auth.js";

document.documentElement.lang = getLanguage();

const root = $("#app");
let settings = null;
let personaRows = [];
let panel = null;
let settingsRequired = false;

const speaker = createSpeaker();
let speakingThread = null;
const feed = createFeed({
  getSettings: () => settings,
  onSpeak: speakThread,
  getPersonas: () => personaRows,
  getPersona: (id) => personaRows.find((row) => row.id === id),
  onReply: () => {
    void reload();
    pollAfterSending();
  },
  onEntryChange: (entry) => pages.upsert(entry),
});
const compose = createCompose({
  getSettings: () => settings,
  getPersonas: () => personaRows,
  onPosted: (entry) => {
    pages.upsert(entry);
    pollAfterSending();
    window.scrollTo({ top: 0, behavior: "smooth" });
  },
});
const personas = createPersonas({
  getSettings: () => settings,
  onSearch: (query) => { open("search"); void searchPanel.searchFor(query); },
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

const searchPanel = createSearch({
  getSettings: () => settings,
  onSpeak: speakThread,
  getPersonas: () => personaRows,
  getPersona: (id) => personaRows.find((row) => row.id === id),
  onEntryChange: (entry) => pages.upsert(entry),
  onReply: () => { void reload(); pollAfterSending(); },
});

function speakThread(root, lines) {
  const stopping = speakingThread === root && speaker.speaking();
  speaker.stop();
  if (stopping) return;
  speakingThread = root;
  void speaker.start(lines);
}

speaker.onState((on) => {
  if (!on) speakingThread = null;
  feed.setSpeaking(speakingThread);
  searchPanel.setSpeaking(speakingThread);
});
speaker.onLine((line) => {
  if (panel === "search") searchPanel.reveal(line.id);
  else feed.reveal(line.id);
  document.querySelector(`[data-id="${line.id}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" });
});

function showPanel() {
  const body = panel === "personas" ? personas.element : panel === "search" ? searchPanel.element : settingsPanel.element;
  const host = $("#sheet");
  host.classList.toggle("required", panel === "settings" && settingsRequired);
  host.replaceChildren(sheetNodes({
    title: panel === "personas" ? "担当" : panel === "search" ? "検索" : "設定",
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
  else if (which === "search") searchPanel.focus();
  else void settingsPanel.load();
}

function close() {
  panel = null;
  const host = $("#sheet");
  host.classList.remove("required");
  clear(host);
}

const more = el("button", { class: "button", label: "さらに20件読み込む", hidden: true,
  onclick: () => void (loadFailedInitially ? pages.refresh() : pages.more()),
});
const pagingStatus = el("p", { class: "hint", role: "status", "aria-live": "polite" });
const pagingFooter = el("div", { class: "timeline-paging" }, pagingStatus, more);
let loadFailedInitially = false;
let autoLoad = false;
let pagingObserver = null;
const pages = createTimelinePages({
  fetchPage: (query) => api.timeline(query),
  onChange: (entries) => feed.set(entries),
  onState: ({ loading, hasMore, error, initial }) => {
    autoLoad = !loading && hasMore && !error;
    loadFailedInitially = Boolean(error && initial);
    pagingObserver?.disconnect();
    if (autoLoad) pagingObserver?.observe(pagingFooter);
    more.hidden = !hasMore && !error;
    more.disabled = loading;
    more.textContent = t(error ? "もう一度読み込む" : "さらに20件読み込む");
    pagingStatus.textContent = loading ? t("読み込んでいます…") : error ? t("読み込めませんでした。もう一度お試しください。") : "";
  },
});

function reload() { return pages.refresh(); }

// The live channel can drop a reaction without noticing (a proxy cutting the
// stream, a reconnect in the wrong moment). Only you and the personas write to
// the timeline, and they answer within a few minutes of being given something,
// so the timeline is re-read in the background for a while after you send.
const POLL_FOR = 5 * 60 * 1000;
const POLL_EVERY = 10 * 1000;
let pollUntil = 0;
let pollTimer = null;

function pollAfterSending() {
  pollUntil = Date.now() + POLL_FOR;
  if (pollTimer) return;
  const tick = async () => {
    if (Date.now() >= pollUntil) {
      pollTimer = null;
      return;
    }
    if (!document.hidden) {
      await pages.poll();
      if (panel === "search") await searchPanel.refresh();
    }
    pollTimer = setTimeout(tick, POLL_EVERY);
  };
  pollTimer = setTimeout(tick, POLL_EVERY);
}

// A reaction arriving is the app working, not an event to celebrate: it lands
// in place and nothing else moves.
api.on((name, data) => {
  if (name === "entry") {
    pages.upsert(data);
    searchPanel.changed(data);
    return;
  }
  if (name === "open") void reload();
});

clear(root).append(
  ...screenNodes({
    compose: compose.element, room, feed: el("div", {}, feed.element, pagingFooter), onOpen: open,
    onLogout: authenticationEnabled() ? () => void logout() : null,
  }),
);

// The button also provides a retry and works when IntersectionObserver is absent.
if ("IntersectionObserver" in window) {
  pagingObserver = new IntersectionObserver((observations) => {
    if (autoLoad && observations.some((entry) => entry.isIntersecting)) void pages.more();
  }, { rootMargin: "300px" });
  if (autoLoad) pagingObserver.observe(pagingFooter);
}

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
  const requestedEntry = new URL(location.href).searchParams.get("entry");
  if (requestedEntry && !settingsRequired) {
    open("search");
    await searchPanel.showEntry(requestedEntry);
  }
  if ("serviceWorker" in navigator) {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  }
  compose.focus();
}

void boot();
