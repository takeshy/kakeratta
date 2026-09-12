// Turning a block into DOM, and turning a DOM selection back into positions.
//
// Both directions count characters over the body text only: ruby readings and
// editorial notes are decoration, and a position must mean the same thing
// whether it was produced by the parser, a click, or the reader's finger.
import { el } from "./dom.js";

export function renderBlock(block, highlights = []) {
  const chars = [...block.plain];
  const rubyAt = new Array(chars.length).fill(-1);
  (block.ruby || []).forEach((ruby, index) => {
    for (let i = ruby.offset; i < ruby.offset + ruby.length && i < chars.length; i++) rubyAt[i] = index;
  });
  const marked = new Array(chars.length).fill(false);
  for (const range of highlights) {
    const start = Math.max(0, range.start - block.range.start.absolute);
    const end = Math.min(chars.length, range.end - block.range.start.absolute);
    for (let i = start; i < end; i++) marked[i] = true;
  }
  const notesAt = new Map();
  for (const note of block.notes || []) {
    if (!notesAt.has(note.offset)) notesAt.set(note.offset, []);
    notesAt.get(note.offset).push(note.text);
  }

  const fragment = document.createDocumentFragment();
  let index = 0;
  const emit = (from, to) => {
    const text = chars.slice(from, to).join("");
    const ruby = rubyAt[from];
    let node = document.createTextNode(text);
    if (ruby >= 0) {
      node = el("ruby", {}, text, el("rt", { text: block.ruby[ruby].text }));
    }
    if (marked[from]) {
      const mark = el("mark", {}, node);
      fragment.append(mark);
      return;
    }
    fragment.append(node);
  };
  while (index < chars.length) {
    for (const note of notesAt.get(index) || []) {
      fragment.append(el("span", { class: "note", text: note }));
    }
    let end = index + 1;
    while (end < chars.length && rubyAt[end] === rubyAt[index] && marked[end] === marked[index] && !notesAt.has(end)) end++;
    emit(index, end);
    index = end;
  }
  for (const note of notesAt.get(chars.length) || []) {
    fragment.append(el("span", { class: "note", text: note }));
  }
  return fragment;
}

// offsetIn counts body characters before a point in the rendered block, so a
// selection can be stored as a position rather than as a pixel.
export function offsetIn(blockNode, node, nodeOffset) {
  let count = 0;
  let found = -1;
  const walk = (current) => {
    if (found >= 0) return;
    if (current.nodeType === Node.TEXT_NODE) {
      if (current === node) {
        found = count + [...current.data.slice(0, nodeOffset)].length;
        return;
      }
      count += [...current.data].length;
      return;
    }
    if (current.nodeType !== Node.ELEMENT_NODE) return;
    // Readings and annotations are not part of the text being counted.
    if (current.tagName === "RT" || current.classList.contains("note")) return;
    for (const child of current.childNodes) walk(child);
  };
  walk(blockNode);
  return found >= 0 ? found : count;
}
