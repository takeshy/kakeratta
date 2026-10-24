import { localRecord, localIdentity, recentEntries } from "./local-store.js";
import { installPrompt, installPromptNode } from "./install-prompt.js";
// Wiring. One screen: a passage to drop something through, and the timeline
// under it.
//
// Everything else is a panel you can open and close. There is no inbox, no
// unread badge and no place where the AI lives - the reactions turn up in the
// timeline beside your own posts, or they do not turn up at all.
import { $, el, clear, toast } from "./dom.js";
import { api, outbox, pendingEntries } from "./api.js";
import { createCompose } from "./compose.js";
import { finishBlueskyConnection } from "./bluesky.js";
import { openSocial } from "./social.js";
import { createFeed } from "./feed.js";
import { showConditionFix } from "./condition-fix.js";
import { showReplyFix } from "./reply-fix.js";
import { createPersonas } from "./personas.js";
import { createSchedules } from "./schedules.js";
import { createRoom } from "./room.js";
import { screenNodes, sheetNodes } from "./screen.js";
import { aiConfigured, createSettings } from "./settings.js";
import { createSpeaker as createReader } from "./speak.js";
import { createTimelinePages } from "./timeline-pages.js";
import { createEntryFilters, filterConversations, entryMatches } from "./entry-filters.js";
import { refreshFavorites, watchFavorites } from "./favorite-state.js";
import { createSearch } from "./search.js";
import { createSpeaker } from "./speaker.js";
import { getLanguage, t } from "./i18n.js";
import { agentMentions, mentionTag } from "./mention-picker.js";
import { applyTextSize } from "./text-size.js";
import { authenticationEnabled, logout, reconnectAuthentication } from "./auth.js";

document.documentElement.lang = getLanguage();
applyTextSize();

const root = $("#app");
let settings = null;
let personaRows = [];
// Built-in AI remains selectable even while the external agent is enabled.
const mentionable = () => [...personaRows, ...agentMentions(personas.agentAvailable())];
let panel = null;
let settingsRequired = false;

const speaker = createReader();
let speakingThread = null;
const feed = createFeed({
  getSettings: () => settings,
  onSpeak: speakThread,
  getPersonas: mentionable,
  getPersona: (id) => personaRows.find((row) => row.id === id),
  onReply: () => {
    void reload();
    pollAfterSending();
  },
  onEntryChange: (entry) => pages.upsert(entry),
  // A persona made from a check-in's suggestion joins the list at once.
  onPersonaAdded: () => personas.load({ quiet: true }),
  // The router's call about a post is where a persona's conditions get
  // corrected; personas is wired below and is only reached on a click.
  onFixCondition: (args) => showConditionFix({ ...args, onSaved: () => personas.load({ quiet: true }) }),
  onFixReply: (args) => showReplyFix({ ...args, onSaved: () => personas.load({ quiet: true }) }),
  onOpenNote: ({ persona, path }) => void personas.showMemoryFor(persona, path),
  onSelectPersona: (persona) => { open("personas"); personas.reveal(persona.id); },
  // A name on the timeline opens what that speaker said.
  onSpeaker: (id, name) => openSpeaker(id, name),
});
// A post made here or on the app's behalf - what was told about a person when
// saving its persona - lands in the timeline the same way.
function posted(entry) {
  if (!entry.pending) pages.upsert(entry);
  void drawLocal();
  pollAfterSending();
}
const compose = createCompose({
  getSettings: () => settings,
  getPersonas: mentionable,
  onPosted: (entry) => {
    posted(entry);
    window.scrollTo({ top: 0, behavior: "smooth" });
  },
});
const personas = createPersonas({
  getSettings: () => settings,
  onSearch: (query) => { open("search"); void searchPanel.searchFor(query); },
  onPosted: posted,
  onChange: (rows) => {
    personaRows = rows;
    // A card over a speaker's list shows what was just changed.
    speakerPanel.redrawCard();
    schedulesPanel.updateRows(rows);
    void localRecord("personas", rows).catch(() => {});
    feed.draw();
    room.set(rows);
  },
});
// Naming one is how you get an answer for certain, so the row of names writes
// the mention for you rather than opening a panel about itself.
const room = createRoom({
  onOpen: () => open("personas"),
  onActivate: (id) => void personas.activate(id),
  onSelect: (row) => compose.mention(mentionTag(row).slice(1)),
  onSettings: (row) => { open("personas"); personas.reveal(row.id); },
  onMemory: (row) => void personas.showMemoryFor(row),
  onEdit: (row) => personas.edit(row),
  onSpeaker: (row) => openSpeaker(row.id, row.name),
});
const schedulesPanel = createSchedules({ onChange: (rows) => personas.updateRows(rows) });
const settingsPanel = createSettings({
  speaker,
  onMCPChange: () => void personas.load({ quiet: true }),
  // Only an account can be closed: alone on a laptop, the directory is yours.
  closeAccount: authenticationEnabled() ? () => logout({ confirmUnsent: false }) : null,
  onSettings: (next) => {
    settings = next;
    const wasRequired = settingsRequired;
    settingsRequired = !aiConfigured(next);
    speaker.setRate(next.readAloudRate || 1.2);
    speaker.setVoice(next.readAloudVoice || "");
    // How text is drawn is a setting now, so the timeline behind the sheet
    // picks the change up without waiting for the next entry.
    feed.draw();
    if (panel === "settings" && wasRequired !== settingsRequired) showPanel();
  },
});

const searchPanel = createSearch({
  getSettings: () => settings,
  onSpeak: speakThread,
  getPersonas: mentionable,
  getPersona: (id) => personaRows.find((row) => row.id === id),
  onEntryChange: (entry) => pages.upsert(entry),
  onReply: () => { void reload(); pollAfterSending(); },
  onSpeaker: (id, name) => openSpeaker(id, name),
});

const speakerPanel = createSpeaker({
  onPersonasChange: (rows) => personas.updateRows(rows),
  getSettings: () => settings,
  onSpeak: speakThread,
  getPersonas: mentionable,
  getPersona: (id) => personaRows.find((row) => row.id === id),
  onEntryChange: (entry) => pages.upsert(entry),
  onReply: () => { void reload(); pollAfterSending(); },
  // The card over the list opens the same things the personas list does.
  onOpenPersona: (row) => { open("personas"); personas.reveal(row.id); },
  onOpenMemory: (row) => void personas.showMemoryFor(row),
  onEditPersona: (row, focus) => personas.edit(row, focus),
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
  speakerPanel.setSpeaking(speakingThread);
});
speaker.onLine((line) => {
  if (panel === "search") searchPanel.reveal(line.id);
  else if (panel === "speaker") speakerPanel.reveal(line.id);
  else feed.reveal(line.id);
  document.querySelector(`[data-id="${line.id}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" });
});

// While a sheet is open the page behind it holds still. Without this a
// finger that runs past the end of the sheet, or lands beside it, scrolls the
// timeline instead - and the sheet, which is what the finger was on, does not
// move. The page keeps its place and gets it back when the sheet closes.
let heldAt = 0;
function holdPage() {
  if (document.body.classList.contains("sheet-open")) return;
  heldAt = window.scrollY || 0;
  document.body.style.top = `-${heldAt}px`;
  document.body.classList.add("sheet-open");
}
function releasePage() {
  if (!document.body.classList.contains("sheet-open")) return;
  document.body.classList.remove("sheet-open");
  document.body.style.top = "";
  window.scrollTo(0, heldAt);
}

// A tap on the held page beside the sheet closes it, as a tap beside a dialog
// does - unless the sheet is the settings the app cannot run without.
document.body.addEventListener("click", (event) => {
  if (event.target !== document.body || !document.body.classList.contains("sheet-open")) return;
  if (panel === "settings" && settingsRequired) return;
  close();
});

function showPanel() {
  const body = panel === "schedules" ? schedulesPanel.element : panel === "personas" ? personas.element : panel === "search" ? searchPanel.element : panel === "speaker" ? speakerPanel.element : settingsPanel.element;
  const host = $("#sheet");
  host.classList.toggle("required", panel === "settings" && settingsRequired);
  host.replaceChildren(sheetNodes({
    title: panel === "schedules" ? "予定一覧" : panel === "personas" ? "担当" : panel === "search" ? "検索" : panel === "speaker" ? "発言一覧" : "設定",
    body,
    onClose: panel === "settings" && settingsRequired ? null : close,
  }));
  holdPage();
}

function open(which) {
  if (panel === which) {
    if (!(which === "settings" && settingsRequired)) close();
    return;
  }
  if (panel === "search") void searchPanel.stopVoice();
  if (panel === "settings") settingsPanel.stopMicCheck();
  panel = which;
  showPanel();
  if (which === "schedules") void schedulesPanel.load();
  else if (which === "personas") void personas.load();
  else if (which === "search") searchPanel.focus();
  else void settingsPanel.load();
}

// A speaker's list is a sheet of its own; a name on it opens another speaker's.
function openSpeaker(id, name) {
  if (panel === "search") void searchPanel.stopVoice();
  if (panel === "settings") {
    if (settingsRequired) return;
    settingsPanel.stopMicCheck();
  }
  const opened = panel === "speaker";
  panel = "speaker";
  if (!opened) showPanel();
  void speakerPanel.show(id, name);
}

function close() {
  // Leaving search must not keep the microphone open behind the sheet.
  if (panel === "search") void searchPanel.stopVoice();
  if (panel === "settings") settingsPanel.stopMicCheck();
  panel = null;
  const host = $("#sheet");
  host.classList.remove("required");
  clear(host);
  releasePage();
}

let serverEntries = [];
let drawVersion = 0;
let connectionFailed = false;
const syncStatus = el("span", { class: "hint", role: "status" });
const syncRetry = el("button", { class: "chip", label: "接続して再送", onclick: () => void reconnect(true) });
const syncBar = el("div", { class: "sync-status" }, syncStatus, syncRetry);
async function drawLocal() {
  const version = ++drawVersion;
  const pending = await pendingEntries().catch(() => []);
  if (version !== drawVersion) return;
  const merged = new Map(serverEntries.map(e => [e.id, e]));
  for (const entry of pending) {
    if (!merged.has(entry.id) || entry.pending) merged.set(entry.id, entry);
    else void localRecord(`ack:${entry.id}`, null).catch(() => {});
  }
  const visible = filterConversations([...merged.values()], timelineFilter);
  feed.set(visible);
  if (timelineFilter.favoritesOnly) for (const entry of visible) {
    if (entry.kind !== "post" && entryMatches(entry, timelineFilter)) feed.reveal(entry.id);
  }
  const count = pending.filter(e => e.pending).length;
  syncStatus.textContent = count ? t("端末に保存済み・送信待ち {0}件", count) : navigator.onLine === false ? t("オフライン・保存済みの会話を表示しています") : connectionFailed ? t("接続とログインを確認してください。") : "";
  syncRetry.hidden = !count && navigator.onLine !== false && !connectionFailed;
}
async function reconnect(retry = false) {
  try {
    await reconnectAuthentication();
    connectionFailed = false;
    await (retry ? outbox.retry() : outbox.flush());
    await reload();
  } catch { connectionFailed = true; void drawLocal(); }
}
window.addEventListener("online", () => { void reconnect(); setTimeout(() => void reconnect(), 2000); });
// A phone that was put away and picked up again has missed whatever arrived
// meanwhile: timers and the live channel do not run in the background.
document.addEventListener("visibilitychange", () => {
  if (document.hidden || navigator.onLine === false) return;
  void pages.poll();
  void feed.refreshRouting();
});
window.addEventListener("offline", () => void drawLocal());
setInterval(() => { if (!document.hidden && navigator.onLine !== false) void outbox.flush(); }, 30000);

const more = el("button", { class: "button", label: "さらに20件読み込む", hidden: true,
  onclick: () => void (loadFailedInitially ? pages.refresh() : pages.more()),
});
const pagingStatus = el("p", { class: "hint", role: "status", "aria-live": "polite" });
const pagingFooter = el("div", { class: "timeline-paging" }, pagingStatus, more);
let loadFailedInitially = false;
let autoLoad = false;
let pagingObserver = null;
let timelineFilter = {};
const timelineFilters = createEntryFilters(next => {
  timelineFilter = next;
  return navigator.onLine === false ? drawLocal() : pages.setFilters(next);
});
watchFavorites(() => {
  if (timelineFilter.favoritesOnly) { void drawLocal(); if (navigator.onLine !== false) void pages.refresh(); }
});
const pages = createTimelinePages({
  fetchPage: (query) => api.timeline({ ...query, ...timelineFilter }),
  onChange: (entries, { reset = false } = {}) => {
    serverEntries = entries;
    void drawLocal();
    if (!reset && !Object.keys(timelineFilter).length) void localRecord("timeline", recentEntries(entries)).catch(() => {});
  },
  onState: ({ loading, hasMore, error, initial }) => {
    autoLoad = !loading && hasMore && !error;
    loadFailedInitially = Boolean(error && initial);
    pagingObserver?.disconnect();
    if (autoLoad) pagingObserver?.observe(pagingFooter);
    more.hidden = !hasMore && !error;
    more.disabled = loading;
    more.textContent = t(error ? "もう一度読み込む" : "さらに20件読み込む");
    pagingStatus.textContent = loading ? (serverEntries.length ? "" : t("読み込んでいます…")) : error ? t(serverEntries.length ? "保存済みの会話を表示しています。接続後に更新します。" : "読み込めませんでした。もう一度お試しください。") : "";
  },
});

function reload() {
  if (navigator.onLine === false) return Promise.resolve();
  void loadRequests();
  return pages.refresh();
}

// Which posts are waiting on the external agent, as the server keeps them.
async function loadRequests() {
  try {
    const { requests = [], agentName = "" } = await api.requests();
    feed.setRequests(requests, agentName);
  } catch {
    /* the badges are a courtesy; the timeline does not depend on them */
  }
}

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
// in place and nothing else moves, except that the feed unfolds the
// conversation it landed in so it is seen.
api.on((name, data) => {
  if (name === "favorite") { void refreshFavorites().catch(() => {}); return; }
  if (name === "routed" || name === "turn") { void feed.refreshRouting(data?.entryId); return; }
  if (name === "request") { feed.request(data); return; }
  if (name === "outbox") { if (data) pages.upsert(data); void drawLocal(); if (data) pollAfterSending(); return; }
  if (name === "entry") {
    pages.upsert(data);
    searchPanel.changed(data);
    speakerPanel.changed(data);
    return;
  }
  if (name === "open") { void refreshFavorites().catch(() => {}); void reload(); void outbox.flush(); }
});

clear(root).append(
  ...screenNodes({
    compose: compose.element, room, filters: timelineFilters.element, feed: el("div", {}, syncBar, feed.element, pagingFooter), onOpen: open,
    onLogout: authenticationEnabled() ? () => void logout() : null,
  }),
);
root.insertBefore(installPromptNode(), root.children[1]);

// The button also provides a retry and works when IntersectionObserver is absent.
if ("IntersectionObserver" in window) {
  pagingObserver = new IntersectionObserver((observations) => {
    if (autoLoad && observations.some((entry) => entry.isIntersecting)) void pages.more();
  }, { rootMargin: "300px" });
  if (autoLoad) pagingObserver.observe(pagingFooter);
}

async function boot() {
  navigator.serviceWorker?.register("/sw.js").catch(() => {});
  const cached = await localRecord("timeline").catch(() => null);
  if (cached) pages.restore(cached);
  await drawLocal();
  const cachedPersonas = await localRecord("personas").catch(() => null);
  if (cachedPersonas) { personaRows = cachedPersonas; room.set(personaRows); schedulesPanel.updateRows(personaRows); feed.draw(); }
  if (navigator.onLine === false) { compose.focus(); return; }
  const loading = reload();
  const loadingPersonas = personas.load({ quiet: true });
  void reconnect();
  try {
    settings = await api.settings();
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (zone && settings.timeZone !== zone) {
      settings = await api.saveSettings({ ...settings, timeZone: zone }).catch(() => settings);
    }
    if (installPrompt?.standalone() && !settings.homeScreenLaunched) {
      settings = await api.saveSettings({ ...settings, homeScreenLaunched: true }).catch(() => settings);
    }
    if (settings.homeScreenLaunched) installPrompt?.markInstalled();
    settingsRequired = !aiConfigured(settings);
    speaker.setRate(settings.readAloudRate || 1.2);
    speaker.setVoice(settings.readAloudVoice || "");
    if (settingsRequired) open("settings");
  } catch {
    /* the timeline still reads without settings */
  }
  // Who is in the room decides what the screen leads with, so it is loaded
  // before the page settles rather than when the panel is first opened.
  await Promise.allSettled([loading, loadingPersonas]);
  const requestedEntry = new URL(location.href).searchParams.get("entry");
  if (requestedEntry && !settingsRequired) {
    open("search");
    await searchPanel.showEntry(requestedEntry);
  }
  compose.focus();
  const bluesky = await finishBlueskyConnection();
  if (bluesky?.returnRoot && !settingsRequired) {
    await openSocial(bluesky.returnRoot, (id) => personaRows.find((row) => row.id === id));
  } else if (bluesky) open("settings");
}

void boot();
