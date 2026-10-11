// Tiny helpers so the views stay readable without a framework.
import { getLanguage, t } from "./i18n.js";
export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "label") node.textContent = t(value);
    else if (key === "html") node.innerHTML = value;
    else if (key === "href") {
      const href = safeLink(value);
      if (href) node.setAttribute(key, href);
    }
    else if (key === "i18nTitle") node.setAttribute("title", t(value));
    else if (key === "i18nPlaceholder") node.setAttribute("placeholder", t(value));
    else if (key.startsWith("on")) node.addEventListener(key.slice(2).toLowerCase(), value);
    else if (value !== undefined && value !== null && value !== false) node.setAttribute(key, value === true ? "" : value);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

// Links can come from a shared post or a search result. They are navigations,
// never executable URLs supplied by those sources.
export function safeLink(value) {
  try {
    const url = new URL(value, "https://kakeratta.invalid/");
    return ["http:", "https:"].includes(url.protocol) ? value : null;
  } catch {
    return null;
  }
}

export const $ = (selector, scope = document) => scope.querySelector(selector);

// Keep the native input for labels, keyboard interaction, and checked state.
export function switchControl(input, { on = "ON", off = "OFF", className = "" } = {}) {
  input.setAttribute("role", "switch");
  return el("span", { class: `schedule-toggle${className ? ` ${className}` : ""}` }, input,
    el("span", { class: "schedule-toggle-track", "aria-hidden": "true" },
      el("span", { class: "schedule-toggle-on", text: on }),
      el("span", { class: "schedule-toggle-off", text: off })));
}

export function clear(node) {
  while (node.firstChild) node.firstChild.remove();
  return node;
}

// Rebuilding or collapsing content can clamp a scrolling ancestor immediately.
export function preserveScroll(node, update) {
  const positions = new Map();
  for (let parent = node; parent; parent = parent.parentElement || parent.parentNode) {
    if (typeof parent.scrollTop === "number") positions.set(parent, [parent.scrollTop, parent.scrollLeft]);
  }
  const root = document.scrollingElement;
  if (root && !positions.has(root)) positions.set(root, [root.scrollTop, root.scrollLeft]);
  try { return update(); }
  finally {
    for (const [parent, [top, left]] of positions) {
      if (parent.scrollTop !== top) parent.scrollTop = top;
      if (parent.scrollLeft !== left) parent.scrollLeft = left;
    }
  }
}

// Overlays can open over the timeline or over an already fixed sheet. A small
// reference count keeps nested overlays from releasing the page too early.
let overlayLocks = 0;
let overlayScrollY = 0;
export function mountOverlay(node) {
  const classes = document.body.classList;
  if (overlayLocks++ === 0 && !classes?.contains?.("sheet-open")) {
    overlayScrollY = globalThis.window?.scrollY || 0;
    if (document.body.style) document.body.style.top = `-${overlayScrollY}px`;
  }
  classes?.add?.("dialog-open");
  document.body.append(node);
}

export function unmountOverlay(node) {
  node.remove();
  if (overlayLocks > 0) overlayLocks--;
  if (overlayLocks) return;
  const classes = document.body.classList;
  classes?.remove?.("dialog-open");
  if (classes?.contains?.("sheet-open")) return;
  if (document.body.style) document.body.style.top = "";
  globalThis.window?.scrollTo?.(0, overlayScrollY);
}

export function dialogCloseButton(close) {
  return el("button", { class: "button close-button dialog-close", label: "閉じる", type: "button", onclick: close });
}

// when reads as how long ago, because that is how a timeline is read. Older
// than a day it becomes a date, because "17日前" is not something anyone
// pictures.
export function when(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const seconds = (Date.now() - date.getTime()) / 1000;
  const language = getLanguage();
  if (seconds < 60) return language === "ja" ? "たった今" : "just now";
  if (seconds < 3600) return language === "ja" ? `${Math.floor(seconds / 60)}分前` : `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return language === "ja" ? `${Math.floor(seconds / 3600)}時間前` : `${Math.floor(seconds / 3600)}h ago`;
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleString(language === "ja" ? "ja-JP" : "en-US", {
    year: sameYear ? undefined : "numeric",
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  });
}

export function toast(message, kind = "info") {
  const host = $("#toasts");
  const node = el("div", { class: `toast toast-${kind}`, text: t(message) });
  // Storybook renders views without the application shell.
  if (!host) return;
  host.append(node);
  setTimeout(() => node.remove(), 4200);
}
