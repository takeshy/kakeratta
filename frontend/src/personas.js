// Who is in the room.
//
// Each one is a domain and a set of tools. There is no ceiling on
// how many: the routing is what keeps a post from waking all of them, so adding
// a domain costs one more line in one routing call rather than one more model
// call per post.
import { avatarNode } from "./avatar.js";
import { showEntryDialog } from "./entry-dialog.js";
import { preserveScroll, switchControl, el, clear, toast, dialogCloseButton, mountOverlay, unmountOverlay } from "./dom.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { showPersonaBuilder } from "./persona-builder.js";
import { showPersonaEditor } from "./persona-editor.js";
import { basicInfoBox } from "./person-basic-info.js";
export { basicInfoBox };
import { gloss } from "./builtins.js";
import { createUsage } from "./usage.js";
import { createMemoryView } from "./memory.js";
import { uniqueTools } from "./tool_labels.js";
import { notificationSchedules, toolLink } from "./persona-display.js";

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

export function createPersonas({ onChange, onSearch, onPosted, getSettings }) {
  const view = el("div", { class: "panel-body" });
  let rows = [];
  let toolNames = [];
  let mcpConfigured = false;
  let agentPaused = false;
  // With no tab chosen yet, people come first when there are any.
  let tab = remembered("kakeratta.personas.tab", "", ["subject", "person"]);
  let order = remembered("kakeratta.personas.order", "added", ["added", "name"]);
  let query = "";
  const list = el("div", { class: "persona-list" });
  const activeControls = new Map();
  const plannerControls = new Map();
  let savingActive = false;

  function updateRows(next) {
    rows = next;
    onChange?.(rows);
    draw();
  }

  async function load({ quiet = false } = {}) {
    try {
      const payload = await api.personas();
      rows = payload.personas || [];
      toolNames = payload.tools || [];
      mcpConfigured = !!payload.mcpConfigured;
      agentPaused = !!payload.externalAgentPaused;
      onChange?.(rows);
      draw();
    } catch (error) {
      if (!quiet) toast(String(error.message || error), "error");
    }
  }

  async function setActive(id, on) {
    if (savingActive) return;
    savingActive = true;
    for (const control of activeControls.values()) control.toggle.disabled = true;
    const active = rows.filter((row) => (row.id === id ? on : row.active)).map((row) => row.id);
    try {
      const payload = await api.setActive(active);
      rows = payload.personas || rows.map(row => ({ ...row, active: active.includes(row.id) }));
      onChange?.(rows);
    } catch (error) {
      toast(String(error.message || error), "error");
    } finally {
      savingActive = false;
      for (const row of rows) {
        const control = activeControls.get(row.id);
        if (!control) continue;
        control.toggle.checked = !!row.active;
        control.toggle.disabled = false;
        control.box.className = `persona ${row.active ? "on" : ""}`;
        if (control.every) control.every.disabled = !row.active;
        Object.assign(control.row, row);
      }
    }
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
          query = "";
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
    activeControls.clear();
    plannerControls.clear();
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
  // checks and runs the notes and schedules either way.
  // Show the preference only while a configured external agent is enabled.
  // Saved preferences remain available when the agent is enabled again.
  function drawPlanner(row) {
    if (!mcpConfigured || agentPaused) return null;
    // "Prefer": the agent plans when it answers in time, and the built-in
    // model still takes the turn when it does not.
    const prefer = el("input", { type: "checkbox", checked: row.planner === "external" });
    const box = el("div", {}, el("div", { class: "compose-row" },
      el("label", { class: "planner-option" }, prefer, el("span", { label: "外部エージェントを優先する" }))));
    plannerControls.set(row.id, { prefer, box });
    prefer.addEventListener("change", async () => {
      prefer.disabled = true;
      try {
        const planner = prefer.checked ? "external" : "builtin";
        const payload = await api.setPlanner(row.id, planner);
        rows = payload.personas || rows.map(current => current.id === row.id ? { ...current, planner } : current);
        mcpConfigured = payload.mcpConfigured ?? mcpConfigured;
        agentPaused = payload.externalAgentPaused ?? agentPaused;
        preserveScroll(view, () => {
          for (const current of rows) {
            const card = activeControls.get(current.id);
            if (!card) continue;
            Object.assign(card.row, current);
            const control = plannerControls.get(current.id);
            if (control) {
              control.prefer.checked = current.planner === "external";
              control.box.hidden = !mcpConfigured || agentPaused;
            }
          }
          onChange?.(rows);
        });
      } catch (error) {
        toast(String(error.message || error), "error");
      } finally {
        prefer.checked = row.planner === "external";
        prefer.disabled = false;
      }
    });
    return box;
  }

  function drawRow(row) {
    const shown = gloss(row);
    const box = el("section", { class: `persona ${row.active ? "on" : ""}`, "data-id": row.id });
    const toggle = el("input", {
      type: "checkbox", checked: row.active, disabled: savingActive,
      "aria-label": t("{0} の有効・無効", shown.name),
      onchange: (event) => setActive(row.id, event.target.checked),
    });
    const control = { box, toggle, row };
    activeControls.set(row.id, control);
    box.append(
      el(
        "div",
        { class: "persona-head" },
        avatarNode(row),
        el("span", { class: "who", text: shown.name }),
        shown.note ? el("span", { class: "note-tag", text: shown.note }) : null,
        row.kind === "person" ? el("span", { class: "tag", label: "人物" }) : null,
        el("span", { class: "spacer" }),
        el("label", { class: "switch" },
          switchControl(toggle, { on: t("有効"), off: t("無効"), className: getLanguage() === "en" ? "persona-active-toggle" : "" })),
      ),
      el("p", { class: "domain", text: shown.domain }),
    );
    // A person is reached by these words and nothing else, so they are what
    // the card has to show.
    if (row.kind === "person") {
      box.append(
        el("p", { class: "hint", text: t("呼び名: {0}", [row.name, ...(row.aliases || [])].join(" / ")) }),
        el("p", { class: "hint", text: t("キーワード: {0}", (row.keywords || []).join(" / ") || t("（なし）")) }));
      box.append(basicInfoBox(row, () => showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load, onPosted, focus: "basicInfo" })));
    }
    const planner = drawPlanner(row);
    if (planner) box.append(planner);
    if (row.memoryEnabled !== false && row.kind === "person") {
      const monthly = row.dailyCheckIn?.every === "monthly";
      const every = el("select", { class: "check-in-every", "aria-label": t("振り返る頻度"), disabled: !row.active },
        el("option", { value: "", label: "頻繁に振り返る" }),
        el("option", { value: "monthly", label: "ときどき振り返る" }));
      every.value = monthly ? "monthly" : "";
      control.every = every;
      every.addEventListener("change", async () => {
        every.disabled = true;
        try {
          const payload = await api.setDailyCheckIn(row.id, { every: every.value });
          rows = payload.personas || rows;
          onChange?.(rows);
          draw();
        } catch (error) {
          toast(String(error.message || error), "error");
          draw();
        }
      });
      box.append(el("div", { class: "compose-row" }, every));
    }
    if (row.schedules?.length) {
      box.append(notificationSchedules(row, (payload) => updateRows(payload.personas || rows)));
      box.append(el("p", { class: "hint", label: "基本情報やかけらから登録された予定と、担当が置いた予定です。不要なら × で消せます。" }));
    }
    if (row.tools?.length) {
      box.append(el("p", { class: "tools" }, `${t("道具")}: `,
        uniqueTools(row.tools).flatMap((name, index) => index ? [" / ", toolLink(name)] : [toolLink(name)])));
    }
    box.append(
      el("p", { class: "hint", text: t("起きた回数 {0}", row.wakes || 0) }),
      el(
        "div",
        { class: "persona-actions" },
        el("button", { class: "chip", label: "修正", onclick: () => showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load, onPosted }) }),
        el("button", { class: "chip", label: "AIで修正", onclick: () => showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load, onPosted, ai: true }) }),
        row.memoryEnabled === false ? null : el("button", { class: "chip", label: "覚えていること", onclick: () => void showMemory(row) }),
        row.builtin
          ? null
          : el("button", {
            class: "chip ghost delete",
            label: "削除",
            onclick: async () => {
              if (!confirm(t("{0} を削除しますか。メモも消え、元に戻せません。", row.name))) return;
              await api.uninstallPersona(row.id).catch((error) => toast(String(error.message || error), "error"));
              await load();
            },
          }),
      ),
    );
    box.append(createUsage({ persona: row.id }).element);
    return box;
  }

  // focusPath: a note to open and scroll to first - the one a remark on the
  // timeline said it wrote.
  async function showMemory(row, focusPath = "") {
    if (row.memoryEnabled === false) return;
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
    // The memory file belongs on top even when it was written too long ago
    // for the first page.
    const withMemo = async (notes, filter = {}) => {
      if (filter.tag || filter.type || notes.some(note => note.path === "Memo.md") || !memory.paths.includes("Memo.md")) return notes;
      const memo = await api.note(row.id, "Memo.md").catch(() => null);
      return memo ? [...notes, memo] : notes;
    };
    try {
      const page = await api.memory(row.id);
      memory.paths = page.paths || (page.notes || []).map(note => note.path);
      memory.next = page.next || "";
      memory.notes = await withMemo(page.notes || []);
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
      onOpenEntry: (id) => showEntryDialog(id, { getSettings, getPersonas: () => rows, getPersona: (id) => rows.find((persona) => persona.id === id) }),
      onEditFact: async (path, fact, text) => {
        const updated = await api.editFact(row.id, fact, text);
        memory.notes = memory.notes.map(note => note.path === path ? { ...note, ...updated } : note);
        drawMemory();
        toast(t("事実を直しました"));
        reveal(path);
      },
      onForgetFact: async (path, fact) => {
        const updated = await api.forgetFact(row.id, fact);
        memory.notes = updated.gone
          ? memory.notes.filter(note => note.path !== path)
          : memory.notes.map(note => note.path === path ? { ...note, ...updated } : note);
        drawMemory();
        reveal(path);
      },
      allPaths: memory.paths,
      filter: memory.filter,
      onFilter: async (filter) => {
        const generation = ++memoryGeneration;
        try {
          const page = await api.memory(row.id, filter);
          const notes = await withMemo(page.notes || [], filter);
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
        // What was saved about the person by the user is not a note: it sits
        // above the notes, open, and is edited in the editor.
        row.kind === "person"
          ? basicInfoBox(row, () => { close(); showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load, onPosted, focus: "basicInfo" }); }, { open: true })
          : null,
        el("p", { class: "hint", label: "分野ごとのまとめ文と事実、かけらごとの記録です。「元の投稿」から、もとになった投稿と会話を開けます。" }),
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

  return {
    element: view,
    load,
    rows: () => rows,
    updateRows,
    // The banner on the main screen can put someone in the room without
    // opening this panel at all.
    activate: (id) => setActive(id, true),
    reveal: (id) => { pending = id; revealPending(); },
    // A remark on the timeline leads to the note written with it.
    showMemoryFor: (row, path) => showMemory(row, path),
    // Another view of a persona - the list of what it said - opens its editor.
    edit: (row, focus = "") => showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load, onPosted, focus }),
  };
}
