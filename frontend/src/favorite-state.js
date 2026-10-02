import { api } from "./api.js";
import { el, toast } from "./dom.js";
import { t } from "./i18n.js";

let ids = new Set();
let revision = 0;
const pending = new Set();
const listeners = new Set();
export function adoptFavoriteIDs(saved) { ids = new Set(saved); syncButtons(); }
export function watchFavorites(listener) { listeners.add(listener); return () => listeners.delete(listener); }
function changed() { for (const listener of listeners) listener(); }
export async function refreshFavorites() {
  const current = revision;
  const found = await api.favorites();
  if (current !== revision) return found;
  ids = new Set(found.ids);
  changed();
  return found;
}
export function favoriteButton(entry, { confirmRemoval = false } = {}) {
  const button = el("button", { class: "chip ghost favorite", disabled: !!entry.pending || pending.has(entry.id),
    text: ids.has(entry.id) ? "★" : "☆", "aria-pressed": String(ids.has(entry.id)),
    "aria-label": t(ids.has(entry.id) ? "お気に入りを解除" : "お気に入りに追加"),
    onclick: async () => {
      if (pending.has(entry.id)) return;
      const remove = ids.has(entry.id);
      if (remove && confirmRemoval && !confirm(t("この投稿をお気に入りから外しますか？"))) return;
      pending.add(entry.id);
      button.disabled = true;
      try {
        await api.setFavorite(entry.id, !remove);
        revision++;
        if (remove) ids.delete(entry.id); else ids.add(entry.id);
        changed();
      } catch (error) { toast(t(error.message)); }
      finally { pending.delete(entry.id); syncButtons(); }
    } });
  button.dataset.favoriteId = entry.id;
  button.dataset.favoritePending = String(!!entry.pending);
  return button;
}
function syncButtons() {
  for (const button of document.querySelectorAll("[data-favorite-id]")) {
    const id = button.dataset.favoriteId;
    button.textContent = ids.has(id) ? "★" : "☆";
    button.setAttribute("aria-pressed", String(ids.has(id)));
    button.setAttribute("aria-label", t(ids.has(id) ? "お気に入りを解除" : "お気に入りに追加"));
    button.disabled = button.dataset.favoritePending === "true" || pending.has(id);
  }
}
watchFavorites(syncButtons);
