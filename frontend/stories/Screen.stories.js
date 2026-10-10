import { getLanguage } from "../src/i18n.js";
import { screen } from "./frame.js";
import { nobody, personas, timeline, words } from "./fixtures.js";

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
  render: () => screen({ rows: personas, entries: timeline().quiet }),
};

export const Settled = {
  name: "溜まってきた（注記は消える）",
  render: () => screen({ rows: personas, entries: timeline().many }),
};

// The shared README/manual capture: a complete exchange with a current built-in
// persona, opened so both its answer and the user's reply are visible.
export const Hero = {
  name: "README・manual 用（返信を開く）",
  render: () => {
    const host = screen({
      rows: personas.filter((row) => row.builtin),
      entries: timeline().hero.filter((entry) => (entry.root || entry.id) === "p1"),
    });
    host.querySelector('.thread-toggle[aria-expanded="false"]')?.click();
    return host;
  },
};

// The landing-page example: a missed restaurant and a later daily check-in.
// Daily check-ins are independent persona posts, rather than direct replies.
export const TravelMemory = {
  name: "LP 用（静岡の店を daily で思い出す）",
  render: () => {
    const ja = getLanguage() === "ja";
    const at = (minutes) => new Date(Date.now() - minutes * 60000).toISOString();
    return screen({
      rows: personas.filter((row) => row.builtin),
      entries: [
        { id: "trip-missed", kind: "post", speaker: "user", at: at(21 * 24 * 60),
          text: ja ? "静岡旅行、さわやかに行ってみたかったけど、時間がなくて寄れなかった。"
            : "On my Shizuoka trip, I wanted to try Sawayaka, but ran out of time." },
        { id: "trip-return", kind: "post", speaker: "user", at: at(120),
          text: ja ? "明日静岡に行く" : "I'm going to Shizuoka tomorrow." },
        { id: "trip-daily", kind: "post", speaker: "gourmet", at: at(5),
          text: ja ? "明日静岡に行くなら、さわやかはいかが？以前行きたかったけど時間がなくて行けなかったみたいだから、今度こそぜひ。"
            : "If you're going to Shizuoka tomorrow, how about Sawayaka? You wanted to go last time but ran out of time. Maybe this trip is your chance." },
      ],
    });
  },
};

export const Mentioning = {
  name: "担当を指名して書いている途中",
  render: () => screen({ rows: personas, entries: timeline().answered, text: words().mentioning }),
};

export const SignedIn = {
  name: "Googleでログイン中（サインアウトあり）",
  render: () => screen({ rows: personas, entries: timeline().answered, signedIn: true }),
};

export const ReadingAloud = {
  name: "読み上げ中",
  render: () => screen({ rows: personas, entries: timeline().answered, speaking: true }),
};

export const MoreOptions = {
  name: "スマホのその他メニュー",
  globals: { viewport: { value: "mobile1", isRotated: false } },
  render: () => {
    const host = screen({ rows: personas, entries: timeline().answered, signedIn: true });
    host.querySelector(".more-toggle")?.click();
    return host;
  },
};

export const Offline = {
  name: "オフライン・送信待ち",
  render: () => {
    Object.defineProperty(navigator, "onLine", { configurable: true, value: false });
    const ja = getLanguage() === "ja";
    return screen({ rows: personas, pending: 1, entries: [
      { id: "offline-cached", root: "offline-cached", kind: "post", speaker: "user", at: new Date(Date.now() - 600000).toISOString(),
        text: ja ? "今日の散歩で、気になる喫茶店を見つけた。" : "Found a cafe that caught my eye on today's walk." },
      { id: "local-offline-pending", root: "local-offline-pending", kind: "post", speaker: "user", pending: true, at: new Date(Date.now() - 60000).toISOString(),
        text: ja ? "帰り道に寄ってみたい。電波がなくてもメモできた。" : "I'd like to stop by on my way home. Saved this note even without a connection." },
    ] });
  },
};
