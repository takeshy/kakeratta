import { api } from "./api.js";
import { el } from "./dom.js";
import { t } from "./i18n.js";
import { gloss } from "./builtins.js";
import { createFeed } from "./feed.js";
import { showEntryDialog } from "./entry-dialog.js";
import { adoptFavoriteIDs, watchFavorites } from "./favorite-state.js";

export function createFavorites(options) {
  const filter = el("select", { "aria-label": t("担当で絞り込み"), onchange: () => load() });
  const status = el("p", { class: "hint", role: "status" });
  const list = el("div");
  const more = el("button", { class: "button", label: "さらに読み込む", hidden: true, onclick: () => load(true) });
  let count = 0, generation = 0, opened = false;
  let shown = new Map();
  async function load(append = false) {
    const current = ++generation;
    more.hidden = true;
    status.textContent = t("読み込んでいます…");
    try {
      const found = await api.favorites({ speaker: filter.value, offset: append ? count : 0 });
      if (current !== generation) return;
      adoptFavoriteIDs(found.ids);
      const selected = filter.value;
      filter.replaceChildren(el("option", { value: "", label: "すべての担当" }), ...Object.keys({ ...found.speakers, ...(selected ? { [selected]: true } : {}) }).sort().map(id =>
        el("option", { value: id, text: id === "user" ? t("あなた") : options.getPersona(id) ? gloss(options.getPersona(id)).name : id })));
      filter.value = selected;
      if (!append) { list.replaceChildren(); count = 0; shown = new Map(); }
      for (const item of found.items) {
        shown.set(item.id, item.entry);
        // Display the saved entry itself; open its full conversation on request.
        const feed = createFeed({ ...options, showIntro: false, standalone: true, confirmFavoriteRemoval: true });
        feed.set([item.entry]);
        list.append(el("section", { class: "favorite-item" }, feed.element,
          el("button", { class: "chip ghost", label: "元の投稿", onclick: () => showEntryDialog(item.id, options) })));
      }
      count += found.items.length;
      status.textContent = found.total ? t("{0}件中{1}件を表示", found.total, count) : t("お気に入りはまだありません。");
      more.hidden = count >= found.total;
    } catch (error) { if (current === generation) status.textContent = t(error.message); }
  }
  watchFavorites(() => { if (opened && element.isConnected) void load(); });
  const element = el("div", { class: "favorites-view" },
    el("label", { class: "compose-row" }, el("span", { label: "担当で絞り込み" }), filter),
    el("p", { class: "hint", label: "最近お気に入りに追加した順に表示します。" }), status, list, more);
  return { element, changed: entry => {
    if (element.isConnected && (shown.has(entry.id) || [...shown.values()].some(item => item.root === entry.id))) void load();
  }, show: () => { opened = true; return load(); } };
}
