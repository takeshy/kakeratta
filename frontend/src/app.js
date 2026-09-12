// Wiring. One screen: a box to put something in, and the timeline under it.
//
// Everything else is a panel you can open and close. There is no inbox, no
// unread badge and no place where the AI lives - the reactions turn up in the
// timeline beside your own posts, or they do not turn up at all.
import { $, el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { createCompose } from "./compose.js";
import { createFeed } from "./feed.js";
import { createPersonas } from "./personas.js";
import { createSettings } from "./settings.js";
import { createSpeaker } from "./speak.js";

const root = $("#app");
let settings = null;
let personaRows = [];
let panel = null;

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
    drawBar();
  },
});
const settingsPanel = createSettings({
  speaker,
  onSettings: (next) => {
    settings = next;
    speaker.setRate(next.readAloudRate || 1);
    speaker.setVoice(next.readAloudVoice || "");
  },
});

const bar = el("div", { class: "who-bar" });
const listenButton = el("button", {
  class: "chip",
  text: "🔈 読み上げ",
  onclick: () => speaker.toggle(feed.spoken()),
});
speaker.onState((on) => {
  listenButton.textContent = on ? "■ 停止" : "🔈 読み上げ";
  listenButton.classList.toggle("active", on);
});
speaker.onLine((line) => {
  document.querySelector(`[data-id="${line.id}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" });
});

// The row of who is in the room. It is also the answer to "who can I ask?",
// which is the only reason the domains have to be far apart.
function drawBar() {
  clear(bar);
  const active = personaRows.filter((row) => row.active);
  if (!active.length) {
    bar.append(el("span", { class: "hint", text: "担当がいません。" }));
  }
  for (const row of active) {
    bar.append(
      el("button", {
        class: "chip persona-chip",
        title: row.domain,
        text: `${row.icon || "◎"} ${row.name}`,
        onclick: () => open("personas"),
      }),
    );
  }
  bar.append(
    el("span", { class: "spacer" }),
    listenButton,
    el("button", { class: "chip ghost", text: "担当", onclick: () => open("personas") }),
    el("button", { class: "chip ghost", text: "設定", onclick: () => open("settings") }),
  );
}

function open(which) {
  if (panel === which) {
    close();
    return;
  }
  panel = which;
  const body = which === "personas" ? personas.element : settingsPanel.element;
  const sheet = el(
    "div",
    { class: "sheet" },
    el(
      "div",
      { class: "sheet-head" },
      el("strong", { text: which === "personas" ? "担当" : "設定" }),
      el("span", { class: "spacer" }),
      el("button", { class: "chip ghost", text: "閉じる", onclick: close }),
    ),
    body,
  );
  $("#sheet").replaceChildren(sheet);
  if (which === "personas") void personas.load();
  else void settingsPanel.load();
}

function close() {
  panel = null;
  clear($("#sheet"));
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
  el("header", { class: "topbar" },
    el("span", { class: "brand", text: "キャプチャタイムライン" }),
    el("span", { class: "spacer" })),
  compose.element,
  bar,
  feed.element,
);

// The share target lands here with the image already in the request. The
// service worker hands it over, so the page opens with it in the box.
navigator.serviceWorker?.addEventListener("message", (event) => {
  if (event.data?.type !== "shared") return;
  if (event.data.file) compose.accept(event.data.file, event.data.text || "");
});

async function boot() {
  drawBar();
  await reload();
  try {
    settings = await api.settings();
    speaker.setRate(settings.readAloudRate || 1);
    speaker.setVoice(settings.readAloudVoice || "");
  } catch {
    /* the timeline still reads without settings */
  }
  try {
    personaRows = (await api.personas()).personas || [];
    drawBar();
    feed.draw();
  } catch {
    /* the panel will load them when it is opened */
  }
  if ("serviceWorker" in navigator) {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  }
  compose.focus();
}

void boot();
