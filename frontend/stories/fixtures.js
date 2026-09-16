// Fixtures, not mocks: the shapes here are the ones /api/personas and
// /api/timeline actually return, so a story that looks right is evidence.
import { getLanguage } from "../src/i18n.js";

const minutes = (n) => new Date(Date.now() - n * 60000).toISOString();

export const personas = [
  { id: "gourmet", name: "tabe", icon: "🍜", model: "high", builtin: true, active: true, wakes: 12,
    domain: "食事。店、味の好み、食べた記録。", tools: ["place_search", "web_search"] },
  { id: "move", name: "michi", icon: "🚃", model: "low", builtin: true, active: true, wakes: 3,
    domain: "移動。経路、所要時間、行き先。", tools: ["route", "place_search"] },
  { id: "book", name: "shiori", icon: "📖", model: "middle", builtin: true, active: true, wakes: 21,
    domain: "読書。本、引用、気になった一節。", tools: ["web_search"] },
  { id: "routine", name: "plana", icon: "🔁", model: "middle", builtin: true, active: true, wakes: 7,
    domain: "やること。繰り返しも、一度きりの用も。", tools: ["schedule"], dailyCheckIn: { enabled: true, time: "07:00", timeZone: "Asia/Tokyo" },
    // A getter, so the sample reads in whichever language the story is showing.
    get schedules() {
      const w = words();
      return [
        { id: "s_medicine", at: "2026-09-16T22:30:00Z", note: w.medicine, daily: true, timeZone: "Asia/Tokyo" },
        { id: "s_flowers", at: "2026-09-17T11:00:00Z", note: w.flowers, daily: false, timeZone: "Asia/Tokyo" },
      ];
    } },
];

// Spreading would run the schedules getter at load, before words() exists, and
// would freeze the sample into one language. Copy the descriptors instead.
export const nobody = personas.map((row) => Object.defineProperties({}, {
  ...Object.getOwnPropertyDescriptors(row),
  active: { value: false, enumerable: true },
}));

const post = (id, at, extra) => ({ id, kind: "post", speaker: "user", at, text: "", ...extra });
const said = (id, root, speaker, at, text, extra) =>
  ({ id, kind: "said", root, speaker, at, text, ...extra });

// The conversation, in the reader's language. The English capture in the
// README has to be readable by someone who cannot read the Japanese one, so
// the fixture speaks whichever language the interface is showing.
const WORDS = {
  ja: {
    noodles: "この前の担々麺、ごまが強すぎた気がする",
    sesame: "ごま強めは前も一度そう言っていました。辛さより先にごまが来る店が苦手なのかもしれません。",
    heat: "たしかに。辛いのは平気",
    shibuya: "@michi 20時に渋谷に着きたい",
    station: "新宿駅",
    route: "車の経路は約4.5km・8分が目安です。渋滞や公共交通は含まないので、到着時刻には余裕を見てください。",
    routeSource: "経路 (OSRM)",
    pagePhoto: "本のページの写真",
    origin: "『動きが心をつくる』らしい",
    ocr: "身体の状態が先にあって、感情はあとから名前を付けられる。\n——という主張の節。",
    sameChapter: "この節は前に撮ったページと同じ章です。同じ主張に二度反応しているので、覚えておきます。",
    later: "あとで読む",
    noVision: "画像を読めるモデルが設定されていません",
    mentioning: "@tabe 今日の昼、どこがいいと思う",
    medicine: "朝の薬",
    flowers: "仕事帰りに花を買う",
  },
  en: {
    noodles: "That dan dan noodle place from the other day - too much sesame, I think",
    sesame: "You said the same about heavy sesame once before. It may be places where the sesame lands before the heat that don't suit you.",
    heat: "True. The heat itself is fine.",
    shibuya: "@michi I want to be in Shibuya by 8pm",
    station: "Shinjuku Station",
    route: "The driving route is about 4.5 km and 8 minutes. This excludes live traffic and public transport, so allow extra time.",
    routeSource: "Route (OSRM)",
    pagePhoto: "A photo of a book page",
    origin: "Looks like \"How the Body Shapes the Mind\"",
    ocr: "The body's state comes first; the feeling gets its name afterwards.\n- the section making that claim.",
    sameChapter: "Same chapter as the page you captured before. Twice now on the same claim - noting that.",
    later: "Read later",
    noVision: "No vision model is configured",
    mentioning: "@tabe where should I have lunch today",
    medicine: "morning medicine",
    flowers: "flowers on the way home",
  },
};

export const words = () => WORDS[getLanguage()] || WORDS.en;

// Built at render time, so the story follows the language toolbar.
export function timeline() {
  const w = words();
  // One post, nothing said about it. This is the ordinary case, not an error.
  const quiet = [post("p1", minutes(6), { text: w.noodles })];
  const answered = [
    post("p1", minutes(140), { text: w.noodles }),
    said("s1", "p1", "gourmet", minutes(128), w.sesame),
    said("s2", "p1", "user", minutes(120), w.heat),
    post("p2", minutes(52), { text: w.shibuya, place: { lat: 35.6896, lon: 139.7006, label: w.station } }),
    said("s3", "p2", "move", minutes(50), w.route, {
      sources: [{ label: w.routeSource, url: "https://example.invalid/route" }],
    }),
    post("p3", minutes(20), {
      text: "",
      media: { blobId: "b1", mime: "image/png", readAt: minutes(19), describe: w.pagePhoto, origin: w.origin, text: w.ocr },
    }),
    said("s4", "p3", "book", minutes(17), w.sameChapter),
  ];
  // What the README leads with: one exchange and one mention, nothing cut off.
  const hero = answered.filter((entry) => ["p1", "p2"].includes(entry.kind === "post" ? entry.id : entry.root));
  const many = [
    ...answered,
    post("p4", minutes(8), { link: "https://example.invalid/article", text: "" }),
    post("p5", minutes(3), { text: w.later }),
  ];
  const reading = [post("p6", minutes(1), { media: { blobId: "b2", mime: "image/png" } })];
  const unreadable = [
    post("p7", minutes(2), { media: { blobId: "b3", mime: "image/png", readAt: minutes(1), error: w.noVision } }),
  ];
  return { quiet, answered, hero, many, reading, unreadable };
}

// ---- what the panels read ---------------------------------------------------

export const tools = ["web_search", "place_search", "route"];

export const about = {
  version: "story",
  workspace: "~/.config/kakeratta",
  providers: [
    { id: "nebius", label: "Nebius Token Factory", endpoint: "https://api.tokenfactory.nebius.com/v1" },
  ],
  tools,
  mcpServers: [],
  needsToken: true,
};

export const settings = {
  provider: "nebius",
  baseUrl: "https://api.tokenfactory.nebius.com/v1",
  apiKey: "********",
  lowModel: "nvidia/Nemotron-3_5-Lightning",
  middleModel: "nvidia/nemotron-3-super-120b-a12b",
  highModel: "nvidia/Nemotron-3-Ultra-550b-a55b",
  visionModel: "zai-org/GLM-5.3-Flash",
  routeModel: "nvidia/Nemotron-3_5-Lightning",
  active: personas.map((row) => row.id),
  search: { apiKey: "" },
  speech: {
    provider: "browser",
    language: "auto", silenceSeconds: 3, sendPhrase: "以上",
  },
  readAloudRate: 1.2,
  readAloudVoice: "",
};

export const dictatingSettings = {
  ...settings,
  search: { apiKey: "********" },
  speech: { ...settings.speech, provider: "browser", formatBeforeSend: true, language: "ja" },
};

// What a persona kept, in the shape it chose. The app never reads these; the
// panel only shows them.
export const memory = {
  gourmet: {
    notes: [
      { path: "preferences/sesame.md", text: "type: Preference\n\nごまが前に出る担々麺は苦手。辛さ自体は平気。\n\n根拠:\n- 2026-09-01 「ごまが強すぎた気がする」\n- 2026-09-13 「たしかに。辛いのは平気」\n\n反例: 担々麺以外のごま（胡麻和え）には言及なし。" },
      { path: "places/2026-09.md", text: "type: Place\n\n- 神保町の担々麺（店名不明）。ごま強め。\n- 東京駅周辺で20時前に食べたがっていた（移動の話に混ざっていたので推測）。" },
    ],
  },
  book: {
    notes: [
      { path: "quotes/embodiment.md", text: "type: Quote\n\n「身体の状態が先にあって、感情はあとから名前を付けられる」\n出典の手がかり: 『動きが心をつくる』\n\n同じ章に二度反応している。" },
    ],
  },
};
