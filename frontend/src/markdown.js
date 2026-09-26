// Markdown for the timeline's bubbles.
//
// A persona's reply is model-written and is never edited by hand, so the app
// renders it: the GFM conventions the gemihub document view draws (headings,
// lists, task lists, tables, quotations, callouts, code fences) read as
// themselves rather than as punctuation. The user's own words stay exactly as
// typed unless the setting asks for them to be honoured, because those are the
// ones a person edits - and editing has always meant writing the source.
//
// Like the memory reader this builds DOM nodes and never touches innerHTML:
// model-authored text is rendered, never run.
import { el, safeLink } from "./dom.js";

// Emphasis, deletion, code and links. A bare URL becomes a link; anything that
// is not http(s) stays inert text, whatever the label says.
const INLINE = /\[([^\]\n]+)\]\(([^\s)]+)\)|\*\*([^*\n]+)\*\*|~~([^~\n]+)~~|`([^`\n]+)`|(https?:\/\/[^\s<>]+)/g;

function inline(text) {
  const out = [];
  let start = 0;
  for (const match of text.matchAll(INLINE)) {
    out.push(text.slice(start, match.index));
    if (match[3]) out.push(el("strong", { text: match[3] }));
    else if (match[4]) out.push(el("del", { text: match[4] }));
    else if (match[5]) out.push(el("code", { text: match[5] }));
    else {
      const label = match[1] || match[6];
      const href = safeLink(match[2] || match[6]);
      out.push(href
        ? el("a", { href, target: "_blank", rel: "noopener noreferrer", text: label })
        : el("span", { text: label }));
    }
    start = match.index + match[0].length;
  }
  out.push(text.slice(start));
  return out;
}

// A line that is ordinary prose: not blank, not a heading, list, quotation or
// table rule. Consecutive such lines are one paragraph.
const plain = (line) => Boolean(line.trim()) && !/^```/.test(line) && !/^#{1,6}\s+/.test(line) &&
  !/^\s*(?:[-*+] |\d+[.)] )/.test(line) && !line.startsWith("> ") && !line.includes("|");

// Blockquotes with a [!type] first line become callouts, the way the gemihub
// document view reads them. Unknown types read as note; synonyms fold in.
const CALLOUT_ALIASES = {
  summary: "abstract", tldr: "abstract", hint: "tip", important: "tip",
  check: "success", done: "success", help: "question", faq: "question",
  caution: "warning", attention: "warning", fail: "failure", missing: "failure",
  error: "danger", cite: "quote",
};
const CALLOUT_ICONS = {
  note: "i", abstract: "≡", info: "i", todo: "✓", tip: "⚑", success: "✓",
  question: "?", warning: "!", failure: "×", danger: "!", bug: "●", example: "□", quote: "“",
};
const titleCase = (value) => value.split("-").filter(Boolean)
  .map((word) => `${word.charAt(0).toUpperCase()}${word.slice(1)}`).join(" ");

function callout(lines) {
  const match = lines[0].match(/^\[!([A-Za-z0-9_-]+)\]([+-])?(?:[ \t]+([^\n]*))?$/);
  if (!match) return null;
  const type = CALLOUT_ALIASES[match[1].toLowerCase()] || match[1].toLowerCase();
  return {
    type: CALLOUT_ICONS[type] ? type : "note",
    icon: CALLOUT_ICONS[type] || CALLOUT_ICONS.note,
    title: match[3]?.trim() || titleCase(match[1]),
    // A trailing "-" folds the callout to its head line; "+" keeps it open.
    closed: match[2] === "-",
    body: lines[0].slice(match[0].length).trim() ? [lines[0].slice(match[0].length)] : lines.slice(1),
  };
}

export function renderMarkdown(text) {
  const result = el("div", { class: "markdown-body" });
  const lines = String(text || "").replace(/\r\n?/g, "\n").split("\n");
  let list = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!line.trim()) { list = null; continue; }
    if (/^```/.test(line)) {
      const code = [];
      while (++i < lines.length && !/^```/.test(lines[i])) code.push(lines[i]);
      result.append(el("pre", {}, el("code", { text: code.join("\n") })));
      list = null;
      continue;
    }
    const heading = line.match(/^#{1,6}\s+(.+)$/);
    const item = line.match(/^\s*(?:[-*+] |\d+[.)] )(.+)$/);
    if (heading) {
      result.append(el("h4", {}, inline(heading[1])));
      list = null;
    } else if (item) {
      const tag = /^\s*\d/.test(line) ? "ol" : "ul";
      if (!list || list.tagName.toLowerCase() !== tag) { list = el(tag); result.append(list); }
      // A "- [x] done" item is a task: its own bullet becomes the checkbox the
      // writer meant. Other items in the same list retain their normal bullet.
      const task = item[1].match(/^\[([ xX])\]\s+(.*)$/);
      if (task) {
        list.append(el("li", { class: "task" },
          el("span", { class: "task-marker", text: task[1].trim() ? "☑" : "☐" }), " ", inline(task[2])));
      } else list.append(el("li", {}, inline(item[1])));
    } else if (line.includes("|") && /^\s*\|?\s*:?-{3,}/.test(lines[i + 1] || "")) {
      const cells = (row) => row.trim().replace(/^\||\|$/g, "").split("|");
      const table = el("table", {}, el("thead", {}, el("tr", {}, cells(line).map(cell => el("th", {}, inline(cell.trim()))))));
      const rows = el("tbody");
      i++;
      while (i + 1 < lines.length && lines[i + 1].includes("|")) {
        rows.append(el("tr", {}, cells(lines[++i]).map(cell => el("td", {}, inline(cell.trim())))));
      }
      table.append(rows);
      result.append(el("div", { class: "markdown-table" }, table));
      list = null;
    } else if (line.startsWith("> ")) {
      // Consecutive quoted lines are one quotation, the way they read.
      const quoted = [line.slice(2)];
      while (i + 1 < lines.length && lines[i + 1].startsWith("> ")) quoted.push(lines[++i].slice(2));
      const marked = callout(quoted);
      if (marked) {
        const head = el("div", { class: "markdown-callout-head" },
          el("span", { class: "markdown-callout-icon", text: marked.icon }), " ", marked.title);
        const body = !marked.closed && marked.body.length
          ? el("div", { class: "markdown-callout-body" }, marked.body.flatMap((part, index) =>
            index ? [el("br"), ...inline(part)] : inline(part)))
          : null;
        result.append(el("div", { class: "markdown-callout", "data-type": marked.type }, head, body));
      } else {
        result.append(el("blockquote", {}, quoted.flatMap((part, index) =>
          index ? [el("br"), ...inline(part)] : inline(part))));
      }
      list = null;
    } else {
      // Consecutive prose lines are one paragraph, the way a blank line is
      // what separates them in Markdown. One paragraph per wrapped line reads
      // broken.
      const prose = [line];
      while (i + 1 < lines.length && plain(lines[i + 1])) prose.push(lines[++i]);
      result.append(el("p", {}, prose.flatMap((part, index) =>
        index ? [el("br"), ...inline(part)] : inline(part))));
      list = null;
    }
  }
  return result;
}

// What reading aloud says: the same Markdown without its punctuation. A
// heading is its words, a list is its items, a link says its label, and the
// task marks and fence lines - which a voice cannot use - drop away.
export function spokenText(text) {
  return String(text || "").replace(/\r\n?/g, "\n").split("\n")
    .map((line) => line
      .replace(/^\s*(?:[-*+] |\d+[.)] )\[[ xX]\]\s+/, "")
      .replace(/^\s*(?:[-*+] |\d+[.)] )/, "")
      .replace(/^#{1,6}\s+/, "")
      .replace(/^>\s?/, "")
      .replace(/\[([^\]\n]+)\]\([^\s)]+\)/g, "$1")
      .replace(/\*\*([^*\n]+)\*\*/g, "$1")
      .replace(/~~([^~\n]+)~~/g, "$1")
      .replace(/`([^`\n]+)`/g, "$1"))
    .join("\n")
    .replace(/```[a-z0-9]*\n?/gi, "")
    .trim();
}
