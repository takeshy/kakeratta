import { screen } from "./frame.js";
import { answered, hero, many, nobody, personas, quiet } from "./fixtures.js";

export default { title: "Screen" };

// The state the app cannot work in. Nothing else on the screen should be able
// to out-shout it while it is up.
export const NobodyInTheRoom = {
  name: "担当が誰もいない",
  render: () => screen({ rows: nobody, entries: [] }),
};

export const FirstRun = {
  name: "はじめての起動（担当あり・投稿なし）",
  render: () => screen({ rows: personas, entries: [] }),
};

// One post and no reaction. A new user reads this as a broken app unless the
// timeline says otherwise, so it says so - and stops once there is a backlog.
export const OnePostNoReaction = {
  name: "1件・反応なし（注記が出る）",
  render: () => screen({ rows: personas, entries: quiet }),
};

export const Settled = {
  name: "溜まってきた（注記は消える）",
  render: () => screen({ rows: personas, entries: many }),
};

// The capture the README opens with. Short enough to fit in one image.
export const Hero = {
  name: "README 用",
  render: () => screen({ rows: personas, entries: hero }),
};

export const Mentioning = {
  name: "担当を指名して書いている途中",
  render: () => screen({ rows: personas, entries: answered, text: "@たべ 今日の昼、どこがいいと思う" }),
};

export const ReadingAloud = {
  name: "読み上げ中",
  render: () => screen({ rows: personas, entries: answered, speaking: true }),
};
