// Fixtures, not mocks: the shapes here are the ones /api/personas and
// /api/timeline actually return, so a story that looks right is evidence.
import { getLanguage } from "../src/i18n.js";

const minutes = (n) => new Date(Date.now() - n * 60000).toISOString();

// michi is a sample user-created travel persona; it no longer ships with the app.
export const personas = [
  { id: "support", name: "support", icon: "🛟", builtin: true, active: true, wakes: 0, planner: "builtin",
    domain: "kakerattaの使い方・仕様・設定・困りごと。", tools: ["kakeratta_help"] },
  { id: "gourmet", name: "tabe", icon: "🍜", builtin: true, active: true, wakes: 12, planner: "builtin",
    domain: "食事。店、味の好み、食べた記録。", tools: ["place_search", "web_search"],
    tone: "丁寧で、少し長め。おすすめを添える。", instructions: "食べた店と味の感想を覚える。似た店を三つまで勧める。" },
  { id: "move", name: "michi", icon: "🚃", builtin: false, active: true, wakes: 3, planner: "builtin",
    domain: "移動や外出先の場所。周辺の店や施設、行き先。", tools: ["place_search"] },
  { id: "book", name: "shiori", icon: "📖", builtin: true, active: true, wakes: 21, planner: "builtin",
    domain: "読書。本、引用、気になった一節。", tools: ["web_search"] },
  { id: "routine", name: "sona", icon: "🔁", builtin: true, active: true, wakes: 7, planner: "builtin",
    domain: "外出前の備え。持ち物、忘れ物、予定と天気。繰り返しも、一度きりの用も。", tools: ["schedule", "weather_forecast"], dailyCheckIn: { enabled: true, talk: "share" },
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
    shibuya: "@michi 渋谷駅の近くで待ち合わせできるカフェを探したい",
    station: "新宿駅",
    placeSuggestion: "渋谷駅の近くに、待ち合わせに使えそうなカフェが見つかりました。営業時間や住所を確認して選べます。",
    placeSource: "場所検索 (OpenStreetMap)",
    pagePhoto: "本のページの写真",
    origin: "『動きが心をつくる』らしい",
    ocr: "身体の状態が先にあって、感情はあとから名前を付けられる。\n——という主張の節。",
    sameChapter: "この節は前に撮ったページと同じ章です。同じ主張に二度反応しているので、覚えておきます。",
    later: "あとで読む",
    noVision: "画像を読めるモデルが設定されていません",
    mentioning: "@tabe 今日の昼、どこがいいと思う",
    medicine: "朝の薬",
    flowers: "仕事帰りに花を買う",
    notTravel: "移動や場所についての話ではありません。",
    notBook: "本や読書に関する内容ではありません。",
    notRoutine: "繰り返しの用事や、あとで確かめる予定ではありません。",
    fixNote: "店の場所や行き方の話もしているので、michi にも見てほしい。",
    fixedDomain: "移動。経路、所要時間、行き先。または、飲食店の場所や行き方の話。",
    replyNote: "おすすめの店は要らない。前に食べた店との違いを一言で言ってほしい。",
    newTone: "短く一言。おすすめは頼まれたときだけ。",
    newInstructions: "食べた店と味の感想を覚える。新しい感想が来たら、前に食べた店との違いを一言で返す。店は頼まれたときだけ勧める。",
    trialReply: "前の担々麺より、ごまが強くて辛さが後ろに下がった感じですね。",
    agentAsk: "LLMの本であるIntroduction to Agentsの要約を教えて",
    agentSummary: "『Introduction to Agents and Agent architectures』（2025年11月）の要約です。\nこの本は、AIを「質問に答えたり文章を生成したりするモデル」から、**目標に向かって計画し、ツールを使い、結果を観察しながら仕事を進めるエージェント**へと捉え直します。\n\n- **基本ループ**：目標を受け取る → 状況を調べる → 計画する → ツールで実行する → 結果を観察・評価する → 必要なら計画を修正する\n- **基本構成**：推論を担う **Model**、外界に働きかける **Tools**、処理の流れを制御する **Orchestration layer**、実行環境などの **Runtime services**\n- **重要な考え方**：エージェントの性能は、モデルそのものだけでなく、毎回どの情報を文脈として選び、組み合わせるかという **context engineering** に大きく左右される\n- **実運用上の課題**：評価、メモリ、権限管理、プロンプトインジェクション対策、人間による監督が不可欠\n\n出典：[[resource/Introduction to Agents.pdf]]",
  },
  en: {
    noodles: "That dan dan noodle place from the other day - too much sesame, I think",
    sesame: "You said the same about heavy sesame once before. It may be places where the sesame lands before the heat that don't suit you.",
    heat: "True. The heat itself is fine.",
    shibuya: "@michi Find a cafe near Shibuya Station where we can meet",
    station: "Shinjuku Station",
    placeSuggestion: "There are cafes near Shibuya Station that could work for meeting up. Check their opening hours and addresses before choosing.",
    placeSource: "Place search (OpenStreetMap)",
    pagePhoto: "A photo of a book page",
    origin: "Looks like \"How the Body Shapes the Mind\"",
    ocr: "The body's state comes first; the feeling gets its name afterwards.\n- the section making that claim.",
    sameChapter: "Same chapter as the page you captured before. Twice now on the same claim - noting that.",
    later: "Read later",
    noVision: "No vision model is configured",
    mentioning: "@tabe where should I have lunch today",
    medicine: "morning medicine",
    flowers: "flowers on the way home",
    notTravel: "Not about getting somewhere or a place.",
    notBook: "Not about a book or reading.",
    notRoutine: "Not a recurring chore or something to check on later.",
    fixNote: "It is also about where the place is and how to get there, so michi should see it.",
    fixedDomain: "Getting around: routes, travel time, destinations. Or where a restaurant is and how to get there.",
    replyNote: "I don't want recommendations. Say in a line how this differs from the place I ate at before.",
    newTone: "One short line. Recommendations only when asked.",
    newInstructions: "Remember the places eaten at and what they tasted like. When a new impression comes in, say in one line how it differs from a place before. Recommend only when asked.",
    trialReply: "Heavier on sesame than the last dan dan noodles, with the heat sitting further back.",
    agentAsk: "Summarise the LLM book Introduction to Agents for me",
    agentSummary: "A summary of \"Introduction to Agents and Agent architectures\" (November 2025).\nThe book recasts AI from \"a model that answers questions and writes text\" into **an agent that plans toward a goal, uses tools, and works while watching the results**.\n\n- **The core loop**: take a goal → look at the situation → plan → act with tools → observe and judge the result → revise the plan if needed\n- **The parts**: a **Model** that reasons, **Tools** that act on the world, an **Orchestration layer** that runs the flow, and **Runtime services** such as the execution environment\n- **The key idea**: an agent's quality depends not only on the model but on **context engineering** - which information is chosen and combined as context each time\n- **In production**: evaluation, memory, permissions, defence against prompt injection, and human oversight are essential\n\nSource: [[resource/Introduction to Agents.pdf]]",
  },
};

// The persona builder's answer when asked, from one of tabe's remarks, to
// change how it talks and what it does - and the reply it would then give.
export function replyDraft() {
  const w = words();
  const gourmet = personas.find((row) => row.id === "gourmet");
  return { message: "", placeholder: "", draft: { ...gourmet, tone: w.newTone, instructions: w.newInstructions } };
}

export function replyPreview() {
  // The token is what /reply/preview returns for a draft that can be kept;
  // without one the dialog offers no "これでよい".
  return { spoke: words().trialReply, quiet: false, notes: ["Preference.md"], token: "trial_sample" };
}

// What the router decided about the sample posts, as /api/entry/{id}/routing
// returns it: who it chose, whom it passed over and its reason for each.
export function routing() {
  const w = words();
  return {
    p1: {
      recorded: true,
      delivered: [{ id: "gourmet", name: "tabe", icon: "🍜", done: true, spoke: true, noted: true, notes: ["Preference.md"] }],
      excluded: [
        { id: "book", name: "shiori", icon: "📖", reason: w.notBook },
        { id: "move", name: "michi", icon: "🚃", reason: w.notTravel },
        { id: "routine", name: "sona", icon: "🔁", reason: w.notRoutine },
      ],
    },
  };
}

// The persona builder's answer when asked, from the router's call, to widen
// michi's condition to the noodle post.
export function conditionDraft() {
  const w = words();
  const move = personas.find((row) => row.id === "move");
  return { message: "", placeholder: "", draft: { ...move, domain: w.fixedDomain } };
}

export const words = () => WORDS[getLanguage()] || WORDS.en;

// ---- a person ---------------------------------------------------------------

const PERSON = {
  ja: {
    name: "佐藤さん", aliases: ["佐藤", "サトウ", "健二", "けんじ"], keywords: ["キャンプ", "キャンプ場", "テント", "焚き火"],
    domain: "友人。",
    tone: "",
    memory: "",
    instructions: "仕事の相談は結論から話す。",
    souvenir: "佐藤くんから北海道のお土産もらった。白い恋人",
    remembered: "前に会ったとき、夏は家族で北海道に行くと言っていました。お土産のこと、覚えておきます。",
    fishing: "週末は海釣りに行くらしい",
    note: "「釣り」の話はだいたい佐藤さんのこと。",
    added: ["海釣り", "釣り", "つり", "釣行"],
    missed: "呼び名・キーワードが投稿にありませんでした",
    notFood: "食べものや飲食店の話ではありません。",
    campsite: "キャンプ場の予約サイトがまた落ちてる",
    unheard: ["キャンプ", "キャンプ場"],
    builderMessage: "佐藤さんの担当の案を作りました。呼び名の書き方の違いと、キャンプまわりの言い換えも入れています。使ってからAIで修正できます。",
    builderRequest: "友人の佐藤健二さん。11月10日生まれで最近キャンプを始めた。2026年10月8日15時に一緒に食事する予定。",
    basicInfo: "11月10日生まれ。最近キャンプを始めた。2026年10月8日15時に一緒に食事する予定。",
  },
  en: {
    name: "Sato", aliases: ["Kenji", "佐藤", "さとう"], keywords: ["camping", "campsite", "tent", "campfire"],
    domain: "A friend.",
    tone: "",
    memory: "",
    instructions: "Get to the point on work matters.",
    souvenir: "Got a souvenir from Hokkaido from Sato",
    remembered: "Last time you met he said the family was going to Hokkaido this summer. I'll remember the souvenir.",
    fishing: "Apparently going sea fishing this weekend",
    note: "Talk about fishing is usually about Sato.",
    added: ["sea fishing", "fishing", "angling"],
    missed: "None of the names or keywords were in the post",
    notFood: "Not about food or a place to eat.",
    campsite: "The campsite booking site is down again",
    unheard: ["campsite"],
    builderMessage: "Here is a persona for Sato, with other spellings of his name and near words for camping. Try it first and refine it later with AI.",
    builderRequest: "My friend Kenji Sato was born on November 10 and recently started camping. We are meeting for a meal on October 8, 2026 at 15:00.",
    basicInfo: "Born on November 10. Recently started camping. Meeting for a meal on October 8, 2026 at 15:00.",
  },
};

export const personWords = () => PERSON[getLanguage()] || PERSON.en;

// A person's persona, as /api/personas returns it.
export function person() {
  const w = personWords();
  return { id: "sato", name: w.name, icon: "🧑", builtin: false, active: true, wakes: 5, planner: "builtin", kind: "person",
    aliases: w.aliases, keywords: w.keywords, domain: w.domain, tone: w.tone, memory: w.memory, instructions: w.instructions, basicInfo: w.basicInfo,
    tools: ["web_search", "place_search", "schedule"], dailyCheckIn: { enabled: true, talk: "share" } };
}

// Confirmed dates shown on the person's card, independent of memory notes.
export function personCalendar() {
  const ja = getLanguage() === "ja";
  const event = { key: "birthday", name: ja ? "誕生日" : "Birthday", source: "basicInfo", annual: true, month: 11, day: 10 };
  return { ...person(), schedules: [
    { id: "sample-birthday-week", at: "2026-11-03T09:00:00+09:00", timeZone: "Asia/Tokyo", note: ja ? "誕生日（1週間前の通知）" : "Birthday (one week before)", personEvent: { ...event, leadDays: 7 } },
    { id: "sample-birthday-day", at: "2026-11-10T09:00:00+09:00", timeZone: "Asia/Tokyo", note: ja ? "誕生日（当日の通知）" : "Birthday (on the day)", personEvent: { ...event, leadDays: 0 } },
    { id: "sample-meeting", at: "2026-10-08T09:00:00+09:00", timeZone: "Asia/Tokyo", note: ja ? "10月8日15時に一緒に食事" : "Meal together on Oct 8 at 15:00", personEvent: { key: "meal", name: ja ? "食事" : "Meal", source: "basicInfo", annual: false } },
  ] };
}

// Three posts: one that reached the person by name, one that said something
// about him in words he was not listed under, and one that reached him by a
// keyword without being about him.
export function personTimeline() {
  const w = personWords();
  return [
    post("q1", minutes(90), { text: w.souvenir }),
    said("r1", "q1", "sato", minutes(88), w.remembered, { notes: ["Event.md"] }),
    post("q2", minutes(12), { text: w.fishing }),
    post("q3", minutes(4), { text: w.campsite }),
  ];
}

export function personRouting() {
  const w = personWords();
  const { name, icon } = person();
  return {
    q1: { recorded: true,
      delivered: [{ id: "sato", name, icon, person: true, heard: name, done: true, spoke: true, noted: true, notes: ["Event.md"] }],
      excluded: [{ id: "gourmet", name: "tabe", icon: "🍜", reason: w.notFood }] },
    q2: { recorded: true, delivered: [],
      excluded: [{ id: "sato", name, icon, person: true, reason: w.missed }] },
    q3: { recorded: true,
      delivered: [{ id: "sato", name, icon, person: true, heard: w.unheard[0], done: true, spoke: false, noted: false }],
      excluded: [] },
  };
}

// The persona builder's answer when asked to make the post reach the person.
export function keywordDraft() {
  const current = person();
  return { message: "", placeholder: "", draft: { ...current, keywords: [...current.keywords, ...personWords().added] } };
}

// Built at render time, so the story follows the language toolbar.
export function timeline() {
  const w = words();
  // One post, nothing said about it. This is the ordinary case, not an error.
  const quiet = [post("p1", minutes(6), { text: w.noodles })];
  const answered = [
    post("p1", minutes(140), { text: w.noodles }),
    said("s1", "p1", "gourmet", minutes(128), w.sesame, { notes: ["Preference.md"] }),
    said("s2", "p1", "user", minutes(120), w.heat),
    post("p2", minutes(52), { text: w.shibuya, place: { lat: 35.6896, lon: 139.7006, label: w.station } }),
    said("s3", "p2", "move", minutes(50), w.placeSuggestion, {
      sources: [{ label: w.placeSource, url: "https://example.invalid/place" }],
    }),
    post("p3", minutes(20), {
      text: "",
      media: { blobId: "b1", mime: "image/png", readAt: minutes(19), describe: w.pagePhoto, origin: w.origin, text: w.ocr },
    }),
    said("s4", "p3", "book", minutes(17), w.sameChapter, { notes: ["Book.md"] }),
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
  // shiori handed to LLM Connect Hub: the answer comes from Obsidian, and the
  // file it read in the vault is named as the source.
  const vault = [
    post("p8", minutes(7), { text: w.agentAsk }),
    said("a1", "p8", "book", minutes(6), w.agentSummary, { model: "agent", notes: ["Book.md"] }),
  ];
  return { quiet, answered, hero, many, reading, unreadable, vault };
}

// ---- what the panels read ---------------------------------------------------

export const tools = ["web_search", "place_search"];

export const about = {
  version: "story",
  workspace: "~/.config/kakeratta",
  providers: [
    { id: "nebius", label: "Nebius Token Factory", endpoint: "https://api.tokenfactory.nebius.com/v1" },
  ],
  tools,
  mcpServers: [],
};

export const settings = {
  provider: "nebius",
  baseUrl: "https://api.tokenfactory.nebius.com/v1",
  apiKey: "********",
  lowModel: "nvidia/Nemotron-3_5-Lightning",
  middleModel: "nvidia/Nemotron-3.5-Super-VL",
  highModel: "nvidia/Nemotron-3-Ultra-550b-a55b",
  visionModel: "zai-org/GLM-5.3-Flash",
  jevEnabled: false,
  openRouterApiKey: "",
  openRouterMode: false,
  openRouterRouterModel: "nvidia/nemotron-3.5-super-vl",
  openRouterHighModel: "nvidia/nemotron-3-ultra-550b-a55b",
  openRouterMiddleModel: "nvidia/nemotron-3.5-super-vl",
  openRouterLowModel: "nvidia/nemotron-3.5-lightning",
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

// What a persona keeps: one summary per field, and a line per fragment by
// month, with the post to go back to and its tags.
export const memory = {
  gourmet: {
    notes: [
      { path: "Preference.md", text: "# Preference\n\n## まとめ（2026-09-14）\n\n辛さは平気だが、ごまが前に出る担々麺は苦手。胡麻和えなど、担々麺以外のごまには触れていない。\n\n## いまの事実\n\n- [f_k2m9qa] 辛いのは平気 from:s2 since:2026-09-13\n- [f_p4r8tz] 担々麺のごまが強すぎると苦手 from:p1 since:2026-09-01\n\n## 以前のこと\n\n- [f_c1d5ew] 辛いのは苦手 from:p0 since:2026-08-20 until:2026-09-13 by:f_k2m9qa\n" },
      { path: "Shop.md", text: "# Shop\n\n## いまの事実\n\n- [f_w7x1bc] 神保町の担々麺（店名不明）はごま強めだった from:p1 since:2026-09-01\n\n## 以前のこと\n\n- [f_g9h2kr] 神保町の担々麺に行ってみたい from:p0 since:2026-08-25 until:2026-09-01\n" },
      { path: "records/2026-09.md", text: "# 2026-09 のかけら\n\n- 2026-09-01 20:14 [p1](/?entry=p1) 神保町の担々麺はごまが強すぎた気がすると言った #担々麺 #ごま #神保町\n- 2026-09-12 18:40 [s3](/?entry=s3) 東京駅の近くで20時前に食べられる店を聞いた #東京駅 #夕食 expires:2026-09-13/1d\n- 2026-09-13 21:05 [s2](/?entry=s2) 辛いのは平気だと言った #担々麺 #辛さ\n" },
    ],
  },
  book: {
    notes: [
      { path: "Book.md", text: "# Book\n\n## まとめ（2026-09-21）\n\n『動きが心をつくる』の、身体が先で感情はあとという一節に引かれている。\n\n## いまの事実\n\n- [f_b3n6yd] 『動きが心をつくる』の「身体の状態が先にあって、感情はあとから名前を付けられる」に線を引いた。同じ章に二度反応している from:s4 since:2026-09-20\n\n## 以前のこと\n\n- [f_m3q7vs] 『動きが心をつくる』を読みたい from:s0 since:2026-09-05 until:2026-09-20 by:f_b3n6yd\n" },
      { path: "records/2026-09.md", text: "# 2026-09 のかけら\n\n- 2026-09-20 22:10 [s4](/?entry=s4) 『動きが心をつくる』の身体が先という一節に線を引いた #身体論 #動きが心をつくる review:2026-12-20/91d\n" },
    ],
  },
};

// sona's scheduled reminders: one already marked done, one still open.
export function reminders() {
  const ja = getLanguage() === "ja";
  return [
    post("r1", minutes(300), { speaker: "routine", root: "r1", scheduled: true, doneAt: minutes(280),
      text: ja ? "そういえば朝の薬。" : "By the way, your morning medicine." }),
    post("r2", minutes(15), { speaker: "routine", root: "r2", scheduled: true,
      text: ja ? "仕事帰りに花、買えそう?" : "Can you pick up flowers on the way home?" }),
  ];
}

// The month's summary the app posts, in the reader's language.
export function monthSummary() {
  const ja = getLanguage() === "ja";
  const text = ja
    ? "### 人\n- 佐藤さんが週末に海釣りへ。キャンプも始めたらしい [→](/?entry=q2)\n- 青木先生の発表会は11月3日 [→](/?entry=q4)\n\n### 食\n- 担々麺の店を2軒。辛さ控えめの方が好み [→](/?entry=q5)\n\n### これからのこと\n- 母の誕生日（11月20日）の贈り物をまだ決めていない [→](/?entry=q6)"
    : "### People\n- Sato went sea fishing at the weekend, and has taken up camping [→](/?entry=q2)\n- Ms Aoki's recital is on 3 November [→](/?entry=q4)\n\n### Food\n- Two dandan noodle places; the milder one was the favourite [→](/?entry=q5)\n\n### Still open\n- No present chosen yet for Mum's birthday (20 November) [→](/?entry=q6)";
  return [{ id: "m1", kind: "post", speaker: "kakeratta", summary: "month", root: "m1", at: minutes(30), text }];
}
