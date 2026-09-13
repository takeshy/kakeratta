// The screen, in the order it is read.
//
// Navigation sits in the header and nothing else does: the read-aloud switch is
// a mode over the timeline, so it travels with the timeline. Below the header
// comes the one thing that can stop the app working (nobody in the room), then
// the way in, then who is in the room, then the timeline - which is what the
// screen is actually for.
import { el } from "./dom.js";

export function screenNodes({ compose, room, feed, onOpen }) {
  return [
    el("header", { class: "topbar" },
      el("span", { class: "brand", label: "kakeratta" }),
      el("span", { class: "spacer" }),
      el("button", { class: "chip ghost nav", label: "担当", onclick: () => onOpen?.("personas") }),
      el("button", { class: "chip ghost nav", label: "設定", onclick: () => onOpen?.("settings") })),
    room.banner,
    compose,
    room.bar,
    feed,
  ];
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
      el("button", { class: "chip ghost", label: "閉じる", onclick: () => onClose?.() }),
    ),
    body,
  );
}
