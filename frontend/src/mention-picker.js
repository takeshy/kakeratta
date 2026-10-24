import { avatarNode } from "./avatar.js";
import { el, clear } from "./dom.js";
import { named, gloss } from "./builtins.js";
import { t } from "./i18n.js";

let sequence = 0;

export function mentionQuery(value, start, end = start) {
  if (start !== end) return null;
  const match = /(?:^|[\s、。])[@＠]([^\s@＠、。]*)$/u.exec(value.slice(0, start));
  return match ? { start: start - match[1].length - 1, end: start, query: match[1] } : null;
}

// Names containing separators or suffixes stripped by the server use their ID.
export function mentionTag(row) {
  if (row.id === "agent") return "@agent";
  if (row.id === "ai") return "@ai";
  if (gloss(row).name !== row.name) return `@${row.id}`;
  return `@${/^[^\s@＠、。]+$/u.test(row.name) && !/[さん!?！？]$/u.test(row.name) ? row.name : row.id}`;
}

// The two addresses always select their own destinations.
export function agentMention(external = false) {
  return { id: external ? "agent" : "ai", name: t(external ? "外部エージェント" : "内蔵AI"), icon: "🤖", active: true,
    domain: t(external ? "担当を通さず外部エージェントに直接依頼します" : "担当を通さず内蔵AIに直接質問します") };
}

export function agentMentions(externalAvailable) {
  return externalAvailable ? [agentMention(true), agentMention(false)] : [agentMention(false)];
}

const MENTION = /(^|[\s、。])[@＠][^\s@＠、。]+/gu;

export function replaceMention(value, tag) {
  MENTION.lastIndex = 0;
  return MENTION.test(value)
    ? value.replace(MENTION, (match, separator) => `${separator}${tag}`)
    : value.trim() ? `${value.replace(/\s*$/, "")} ${tag} ` : `${tag} `;
}

function hasOtherMention(value, current) {
  MENTION.lastIndex = 0;
  for (const match of value.matchAll(MENTION)) {
    const start = match.index + match[1].length;
    if (start !== current.start) return true;
  }
  return false;
}

export function createMentionPicker({ input, getPersonas = () => [], onChange = () => {} }) {
  const id = `mention-picker-${++sequence}`;
  const list = el("div", { class: "mention-picker", id, role: "listbox", "aria-label": t("担当をえらぶ"), hidden: true });
  input.setAttribute("aria-controls", id);
  input.setAttribute("aria-autocomplete", "list");
  let range = null;
  let rows = [];
  let selected = 0;
  let composing = false;
  function hide() {
    list.hidden = true;
    input.setAttribute("aria-activedescendant", "");
  }
  function highlight() {
    [...list.children].forEach((node, index) => {
      node.setAttribute("aria-selected", String(index === selected));
    });
    input.setAttribute("aria-activedescendant", `${id}-${selected}`);
    list.children[selected]?.scrollIntoView?.({ block: "nearest" });
  }
  function choose(row) {
    if (!range) return;
    const insertion = `${mentionTag(row)} `;
    input.value = input.value.slice(0, range.start) + insertion + input.value.slice(range.end);
    const caret = range.start + insertion.length;
    hide();
    input.focus();
    input.setSelectionRange(caret, caret);
    onChange();
  }
  function update() {
    if (composing) return;
    range = mentionQuery(input.value, input.selectionStart, input.selectionEnd);
    if (!range) { hide(); return; }
    if (hasOtherMention(input.value, range)) { hide(); return; }
    const query = range.query.toLocaleLowerCase();
    rows = getPersonas().filter(row => row.active &&
      [row.name, row.id, named(row)].some(value => value?.toLocaleLowerCase().includes(query)));
    clear(list);
    selected = 0;
    if (!rows.length) { hide(); return; }
    rows.forEach((row, index) => list.append(el("button", {
      type: "button", class: "mention-option", role: "option", id: `${id}-${index}`, tabindex: "-1",
      title: row.domain,
      onmousedown: event => event.preventDefault(),
      onclick: () => choose(row),
    }, avatarNode(row, { className: "avatar avatar-inline" }), el("span", { text: named(row) }))));
    list.hidden = false;
    highlight();
  }
  input.addEventListener("input", update);
  input.addEventListener("click", update);
  input.addEventListener("compositionstart", () => { composing = true; hide(); });
  input.addEventListener("compositionend", () => { composing = false; update(); });
  input.addEventListener("blur", () => setTimeout(hide, 150));
  input.addEventListener("keydown", event => {
    if (list.hidden || composing || event.isComposing || event.keyCode === 229 || event.ctrlKey || event.metaKey || event.altKey) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      selected = (selected + (event.key === "ArrowDown" ? 1 : -1) + rows.length) % rows.length;
      highlight();
    } else if (event.key === "Enter" || event.key === "Tab") {
      event.preventDefault();
      choose(rows[selected]);
    } else if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      hide();
    }
  });
  input.addEventListener("keyup", event => {
    if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) update();
  });
  return { element: list, hide };
}
