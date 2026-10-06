import { el } from "./dom.js";
import { t } from "./i18n.js";
import { entryFilterStyles } from "./entry-filter-styles.js";
import { isFavorite } from "./favorite-state.js";

// iOS Safari shows an empty date field as a blank box; the wrapper carries a
// hint for it (drawn only there) while the field is empty.
function dateField(input) {
  return el("span", { class: "entry-filter-date", "data-hint": t("年/月/日"), "data-empty": true }, input);
}

export function createEntryFilters(onChange) {
  const favorite = el("input", { type: "checkbox" });
  const from = el("input", { type: "date", "aria-label": t("開始日") });
  const to = el("input", { type: "date", "aria-label": t("終了日") });
  const error = el("span", { class: "hint", role: "alert" });
  const status = el("span", { class: "entry-filter-status", "aria-live": "polite" });
  function value() {
    const query = {};
    if (favorite.checked) query.favoritesOnly = true;
    if (from.value) query.from = new Date(from.value + "T00:00:00").toISOString();
    if (to.value) {
      const end = new Date(to.value + "T00:00:00");
      end.setDate(end.getDate() + 1);
      query.to = new Date(end.getTime() - 1).toISOString();
    }
    return query;
  }
  function changed() {
    for (const input of [from, to]) input.parentElement?.toggleAttribute?.("data-empty", !input.value);
    if (from.value && to.value && from.value > to.value) {
      error.textContent = t("終了日は開始日以降にしてください");
      return;
    }
    error.textContent = "";
    const filters = value();
    status.textContent = Object.keys(filters).length ? t("絞り込み中") : "";
    return onChange(filters);
  }
  for (const input of [favorite, from, to]) input.addEventListener("change", changed);
  const element = el("details", { class: "entry-filters" },
    el("style", { text: entryFilterStyles, "data-entry-filter-styles": "" }),
    el("summary", {}, el("span", { label: "絞り込み" }), status),
    el("div", { class: "entry-filter-body" },
      el("div", { class: "entry-filters-head" },
        el("label", { class: "entry-filter-favorite" }, favorite, el("span", { label: "お気に入りのみ" })),
        el("button", { type: "button", class: "entry-filter-clear", label: "絞り込みを解除", onclick: () => {
          favorite.checked = false; from.value = ""; to.value = ""; return changed();
        } })),
      el("div", { class: "entry-filter-dates" },
        el("label", {}, el("span", { label: "開始日" }), dateField(from)),
        el("label", {}, el("span", { label: "終了日" }), dateField(to))), error));
  return { element, value };
}

export function entryMatches(entry, filters, favorite = isFavorite) {
  const at = new Date(entry.at).getTime();
  return !entry.deleted && (!filters.favoritesOnly || favorite(entry.id)) &&
    (!filters.from || at >= Date.parse(filters.from)) && (!filters.to || at <= Date.parse(filters.to));
}

// Keep conversation context, including the parent of a favorited reply.
export function filterConversations(entries, filters) {
  if (!Object.keys(filters).length) return entries;
  const roots = new Set(entries.filter(e => (filters.favoritesOnly || e.kind === "post") && entryMatches(e, filters))
    .map(e => e.root || e.id));
  return entries.filter(e => roots.has(e.root || e.id));
}
