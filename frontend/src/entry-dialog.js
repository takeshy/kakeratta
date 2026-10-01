// A post and the conversation it sits in, shown over whatever is open: the
// memory view opens one this way from a fact's or a record's source, so the
// person reading what a persona remembers does not lose their place.
import { api } from "./api.js";
import { dialogCloseButton, el, mountOverlay, unmountOverlay } from "./dom.js";
import { createFeed } from "./feed.js";
import { t } from "./i18n.js";

export function showEntryDialog(id, { getPersona, getPersonas, getSettings } = {}) {
  const backdrop = el("div", { class: "dialog-backdrop" });
  const previousFocus = document.activeElement;
  const close = () => { unmountOverlay(backdrop); previousFocus?.focus?.(); };
  backdrop.addEventListener("click", (event) => { if (event.target === backdrop) close(); });
  backdrop.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); }
  });
  const feed = createFeed({ getPersona, getPersonas, getSettings, showIntro: false });
  const body = el("div", { class: "entry-dialog-body" }, el("p", { class: "hint", label: "読み込んでいます…" }));
  const dialog = el("div", { class: "dialog entry-dialog", role: "dialog", "aria-modal": "true", "aria-label": t("元の投稿"), tabindex: "-1" },
    dialogCloseButton(close),
    el("h2", { label: "元の投稿" }),
    body,
    el("div", { class: "compose-row" }, el("span", { class: "spacer" }), el("button", { class: "button primary", label: "閉じる", onclick: close })));
  backdrop.append(dialog);
  mountOverlay(backdrop);
  dialog.focus?.();
  const loading = api.thread(id).then((found) => {
    const entries = found.entries || [];
    if (!entries.some((entry) => entry.id === id)) {
      body.replaceChildren(el("p", { class: "hint", label: "その投稿は見つかりません（消された可能性があります）。" }));
      return;
    }
    body.replaceChildren(feed.element);
    feed.set(entries);
    feed.reveal?.(id);
  }).catch((error) => body.replaceChildren(el("p", { role: "alert", text: t(error.message || String(error)) })));
  return { element: backdrop, close, loading };
}
