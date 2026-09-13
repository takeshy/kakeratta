import { feed, panel } from "./frame.js";
import { answered, many, personas, quiet, reading, unreadable } from "./fixtures.js";

export default { title: "Timeline" };

const show = (entries) => panel(feed(entries, personas).element);

export const Empty = { name: "空", render: () => show([]) };
export const Quiet = { name: "1件・反応なし", render: () => show(quiet) };
export const Answered = { name: "反応がついた", render: () => show(answered) };
export const Settled = { name: "溜まってきた", render: () => show(many) };
export const Reading = { name: "画像を読み取り中", render: () => show(reading) };
export const Unreadable = { name: "画像を読めなかった", render: () => show(unreadable) };
