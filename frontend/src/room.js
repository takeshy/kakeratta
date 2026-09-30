// Who is in the room.
//
// The app does one thing with a kakera: it hands it to whoever covers that
// subject. With nobody here it hands it to nobody - so this is the one state
// where the row stops being a quiet label and becomes the loudest thing on the
// screen. Everywhere else it stays a row of names, because it is also the
// answer to "who can I ask?", and asking is a tap on a name.
import { el, clear } from "./dom.js";
import { t } from "./i18n.js";
import { gloss, named } from "./builtins.js";

// The order of the badges is the viewer's own, kept in this browser like the
// list's sort: people who arrange them differently on a phone and a desktop are
// both right. A persona that is not in the saved order goes after the ones that are.
const ORDER_KEY = "kakeratta.room.order";
const HINT_KEY = "kakeratta.room.hinted";

function savedOrder() {
  try {
    const ids = JSON.parse(globalThis.localStorage?.getItem(ORDER_KEY) || "[]");
    return Array.isArray(ids) ? ids.filter((id) => typeof id === "string") : [];
  } catch { return []; }
}

function saveOrder(ids) {
  try { globalThis.localStorage?.setItem(ORDER_KEY, JSON.stringify(ids)); } catch { /* not kept */ }
}

export function arranged(rows, ids = savedOrder()) {
  const rank = new Map(ids.map((id, index) => [id, index]));
  return rows
    .map((row, index) => ({ row, index }))
    .sort((a, b) => (rank.get(a.row.id) ?? Infinity) - (rank.get(b.row.id) ?? Infinity) || a.index - b.index)
    .map(({ row }) => row);
}

// The badges that step aside slide into their new places rather than jumping,
// which is what shows that they can be moved: each starts where it was and eases
// to where it is now.
function glide(nodes, from) {
  if (globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;
  for (const node of nodes) {
    const before = from.get(node);
    const after = node.getBoundingClientRect();
    const dx = before.left - after.left;
    const dy = before.top - after.top;
    if (!dx && !dy) continue;
    node.animate?.([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" }], { duration: 180, easing: "ease-out" });
  }
}

// Dragging a badge over another one puts it in that place. A press that does
// not travel is still a tap, which opens the persona; one that does is not.
function draggable(chip, bar, onDrop) {
  let start = null;
  let dragging = false;
  chip.addEventListener("pointerdown", (event) => {
    if (event.button) return;
    start = { x: event.clientX, y: event.clientY, id: event.pointerId };
    dragging = false;
  });
  chip.addEventListener("pointermove", (event) => {
    if (!start || event.pointerId !== start.id) return;
    if (!dragging) {
      if (Math.hypot(event.clientX - start.x, event.clientY - start.y) < 8) return;
      dragging = true;
      chip.setPointerCapture?.(event.pointerId);
      chip.classList.add("dragging");
    }
    const over = document.elementFromPoint(event.clientX, event.clientY)?.closest?.(".persona-chip");
    if (!over || over === chip || over.parentNode !== chip.parentNode) return;
    const before = Boolean(over.compareDocumentPosition(chip) & Node.DOCUMENT_POSITION_FOLLOWING);
    const siblings = [...chip.parentNode.querySelectorAll(".persona-chip")];
    const from = new Map(siblings.map((node) => [node, node.getBoundingClientRect()]));
    over[before ? "before" : "after"](chip);
    glide(siblings.filter((node) => node !== chip), from);
  });
  const finish = () => {
    if (!start) return;
    start = null;
    if (!dragging) return;
    chip.classList.remove("dragging");
    chip.addEventListener("click", (event) => { event.stopImmediatePropagation(); event.preventDefault(); }, { capture: true, once: true });
    setTimeout(() => { dragging = false; }, 0);
    onDrop();
  };
  chip.addEventListener("pointerup", finish);
  chip.addEventListener("pointercancel", finish);
}

export function createRoom({ onOpen, onActivate, onSelect }) {
  const banner = el("section", { class: "nobody", hidden: true });
  const bar = el("div", { class: "who-bar" });
  let expanded = false;
  let rows = [];

  function drawBanner() {
    const active = rows.filter((row) => row.active);
    banner.hidden = active.length > 0;
    if (!banner.hidden) clear(banner);
    if (active.length) return;
    clear(banner).append(
      el("p", { class: "nobody-line", label: "担当が誰もいません。" }),
      el("p", { class: "hint", label: "いま放り込んだかけらは、どこにも渡りません。1人呼べば、そこから受け取ります。" }),
    );
    const quick = el("div", { class: "nobody-row" });
    for (const row of rows.filter((row) => row.builtin && !row.active)) {
      quick.append(
        el("button", {
          class: "chip add",
          title: gloss(row).domain,
          text: t("{0} を呼ぶ", `${row.icon || "◎"} ${named(row)}`),
          onclick: () => onActivate?.(row.id),
        }),
      );
    }
    if (quick.children.length) banner.append(quick);
    banner.append(
      el("div", { class: "nobody-row" },
        el("button", { class: "button primary", label: "担当をえらぶ", onclick: () => onOpen?.() })),
    );
  }

  // Interests and people are different things to have in the room - one is a
  // subject that gets asked, the other somebody who is named - so each gets its
  // own group, with a label once there is more than one kind to tell apart.
  function drawBar() {
    clear(bar);
    const active = rows.filter(row => row.active);
    const groups = [["subject", "関心ごと"], ["person", "人物"]]
      .map(([kind, label]) => [kind, label, arranged(active.filter(row => (row.kind === "person" ? "person" : "subject") === kind))])
      .filter(([, , members]) => members.length);
    bar.classList.toggle("expanded", expanded);
    for (const [kind, label, members] of groups) {
      const group = el("div", { class: "who-group", "data-kind": kind });
      if (groups.length > 1) group.append(el("span", { class: "who-label", label }));
      for (const [index, row] of members.entries()) {
        const chip = el("button", {
          class: `chip persona-chip${kind === "person" ? " person-chip" : ""}${index >= 2 ? " persona-overflow" : ""}`,
          title: `${gloss(row).domain}\n${t("押すと @{0} を入力します", row.name)}\n${t("ドラッグで並べ替えできます")}`,
          text: `${row.icon || "◎"} ${row.name}`,
          "data-id": row.id,
          onclick: () => onSelect?.(row),
        });
        draggable(chip, bar, () => {
          const shown = [...bar.querySelectorAll(".persona-chip")].map((node) => node.getAttribute("data-id"));
          saveOrder([...shown, ...savedOrder().filter((id) => !shown.includes(id))]);
          drawBar();
        });
        group.append(chip);
      }
      if (members.length > 2) {
        const hidden = members.length - 2;
        const text = () => expanded ? t("折りたたむ") : t(kind === "person" ? "その他{0}人" : "その他{0}体", hidden);
        const more = el("button", {
          class: "chip ghost persona-more", type: "button", text: text(),
          "aria-expanded": String(expanded),
          onclick: () => {
            expanded = !expanded;
            bar.classList.toggle("expanded", expanded);
            for (const button of bar.querySelectorAll(".persona-more")) button.setAttribute("aria-expanded", String(expanded));
            drawBar();
          },
        });
        group.append(more);
      }
      bar.append(group);
    }
    nudge(groups.some(([, , members]) => members.length > 1));
  }

  // The first time a group has badges to swap, the first one leans aside and
  // back once, so that they read as movable. After that it stays still.
  function nudge(movable) {
    if (!movable) return;
    try {
      if (globalThis.localStorage?.getItem(HINT_KEY)) return;
      globalThis.localStorage?.setItem(HINT_KEY, "1");
    } catch { return; }
    const first = bar.querySelectorAll(".persona-chip")[0];
    first?.classList.toggle("nudge", true);
  }

  return {
    banner,
    bar,
    set(next) {
      rows = next || [];
      drawBanner();
      drawBar();
    },
  };
}
