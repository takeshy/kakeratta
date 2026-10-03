// What one speaker said, read as the timeline is: each remark under its post,
// newest conversation first, under the persona as its card in the personas list
// shows it. Opened from a name on the timeline; it is not a search.
import { createEntryFilters } from "./entry-filters.js";
import { watchFavorites } from "./favorite-state.js";
import { el } from "./dom.js";
import { t } from "./i18n.js";
import { api } from "./api.js";
import { createFeed } from "./feed.js";
import { basicInfoBox } from "./person-basic-info.js";
import { gloss } from "./builtins.js";
import { uniqueTools } from "./tool_labels.js";
import { notificationSchedules, toolLink } from "./persona-display.js";

export function createSpeaker({ getSettings, getPersona, getPersonas, onEntryChange, onReply, onSpeak, onOpenPersona, onOpenMemory, onEditPersona, onPersonasChange }) {
  const status = el("p", { class: "hint", role: "status", "aria-live": "polite" });
  const cardHost = el("div", { class: "speaker-card-host" });
  cardHost.hidden = true;
  const more = el("button", { class: "button", label: "さらに読み込む", hidden: true, onclick: () => void load(true, ++generation) });
  const feed = createFeed({ getSettings, getPersona, getPersonas, onSpeak, showIntro: false,
    onSpeaker: (id, name) => void show(id, name),
    onReply: () => { onReply?.(); return Promise.resolve(); },
    onEntryChange: (entry) => { onEntryChange?.(entry); changed(entry); },
  });
  let filters = {};
  const filterControls = createEntryFilters(next => { filters = next; return refresh(); });
  function refresh() {
    if (!speaker) return;
    entries = new Map(); count = 0; total = 0; feed.set([]);
    return load(false, ++generation);
  }
  watchFavorites(() => { if (filters.favoritesOnly && element.isConnected) void refresh(); });
  let speaker = null;
  let generation = 0;
  let busy = false;
  let count = 0;
  let total = 0;
  // Every entry of the conversations read so far, by id.
  let entries = new Map();

  // The persona as its card in the personas list shows it, with the same ways
  // to open it. The user has no card.
  function card(id) {
    const row = getPersona(id);
    if (!row) return null;
    const person = row.kind === "person";
    const shown = gloss(row);
    const node = el("section", { class: "persona speaker-card", "data-id": row.id },
      el("div", { class: "persona-head" },
        el("span", { class: "avatar", text: row.icon || "◎" }),
        el("span", { class: "who", text: shown.name }),
        shown.note ? el("span", { class: "note-tag", text: shown.note }) : null,
        person ? el("span", { class: "tag", label: "人物" }) : null,
        row.planner === "external" ? el("span", { class: "tag", label: "外部エージェント" }) : null,
        el("span", { class: "tag", label: row.active ? "有効" : "停止中" })),
      shown.domain ? el("p", { class: "domain", text: shown.domain }) : null);
    if (person) {
      node.append(
        el("p", { class: "hint", text: t("呼び名: {0}", [row.name, ...(row.aliases || [])].join(" / ")) }),
        el("p", { class: "hint", text: t("キーワード: {0}", (row.keywords || []).join(" / ") || t("（なし）")) }),
        basicInfoBox(row, () => onEditPersona?.(row, "basicInfo")));
    }
    if (row.schedules?.length) {
      node.append(notificationSchedules(row, (payload) => {
        row.schedules = payload.personas?.find(persona => persona.id === row.id)?.schedules || [];
        onPersonasChange?.(payload.personas);
        drawCard();
      }));
    }
    if (row.tools?.length) {
      node.append(el("p", { class: "tools" }, `${t("道具")}: `,
        uniqueTools(row.tools).flatMap((name, index) => index ? [" / ", toolLink(name)] : [toolLink(name)])));
    }
    node.append(el("p", { class: "hint", text: t("起きた回数 {0}", row.wakes || 0) }));
    const links = [
      onOpenPersona ? el("button", { type: "button", class: "chip ghost", label: "担当の設定", onclick: () => onOpenPersona(row) }) : null,
      onEditPersona ? el("button", { type: "button", class: "chip ghost", label: "修正", onclick: () => onEditPersona(row) }) : null,
      row.memoryEnabled !== false && onOpenMemory ? el("button", { type: "button", class: "chip ghost", label: "覚えていること", onclick: () => onOpenMemory(row) }) : null,
    ].filter(Boolean);
    if (links.length) node.append(el("div", { class: "compose-row" }, ...links));
    return node;
  }

  function drawCard() {
    const node = speaker ? card(speaker.id) : null;
    cardHost.replaceChildren(...(node ? [node] : []));
    cardHost.hidden = !node;
  }

  async function show(id, name) {
    const current = ++generation;
    speaker = { id, name: name || (id === "user" ? t("あなた") : getPersona(id)?.name || id) };
    drawCard();
    entries = new Map();
    count = 0;
    total = 0;
    feed.set([]);
    await load(false, current);
  }

  async function load(append, current) {
    const asked = speaker;
    busy = true;
    more.hidden = true;
    status.textContent = t("読み込んでいます…");
    try {
      const found = await api.speaker(asked.id, { limit: 20, offset: append ? count : 0, ...filters });
      if (current !== generation || speaker !== asked) return;
      // The page comes with the conversations its remarks belong to.
      for (const entry of found.entries || []) entries.set(entry.id, entry);
      count = (append ? count : 0) + found.hits.length;
      total = found.total;
      feed.set([...entries.values()]);
      // A remark sits folded under its post; here the remarks are the point.
      for (const { entry } of found.hits) if (entry.kind !== "post") feed.reveal(entry.id);
      status.textContent = found.total
        ? t("{0}件中{1}件を表示", found.total, count)
        : t("まだ発言がありません。");
    } catch (error) {
      if (current === generation && speaker === asked) status.textContent = t(error.message);
    } finally {
      if (current === generation && speaker === asked) {
        busy = false;
        more.hidden = busy || count >= total;
      }
    }
  }

  function changed(entry) {
    if (!speaker) return;
    if (Object.keys(filters).length && element.isConnected) { void refresh(); return; }
    if (!entries.has(entry.id)) return;
    entries.set(entry.id, entry);
    feed.upsert(entry);
  }

  const element = el("div", { class: "speaker-view" }, cardHost, filterControls.element, status, feed.element, more);
  return { element, show, redrawCard: drawCard, changed, setSpeaking: feed.setSpeaking, reveal: feed.reveal };
}
