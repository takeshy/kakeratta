// The reading view. It owns the current position, and it is what decides that a
// stretch has been read: a screen that stayed in front of the reader long
// enough is reported to Go, which keeps it as a read interval.
import { clear, el, toast } from "./dom.js";
import { offsetIn, renderBlock } from "./render.js";

// How long one screen may accumulate before it is reported. A reader on a long
// scroll should not have their whole sitting arrive as a single entry when they
// finally close the book: the reading log is written as they go, so a crash
// costs at most this much.
const FLUSH_MS = 20000;

export function createReader({ api, onPosition }) {
  const pages = el("div", { class: "pages" });
  const tools = el("div", { class: "selection-tools", style: "display:none" });
  let opened = null;
  let blockNodes = new Map();
  let saving = null;
  let onQuote = () => {};
  // The screen currently in front of the reader, and since when.
  let staying = null;

  const blockOf = (id) => {
    for (const section of opened.book.sections || []) {
      for (const block of section.blocks || []) if (block.id === id) return block;
    }
    return null;
  };

  function render(next) {
    opened = next;
    blockNodes = new Map();
    clear(pages);
    const highlights = (opened.memos || []).map((memo) => ({
      start: memo.range.start.absolute,
      end: Math.max(memo.range.end.absolute, memo.range.start.absolute),
    }));
    for (const section of opened.book.sections || []) {
      const node = el("section", { "data-section": section.id });
      // Every parser takes the section title from its first heading block, and
      // that block is body text: it has a position and it is read aloud. Drawing
      // the title again above it would say the same line twice.
      const first = (section.blocks || [])[0];
      const titled = first && first.kind === "heading" &&
        first.plain.trim() === (section.title || "").trim();
      if (section.title && !titled) node.append(el("h2", { class: "section-title", text: section.title }));
      for (const block of section.blocks || []) {
        const covering = highlights.filter(
          (range) => range.start < block.range.end.absolute && block.range.start.absolute < range.end,
        );
        const paragraph = el("p", {
          class: `block ${block.kind === "heading" ? "heading" : ""}`,
          "data-block": block.id,
          "data-start": block.range.start.absolute,
        });
        paragraph.append(renderBlock(block, covering));
        blockNodes.set(block.id, paragraph);
        node.append(paragraph);
      }
      pages.append(node);
    }
    staying = null;
    goTo(opened.state.current, { flash: false });
  }

  // What is on screen now: from the first block still visible to the last one.
  function visible() {
    const top = pages.getBoundingClientRect().top;
    const bottom = top + pages.clientHeight;
    let first = null;
    let last = null;
    for (const [id, node] of blockNodes) {
      const rect = node.getBoundingClientRect();
      if (rect.bottom <= top + 12 || rect.top >= bottom) continue;
      if (!first) first = id;
      last = id;
    }
    const firstBlock = first ? blockOf(first) : null;
    const lastBlock = last ? blockOf(last) : null;
    if (!firstBlock || !lastBlock) return null;
    return { start: firstBlock.range.start, end: lastBlock.range.end };
  }

  // Reading is a screen that stayed put, not a screen that went past. The
  // stretch and how long it was there go to Go, which applies the threshold:
  // the rule about what counts as read lives in one place, and it is not here.
  function report(stay, at = Date.now()) {
    if (!opened || !stay) return;
    const seconds = (at - stay.since) / 1000;
    if (stay.end.absolute <= stay.start.absolute) return;
    api
      .markRead(opened.book.id, { start: stay.start, end: stay.end }, seconds)
      .then((state) => {
        if (!opened) return;
        opened.state = state;
        onPosition(state);
      })
      .catch((error) => toast(String(error), "error"));
  }

  function trackScroll() {
    if (!opened) return;
    const now = visible();
    if (!now) return;
    save(now.start);
    if (!staying) {
      staying = { start: now.start, end: now.end, since: Date.now() };
      return;
    }
    const left = now.start.absolute >= staying.end.absolute;
    const stale = Date.now() - staying.since >= FLUSH_MS;
    if (left || stale) {
      report(staying);
      staying = { start: now.start, end: now.end, since: Date.now() };
      return;
    }
    // Still on the same screen, or part way into the next: the stretch in front
    // of the reader has grown rather than changed.
    if (now.end.absolute > staying.end.absolute) staying.end = now.end;
  }

  // Leaving the page - closing the book, switching away, reading aloud past the
  // end of a screen - is a report too. Keeping it to "on close" would lose a
  // sitting to a crash and would never fire at all in the listening case.
  function flush() {
    if (!staying) return;
    report(staying);
    staying = null;
  }

  // The current position follows the eye and nothing else. It is saved on its
  // own so going back fifty pages to check something does not touch what has
  // been read.
  function save(current) {
    if (!opened || !current) return;
    opened.state.current = current;
    onPosition(opened.state);
    clearTimeout(saving);
    saving = setTimeout(async () => {
      try {
        const state = await api.seek(opened.book.id, current);
        opened.state = { ...state, entries: state.entries };
        onPosition(opened.state);
      } catch (error) {
        toast(String(error), "error");
      }
    }, 700);
  }

  // Reading aloud is reading: the voice passing the end of a block is the same
  // event as a screen scrolling past.
  function spoke(current, reached) {
    save(current);
    if (!staying) {
      staying = { start: current, end: reached, since: Date.now() };
      return;
    }
    if (reached.absolute > staying.end.absolute) staying.end = reached;
    if (Date.now() - staying.since >= FLUSH_MS) flush();
  }

  // Jumping to an earlier scene is the feature the app exists for, so it never
  // adds to what has been read: the AI keeps knowing what the reader knows.
  function goTo(position, { flash = true } = {}) {
    if (!opened || !position) return;
    const node = blockNodes.get(position.blockId) || nodeAtAbsolute(position.absolute);
    if (!node) return;
    node.scrollIntoView({ block: "start", behavior: flash ? "smooth" : "auto" });
    if (flash) {
      node.classList.add("flash");
      setTimeout(() => node.classList.remove("flash"), 1400);
    }
    // Jumping is not reading: whatever screen was in front of the reader is
    // reported before the jump, and the new one starts its own clock.
    flush();
    const block = blockOf(node.dataset.block);
    if (block) save(block.range.start);
  }

  function nodeAtAbsolute(absolute) {
    let best = null;
    for (const node of blockNodes.values()) {
      if (Number(node.dataset.start) <= absolute) best = node;
    }
    return best;
  }

  function quoteFromSelection() {
    const selection = window.getSelection();
    if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;
    const range = selection.getRangeAt(0);
    const startNode = range.startContainer.parentElement?.closest("[data-block]");
    const endNode = range.endContainer.parentElement?.closest("[data-block]");
    if (!startNode || !endNode) return null;
    const startBlock = blockOf(startNode.dataset.block);
    const endBlock = blockOf(endNode.dataset.block);
    if (!startBlock || !endBlock) return null;
    const startOffset = offsetIn(startNode, range.startContainer, range.startOffset);
    const endOffset = offsetIn(endNode, range.endContainer, range.endOffset);
    return {
      text: selection.toString().trim(),
      range: {
        start: {
          sectionId: startBlock.range.start.sectionId,
          blockId: startBlock.id,
          charOffset: startOffset,
          absolute: startBlock.range.start.absolute + startOffset,
        },
        end: {
          sectionId: endBlock.range.start.sectionId,
          blockId: endBlock.id,
          charOffset: endOffset,
          absolute: endBlock.range.start.absolute + endOffset,
        },
      },
    };
  }

  function showTools() {
    const quote = quoteFromSelection();
    if (!quote || !quote.text) {
      tools.style.display = "none";
      return;
    }
    const rect = window.getSelection().getRangeAt(0).getBoundingClientRect();
    tools.style.display = "flex";
    tools.style.left = `${Math.max(12, rect.left)}px`;
    tools.style.top = `${Math.max(12, rect.top - 42)}px`;
    clear(tools).append(
      el("button", {
        class: "button primary",
        text: "メモにする",
        onclick: () => {
          tools.style.display = "none";
          onQuote(quote);
        },
      }),
    );
  }

  pages.addEventListener("scroll", trackScroll, { passive: true });
  // A reader who stops scrolling is still reading, so the clock is checked on a
  // tick as well as on movement.
  setInterval(trackScroll, 5000);
  window.addEventListener("beforeunload", flush);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") flush();
  });
  document.addEventListener("selectionchange", () => {
    if (!pages.contains(document.activeElement) && window.getSelection()?.isCollapsed) {
      tools.style.display = "none";
    }
  });
  pages.addEventListener("mouseup", () => setTimeout(showTools, 0));
  pages.addEventListener("keyup", (event) => {
    if (event.shiftKey) setTimeout(showTools, 0);
  });

  return {
    element: pages,
    tools,
    render,
    goTo,
    save,
    spoke,
    flush,
    get opened() {
      return opened;
    },
    blockNode: (id) => blockNodes.get(id),
    blockOf,
    setQuoteHandler: (handler) => {
      onQuote = handler;
    },
    // Page turning is a scroll of one screen: the layout stays a single model,
    // and paging is a way of moving through it rather than a second one.
    turn(direction) {
      pages.scrollBy({ top: direction * (pages.clientHeight - 60), behavior: "smooth" });
    },
    setPaged(paged) {
      pages.classList.toggle("paged", paged);
    },
  };
}
