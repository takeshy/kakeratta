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

// Phones show the icon alone; wider headers pair it with the label when it fits.
// The wordmark is drawn rather than set in a font, so every device shows the
// same letters; rounded strokes soften the angular fragment logo beside it.
const WORDMARK = '<svg viewBox="0 0 106 22" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path transform="translate(0 0)" d="M3 2V20M11 8.5L3.5 15M6.2 12.8L11.4 20"/><path transform="translate(12 0)" d="M3 8H8.6L10 9.4V20M10 13.2H4.6L3 14.8V18.4L4.6 20H10"/><path transform="translate(24 0)" d="M3 2V20M11 8.5L3.5 15M6.2 12.8L11.4 20"/><path transform="translate(36 0)" d="M3 14H10.4V9.6L8.8 8H4.6L3 9.6V18.4L4.6 20H10.4"/><path transform="translate(48 0)" d="M3 8V20M3 11.4L6.4 8H9.6"/><path transform="translate(58 0)" d="M3 8H8.6L10 9.4V20M10 13.2H4.6L3 14.8V18.4L4.6 20H10"/><path transform="translate(70 0)" d="M5 3V18.4L6.6 20H9.4M2 8H9.4"/><path transform="translate(80 0)" d="M5 3V18.4L6.6 20H9.4M2 8H9.4"/><path transform="translate(90 0)" d="M3 8H8.6L10 9.4V20M10 13.2H4.6L3 14.8V18.4L4.6 20H10"/></svg>';

function navigationContent(which, label, compact, labelIcons) {
  const size = compact ? 22 : 18;
  const icon = el("span", { class: "nav-icon", "aria-hidden": "true", html: `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" focusable="false">${NAV_ICONS[which]}</svg>` });
  const text = el("span", { class: "nav-label", label });
  return compact ? icon : labelIcons ? [icon, text] : text;
}

function navigationButton(which, label, onOpen, compact, labelIcons, accessible = label) {
  return el("button", { class: `chip ghost nav nav-${which}`, "aria-label": t(accessible), i18nTitle: accessible, onclick: () => onOpen?.(which) },
    navigationContent(which, label, compact, labelIcons));
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

export function screenNodes({ compose, room, feed, filters, onOpen, onLogout }) {
  let compact = (globalThis.innerWidth ?? 1024) <= 600;
  // 22px brand + 44px per action + 4px gaps; reserve Personas before Settings.
  let narrow = (globalThis.innerWidth ?? 1024) - 32 < 214;
  let showSettings = (globalThis.innerWidth ?? 1024) - 32 >= 262;
  let more;
  // Labels vary with language and text size. A labelled header that overflows
  // gives up the brand name, then the icons beside labels, then the labels.
  // Each step keeps the width it needed, so widening the window restores it.
  const LABELLED = [{ brandName: true, labelIcons: true }, { brandName: false, labelIcons: true }, { brandName: false, labelIcons: false }];
  const needs = LABELLED.map(() => 0);
  let step = 0;
  const navigation = () => {
    more = moreNavigation(onLogout, onOpen, compact, showSettings, narrow);
    const { brandName, labelIcons } = LABELLED[step];
    return [
      el("style", { text: navigationStyles, "data-navigation-styles": "" }),
      el("span", { class: "brand" },
        el("img", { class: "brand-mark", src: "/icon.svg", alt: "", width: "22", height: "22" }),
        compact || !brandName ? null : el("span", { class: "brand-name", role: "img", "aria-label": "kakeratta", html: WORDMARK })),
      el("span", { class: "spacer" }),
      navigationButton("search", "検索", onOpen, compact, labelIcons),
      navigationButton("schedules", "予定", onOpen, compact, labelIcons, "予定一覧"),
      ...(!narrow ? [navigationButton("personas", "担当", onOpen, compact, labelIcons)] : []),
      ...(showSettings ? [navigationButton("settings", "設定", onOpen, compact, labelIcons)] : []),
      more.menu,
    ];
  };
  const header = el("header", { class: `topbar${compact ? " compact" : ""}${narrow ? " narrow" : ""}` }, ...navigation());
  const rebuild = () => {
    more.close();
    header.classList.toggle("compact", compact);
    header.classList.toggle("narrow", narrow);
    header.replaceChildren(...navigation());
  };
  // Measure the embedded canvas too. Each action exists in only one location.
  if (globalThis.ResizeObserver) {
    const observer = new ResizeObserver(([entry]) => {
      if (!header.isConnected) { more.close(); observer.disconnect(); return; }
      const width = entry.contentRect.width;
      if (!width) return;
      let nextStep = needs.findIndex(need => width >= need);
      const nextCompact = width <= 568 || nextStep < 0;
      if (nextStep < 0) nextStep = LABELLED.length - 1;
      const nextNarrow = width < 214;
      const nextShowSettings = width >= 262;
      if (compact !== nextCompact || step !== nextStep || narrow !== nextNarrow || showSettings !== nextShowSettings) {
        compact = nextCompact;
        step = nextStep;
        narrow = nextNarrow;
        showSettings = nextShowSettings;
        rebuild();
      }
      while (!compact && header.scrollWidth > header.clientWidth + 1) {
        needs[step] = header.scrollWidth;
        if (step < LABELLED.length - 1) step++;
        else compact = true;
        rebuild();
      }
    });
    observer.observe(header);
  }
  return [header, room.banner, compose, filters ? el("div", { class: "timeline-toolbar" }, room.bar, filters) : room.bar, feed];
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
      onClose ? el("button", { class: "button close-button sheet-close", label: "閉じる", type: "button", onclick: () => onClose() }) : null,
    ),
    body,
  );
}
