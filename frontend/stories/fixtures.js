// Fixtures, not mocks: the shapes here are the ones /api/personas and
// /api/timeline actually return, so a story that looks right is evidence.
const minutes = (n) => new Date(Date.now() - n * 60000).toISOString();

export const personas = [
  { id: "gourmet", name: "tabe", icon: "🍜", model: "high", builtin: true, active: true, wakes: 12,
    domain: "食事。店、味の好み、食べた記録。", tools: ["place_search", "web_search"] },
  { id: "move", name: "michi", icon: "🚃", model: "low", builtin: true, active: true, wakes: 3,
    domain: "移動。経路、所要時間、行き先。", tools: ["route", "place_search"] },
  { id: "tech", name: "kuda", icon: "💾", model: "low", builtin: true, active: true, wakes: 7,
    domain: "技術。コード、道具、仕組み。", tools: ["web_search"] },
  { id: "book", name: "shiori", icon: "📖", model: "middle", builtin: true, active: true, wakes: 21,
    domain: "読書。本、引用、気になった一節。", tools: ["web_search"] },
];

export const nobody = personas.map((row) => ({ ...row, active: false }));

const post = (id, at, extra) => ({ id, kind: "post", speaker: "user", at, text: "", ...extra });
const said = (id, root, speaker, at, text, extra) =>
  ({ id, kind: "said", root, speaker, at, text, ...extra });

// One post, nothing said about it. This is the ordinary case, not an error.
export const quiet = [post("p1", minutes(6), { text: "この前の担々麺、ごまが強すぎた気がする" })];

export const answered = [
  post("p1", minutes(140), { text: "この前の担々麺、ごまが強すぎた気がする" }),
  said("s1", "p1", "gourmet", minutes(128),
    "ごま強めは前も一度そう言っていました。辛さより先にごまが来る店が苦手なのかもしれません。"),
  said("s2", "p1", "user", minutes(120), "たしかに。辛いのは平気"),
  post("p2", minutes(52), {
    text: "@michi 20時に渋谷に着きたい",
    place: { lat: 35.681, lon: 139.767, label: "東京駅" },
  }),
  said("s3", "p2", "move", minutes(50),
    "いまの時間なら山手線で30分ほど。19時台は混むので、余裕を見て19時15分に出ると確実です。", {
      sources: [{ label: "経路 (OSRM)", url: "https://example.invalid/route" }],
    }),
  post("p3", minutes(20), {
    text: "",
    media: {
      blobId: "b1", mime: "image/png", readAt: minutes(19),
      describe: "本のページの写真",
      origin: "『動きが心をつくる』らしい",
      text: "身体の状態が先にあって、感情はあとから名前を付けられる。\n——という主張の節。",
    },
  }),
  said("s4", "p3", "book", minutes(17),
    "この節は前に撮ったページと同じ章です。同じ主張に二度反応しているので、覚えておきます。"),
];

// What the README leads with: one exchange and one mention, nothing cut off.
export const hero = answered.filter((entry) => ["p1", "p2"].includes(entry.kind === "post" ? entry.id : entry.root));

export const many = [
  ...answered,
  post("p4", minutes(8), { link: "https://example.invalid/article", text: "" }),
  post("p5", minutes(3), { text: "あとで読む" }),
];

export const reading = [
  post("p6", minutes(1), {
    media: { blobId: "b2", mime: "image/png" },
  }),
];

export const unreadable = [
  post("p7", minutes(2), {
    media: { blobId: "b3", mime: "image/png", readAt: minutes(1), error: "画像を読めるモデルが設定されていません" },
  }),
];

// ---- what the panels read ---------------------------------------------------

export const tools = ["web_search", "place_search", "route"];

export const about = {
  version: "story",
  workspace: "~/.config/kakeratta",
  providers: [
    { id: "nebius", label: "Nebius Token Factory", endpoint: "https://api.tokenfactory.nebius.com/v1" },
    { id: "openai-compatible", label: "OpenAI 互換", endpoint: "https://api.openai.com/v1" },
    { id: "local", label: "ローカルLLM", endpoint: "http://127.0.0.1:11434/v1" },
  ],
  frameworks: {
    ollama: { label: "Ollama", endpoint: "http://127.0.0.1:11434/v1" },
    "lm-studio": { label: "LM Studio", endpoint: "http://127.0.0.1:1234/v1" },
    opencode: { label: "OpenCode", endpoint: "http://127.0.0.1:4096/v1" },
  },
  search: { tavily: { label: "Tavily", baseUrl: "https://api.tavily.com" } },
  tools,
  needsToken: true,
};

export const settings = {
  provider: "nebius",
  localFramework: "ollama",
  baseUrl: "https://api.tokenfactory.nebius.com/v1",
  apiKey: "********",
  lowModel: "nvidia/NVIDIA-Nemotron-Nano-9B-v2",
  middleModel: "nvidia/Llama-3_3-Nemotron-Super-49B-v1_5",
  highModel: "",
  visionModel: "nvidia/NVIDIA-Nemotron-Nano-12B-v2-VL",
  routeModel: "nvidia/NVIDIA-Nemotron-Nano-9B-v2",
  active: personas.map((row) => row.id),
  search: { provider: "", baseUrl: "", apiKey: "" },
  places: {
    nominatimUrl: "https://nominatim.openstreetmap.org",
    overpassUrl: "https://overpass-api.de/api/interpreter",
    routeUrl: "https://router.project-osrm.org",
  },
  speech: {
    provider: "browser", endpointType: "openai", baseUrl: "", apiKey: "", model: "",
    language: "auto", silenceSeconds: 3, sendPhrase: "以上",
  },
  readAloudRate: 1,
  readAloudVoice: "",
  keepPlace: true,
};

export const localSettings = {
  ...settings, provider: "local", localFramework: "ollama", baseUrl: "http://127.0.0.1:11434/v1", apiKey: "",
  lowModel: "gemma3:4b", middleModel: "qwen3:14b", highModel: "qwen3:32b", visionModel: "gemma3:4b", routeModel: "gemma3:4b",
};

export const dictatingSettings = {
  ...settings,
  search: { provider: "tavily", baseUrl: "https://api.tavily.com", apiKey: "********" },
  speech: { ...settings.speech, provider: "openai-compatible", endpointType: "whisper-cpp", baseUrl: "http://127.0.0.1:8080/v1", model: "whisper-1", language: "ja" },
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
