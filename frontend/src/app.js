// Wiring. A home screen - the shelf, and what you have read - and a book, with
// a chat beside it that only ever shows what the reader has already reached.
import { api } from "./bridge.js";
import { $, clear, el, fraction, toast } from "./dom.js";
import { createShelf } from "./shelf.js";
import { createRecords } from "./records.js";
import { createReader } from "./reader.js";
import { createNarrator } from "./narrator.js";
import { createSetup } from "./setup.js";
import { createChatPanel } from "./chatpanel.js";
import { createMemoPanel } from "./memopanel.js";
import { createSettingsPanel } from "./settingspanel.js";
import { createMicButton } from "./micbutton.js";
import { dictationSupported } from "./dictation.js";

const root = $("#app");
let opened = null;
let settings = null;
let homeTab = "shelf";
let panelTab = "chat";
let panelOpen = true;

const reader = createReader({
  api,
  onPosition: (state) => {
    if (opened) opened.state = state;
    updateProgress();
  },
});
const narrator = createNarrator({
  reader,
  // Listening is reading: the voice passing a block is the same event as a
  // screen scrolling by, and it moves the same two positions.
  onPosition: (current, reached) => reader.spoke(current, reached),
});
const chat = createChatPanel({ api, reader, getSettings: () => settings });
const memoPanel = createMemoPanel({ api, reader, refresh: reloadMemos });
const settingsPanel = createSettingsPanel({ api, narrator, onSettings: (next) => (settings = next) });
const shelf = createShelf({ api, onOpen: openBook });
const records = createRecords({ api, onOpen: (id, at) => openBook(id, at) });
const setup = createSetup({ api, onDone: (next) => drawBook(next) });

const progressLabel = el("span", { class: "progress" });

function updateProgress() {
  if (!opened) return;
  const end = opened.book.end.absolute || 1;
  const entries = opened.state.entries || [];
  const boundary = entries.reduce(
    (far, entry) => Math.max(far, entry.range.end.absolute),
    opened.state.furthest?.absolute || 0,
  );
  const read = entries.reduce((sum, entry) => sum + (entry.range.end.absolute - entry.range.start.absolute), 0);
  const current = opened.state.current.absolute / end;
  // Where the eye is, how far in the reading has got, and how much of the book
  // was actually read. They differ the moment something is skipped.
  progressLabel.textContent =
    `現在 ${fraction(current)} ・ 到達 ${fraction(boundary / end)} ・ 読んだ ${fraction(read / end)}`;
}

async function reloadMemos() {
  if (!opened) return;
  opened.memos = await api.memos(opened.book.id);
  reader.render(opened);
  await drawPanel();
}

async function drawPanel() {
  if (!opened) return;
  if (panelTab === "chat") await chat.load(opened);
  if (panelTab === "memo") memoPanel.draw(opened, await api.timeline(opened.book.id));
  if (panelTab === "settings") settingsPanel.draw(opened);
}

function showHome() {
  narrator.stop();
  reader.flush();
  opened = null;
  const tab = (id, label) =>
    el("button", {
      class: `button ${homeTab === id ? "active" : ""}`,
      text: label,
      onclick: () => {
        homeTab = id;
        showHome();
      },
    });
  clear(root).append(
    el(
      "div",
      { class: "home" },
      el("div", { class: "home-tabs" }, tab("shelf", "書棚"), tab("records", "記録")),
      homeTab === "shelf" ? shelf.element : records.element,
    ),
  );
  if (homeTab === "shelf") void shelf.draw();
  else void records.draw();
}

async function openBook(id, at) {
  try {
    opened = await api.openBook(id);
  } catch (error) {
    toast(String(error), "error");
    return;
  }
  settings = await api.settings();
  // A book whose opening choices have not been made yet: the category decides
  // what the AI may see, and nothing is sent to a model until it is made.
  if (!opened.book.category || !opened.state.ready) {
    clear(root).append(
      el(
        "div",
        { class: "setup-screen" },
        el("button", { class: "button ghost", text: "← 書棚", onclick: showHome }),
        setup.element,
      ),
    );
    await setup.draw(opened);
    return;
  }
  drawBook(opened, at);
}

function drawBook(next, at) {
  opened = next;
  // Read-aloud is the one thing that depends on the text layer being clean.
  // A PDF whose characters could not be recovered is still perfectly readable
  // by eye, so the book opens and only the listening half is missing.
  const canListen = narrator.supported && opened.book.speakable !== false;
  const listenButton = el("button", {
    class: "button",
    text: !narrator.supported
      ? "聞く（この環境では使えません）"
      : canListen
      ? "聞く"
      : "聞く（この本は文字が壊れています）",
    title: canListen ? "" : "テキストレイヤーが読み取れないため、読み上げは提供しません",
    disabled: !canListen,
    onclick: () => narrator.toggle(),
  });
  narrator.onState((speaking) => {
    listenButton.textContent = speaking ? "停止" : "聞く";
    listenButton.classList.toggle("active", speaking);
  });

  const pagedButton = el("button", {
    class: "button",
    text: "ページ送り",
    onclick: () => {
      const paged = !pagedButton.classList.contains("active");
      pagedButton.classList.toggle("active", paged);
      reader.setPaged(paged);
    },
  });

  const panelBody = el("div", { class: "panel-body" });
  const tabs = el("div", { class: "panel-tabs" });
  const paneFor = (id) =>
    id === "chat" ? chat.element : id === "memo" ? memoPanel.element : settingsPanel.element;
  const tab = (id, label) =>
    el("button", {
      class: `button ${panelTab === id ? "active" : ""}`,
      text: label,
      onclick: async () => {
        panelTab = id;
        for (const node of tabs.querySelectorAll("button")) node.classList.remove("active");
        for (const node of tabs.querySelectorAll("button")) {
          if (node.textContent === label) node.classList.add("active");
        }
        clear(panelBody).append(paneFor(id));
        await drawPanel();
      },
    });
  tabs.append(tab("chat", "チャット"), tab("memo", "メモ"), tab("settings", "設定"));

  const panel = el("div", { class: `panel ${panelOpen ? "" : "folded"}` }, tabs, panelBody);
  // Folding it away leaves the page and nothing else. Anything a companion says
  // while it is folded is still there when it is opened again.
  const foldButton = el("button", {
    class: "button ghost",
    text: panelOpen ? "畳む" : "チャット",
    onclick: () => {
      panelOpen = !panelOpen;
      panel.classList.toggle("folded", !panelOpen);
      foldButton.textContent = panelOpen ? "畳む" : "チャット";
    },
  });

  clear(root).append(
    el(
      "div",
      { class: "topbar" },
      el("button", { class: "button ghost", text: "← 書棚", onclick: showHome }),
      el("span", { class: "title", text: opened.book.title }),
      el("span", { class: "category", text: opened.book.category === "technical" ? "技術書" : "小説" }),
      personaBar(),
      el("span", { class: "spacer" }),
      progressLabel,
      pagedButton,
      listenButton,
      el("button", {
        class: "button",
        text: "音声メモ",
        title: "話してメモを残す",
        onclick: () => openMemoDialog(null, "", "", { dictate: true }),
        disabled: !dictationSupported(settings),
      }),
      foldButton,
    ),
    el("div", { class: "reader" }, reader.element, panel),
    reader.tools,
  );
  panelBody.append(paneFor(panelTab));
  reader.render(opened);
  reader.setQuoteHandler((quote) => openMemoDialog(quote));
  updateProgress();
  if (at) reader.goTo(at);
  void settingsPanel.load().then(drawPanel);
}

// The companions in this book, added and taken out from the header. Taking one
// out does not erase it: put it back and it is the one that was reading along.
function personaBar() {
  const bar = el("span", { class: "persona-bar" });
  for (const plugin of opened.personas || []) {
    bar.append(
      el(
        "button",
        {
          class: "persona-chip",
          title: `${plugin.name}を外す（メモと会話は残ります）`,
          onclick: async () => {
            try {
              drawBook(await api.removePersona(opened.book.id, plugin.id));
            } catch (error) {
              toast(String(error), "error");
            }
          },
        },
        el("span", { class: "avatar", text: plugin.icon || "◎" }),
        plugin.name,
      ),
    );
  }
  bar.append(
    el("button", {
      class: "persona-chip add",
      text: (opened.personas || []).length ? "＋" : "＋ 一緒に読む相手",
      title: "ペルソナを選ぶ",
      onclick: () => choosePersona(),
    }),
  );
  return bar;
}

async function choosePersona() {
  let plugins = [];
  try {
    plugins = (await api.personas(opened.book.category)) || [];
  } catch (error) {
    toast(String(error), "error");
    return;
  }
  const chosen = (opened.personas || []).map((plugin) => plugin.id);
  const backdrop = el("div", { class: "dialog-backdrop" });
  const close = () => backdrop.remove();
  backdrop.append(
    el(
      "div",
      { class: "dialog" },
      el("h3", { text: "一緒に読む相手" }),
      el("p", { class: "hint", text: "最大3体。途中から入った相手にも、これまでの会話は渡ります。" }),
      el(
        "div",
        { class: "choices" },
        plugins.map((plugin) => {
          const inBook = chosen.includes(plugin.id);
          return el(
            "button",
            {
              class: `choice persona ${inBook ? "active" : ""}`,
              disabled: !inBook && chosen.length >= 3,
              onclick: async () => {
                try {
                  const next = inBook
                    ? await api.removePersona(opened.book.id, plugin.id)
                    : await api.addPersona(opened.book.id, plugin.id);
                  close();
                  drawBook(next);
                } catch (error) {
                  toast(String(error), "error");
                }
              },
            },
            el("span", { class: "choice-label" }, el("span", { class: "avatar", text: plugin.icon || "◎" }), plugin.name),
            el("span", { class: "choice-detail", text: plugin.tone }),
            plugin.webSearch ? el("span", { class: "tag", text: "Web検索を使います" }) : null,
          );
        }),
      ),
      el(
        "div",
        { class: "row" },
        el("button", {
          class: "button ghost",
          text: "ペルソナを追加（persona.json）",
          onclick: async () => {
            try {
              const plugin = await api.installPersonaDialog();
              if (plugin && plugin.id) toast(`${plugin.name} を追加しました`);
              close();
              void choosePersona();
            } catch (error) {
              toast(String(error), "error");
            }
          },
        }),
        el("button", { class: "button ghost", text: "閉じる", onclick: close }),
      ),
    ),
  );
  document.body.append(backdrop);
}

function openMemoDialog(quote, prefill = "", spokenText = "", { dictate = false } = {}) {
  const text = el("textarea", { placeholder: "思ったことを書く（マイクで話しても書けます）" });
  text.value = prefill;
  let spoken = spokenText;
  const backdrop = el("div", { class: "dialog-backdrop" });
  const close = () => {
    void mic.discard();
    backdrop.remove();
  };
  const save = async () => {
    await mic.stop();
    try {
      await api.addMemo(
        opened.book.id,
        text.value,
        spoken,
        quote?.text || "",
        quote?.range || { start: opened.state.current, end: opened.state.current },
      );
      close();
      await reloadMemos();
      toast("メモを保存しました");
    } catch (error) {
      toast(String(error), "error");
    }
  };
  // Speech is not writing: what was said is kept as it was said, and the tidied
  // version is what the memo shows.
  const mic = createMicButton({
    api,
    getSettings: () => settings,
    getText: () => text.value,
    setText: (value) => {
      spoken = value;
      text.value = value;
    },
    onSend: () => void save(),
    label: "話す",
  });
  backdrop.append(
    el(
      "div",
      { class: "dialog" },
      el("h3", { text: "メモ" }),
      quote?.text ? el("div", { class: "quote", text: quote.text }) : null,
      text,
      el(
        "div",
        { class: "row" },
        mic.element,
        el("button", {
          class: "button ghost",
          text: "話し言葉を整える",
          onclick: async () => {
            const tidied = await mic.tidy();
            spoken = tidied.spoken || spoken;
            text.value = tidied.text;
          },
        }),
      ),
      el(
        "div",
        { class: "row" },
        el("button", { class: "button primary", text: "保存", onclick: () => void save() }),
        el("button", { class: "button ghost", text: "やめる", onclick: close }),
      ),
    ),
  );
  document.body.append(backdrop);
  text.focus();
  if (dictate) void mic.element.querySelector("button").click();
}

document.addEventListener("keydown", (event) => {
  if (!opened || event.target.matches("input, textarea")) return;
  if (event.key === " " || event.key === "PageDown") {
    event.preventDefault();
    reader.turn(1);
  }
  if (event.key === "PageUp") {
    event.preventDefault();
    reader.turn(-1);
  }
});

// A companion speaking up is not the answer to anything: it arrives on its own,
// and the thread has to notice.
api.on("persona", (event) => {
  const data = event?.data?.[0] || event?.data || {};
  if (!opened || data.bookId !== opened.book.id) return;
  void chat.refresh();
  if (panelTab !== "chat" || !panelOpen) toast(`${data.name || "ペルソナ"} が何か言っています`);
});

api.on("pipeline", (event) => {
  const status = event?.data?.[0] || event?.data || {};
  if (!opened || status.bookId !== opened.book.id) return;
  opened.status = status;
  if (panelTab === "settings") settingsPanel.draw(opened);
});

// The settings decide whether dictation is offered at all, so they are loaded
// before the first screen is drawn.
api.settings().then((loaded) => {
  settings = loaded;
  narrator.setRate(loaded.readAloudRate || 1);
  narrator.setVoice(loaded.readAloudVoice || "");
}).catch(() => {});

showHome();
