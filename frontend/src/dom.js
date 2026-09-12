// Tiny helpers so the views stay readable without a framework.
export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "html") node.innerHTML = value;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2).toLowerCase(), value);
    else if (value !== undefined && value !== null && value !== false) node.setAttribute(key, value === true ? "" : value);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

export const $ = (selector, scope = document) => scope.querySelector(selector);

export function clear(node) {
  while (node.firstChild) node.firstChild.remove();
  return node;
}

// when reads as how long ago, because that is how a timeline is read. Older
// than a day it becomes a date, because "17日前" is not something anyone
// pictures.
export function when(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const seconds = (Date.now() - date.getTime()) / 1000;
  if (seconds < 60) return "たった今";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}分前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}時間前`;
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleString("ja-JP", {
    year: sameYear ? undefined : "numeric",
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  });
}

export function toast(message, kind = "info") {
  const host = $("#toasts");
  const node = el("div", { class: `toast toast-${kind}`, text: message });
  host.append(node);
  setTimeout(() => node.remove(), 4200);
}
