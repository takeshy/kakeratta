import { feed, panel } from "./frame.js";
import { personas, timeline } from "./fixtures.js";

export default { title: "Timeline" };

const show = (entries) => panel(feed(entries, personas).element);

export const Empty = { name: "空", render: () => show([]) };
export const Quiet = { name: "1件・反応なし", render: () => show(timeline().quiet) };
export const Answered = { name: "反応がついた", render: () => show(timeline().answered) };
export const Opened = {
  name: "返信を開いた",
  render: () => {
    const view = feed(timeline().answered, personas);
    view.element.querySelector(".thread-toggle")?.click();
    return panel(view.element);
  },
};
export const Settled ={ name: "溜まってきた", render: () => show(timeline().many) };
export const Reading = { name: "画像を読み取り中", render: () => show(timeline().reading) };
export const Unreadable = { name: "画像を読めなかった", render: () => show(timeline().unreadable) };
