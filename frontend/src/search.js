import { initialSearchMode } from "./search-intent.js";
import { el, when } from "./dom.js";
import { t } from "./i18n.js";
import { api } from "./api.js";
import { createFeed } from "./feed.js";
import { createVoiceInput } from "./voice_input.js";

export function createSearch({ getSettings, getPersona, getPersonas, onEntryChange, onReply, onSpeak, onSpeaker }) {
  const query = el("input", { type: "search", maxlength: 500, required: true,
    i18nPlaceholder: "かけらを検索", "aria-label": t("かけらを検索") });
  const submit = el("button", { type: "submit", class: "button", label: "検索" });
  const status = el("p", { class: "hint", role: "status", "aria-live": "polite" });
  const results = el("div", { class: "search-results" });
  const detail = el("div", { hidden: true });
  const more = el("button", { class: "button", label: "さらに読み込む", hidden: true,
    onclick: () => void run(true) });
  const switchMode = el("button", { class: "button secondary search-expand", hidden: true,
    onclick: () => run(false, searchMode === "keyword" || semanticUnavailable ? "hybrid" : "keyword") });
  let searchMode = "keyword";
  let semanticUnavailable = false;
  let busy = false;
  let completed = false;
  let generation = 0;
  let threadGeneration = 0;
  let selected = null;
  let selectedRoot = null;
  let hits = [];
  let total = 0;
  let searchQuery = "";
  const thread = createFeed({ getSettings, getPersona, getPersonas, onSpeak, showIntro: false,
    onSpeaker,
    onReply: () => { onReply?.(); return refresh(); },
    onEntryChange: (entry) => { onEntryChange?.(entry); changed(entry); },
  });
  const back = el("button", { class: "chip", label: "検索結果に戻る", onclick: () => {
    selected = null;
    ++threadGeneration;
    detail.hidden = true;
    results.hidden = false;
    controls();
  } });
  detail.append(back, thread.element);

  function draw() {
    results.replaceChildren(...hits.map(({ entry, snippet }) => el("button", {
      class: "search-hit", onclick: () => void showEntry(entry.id),
    }, el("span", { class: "hint", text: `${when(entry.at)} · ${entry.speaker === "user" ? t("あなた") : getPersona(entry.speaker)?.name || entry.speaker}` }),
    el("span", { class: "search-snippet", text: snippet }),
    el("span", { class: "hint", label: "投稿を開く" }))));
  }

  function controls() {
    more.hidden = Boolean(selected) || busy || hits.length >= total;
    switchMode.hidden = Boolean(selected) || !completed || query.value.trim() !== searchQuery;
    switchMode.disabled = busy;
    switchMode.textContent = t(searchMode === "keyword" || semanticUnavailable ? "似たような単語でも探す" : "キーワードだけで探す");
  }

  async function run(append = false, forcedMode = null) {
    const current = ++generation;
    const requestedMode = append ? searchMode : forcedMode || initialSearchMode(query.value);
    if (!append && !forcedMode) {
      searchQuery = query.value.trim();
      hits = [];
      total = 0;
      completed = false;
      draw();
    }
    if (!searchQuery) {
      busy = false;
      submit.disabled = false;
      status.textContent = "";
      controls();
      return;
    }
    selected = null;
    ++threadGeneration;
    detail.hidden = true;
    results.hidden = false;
    busy = true;
    submit.disabled = true;
    controls();
    status.textContent = t(requestedMode === "keyword" ? "キーワードで検索中…" : "意味をもとに検索中…");
    try {
      const found = await api.searchTimeline({ query: searchQuery, mode: requestedMode, limit: 20, offset: append ? hits.length : 0 });
      if (current !== generation) return;
      hits = append ? [...hits, ...found.hits] : found.hits;
      total = found.total;
      searchMode = requestedMode;
      semanticUnavailable = Boolean(found.semanticUnavailable);
      completed = true;
      draw();
      status.textContent = t(searchMode === "keyword" ? "キーワード検索" : "意味検索") + " · " +
        (found.total ? t(searchMode === "keyword" ? "{0}件中{1}件を表示" : "近い候補{0}件中{1}件を表示", found.total, hits.length) : t("一致するかけらがありません"));
      if (found.semanticUnavailable) {
        status.textContent += " " + t("意味検索を利用できなかったため、語句の一致で探しました。あとでもう一度検索できます。");

      }
    } catch (error) {
      if (current === generation) status.textContent = t(error.message);
    } finally {
      if (current === generation) {
        busy = false;
        submit.disabled = false;
        controls();
        if (append && hits.length < total && !selected) more.hidden = false;
      }
    }
  }

  async function showEntry(id) {
    const current = ++threadGeneration;
    selected = id;
    selectedRoot = null;
    detail.hidden = false;
    results.hidden = true;
    controls();
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

  // Speaking a query fills the field; the send phrase searches, like sending a post.
  const voice = createVoiceInput({
    getSettings,
    getText: () => query.value,
    setText: (value) => {
      query.value = value;
      controls();
    },
    onSend: () => void submitQuery(),
  });

  async function submitQuery() {
    await voice.discard();
    return run();
  }

  query.addEventListener("input", controls);
  const form = el("form", { class: "timeline-search-form", role: "search", onsubmit: (event) => {
    event.preventDefault(); void submitQuery();
  } }, query, voice.button, submit);
  const element = el("div", { class: "timeline-search" }, form, voice.activity,
    el("p", { class: "hint", label: "単語や短い語句はキーワード検索。「？」「教えて」「探して」などを含む質問や文章は意味検索します。" }),
    el("p", { class: "hint", label: "キーワード検索の後は「似たような単語でも探す」で範囲を広げられます。0件でも利用でき、自動判定が合わないときも結果から切り替えられます。" }),
    status, results, more, switchMode, detail);
  return { element, setSpeaking: thread.setSpeaking, reveal: thread.reveal, showEntry, changed, refresh, focus: () => query.focus(),
    stopVoice: () => voice.discard(),
    searchFor: (text) => { query.value = text; return run(); },
  };
}
