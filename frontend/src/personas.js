// Who is in the room.
//
// Each one is a domain and a set of tools. There is no ceiling on
// how many: the routing is what keeps a post from waking all of them, so adding
// a domain costs one more line in one routing call rather than one more model
// call per post.
import { el, clear, toast, dialogCloseButton, mountOverlay, unmountOverlay } from "./dom.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { showPersonaBuilder, addedToast } from "./persona-builder.js";
import { showPersonaEditor } from "./persona-editor.js";
import { gloss } from "./builtins.js";
import { createMemoryView } from "./memory.js";
import { capability, toolLabel, uniqueTools } from "./tool_labels.js";

// The panel shows subjects and people apart, each with a search, and people
// can be put in name order. Which tab and which order are the viewer's own
// convenience, kept in this browser when it lets us.
const remembered = (key, fallback, allowed) => {
  try {
    const value = globalThis.localStorage?.getItem(key);
    return allowed.includes(value) ? value : fallback;
  } catch {
    return fallback;
  }
};
const remember = (key, value) => {
  try { globalThis.localStorage?.setItem(key, value); } catch { /* not kept */ }
};

// fold makes width, case, kana and spacing not matter, as the app does when it
// hears a person's name in a post.
export function fold(text) {
  return String(text || "").normalize("NFKC").toLowerCase()
    .replace(/\s+/g, "")
    .replace(/[\u30a1-\u30f6]/g, (c) => String.fromCharCode(c.charCodeAt(0) - 0x60));
}

// matches reports whether a persona is found by the words typed: its name, id,
// what it covers, and for a person its names and keywords.
export function matches(row, query) {
  const words = fold(query) ? String(query).split(/[\s　]+/).map(fold).filter(Boolean) : [];
  if (!words.length) return true;
  const shown = gloss(row);
  const text = fold([shown.name, row.id, shown.note, shown.domain, row.domain, ...(row.aliases || []), ...(row.keywords || [])].join("\n"));
  return words.every((word) => text.includes(word));
}

// A name in kanji does not sort by how it is read. A person's names usually
// include a spelling in kana (the builder adds them), so that reading is what
// name order goes by; without one, the name itself.
const KANA = /^[\u3041-\u309f\u30a0-\u30ff\u30fcー・]+$/u;
const KANJI = /\p{Script=Han}/u;
export function readingOf(row) {
  const name = String(row.name || "");
  if (!KANJI.test(name)) return name;
  return (row.aliases || []).find((alias) => KANA.test(alias)) || name;
}

export function byName(rows, language = getLanguage()) {
  const collator = new Intl.Collator(language === "ja" ? "ja" : "en", { numeric: true, sensitivity: "base" });
  return [...rows].sort((a, b) => collator.compare(fold(readingOf(a)), fold(readingOf(b))) || collator.compare(a.name || "", b.name || ""));
}

const localZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

// A schedule is shown in the zone it was set in, so the card reads the way the
// persona thinks about it.
function scheduleWhen(schedule) {
  const at = new Date(schedule.at);
  if (Number.isNaN(at.getTime())) return "";
  const locale = getLanguage() === "ja" ? "ja-JP" : "en-US";
  const parts = (options) => {
    try {
      return new Intl.DateTimeFormat(locale, { timeZone: schedule.timeZone || localZone(), ...options }).format(at);
    } catch {
      return new Intl.DateTimeFormat(locale, options).format(at);
    }
  };
  const clock = parts({ hour: "2-digit", minute: "2-digit" });
  return schedule.daily ? `${t("毎日")} ${clock}` : `${parts({ month: "2-digit", day: "2-digit" })} ${clock}`;
}

export function createPersonas({ onChange, onSearch, onPosted, getSettings }) {
  const view = el("div", { class: "panel-body" });
  let rows = [];
  let toolNames = [];
  let mcpConfigured = false;
  let agentPaused = false;
  let agentAway = false;
  // With no tab chosen yet, people come first when there are any.
  let tab = remembered("kakeratta.personas.tab", "", ["subject", "person"]);
  let order = remembered("kakeratta.personas.order", "added", ["added", "name"]);
  let query = "";
  const list = el("div", { class: "persona-list" });

  async function load({ quiet = false } = {}) {
    try {
      const payload = await api.personas();
      rows = payload.personas || [];
      toolNames = payload.tools || [];
      mcpConfigured = !!payload.mcpConfigured;
      agentPaused = !!payload.externalAgentPaused;
      agentAway = !!payload.externalAgentAway;
      onChange?.(rows);
      draw();
    } catch (error) {
      if (!quiet) toast(String(error.message || error), "error");
    }
  }

  async function setActive(id, on) {
    const active = rows.filter((row) => (row.id === id ? on : row.active)).map((row) => row.id);
    try {
      const payload = await api.setActive(active);
      rows = payload.personas || rows;
      onChange?.(rows);
      draw();
    } catch (error) {
      toast(String(error.message || error), "error");
    }
  }

  function toolLink(name, label = t(toolLabel(name))) {
    const known = ["web_search", "place_search", "route", "timeline_search", "notes", "schedule"];
    const lang = getLanguage();
    const base = capability(name);
    const anchor = known.includes(base) ? `${lang}-tool-${base}` : `${lang}-tools`;
    return el("a", { href: `/manual/?lang=${lang}#${anchor}`, target: "_blank", rel: "noopener noreferrer",
      text: label, title: t("{0} の説明を開く（新しいタブ）", label) });
  }

  // A name tapped on the main screen opens this panel, which reloads as it
  // opens: the card to scroll to is remembered until there is one to scroll to.
  let pending = null;
  function revealPending() {
    if (!pending) return;
    // The card may be on the other tab or hidden by the search: go to it.
    const row = rows.find((candidate) => candidate.id === pending);
    if (row) {
      const kind = row.kind === "person" ? "person" : "subject";
      if (kind !== tab || !matches(row, query)) {
        tab = kind;
        query = "";
        draw();
        return;
      }
    }
    const card = view.querySelector(`[data-id="${pending}"]`);
    if (!card) return;
    pending = null;
    card.scrollIntoView({ block: "center", behavior: "smooth" });
    card.classList.add("revealed");
    setTimeout(() => card.classList.remove("revealed"), 2000);
  }

  // A persona just added is shown where it landed, its switch already on.
  async function showAdded(skill) {
    await load();
    pending = skill.id;
    revealPending();
  }

  function draw() {
    if (!tab) tab = rows.some((row) => kindOf(row) === "person") ? "person" : "subject";
    clear(view);
    view.append(
      el("p", {
        class: "hint",
        label: "人や関心ごとに、担当の記憶が分かれています。人物の担当にはその人の名前が出た投稿が、" +
          "関心ごとの担当には本体が振り分けた投稿が届き、喋るかどうかは各担当が決めます。@名前 で呼べば必ず答えます。",
      }),
    );
    view.append(el("button", { class: "button primary", label: "AIと担当を作る", onclick: () => showPersonaBuilder({ kind: tab, getSettings, onSave: showAdded, onPosted }) }));
    view.append(el("p", { class: "hint", label: "どの担当も、必要なときに過去のかけらを検索し、元の投稿を参照できます。" }));
    view.append(el("p", { class: "hint" },
      el("span", { label: "すべての担当が使える道具: " }),
      toolLink("timeline_search", t("過去のかけら検索")), " / ",
      toolLink("notes", t("記憶の読み書き"))));
    view.append(drawTabs(), drawFilters(), list);
    drawList();
    view.append(drawInstall());
    revealPending();
  }

  const kindOf = (row) => row.kind === "person" ? "person" : "subject";

  function drawTabs() {
    const count = (kind) => rows.filter((row) => kindOf(row) === kind).length;
    const bar = el("div", { class: "persona-tabs", role: "tablist", "aria-label": t("担当の種類") });
    // "人物" already reads "Person" on a card; a tab holds several.
    const ja = getLanguage() === "ja";
    for (const [kind, label] of [["person", ja ? "人物" : "People"], ["subject", ja ? "関心ごと" : "Interests"]]) {
      const selected = kind === tab;
      bar.append(el("button", {
        class: "persona-tab", type: "button", role: "tab", "aria-selected": selected ? "true" : "false",
        text: `${label} ${count(kind)}`,
        onclick: () => {
          if (tab === kind) return;
          tab = kind;
          remember("kakeratta.personas.tab", tab);
          draw();
        },
      }));
    }
    return bar;
  }

  function drawFilters() {
    const search = el("input", {
      type: "search", class: "persona-search", maxlength: 200, "aria-label": t("担当を検索"),
      i18nPlaceholder: tab === "person" ? "名前・呼び名・キーワードで探す" : "名前・担当条件で探す",
    });
    search.value = query;
    search.addEventListener("input", () => {
      query = search.value;
      drawList();
    });
    const row = el("div", { class: "persona-filters" }, search);
    if (tab === "person") {
      const sort = el("select", { class: "persona-order", "aria-label": t("並べ順") },
        el("option", { value: "added", label: "追加順" }),
        el("option", { value: "name", label: "名前順" }));
      sort.value = order;
      sort.addEventListener("change", () => {
        order = sort.value === "name" ? "name" : "added";
        remember("kakeratta.personas.order", order);
        drawList();
      });
      row.append(sort);
    }
    return row;
  }

  function drawList() {
    clear(list);
    const ofTab = rows.filter((row) => kindOf(row) === tab);
    const found = ofTab.filter((row) => matches(row, query));
    const shown = tab === "person" && order === "name" ? byName(found) : found;
    if (!ofTab.length) {
      list.append(el("p", { class: "hint", label: tab === "person"
        ? "人物の担当はまだありません。「AIと担当を作る」で「佐藤さんのことを覚えて」のように頼むと作れます。"
        : "関心ごとの担当はまだありません。" }));
      return;
    }
    if (!shown.length) {
      list.append(el("p", { class: "hint", label: "一致する担当がありません" }));
      return;
    }
    for (const row of shown) list.append(drawRow(row));
  }

  // Whether the external agent does the thinking - reading the context,
  // looking things up and writing the answer - when it can. The app decides,
  // checks and runs the notes, schedules and actions either way.
  // The choice only appears once an external agent is connected: most people
  // never connect one and need not know there is anything to choose. A
  // persona already handed over keeps it, so it can be handed back.
  function drawPlanner(row) {
    if (!mcpConfigured && row.planner !== "external") return null;
    // "Prefer": the agent plans when it answers in time, and the built-in
    // model still takes the turn when it does not.
    const prefer = el("input", { type: "checkbox", checked: row.planner === "external" });
    prefer.addEventListener("change", async () => {
      prefer.disabled = true;
      try {
        const payload = await api.setPlanner(row.id, prefer.checked ? "external" : "builtin");
        rows = payload.personas || rows;
        mcpConfigured = payload.mcpConfigured ?? mcpConfigured;
        agentPaused = payload.externalAgentPaused ?? agentPaused;
        onChange?.(rows);
        draw();
      } catch (error) {
        toast(String(error.message || error), "error");
        draw();
      }
    });
    const box = el("div", {}, el("div", { class: "compose-row" },
      el("label", { class: "switch" }, prefer, el("span", { label: "外部エージェントを優先する" }))));
    if (row.planner === "external") {
      box.append(el("p", { class: "hint", label: !mcpConfigured
        ? "外部エージェントが未接続です。設定 → 上級オプション → 外部連携 で接続してください。それまでは内蔵モデルが代わりに考えます。"
        : agentPaused
        ? "外部エージェントは一時停止中です。再開するまで内蔵モデルが考えます。"
        : agentAway
        ? "外部エージェントから応答がありません。接続があるまで内蔵モデルが考えます。"
        : "外部エージェントが文脈を読んで調べ、回答を書きます。記憶・予定・操作はアプリが決めます。時間内に取得・返却されなければ内蔵モデルが代わりに答えます。" }));
    }
    return box;
  }

  function drawRow(row) {
    const shown = gloss(row);
    const box = el("section", { class: `persona ${row.active ? "on" : ""}`, "data-id": row.id });
    box.append(
      el(
        "div",
        { class: "persona-head" },
        el("span", { class: "avatar", text: row.icon || "◎" }),
        el("span", { class: "who", text: shown.name }),
        shown.note ? el("span", { class: "note-tag", text: shown.note }) : null,
        row.kind === "person" ? el("span", { class: "tag", label: "人物" }) : null,
        row.planner === "external" ? el("span", { class: "tag", label: "外部エージェント" }) : null,
        el("span", { class: "spacer" }),
        el("label", { class: "switch" },
          el("input", {
            type: "checkbox",
            checked: row.active,
            onchange: (event) => void setActive(row.id, event.target.checked),
          }),
          el("span", { text: t("有効にする") })),
      ),
      el("p", { class: "domain", text: shown.domain }),
    );
    // A person is reached by these words and nothing else, so they are what
    // the card has to show.
    if (row.kind === "person") {
      box.append(
        el("p", { class: "hint", text: t("呼び名: {0}", [row.name, ...(row.aliases || [])].join(" / ")) }),
        el("p", { class: "hint", text: t("キーワード: {0}", (row.keywords || []).join(" / ") || t("（なし）")) }));
    }
    const planner = drawPlanner(row);
    if (planner) box.append(planner);
    // Every active persona checks in, at a time of the app's choosing between
    // 07:00 and 21:00: once a day, or - for a person seen now and then - once
    // a month, reading the month's summary. The user chooses how often a
    // person is looked back on, and how much any of them says.
    const schedule = row.dailyCheckIn || {};
    const monthly = row.kind === "person" && schedule.every === "monthly";
    const save = async (change, control) => {
      control.disabled = true;
      try {
        const base = { talk: schedule.talk || "share", ...(row.kind === "person" ? { every: monthly ? "monthly" : "" } : {}) };
        const payload = await api.setDailyCheckIn(row.id, { ...base, ...change });
        rows = payload.personas || rows;
        onChange?.(rows);
        draw();
      } catch (error) {
        toast(String(error.message || error), "error");
        draw();
      }
    };
    const minimal = el("input", { type: "checkbox", checked: schedule.talk === "minimal", disabled: !row.active });
    minimal.addEventListener("change", () => void save({ talk: minimal.checked ? "minimal" : "share" }, minimal));
    const controls = el("div", { class: "compose-row" });
    if (row.kind === "person") {
      const every = el("select", { class: "check-in-every", "aria-label": t("振り返る頻度"), disabled: !row.active },
        el("option", { value: "", label: "毎日振り返る" }),
        el("option", { value: "monthly", label: "月1回振り返る" }));
      every.value = monthly ? "monthly" : "";
      every.addEventListener("change", () => void save({ every: every.value }, every));
      controls.append(every);
    }
    controls.append(el("label", { class: "switch" }, minimal, el("span", { label: "チェックインでは最低限のことだけ話す" })));
    box.append(controls);
    box.append(el("p", { class: "hint", text: monthly
      ? t(schedule.talk === "minimal"
        ? "毎月{0}日、本体が月のまとめを書いたあとに起き、まとめと名前が出た投稿を読んでメモを整えます。話すのは、あなたの判断が必要なときだけです。"
        : "毎月{0}日、本体が月のまとめを書いたあとに起き、まとめと名前が出た投稿と自分のメモを見直して、伝える価値があることを見つけたときに話します。", schedule.on || "")
      : t(schedule.talk === "minimal"
        ? "毎日7時〜21時のどこかで起きてメモを整えます。話すのは、メモを忘れてよいかの確認など、あなたの判断が必要なときだけです。"
        : "毎日7時〜21時のどこかで起き、最近の記録と自分のメモを見直して、伝える価値があることを見つけたときに話します。") }));
    if (row.schedules?.length) {
      box.append(el("div", { class: "compose-row" },
        el("span", { class: "hint", label: "この担当が預けた予定" }),
        ...row.schedules.map((schedule) => {
          const text = `${scheduleWhen(schedule)} · ${schedule.note || ""}`.trim();
          return el("span", { class: "chip ghost schedule", text },
            el("button", { type: "button", class: "chip-remove", text: "×", "aria-label": t("予定「{0}」を消す", text),
              onclick: async (event) => {
                const button = event.currentTarget;
                if (!confirm(t("予定「{0}」を消しますか", text))) return;
                button.disabled = true;
                try {
                  const payload = await api.deleteSchedule(row.id, schedule.id);
                  rows = payload.personas || rows;
                  onChange?.(rows);
                  draw();
                } catch (error) {
                  toast(String(error.message || error), "error");
                  button.disabled = false;
                }
              } }));
        }),
      ));
      box.append(el("p", { class: "hint", label: "予定は担当が置きます。不要なら × で消せます。メモを消すと担当が見直します。" }));
    }
    if (row.tools?.length) {
      box.append(el("p", { class: "tools" }, `${t("道具")}: `,
        uniqueTools(row.tools).flatMap((name, index) => index ? [" / ", toolLink(name)] : [toolLink(name)])));
    }
    box.append(
      el(
        "div",
        { class: "compose-row" },
        el("span", { class: "hint", text: t("起きた回数 {0}", row.wakes || 0) }),
        el("span", { class: "spacer" }),
        el("button", { class: "chip ghost", label: "修正", onclick: () => showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load, onPosted }) }),
        el("button", { class: "chip ghost", label: "AIで修正", onclick: () => showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load, onPosted, ai: true }) }),
        el("button", { class: "chip ghost", label: "覚えていること", onclick: () => void showMemory(row) }),
        row.builtin
          ? null
          : el("button", {
            class: "chip ghost",
            label: "削除",
            onclick: async () => {
              if (!confirm(t("{0} を削除しますか。メモも消え、元に戻せません。", row.name))) return;
              await api.uninstallPersona(row.id).catch((error) => toast(String(error.message || error), "error"));
              await load();
            },
          }),
      ),
    );
    return box;
  }

  // focusPath: a note to open and scroll to first - the one a remark on the
  // timeline said it wrote.
  async function showMemory(row, focusPath = "") {
    // The notes only grow, so they come a page at a time, the most recently
    // written first. The first page also names every note, for links and the
    // count.
    const memory = { notes: [], paths: [], next: "", filter: { tag: "", type: "" } };
    let memoryGeneration = 0;
    const newest = (a, b) => String(b.written || "").localeCompare(String(a.written || "")) ||
      (a.path < b.path ? 1 : a.path > b.path ? -1 : 0);
    const take = (notes) => {
      const known = new Set(memory.notes.map(note => note.path));
      memory.notes = [...memory.notes, ...(notes || []).filter(note => !known.has(note.path))].sort(newest);
    };
    const withOverview = async (notes, filter = {}) => {
      if (filter.tag || filter.type || !memory.paths.includes("index.md") || notes.some(note => note.path === "index.md")) return notes;
      const index = await api.note(row.id, "index.md").catch(() => null);
      return index ? [...notes, index] : notes;
    };
    try {
      const page = await api.memory(row.id);
      memory.paths = page.paths || (page.notes || []).map(note => note.path);
      memory.next = page.next || "";
      // The overview belongs above the list even when its last update is too
      // old to be included in the first page of recently written notes.
      memory.notes = await withOverview(page.notes || []);
      // A note named from the timeline may sit on a later page.
      if (focusPath && !memory.notes.some(note => note.path === focusPath) && memory.paths.includes(focusPath)) {
        // A note gone since the link was drawn still opens the memory.
        const note = await api.note(row.id, focusPath).catch(() => null);
        if (note) take([note]);
      }
    } catch (error) {
      toast(String(error.message || error), "error");
      return;
    }
    const memoryHost = el("div");
    const reveal = (path) => {
      const card = [...memoryHost.querySelectorAll("[data-path]")].find((node) => node.getAttribute("data-path") === path);
      if (!card) return;
      if (card.tagName === "DETAILS") card.open = true;
      card.scrollIntoView?.({ block: "start" });
      card.focus?.({ preventScroll: true });
    };
    const failed = (error) => toast(String(error.message || error), "error");
    const drawMemory = () => memoryHost.replaceChildren(createMemoryView(memory.notes, {
      allPaths: memory.paths,
      filter: memory.filter,
      onFilter: async (filter) => {
        const generation = ++memoryGeneration;
        try {
          const page = await api.memory(row.id, filter);
          const notes = await withOverview(page.notes || [], filter);
          if (generation !== memoryGeneration) return;
          Object.assign(memory, { notes, next: page.next || "", filter });
          drawMemory();
          memoryHost.focus();
        } catch (error) { if (generation === memoryGeneration) failed(error); }
      },
      onMore: memory.next ? async () => {
        const generation = memoryGeneration;
        const after = memory.next;
        try {
          const page = await api.memory(row.id, { ...memory.filter, after });
          if (generation !== memoryGeneration) return;
          take(page.notes);
          memory.next = page.next || "";
          drawMemory();
          memoryHost.querySelector(".memory-more")?.focus();
        } catch (error) { if (generation === memoryGeneration) failed(error); }
      } : undefined,
      onOpenMissing: async (path) => {
        try {
          take([await api.note(row.id, path)]);
          drawMemory();
          reveal(path);
        } catch (error) { failed(error); }
      },
      onSearch: onSearch ? (query) => { close(); onSearch(query); } : undefined,
      onForget: async (path) => {
        await api.forgetNote(row.id, path);
        memory.notes = memory.notes.filter(note => note.path !== path);
        memory.paths = memory.paths.filter(other => other !== path);
        drawMemory();
        memoryHost.focus();
      },
      onEdit: async (path, text) => {
        const updated = await api.editNote(row.id, path, text);
        memory.notes = memory.notes.map(note => note.path === path ? { ...note, ...updated } : note);
        drawMemory();
        toast(t("メモを保存しました"));
        memoryHost.focus();
      },
    }));
    memoryHost.setAttribute("tabindex", "-1");
    const backdrop = el("div", { class: "dialog-backdrop" });
    const previousFocus = document.activeElement;
    const close = () => { unmountOverlay(backdrop); previousFocus?.focus(); };
    backdrop.addEventListener("click", (event) => { if (event.target === backdrop) close(); });
    backdrop.addEventListener("keydown", (event) => {
      if (event.key === "Escape") { event.preventDefault(); close(); }
      if (event.key !== "Tab") return;
      const focusable = [...backdrop.querySelectorAll("button, a[href], summary, textarea")]
        .filter(node => !node.disabled && node.getClientRects().length);
      const first = focusable[0], last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    });
    drawMemory();
    backdrop.append(
      el(
        "div",
        { class: "dialog memory-dialog", role: "dialog", "aria-modal": "true", "aria-label": t("{0} があなたについて覚えていること", gloss(row).name) },
        dialogCloseButton(close),
        el("h2", { text: t("{0} があなたについて覚えていること", gloss(row).name) }),
        el("p", { class: "hint", label: "担当が保存したメモです。根拠のリンクから元の投稿や関連する記憶を確認できます。" }),
        memoryHost,
        el(
          "div",
          { class: "compose-row" },
          el("button", {
            class: "chip ghost",
            label: "すべてのメモを忘れさせる",
            onclick: async () => {
              if (!confirm(t("{0} の記憶を消しますか。元に戻せません。", row.name))) return;
              try {
                await api.forget(row.id);
                close();
                await load();
              } catch (error) { toast(String(error.message || error), "error"); }
            },
          }),
          el("span", { class: "spacer" }),
          el("button", { class: "button primary", label: "閉じる", onclick: close }),
        ),
      ),
    );
    mountOverlay(backdrop);
    backdrop.querySelector(".memory-dialog").setAttribute("tabindex", "-1");
    backdrop.querySelector(".memory-dialog").focus();
    if (focusPath) reveal(focusPath);
  }

  function drawInstall() {
    const area = el("textarea", {
      class: "skill-input",
      rows: "6",
      placeholder: JSON.stringify(
        {
          id: "wine",
          name: "ぶどう",
          icon: "🍷",
          domain: "ワイン。銘柄、産地、ラベルの写真、飲んだ記録。食事全般や技術の話には反応しない。",
          tone: "控えめ。おすすめを押しつけない。",
          memory: "型は Wine（銘柄。産地・品種・感想）と Preference（好みの傾向）の2つ。同じ好みが3回出たら Preference に統合する。",
          instructions: "引用と本人の意見を分け、推測は明記し、統合後も代表例と反例を残す。",
          tools: uniqueTools(toolNames).slice(0, 2),
        },
        null,
        2,
      ),
    });
    return el(
      "details",
      { class: "install" },
      el("summary", { label: "担当を追加する（skill.json）" }),
      el("p", {
        class: "hint",
        text: `${t("担当領域は本体が読んで振り分けに使います。指示は本人だけが読みます。メモの形式（OKF）は全員に渡るので、指示には「どんな type を使い、いつ統合するか」を書いてください。")} ` +
          `${t("使える道具")}: ${uniqueTools(toolNames).join(" / ") || t("（なし）")}`,
      }),
      area,
      el(
        "div",
        { class: "compose-row" },
        el("button", {
          class: "button primary",
          label: "この担当を追加",
          onclick: async () => {
            try {
              const skill = await api.installPersona(area.value);
              addedToast(skill);
              area.value = "";
              await showAdded(skill);
            } catch (error) {
              toast(String(error.message || error), "error");
            }
          },
        }),
      ),
    );
  }

  return {
    element: view,
    load,
    rows: () => rows,
    // The banner on the main screen can put someone in the room without
    // opening this panel at all.
    activate: (id) => setActive(id, true),
    reveal: (id) => { pending = id; revealPending(); },
    // A remark on the timeline leads to the note written with it.
    showMemoryFor: (row, path) => showMemory(row, path),
  };
}
