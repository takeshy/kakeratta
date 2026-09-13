import { el, when } from "./dom.js";
import { t } from "./i18n.js";
import { api } from "./api.js";
import { createFeed } from "./feed.js";

export function createSearch({ getSettings, getPersona, onEntryChange, onReply }) {
  const query = el("input", { type: "search", maxlength: 500, required: true,
    i18nPlaceholder: "かけらを検索", "aria-label": t("かけらを検索") });
  const mode = el("select", { "aria-label": t("検索方法") },
    el("option", { value: "keyword", label: "キーワード" }),
    el("option", { value: "semantic", label: "意味検索" }));
  const submit = el("button", { type: "submit", class: "button", label: "検索" });
  const status = el("p", { class: "hint", role: "status", "aria-live": "polite" });
  const results = el("div", { class: "search-results" });
  const detail = el("div", { hidden: true });
  const more = el("button", { class: "button", label: "さらに読み込む", hidden: true,
    onclick: () => void run(true) });
  let generation = 0;
  let threadGeneration = 0;
  let selected = null;
  let selectedRoot = null;
  let hits = [];
  let total = 0;
  let searchQuery = "";
  let searchMode = "keyword";
  const thread = createFeed({ getSettings, getPersona, showIntro: false,
    onReply: () => { onReply?.(); return refresh(); },
    onEntryChange: (entry) => { onEntryChange?.(entry); changed(entry); },
  });
  const back = el("button", { class: "chip", label: "検索結果に戻る", onclick: () => {
    selected = null;
    ++threadGeneration;
    detail.hidden = true;
    results.hidden = false;
    more.hidden = hits.length >= total;
  } });
  detail.append(back, thread.element);

  function draw() {
    results.replaceChildren(...hits.map(({ entry, snippet }) => el("button", {
      class: "search-hit", onclick: () => void showEntry(entry.id),
    }, el("span", { class: "hint", text: `${when(entry.at)} · ${entry.speaker === "user" ? t("あなた") : getPersona(entry.speaker)?.name || entry.speaker}` }),
    el("span", { class: "search-snippet", text: snippet }),
    el("span", { class: "hint", label: "投稿を開く" }))));
  }

  async function run(append = false) {
    const current = ++generation;
    if (!append) {
      searchQuery = query.value.trim();
      searchMode = mode.value;
      hits = [];
      total = 0;
      draw();
    }
    if (!searchQuery) {
      submit.disabled = false;
      more.hidden = true;
      status.textContent = "";
      return;
    }
    selected = null;
    ++threadGeneration;
    detail.hidden = true;
    results.hidden = false;
    more.hidden = true;
    submit.disabled = true;
    status.textContent = t(searchMode === "semantic"
      ? "意味をもとに検索中…初回は過去のかけらの準備に時間がかかります"
      : "検索中…");
    try {
      const found = await api.searchTimeline({ query: searchQuery, mode: searchMode, limit: 20, offset: hits.length });
      if (current !== generation) return;
      hits = [...hits, ...found.hits];
      total = found.total;
      draw();
      status.textContent = found.total ? t(searchMode === "semantic" ? "近い候補{0}件中{1}件を表示" : "{0}件中{1}件を表示", found.total, hits.length) : t("一致するかけらがありません");
      more.hidden = hits.length >= found.total;
    } catch (error) {
      if (current === generation) {
        status.textContent = t(error.message);
        more.hidden = !append;
      }
    } finally {
      if (current === generation) submit.disabled = false;
    }
  }

  async function showEntry(id) {
    const current = ++threadGeneration;
    selected = id;
    selectedRoot = null;
    detail.hidden = false;
    results.hidden = true;
    more.hidden = true;
    thread.element.replaceChildren(el("p", { class: "hint", label: "読み込んでいます…" }));
    try {
      const found = await api.thread(id);
      if (current !== threadGeneration) return;
      selectedRoot = found.entries.find((entry) => entry.kind === "post")?.id;
      thread.set(found.entries);
      thread.reveal(id);
      const target = [...thread.element.querySelectorAll("[data-id]")].find((node) => node.dataset.id === id);
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
    } catch (error) {
      if (current === threadGeneration) thread.element.replaceChildren(el("p", { role: "alert", text: t(error.message) }));
    }
  }

  // Poll without replacing the reply editor or moving the scroll position.
  async function refresh() {
    if (!selected) return;
    const current = threadGeneration;
    const id = selected;
    try {
      const found = await api.thread(id);
      if (current === threadGeneration && id === selected) thread.set(found.entries);
    } catch {
      // A transient poll failure leaves the conversation and draft visible.
    }
  }

  function changed(entry) {
    if (entry.deleted) {
      const previousCount = hits.length;
      hits = hits.filter((hit) => hit.entry.id !== entry.id && hit.entry.root !== entry.id);
      total = Math.max(hits.length, total - (previousCount - hits.length));
      draw();
    }
    if (selected && (entry.id === selectedRoot || entry.root === selectedRoot)) {
      if (entry.deleted && (entry.id === selectedRoot || entry.id === selected)) void showEntry(selected);
      else thread.upsert(entry);
    }
  }

  const form = el("form", { class: "timeline-search-form", role: "search", onsubmit: (event) => {
    event.preventDefault(); void run();
  } }, query, mode, submit);
  const element = el("div", { class: "timeline-search" }, form,
    el("p", { class: "hint", label: "キーワードは空白で区切ると全語一致。意味検索は言い換えや曖昧な記憶から近い順に探します。" }),
    status, results, more, detail);
  return { element, showEntry, changed, refresh, focus: () => query.focus(),
    searchFor: (text) => { query.value = text; mode.value = "keyword"; return run(); },
  };
}
