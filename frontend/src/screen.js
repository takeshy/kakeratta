// The screen, in the order it is read.
//
// Navigation sits in the header and nothing else does: the read-aloud switch is
// a mode over the timeline, so it travels with the timeline. Below the header
// comes the one thing that can stop the app working (nobody in the room), then
// the way in, then who is in the room, then the timeline - which is what the
// screen is actually for.
import { el } from "./dom.js";
import { getLanguage, t } from "./i18n.js";
import { navigationStyles } from "./navigation-styles.js";

const MORE_ICON = '<svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true" focusable="false"><circle cx="12" cy="5" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="12" cy="19" r="1.8"/></svg>';
const NAV_ICONS = {
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
  schedules: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M7 3v4m10-4v4M3 11h18m-13 5h3m3 0h3"/>',
  settings: '<path d="m9 3-1 3-3 1 1 3-2 2 2 2-1 3 3 1 1 3h6l1-3 3-1-1-3 2-2-2-2 1-3-3-1-1-3Z"/><circle cx="12" cy="12" r="3"/>',
  personas: '<circle cx="9" cy="7" r="3"/><path d="M3 21v-2a6 6 0 0 1 12 0v2m1-17a3 3 0 0 1 0 6m2 4a5 5 0 0 1 3 5v2"/>',
};

function navigationContent(which, label, compact) {
  return compact
    ? el("span", { class: "nav-icon", "aria-hidden": "true", html: `<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" focusable="false">${NAV_ICONS[which]}</svg>` })
    : el("span", { class: "nav-label", label });
}

function navigationButton(which, label, onOpen, compact, accessible = label) {
  return el("button", { class: `chip ghost nav nav-${which}`, "aria-label": t(accessible), i18nTitle: accessible, onclick: () => onOpen?.(which) },
    navigationContent(which, label, compact));
}

// Where the manual lives, in the reader's language. It opens in a new tab so
// the timeline, and anything half-typed in the box, stays where it was.
export function manualLink() {
  return `/manual/?lang=${getLanguage()}`;
}

function moreNavigation(onLogout, onOpen, compact, showSettings, narrow) {
  if (!compact) {
    return {
      menu: el("div", { class: "nav-secondary" },
        el("a", { class: "chip ghost nav help", href: manualLink(), target: "_blank", rel: "noopener", label: "使い方" }),
        onLogout ? el("button", { type: "button", class: "chip ghost nav", label: "ログアウト", onclick: onLogout }) : null),
      close: () => {},
    };
  }
  let open = false;
  const close = () => {
    open = false;
    menu.closest(".topbar")?.classList.remove("nav-menu-open");
    items.hidden = true;
    items.style.display = "none";
    toggle.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", outside);
  };
  const outside = event => { if (!menu.contains(event.target)) close(); };
  const toggle = el("button", {
    type: "button", class: "chip ghost nav more-toggle", html: MORE_ICON,
    "aria-label": t("その他のメニュー"), "aria-expanded": "false",
    onclick: () => {
      if (open) { close(); return; }
      open = true;
      menu.closest(".topbar")?.classList.add("nav-menu-open");
      items.hidden = false;
      items.style.display = "block";
      toggle.setAttribute("aria-expanded", "true");
      document.addEventListener("pointerdown", outside);
    },
  });
  const items = el("div", { class: "nav-more-items", hidden: true, style: "display: none" },
    narrow ? el("button", { class: "nav-more-personas", type: "button", label: "担当", onclick: () => { close(); onOpen?.("personas"); } }) : null,
    !showSettings ? el("button", { class: "nav-more-settings", type: "button", label: "設定", onclick: () => { close(); onOpen?.("settings"); } }) : null,
    el("a", { class: "help", href: manualLink(), target: "_blank", rel: "noopener", label: "使い方", onclick: close }),
    onLogout ? el("button", { type: "button", label: "ログアウト", onclick: () => { close(); onLogout(); } }) : null,
  );
  const menu = el("div", {
    class: "nav-more",
    onkeydown: event => {
      if (event.key === "Escape" && open) { event.preventDefault(); close(); toggle.focus(); }
    },
    onfocusout: event => { if (event.relatedTarget && !menu.contains(event.relatedTarget)) close(); },
  }, toggle, items);
  return { menu, close };
}

export function screenNodes({ compose, room, feed, onOpen, onLogout }) {
  let compact = (globalThis.innerWidth ?? 1024) <= 600;
  // 22px brand + 44px per action + 4px gaps; reserve Personas before Settings.
  let narrow = (globalThis.innerWidth ?? 1024) - 32 < 214;
  let showSettings = (globalThis.innerWidth ?? 1024) - 32 >= 262;
  let more;
  const navigation = () => {
    more = moreNavigation(onLogout, onOpen, compact, showSettings, narrow);
    return [
      el("style", { text: navigationStyles, "data-navigation-styles": "" }),
      el("span", { class: "brand" },
        el("img", { class: "brand-mark", src: "/icon.svg", alt: "", width: "22", height: "22" }),
        compact ? null : el("span", { class: "brand-name", text: "kakeratta" })),
      el("span", { class: "spacer" }),
      navigationButton("search", "検索", onOpen, compact),
      navigationButton("schedules", "予定", onOpen, compact, "予定一覧"),
      ...(!narrow ? [navigationButton("personas", "担当", onOpen, compact)] : []),
      ...(showSettings ? [navigationButton("settings", "設定", onOpen, compact)] : []),
      more.menu,
    ];
  };
  const header = el("header", { class: `topbar${compact ? " compact" : ""}${narrow ? " narrow" : ""}` }, ...navigation());
  // Measure the embedded canvas too. Each action exists in only one location.
  if (globalThis.ResizeObserver) {
    const observer = new ResizeObserver(([entry]) => {
      if (!header.isConnected) { more.close(); observer.disconnect(); return; }
      const width = entry.contentRect.width;
      if (!width) return;
      const nextCompact = width <= 568;
      const nextNarrow = width < 214;
      const nextShowSettings = width >= 262;
      if (compact === nextCompact && narrow === nextNarrow && showSettings === nextShowSettings) return;
      more.close();
      compact = nextCompact;
      narrow = nextNarrow;
      showSettings = nextShowSettings;
      header.classList.toggle("compact", compact);
      header.classList.toggle("narrow", narrow);
      header.replaceChildren(...navigation());
    });
    observer.observe(header);
  }
  return [header, room.banner, compose, room.bar, feed];
}

// A panel over the screen. The sheet is the same whichever panel is in it, so
// the story and the app cannot disagree about what a panel looks like.
export function sheetNodes({ title, body, onClose }) {
  return el(
    "div",
    { class: "sheet" },
    el(
      "div",
      { class: "sheet-head" },
      el("strong", { label: title }),
      el("span", { class: "spacer" }),
      onClose ? el("button", { class: "chip ghost sheet-close", text: "×", "aria-label": t("閉じる"), onclick: () => onClose() }) : null,
    ),
    body,
  );
}
