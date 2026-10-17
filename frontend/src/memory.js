// A view of what a persona keeps: its memory file (Memo.md) - a summary, the
// facts that hold now, and a line per kakera with the entry it leads to and
// its tags. No inference or rewriting: unfamiliar metadata and Markdown are
// always available in the original text. Notes kept the old way, one per
// thing with frontmatter, still read.
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
  // A closing fence glued to the last value ("+09:00---") still closes the
  // block: notes saved before the app mended that on write read this way.
  const front = text.match(/^---\n([\s\S]*?)\n?---(?:\n|$)/);
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
      } else if (!value.trim() && i + 1 < lines.length && /^\s+-\s/.test(lines[i + 1])) {
        // A block list: tags written one per line.
        const items = [];
        while (i + 1 < lines.length && /^\s+-\s/.test(lines[i + 1])) items.push(lines[++i].replace(/^\s+-\s*/, "").trim());
        metadata[key] = items;
      } else metadata[key] = scalar(value);
    }
  }
  const field = (key) => typeof metadata[key] === "string" ? metadata[key] : "";
  const heading = body.match(/^#\s+(.+)$/m)?.[1];
  const title = field("title") || heading || note.path.replace(/\.md$/i, "").split("/").pop();
  if (body.startsWith(`# ${title}\n`) || body === `# ${title}`) body = body.slice(title.length + 2).trim();
  return {
    ...note, title, type: field("type"), description: field("description"), resource: field("resource"), tags: tagsOf(metadata.tags), body,
    facts: isMemo(note.path),
  };
}

// isMemo says whether a path is a persona's memory file.
export const isMemo = (path) => path === "Memo.md";

// tagsOf reads the tags however they were written: [a, b], or one per line.
function tagsOf(value) {
  const raw = Array.isArray(value) ? value
    : typeof value === "string" ? value.replace(/^\[|\]$/g, "").split(",") : [];
  const seen = new Set();
  const tags = [];
  for (const item of raw) {
    const tag = String(item).trim().replace(/^["']|["']$/g, "").trim();
    if (!tag || seen.has(tag.toLowerCase())) continue;
    seen.add(tag.toLowerCase());
    tags.push(tag);
  }
  return tags;
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
  // In a record, a trailing "#tag" is a record's tag and narrows the
  // view; "expires:" and "review:" are the record's dates, and "asked:" the
  // post that asked about it, which is the app's own bookkeeping.
  const pattern = context.records
    ? /\[([^\]\n]+)\]\(([^\s)]+)\)|\*\*([^*\n]+)\*\*|`([^`\n]+)`|(https?:\/\/[^\s<>]+|\/\?entry=[^\s<>]+)|(?<=\s)#([^\s#]+)(?=\s|$)|(?<=\s)(expires|review):(\d{4}-\d{2}-\d{2})\/\d+d(?=\s|$)|(?<=\s)(?:asked|of):\S+(?=\s|$)/g
    : context.facts
      // A fact's trailer: the kakera it rests on, and since when it holds.
      // "reviewed:" is the app's own bookkeeping of a past event it has kept.
      ? /\[([^\]\n]+)\]\(([^\s)]+)\)|\*\*([^*\n]+)\*\*|`([^`\n]+)`|(https?:\/\/[^\s<>]+|\/\?entry=[^\s<>]+)|(?<=\s)from:(\S+)(?=\s|$)|(?<=\s)(since):(\d{4}-\d{2}-\d{2})(?=\s|$)|(?<=\s)reviewed:\S+(?=\s|$)/g
      : /\[([^\]\n]+)\]\(([^\s)]+)\)|\*\*([^*\n]+)\*\*|`([^`\n]+)`|(https?:\/\/[^\s<>]+|\/\?entry=[^\s<>]+)/g;
  let start = 0;
  for (const match of text.matchAll(pattern)) {
    out.push(text.slice(start, match.index));
    if (context.facts && match[6]) out.push(el("span", { class: "memory-sources" }, ...match[6].split(",").flatMap((id, index, all) => [
      index ? " " : "",
      entryLink(id, all.length > 1 ? t("元の投稿{0}", index + 1) : t("元の投稿"), context),
    ])));
    else if (context.facts && match[7]) out.push(el("span", { class: "chip ghost memory-since", text: t("{0}から", match[8]) }));
    else if (context.facts && match[0].startsWith("reviewed:")) out.push("");
    else if (match[7]) out.push(match[7] === "expires"
      ? el("span", { class: "chip ghost memory-expires", text: t("{0} に消える", match[8]), i18nTitle: "期限が過ぎると本体が消す記録" })
      : el("span", { class: "chip ghost memory-review", text: t("{0} に見直し", match[8]), i18nTitle: "この日を過ぎると担当が「まだ要る?」と聞く記録" }));
    else if (/^(asked|of):/.test(match[0])) out.push("");
    else if (match[6]) out.push(context.onTag
      ? el("button", { class: `chip ghost memory-tag${context.tag === match[6] ? " active" : ""}`, text: `#${match[6]}`, i18nTitle: "このタグの記録があるメモだけを表示", onclick: () => context.onTag(match[6]) })
      : el("span", { class: "memory-tag", text: `#${match[6]}` }));
    else if (match[3]) out.push(el("strong", { text: match[3] }));
    else if (match[4]) out.push(el("code", { text: match[4] }));
    else {
      const label = match[1] || match[5];
      const target = memoryLink(match[2] || match[5], context.path, context.paths);
      if (target?.entry && context.records) {
        // A record's id is the post to go back to, not a word to read.
        out.push(entryLink(new URL(target.href, "https://kakeratta.invalid").searchParams.get("entry"), t("元の投稿"), context));
      } else if (target?.note) out.push(el("button", { class: "memory-note-link", text: label,
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

// factControls put one fact right, or throw it away, from where it is read.
// Editing changes only its words; the posts it rests on and its days stay.
function factControls(li, id, line, context) {
  const words = line.replace(/(?:\s(?:from|since|until|by|reviewed):\S+)+$/, "");
  const error = el("span", { class: "hint warn", role: "alert", hidden: true });
  const fail = (reason) => { error.textContent = t(reason?.message || String(reason)); error.hidden = false; };
  const pencil = el("button", { class: "chip ghost icon memory-fact-edit", text: "✎", "aria-label": t("この事実を直す"), i18nTitle: "この事実を直す", onclick: (event) => {
    event?.preventDefault?.();
    const reading = [...li.childNodes || li.children || []];
    const input = el("input", { class: "skill-input memory-fact-input", maxlength: 200, "aria-label": t("事実") });
    input.value = words;
    const save = el("button", { class: "button primary", label: "保存", onclick: async () => {
      if (save.disabled) return;
      save.disabled = true;
      try { await context.onFactEdit(id, input.value); } catch (reason) { fail(reason); save.disabled = false; }
    } });
    const cancel = el("button", { class: "chip ghost", label: "キャンセル", onclick: () => li.replaceChildren(...reading) });
    li.replaceChildren(input, save, cancel, error);
    input.focus?.();
  } });
  const forget = el("button", { class: "chip ghost icon memory-fact-forget", text: "×", "aria-label": t("この事実を忘れさせる"), i18nTitle: "この事実を忘れさせる", onclick: async () => {
    if (forget.disabled || !confirm(t("「{0}」を忘れさせますか。", words))) return;
    forget.disabled = true;
    try { await context.onFactForget(id); } catch (reason) { fail(reason); forget.disabled = false; }
  } });
  return el("span", { class: "memory-fact-controls" }, pencil, forget, error);
}

// entryLink opens the post a fact or a record rests on over the memory view
// when the view can, and in a new tab when it cannot.
function entryLink(id, label, context) {
  const href = `/?entry=${encodeURIComponent(id)}`;
  if (!context.onOpenEntry) return el("a", { href, target: "_blank", rel: "noopener noreferrer", i18nTitle: "元の投稿を開く（新しいタブ）", text: label });
  return el("a", { href, class: "memory-entry-link", text: label, onclick: (event) => { event?.preventDefault?.(); context.onOpenEntry(id); } });
}

// Deliberately render text nodes, never model-authored HTML. Support the OKF
// conventions (headings, lists, tables, quotations, links), retaining raw text.
// plain reports a line that is ordinary prose: not blank, not a heading, list,
// quotation, table rule, code fence, or one of the app's own markers.
function plain(line) {
  return Boolean(line.trim()) && !/^```/.test(line) && !/^#{1,6}\s+/.test(line) &&
    !/^\s*(?:[-*+] |\d+[.)] )/.test(line) && !line.startsWith("> ") && !line.includes("|") &&
    !/^<!-- (?:\/?kakeratta:note-sources|kakeratta:summary-dirty|kakeratta:(?:checked|persona-suggested) \S+) -->$/.test(line);
}

function markdown(body, context) {
  const result = el("div", { class: "memory-body" });
  const lines = body.split("\n");
  let list = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!line.trim() || /^<!-- (?:\/?kakeratta:note-sources|kakeratta:summary-dirty|kakeratta:(?:checked|persona-suggested) \S+) -->$/.test(line)) { list = null; continue; }
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
      const summary = context.facts && heading[1].match(/^まとめ（(\d{4}-\d{2}-\d{2})(?: 更新)?）$/);
      // The facts the daily pass made, and the records it has not folded yet,
      // are told apart by their own headings.
      const localized = heading[1] === "記憶を更新した投稿" ||
        (context.facts && ["いまの事実", "かけらの記録"].includes(heading[1]));
      const label = summary ? t("まとめ（{0} 更新）", summary[1]) : localized ? t(heading[1]) : heading[1];
      result.append(el("h5", {}, inline(label, context)));
      list = null;
    } else if (item) {
      const tag = /^\s*\d/.test(line) ? "ol" : "ul";
      if (!list || list.tagName.toLowerCase() !== tag) { list = el(tag); result.append(list); }
      // A fact's id is how the persona names it; a reader is not shown it.
      const factID = context.facts ? item[1].match(/^\[(f_[A-Za-z0-9]+)\]/)?.[1] : null;
      const text = factID ? item[1].replace(/^\[f_[A-Za-z0-9]+\]\s*/, "") : item[1];
      const record = context.facts && /^\d{4}-\d{2}-\d{2} \d{2}:\d{2} \[/.test(text);
      const li = el("li", {}, inline(text, record ? { ...context, records: true, facts: false } : context));
      if (factID && context.onFactEdit) li.append(factControls(li, factID, text, context));
      list.append(li);
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
  return String(text || "").split(SOURCES_MARKER)[0].trimEnd();
};

// typeLabel is how a note's type reads: the word for a known one, else the
// type as the persona wrote it.
const typeLabel = (type) => typeLabels[String(type || "").toLowerCase()] ? t(typeLabels[String(type).toLowerCase()]) : type;
const sameType = (a, b) => String(a || "").toLowerCase() === String(b || "").toLowerCase();

// The notes are one list, in the order given (the most recently written
// first); a note's type is a label on it, not a heading over it.
//
// notes may be one page of many. allPaths then names every note, so a link to
// one not read yet still resolves, and onOpenMissing fetches it. With onFilter
// the notes come already narrowed to filter ({ tag, type }), and a tag or type
// chip asks for another narrowing; without it the view narrows what it has.
// onMore, when given, reads the next page.
export function createMemoryView(notes, { onSearch, onForget, onEdit, allPaths, filter: given = {}, onFilter, onMore, onOpenMissing, onOpenEntry, onEditFact, onForgetFact } = {}) {
  const parsed = (notes || []).map(parseMemoryNote);
  const paths = new Set([...(allPaths || []), ...parsed.map(note => note.path)]);
  const cards = new Map();
  const view = el("div", { class: "memory-inspector" });
  // A tag or a type narrows the view to the notes that carry it; the whole
  // view is drawn again either way, which is how the memory reader already
  // works.
  let filter = { tag: given.tag || "", type: given.type || "" };
  const narrow = (change) => {
    const next = { ...filter, ...change };
    if (onFilter) onFilter(next);
    else { filter = next; draw(); }
  };
  function tagChips(note) {
    if (!note.tags?.length && !note.type) return null;
    return el("div", { class: "memory-tags" },
      note.type ? el("button", {
        class: `chip memory-type${sameType(filter.type, note.type) ? " active" : ""}`, text: typeLabel(note.type), i18nTitle: "この種類のメモだけを表示",
        onclick: () => narrow({ type: sameType(filter.type, note.type) ? "" : note.type }),
      }) : null,
      ...(note.tags || []).map((tag) => el("button", {
        class: `chip ghost memory-tag${filter.tag === tag ? " active" : ""}`, text: `#${tag}`, i18nTitle: "このタグのメモだけを表示",
        onclick: () => narrow({ tag: filter.tag === tag ? "" : tag }),
      })));
  }
  const openNote = (path) => {
    const card = cards.get(path);
    if (!card) { onOpenMissing?.(path); return; }
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
  // A pencil on the card. Since a persona only knows what it was told, the
  // text can be put right by hand: the card turns into an editor of the
  // persona's own text, and the links the app keeps underneath stay as they
  // are.
  function head(note, title, mount, reading) {
    if (!onEdit) return el("div", { class: "memory-head" }, title);
    const error = el("p", { class: "hint warn", role: "alert", hidden: true });
    const pencil = el("button", { class: "chip ghost icon memory-edit", text: "✎", "aria-label": t("編集"), i18nTitle: "編集", onclick: (event) => {
      event?.preventDefault?.();
      const input = el("textarea", { class: "skill-input memory-input", rows: 12, maxlength: 262144, "aria-label": t("メモの本文") });
      input.value = editableBody(note.text);
      const save = el("button", { class: "button primary memory-save", label: "メモを保存", onclick: async () => {
        if (save.disabled) return;
        save.disabled = true;
        error.hidden = true;
        try { await onEdit(note.path, input.value); }
        catch (reason) { error.textContent = t(reason.message || String(reason)); error.hidden = false; save.disabled = false; }
      } });
      const cancel = el("button", { class: "chip ghost", label: "キャンセル", onclick: () => mount(reading()) });
      mount([
        el("div", { class: "memory-head" }, title),
        el("p", { class: "hint", text: note.path }),
        el("p", { class: "hint", label: note.facts ? "担当が覚えているまとめと記録です。綴りや事実の誤りはここで直せます。記録の日時・ID・タグや、事実の[id]・from:・since:は崩さないでください。" : "担当がまとめた記憶です。綴りや事実の誤りはここで直せます。" }),
        input, el("div", { class: "compose-row" }, save, cancel), error,
      ]);
      input.focus?.();
    } });
    return el("div", { class: "memory-head" }, title, el("span", { class: "spacer" }), pencil);
  }

  function content(note) {
    const context = { path: note.path, paths, openNote, facts: note.facts, tag: filter.tag, onOpenEntry,
      onFactEdit: note.facts && onEditFact ? (id, text) => onEditFact(note.path, id, text) : undefined,
      onFactForget: note.facts && onForgetFact ? (id) => onForgetFact(note.path, id) : undefined,
      onTag: (tag) => narrow({ tag: filter.tag === tag ? "" : tag }) };
    return [
      tagChips(note),
      note.description ? el("p", { class: "memory-description", text: note.description }) : null,
      markdown(note.body, context),
      // The summary rests on many kakera, whose records lead to them; only a
      // note from before has a post of its own to look for.
      onSearch && note.type && !/\/\?entry=/.test(note.text)
        ? el("div", { class: "memory-source-search" },
          el("p", { class: "hint", label: "このメモには元投稿のリンクが保存されていません。" }),
          el("button", { class: "chip ghost", label: "元の投稿を探す", onclick: () => onSearch(note.title) })) : null,
      note.resource && /^https?:\/\//i.test(note.resource) && safeLink(note.resource)
        ? el("a", { href: note.resource, target: "_blank", rel: "noopener noreferrer", label: "関連リンク" }) : null,
      forgetButton(note),
    ];
  }
  // What the view is narrowed to, in words: "タグ「赤」のメモ" and the like.
  function narrowedTo(suffix) {
    const type = filter.type ? typeLabel(filter.type) : "";
    if (type && filter.tag) return t(`種類「{0}」・タグ「{1}」のメモ${suffix}`, type, filter.tag);
    return type ? t(`種類「{0}」のメモ${suffix}`, type) : t(`タグ「{0}」のメモ${suffix}`, filter.tag);
  }
  function draw() {
  view.replaceChildren();
  cards.clear();
  const narrowed = Boolean(filter.tag || filter.type);
  if (narrowed) {
    view.append(el("div", { class: "memory-filter" },
      el("span", { class: "hint", text: narrowedTo("") }),
      el("button", { class: "chip ghost", label: "すべてのメモを表示", onclick: () => narrow({ tag: "", type: "" }) })));
  }
  const tagged = (note) => note.tags?.some((tag) => tag === filter.tag) ||
    (note.facts && note.body.split("\n").some((line) => line.split(/\s+/).includes(`#${filter.tag}`)));
  // The memory file comes first, then any other notes.
  const shown = (narrowed && !onFilter
    ? parsed.filter((note) => (!filter.tag || tagged(note)) && (!filter.type || sameType(note.type, filter.type)))
    : parsed).map((note, order) => ({ note, order }))
    .sort((a, b) => (b.note.facts - a.note.facts) || a.order - b.order)
    .map(({ note }) => note);
  const reserved = new Map();
  const list = el("div", { class: "memory-list" });
  for (const note of shown) {
    if (["index.md", "log.md"].includes(note.path)) { reserved.set(note.path, note); continue; }
    const title = note.facts ? t("まとめと記録") : note.title;
    const card = el("article", { class: "memory-card", tabindex: "-1", "data-path": note.path });
    const mount = (nodes) => card.replaceChildren(...nodes.filter(Boolean));
    const reading = () => [head(note, el("h4", { text: title }), mount, reading), ...content(note)];
    mount(reading());
    cards.set(note.path, card);
    list.append(card);
  }
  const regular = shown.length - reserved.size;
  const appendReserved = (note) => {
    if (!note) return;
    const summary = el("summary", { label: note.path === "index.md" ? "記憶の目次" : "記憶の更新履歴" });
    const card = el("details", { class: "memory-card", tabindex: "-1", open: !regular, "data-path": note.path });
    const mount = (nodes) => card.replaceChildren(summary, ...nodes.filter(Boolean));
    const reading = () => [head(note, el("h4", { text: note.path }), mount, reading), ...content(note)];
    mount(reading());
    cards.set(note.path, card);
    view.append(card);
  };
  appendReserved(reserved.get("index.md"));
  if (regular) view.append(list);
  appendReserved(reserved.get("log.md"));
  if (narrowed && !shown.length) view.append(el("p", { class: "hint", text: narrowedTo("はありません。") }));
  else if (!parsed.length) view.append(el("p", { class: "hint", label: "まだ覚えていることはありません。" }));
  if (onMore) {
    const more = el("button", { class: "button memory-more", label: "さらに読み込む", onclick: async () => {
      if (more.disabled) return;
      more.disabled = true;
      try { await onMore(); } finally { more.disabled = false; }
    } });
    view.append(el("div", { class: "memory-more-row" },
      allPaths && !narrowed ? el("span", { class: "hint", text: t("{0}件中{1}件を表示", allPaths.length, parsed.length) }) : null,
      more));
  }
  }
  draw();
  return view;
}
