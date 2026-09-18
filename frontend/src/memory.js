// A view of the notes already saved by a persona. No inference or rewriting:
// unfamiliar metadata and Markdown are always available in the original note.
import { el, safeLink } from "./dom.js";
import { t } from "./i18n.js";

function scalar(value) {
  value = value.trim();
  if (value.startsWith('"')) {
    try { return JSON.parse(value); } catch { return value; }
  }
  if (value.startsWith("'") && value.endsWith("'")) return value.slice(1, -1).replace(/''/g, "'");
  return value.replace(/\s+#.*$/, "").trim();
}

export function parseMemoryNote(note) {
  let text = String(note.text || "").replace(/\r\n?/g, "\n");
  // A model that escaped its own line breaks leaves one long line. The note is
  // repaired when it is written now, but the ones already saved have to read.
  // Only what the persona wrote is examined: the sources block the app appends
  // always has real line breaks, and would otherwise hide the damage above it.
  const marker = text.indexOf("<!-- kakeratta:note-sources -->");
  const head = (marker < 0 ? text : text.slice(0, marker)).trim();
  if (!head.includes("\n") && head.includes("\\n")) {
    text = head.replace(/\\n/g, "\n").replace(/\\t/g, "\t") +
      (marker < 0 ? "" : "\n\n" + text.slice(marker));
  }
  const metadata = Object.create(null);
  let body = text;
  const front = text.match(/^---\n([\s\S]*?)\n---(?:\n|$)/);
  if (front) {
    body = text.slice(front[0].length).trim();
    const lines = front[1].split("\n");
    for (let i = 0; i < lines.length; i++) {
      const match = lines[i].match(/^([\w-]+):\s*(.*)$/);
      if (!match) continue;
      const [, key, value] = match;
      if (/^[>|][-+]?\s*$/.test(value)) {
        const parts = [];
        while (i + 1 < lines.length && /^(?:\s+|$)/.test(lines[i + 1])) parts.push(lines[++i].trim());
        metadata[key] = parts.join(value.startsWith(">") ? " " : "\n").trim();
      } else metadata[key] = scalar(value);
    }
  }
  const field = (key) => typeof metadata[key] === "string" ? metadata[key] : "";
  const heading = body.match(/^#\s+(.+)$/m)?.[1];
  const title = field("title") || heading || note.path.replace(/\.md$/i, "").split("/").pop();
  if (body.startsWith(`# ${title}\n`) || body === `# ${title}`) body = body.slice(title.length + 2).trim();
  return { ...note, title, type: field("type"), description: field("description"), resource: field("resource"), body };
}

// OKF links are bundle-relative; also accept ordinary sibling-relative links.
export function memoryLink(value, path, paths) {
  if (/^(?:\/\/|[a-z][a-z\d+.-]*:)/i.test(value)) {
    return safeLink(value) ? { href: value } : null;
  }
  try {
    const url = new URL(value, `https://kakeratta.invalid/${path}`);
    if (url.pathname === "/" && url.searchParams.get("entry")) {
      return { href: `/?entry=${encodeURIComponent(url.searchParams.get("entry"))}`, entry: true };
    }
    const target = decodeURIComponent(url.pathname.slice(1));
    const found = paths.has(target) ? target : paths.has(`${target}.md`) ? `${target}.md` : null;
    if (found) return { note: found };
    // Missing bundle links must not accidentally navigate to the app's SPA fallback.
    return null;
  } catch { return null; }
}

function inline(text, context) {
  const out = [];
  const pattern = /\[([^\]\n]+)\]\(([^\s)]+)\)|\*\*([^*\n]+)\*\*|`([^`\n]+)`|(https?:\/\/[^\s<>]+|\/\?entry=[^\s<>]+)/g;
  let start = 0;
  for (const match of text.matchAll(pattern)) {
    out.push(text.slice(start, match.index));
    if (match[3]) out.push(el("strong", { text: match[3] }));
    else if (match[4]) out.push(el("code", { text: match[4] }));
    else {
      const label = match[1] || match[5];
      const target = memoryLink(match[2] || match[5], context.path, context.paths);
      if (target?.note) out.push(el("button", { class: "memory-note-link", text: label,
        onclick: () => context.openNote(target.note) }));
      else if (target?.href) out.push(el("a", { href: target.href, target: "_blank", rel: "noopener noreferrer",
        ...(target.entry ? { i18nTitle: "元の投稿を開く（新しいタブ）" } : {}), text: label }));
      else out.push(el("span", { text: label, title: match[2] || match[5] }));
    }
    start = match.index + match[0].length;
  }
  out.push(text.slice(start));
  return out;
}

// Deliberately render text nodes, never model-authored HTML. Support the OKF
// conventions (headings, lists, tables, quotations, links), retaining raw text.
// plain reports a line that is ordinary prose: not blank, not a heading, list,
// quotation, table rule, code fence, or one of the app's own markers.
function plain(line) {
  return Boolean(line.trim()) && !/^```/.test(line) && !/^#{1,6}\s+/.test(line) &&
    !/^\s*(?:[-*+] |\d+[.)] )/.test(line) && !line.startsWith("> ") && !line.includes("|") &&
    !/^<!-- \/?kakeratta:note-sources -->$/.test(line);
}

function markdown(body, context) {
  const result = el("div", { class: "memory-body" });
  const lines = body.split("\n");
  let list = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!line.trim() || /^<!-- \/?kakeratta:note-sources -->$/.test(line)) { list = null; continue; }
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
      result.append(el("h5", {}, inline(heading[1] === "記憶を更新した投稿" ? t(heading[1]) : heading[1], context)));
      list = null;
    } else if (item) {
      const tag = /^\s*\d/.test(line) ? "ol" : "ul";
      if (!list || list.tagName.toLowerCase() !== tag) { list = el(tag); result.append(list); }
      list.append(el("li", {}, inline(item[1], context)));
    } else if (line.includes("|") && /^\s*\|?\s*:?-{3,}/.test(lines[i + 1] || "")) {
      const cells = (row) => row.trim().replace(/^\||\|$/g, "").split("|");
      const table = el("table", {}, el("thead", {}, el("tr", {}, cells(line).map(cell => el("th", {}, inline(cell.trim(), context))))));
      const rows = el("tbody");
      i++;
      while (i + 1 < lines.length && lines[i + 1].includes("|")) {
        rows.append(el("tr", {}, cells(lines[++i]).map(cell => el("td", {}, inline(cell.trim(), context)))));
      }
      table.append(rows);
      result.append(el("div", { class: "memory-table" }, table));
      list = null;
    } else if (line.startsWith("> ")) {
      result.append(el("blockquote", {}, inline(line.slice(2), context)));
      list = null;
    } else {
      // Consecutive prose lines are one paragraph, the way a blank line is what
      // separates them in Markdown. One paragraph per wrapped line reads broken.
      const prose = [line];
      while (i + 1 < lines.length && plain(lines[i + 1])) prose.push(lines[++i]);
      result.append(el("p", {}, prose.flatMap((part, index) =>
        index ? [el("br"), ...inline(part, context)] : inline(part, context))));
      list = null;
    }
  }
  return result;
}

const typeLabels = { feedback: "感想", preference: "好み", wine: "ワイン", shop: "お店", book: "本", theme: "テーマ", thread: "会話", place: "場所", routine: "繰り返し", errand: "一度きりの用" };

// The block the app appends to a note, listing the posts it rests on. It is
// the app's own and stays out of the editor.
const SOURCES_MARKER = "<!-- kakeratta:note-sources -->";
export const editableBody = (text) => {
  const marker = String(text || "").indexOf(SOURCES_MARKER);
  return (marker < 0 ? String(text || "") : String(text || "").slice(0, marker)).trimEnd();
};

export function createMemoryView(notes, { onSearch, onForget, onEdit } = {}) {
  const parsed = (notes || []).map(parseMemoryNote);
  const paths = new Set(parsed.map(note => note.path));
  const cards = new Map();
  const view = el("div", { class: "memory-inspector" });
  const openNote = (path) => {
    const card = cards.get(path);
    if (!card) return;
    if (card.tagName === "DETAILS") card.open = true;
    card.scrollIntoView({ block: "nearest", behavior: "smooth" });
    card.focus({ preventScroll: true });
  };
  function forgetButton(note) {
    if (!onForget) return null;
    const error = el("p", { class: "hint", role: "alert", hidden: true });
    const button = el("button", { class: "chip ghost", label: "このメモを忘れさせる", onclick: async () => {
      if (button.disabled || !confirm(t("「{0}」を忘れさせますか。このメモだけを削除します。", note.title))) return;
      button.disabled = true;
      error.hidden = true;
      try { await onForget(note.path); }
      catch (reason) { error.textContent = t(reason.message || String(reason)); error.hidden = false; }
      finally { button.disabled = false; }
    } });
    return el("div", { class: "memory-forget" }, button, error);
  }
  // The note as saved, and - since a persona only knows what it was told - a
  // way to put the text right by hand. What is edited is the persona's own
  // text; the links the app keeps underneath stay as they are.
  function original(note) {
    const shown = el("pre", { class: "memo", text: note.text });
    const host = el("div", { class: "memory-text" }, shown);
    const details = el("details", { class: "memory-original" }, el("summary", { label: "元のメモを見る" }),
      el("p", { class: "hint", text: note.path }), host);
    if (!onEdit) return details;
    const error = el("p", { class: "hint warn", role: "alert", hidden: true });
    const edit = el("button", { class: "chip ghost memory-edit", label: "編集", onclick: () => {
      const input = el("textarea", { class: "skill-input memory-input", rows: 12, maxlength: 262144, "aria-label": t("メモの本文") });
      input.value = editableBody(note.text);
      const save = el("button", { class: "button primary memory-save", label: "メモを保存", onclick: async () => {
        if (save.disabled) return;
        save.disabled = true;
        error.hidden = true;
        try { await onEdit(note.path, input.value); }
        catch (reason) { error.textContent = t(reason.message || String(reason)); error.hidden = false; save.disabled = false; }
      } });
      const cancel = el("button", { class: "chip ghost", label: "キャンセル", onclick: () => host.replaceChildren(shown, row) });
      host.replaceChildren(
        el("p", { class: "hint", label: "担当が書いたメモです。綴りや事実の誤りはここで直せます。下の「記憶を更新した投稿」のリンクはそのまま残ります。" }),
        input, el("div", { class: "compose-row" }, save, cancel), error);
      input.focus?.();
    } });
    const row = el("div", { class: "memory-edit-row" }, edit);
    host.append(row);
    return details;
  }

  function content(note) {
    const context = { path: note.path, paths, openNote };
    return [
      note.description ? el("p", { class: "memory-description", text: note.description }) : null,
      markdown(note.body, context),
      onSearch && !/\/\?entry=/.test(note.text)
        ? el("div", { class: "memory-source-search" },
          el("p", { class: "hint", label: "このメモには元投稿のリンクが保存されていません。" }),
          el("button", { class: "chip ghost", label: "元の投稿を探す", onclick: () => onSearch(note.title) })) : null,
      note.resource && /^https?:\/\//i.test(note.resource) && safeLink(note.resource)
        ? el("a", { href: note.resource, target: "_blank", rel: "noopener noreferrer", label: "関連リンク" }) : null,
      original(note),
      forgetButton(note),
    ];
  }
  const groups = new Map();
  const reserved = [];
  for (const note of parsed) {
    if (["index.md", "log.md"].includes(note.path)) { reserved.push(note); continue; }
    const key = note.type.toLowerCase() || "other";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(note);
  }
  const ordered = [...groups].sort(([a], [b]) => a === b ? 0 : a === "preference" ? -1 : b === "preference" ? 1 : 0);
  for (const [key, group] of ordered) {
    const section = el("section", { class: "memory-group" }, el("h3", {
      text: typeLabels[key] ? t(typeLabels[key]) : group[0].type || t("その他のメモ"),
    }));
    for (const note of group) {
      const card = el("article", { class: "memory-card", tabindex: "-1", "data-path": note.path },
        el("h4", { text: note.title }), ...content(note));
      cards.set(note.path, card);
      section.append(card);
    }
    view.append(section);
  }
  for (const note of reserved) {
    const card = el("details", { class: "memory-card", tabindex: "-1", open: !groups.size, "data-path": note.path },
      el("summary", { label: note.path === "index.md" ? "記憶の目次" : "記憶の更新履歴" }), ...content(note));
    cards.set(note.path, card);
    view.append(card);
  }
  if (!parsed.length) view.append(el("p", { class: "hint", label: "まだ覚えていることはありません。" }));
  return view;
}
