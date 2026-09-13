const STORAGE_KEY = "kakeratta.language";

const EN = {
  "さらに20件読み込む": "Load 20 more posts", "もう一度読み込む": "Try loading again",
  "読み込んでいます…": "Loading…", "読み込めませんでした。もう一度お試しください。": "Could not load posts. Please try again.",
  "画像対応モデルを選んでください。Nebiusでは空欄にすると画像用の標準モデルを使います。": "Choose a model that accepts images. On Nebius, leave blank to use the default vision model.",
  "例：飲んだワインを覚えて、好みに合うワインを一緒に探す担当を作りたい。": "Example: A persona that remembers wines I have tried and helps me find wines I would enjoy.",
  "例：もっと短く返事してほしい。おすすめより、記録を中心にしてほしい。": "Example: Give shorter replies and focus on keeping records rather than recommendations.",
  "例：もう少し親しみやすい話し方にしてほしい。": "Example: Make the tone a little friendlier.",
  "上の質問に答えてください。迷うところは「おまかせ」でも大丈夫です。": "Answer the question above. If you are unsure, you can say “You decide.”",
  "担当すること": "What this persona helps with", "覚えること・返事をするタイミング": "What to remember and when to reply",
  "考え方": "Response style", "できること": "Capabilities", "会話と記憶": "Conversation and memory",
  "すばやく短く答える": "Quick, short answers", "速さと丁寧さのバランス": "A balance of speed and detail", "じっくり考えて答える": "Thoughtful, detailed answers",
  "Webで調べる": "Search the web", "お店や場所を探す": "Find places and restaurants", "行き方を調べる": "Look up directions",
  "AIと担当を作る": "Create a persona with AI", "AIで修正": "Edit with AI",
  "どんなことの担当を作りたい？": "What would you like your persona to help with?", "どんなところを変えたい？": "What would you like to change?",
  "回答や変更したいこと": "Your answer or requested changes", "この内容で保存": "Save this persona",
  "考えています…": "Thinking…", "{0} を保存しました": "Saved {0}.", "{0} を修正": "Edit {0}",
  "担当領域": "Domain", "話し方": "Tone", "記憶と発言のルール": "Memory and response rules",
  "まずはこの内容で使ってみましょう。使ったあとでも「AIで修正」で変えられます。": "Try this version first. You can improve it later with “Edit with AI”.",
  "かけらを放り込む": "Toss in a fragment", "Ctrl（⌘）+ Enter": "Ctrl (⌘) + Enter",
  "キャプチャ": "Capture", "取り消す": "Remove",
  "何か書くか、画像を貼ってください": "Write something or attach an image.",
  "送っています…": "Sending…", "ページを読んでいます…": "Reading the page…",
  "🎙 話す": "🎙 Speak", "聞いています…": "Listening…",
  "無音部分を解析中…（録音は継続）": "Processing the previous phrase… (still recording)",
  "マイクを準備しています…": "Preparing the microphone…", "音声を準備しています…": "Preparing audio…",
  "音声を解析しています…": "Transcribing audio…", "入力を終えるときは「{0}」と言ってください": "Say “{0}” when you are finished",
  "この環境では音声入力が使えません": "Voice input is unavailable in this environment.",
  "📷 画像": "📷 Image", "流す": "Post",
  "ここにかけらが並びます。": "Your fragments line up here.",
  "書く、貼る（Ctrl+V）、URL、話す。担当は黙っていることもあり、反応は遅れて届きます。": "Write, paste (Ctrl+V), send a URL, or speak. A persona may stay quiet, and reactions arrive late. The built-in personas are tabe (food), michi (travel), kuda (tech) and shiori (reading); mention one with @name.",
  "反応が付かないことがあります。付くときも、遅れて届きます。": "Some posts get no reaction. The ones that do arrive later.",
  "消す": "Delete", "この投稿を消しますか": "Delete this post?",
  "読み取り中…": "Reading…", "文字は読み取れませんでした。": "No text could be read.",
  "あなた": "You", "返す": "Reply", "一言足す": "Add a comment",
  "返事や、@名前 で指名": "Reply, or mention @name", "送る": "Send", "やめる": "Cancel", "投稿": "Post",
  "担当が誰もいません。": "Nobody is in the room.",
  "いま放り込んだかけらは、どこにも渡りません。1人呼べば、そこから受け取ります。": "A fragment tossed in now reaches nobody. Call one in and it starts receiving them.",
  "{0} を呼ぶ": "Call in {0}", "担当をえらぶ": "Choose personas",
  "溜まった反応を順に読み上げます": "Reads the collected reactions aloud, in order",
  "押すと @{0} を書き入れます": "Tap to write @{0}",
  "🔈 読み上げ": "🔈 Read aloud", "■ 停止": "■ Stop",
  "担当": "Personas", "設定": "Settings", "閉じる": "Close",
  "共有した内容を保存できませんでした。接続と認証を確認し、もう一度共有してください。": "The shared item could not be saved. Check the connection and authentication, then try again.",
  "担当領域ごとに記憶が分かれています。投稿がどれに渡るかは本体が振り分け、そのうえで喋るかどうかは各担当が決めます。@名前 で呼べば必ず答えます。": "Each persona keeps separate memories for its own domain. The app routes each post, and each persona decides whether to respond. Mention @name to always get a reply.",
  "有効": "Active", "停止中": "Paused", "有効にする": "Enable", "道具": "Tools", "メモを見る": "View memory", "削除": "Delete",
  "low（先に動く）": "low (first)", "high（最後に動く）": "high (last)",
  "起きた回数 {0}": "Woken {0} times", "{0} を削除しますか（メモは残ります）": "Delete {0}? Its memory will be kept.",
  "{0} のメモ": "{0}'s memory", "{0}件。何を残し何を捨てるかは本人の判断で、本体は中身を読みません。": "{0} notes. The persona decides what to keep or discard; the app does not read their contents.",
  "{0} の記憶を消しますか。元に戻せません。": "Erase {0}'s memory? This cannot be undone.",
  "{0} を追加しました": "Added {0}.",
  "（まだ何もありません）": "(Nothing yet)", "忘れさせる": "Forget",
  "担当を追加する（skill.json）": "Add a persona (skill.json)", "追加": "Add",
  "モデル一覧を取得": "Fetch models", "取得できませんでした": "Could not fetch models", "モデル": "Models",
  "接続確認": "Check connection", "接続中…": "Connecting…", "接続できました": "Connected",
  "AIにはNebius Token Factoryを使用します。": "AI is provided by Nebius Token Factory.", "APIキーを取得": "Get an API key",
  "モデルの上級者向け設定": "Advanced model settings", "音声入力の上級者向け設定": "Advanced voice input settings",
  "Web検索の上級者向け設定": "Advanced web search settings", "検索サービス": "Search service",
  "種別": "Type", "なし": "None",
  "Web検索の接続確認": "Check web search connection", "Web検索に接続できました": "Web search connected",
  "Tavily APIキー": "Tavily API key", "Brave Search APIキー": "Brave Search API key",
  "Web検索に使います。AIモデルのAPIキーとは別です": "Used for web search. This is separate from the AI model API key.",
  "最初にAI接続を設定してください。設定が完了するまでこの画面は閉じられません。": "Set up an AI connection first. This screen cannot be closed until setup is complete.",
  "実行場所": "Provider", "サーバー": "Server", "エンドポイント": "Endpoint", "APIキー": "API key",
  "low（先に動く。浅く速い）": "low (first; fast and lightweight)",
  "high（最後に動く。下の発言を見て突っ込める）": "high (last; can build on earlier responses)",
  "画像を読むモデル（vision）": "Image model (vision)", "振り分けに使うモデル": "Routing model",
  "Web検索": "Web search", "使わない": "Disabled",
  "店舗検索・経路検索の接続先": "Place and route service endpoints",
  "地名検索 (Nominatim)": "Place search (Nominatim)", "周辺検索 (Overpass)": "Nearby search (Overpass)", "経路 (OSRM)": "Routes (OSRM)",
  "音声入力": "Voice input", "方式": "Mode", "ブラウザ音声認識": "Browser speech recognition",
  "音声認識API（録音して送る）": "Speech recognition API (record and send)", "ライブ文字起こし": "Live transcription",
  "サービス": "Service", "言語": "Language", "無音で自動停止": "Stop after silence", "しない": "Never",
  "送信の合図": "Send phrase", "読み上げ": "Read aloud", "既定の声": "Default voice", "速さ": "Speed", "声": "Voice",
  "表示言語": "Display language", "この端末": "This device", "保存先": "Storage",
  "あなた専用の記憶へログインします。": "Sign in to your private memory.",
  "Googleでログイン": "Sign in with Google", "ログアウト": "Sign out",
  "ログインセッションを開始できませんでした。": "Could not start a login session.",
  "保存先 {0}": "Storage: {0}",
  "OpenAI 互換": "OpenAI-compatible",
  "担当ごとに low / middle / high を選びます。low が先に動き、その発言を見て上の段が突っ込めます。どのモデルをどの段に置くかが、そのまま体験の差になります。": "Choose low, middle, or high for each persona. Low runs first, and higher tiers can build on earlier responses. How you assign models to tiers directly shapes the experience.",
  "サーバー側にだけ保存され、指定した接続先以外へは送りません": "Stored only on the server and sent only to the configured endpoint.",
  "空にすると1つ下の段に落ちます。全部同じにしてもいいですが、対比は無くなります": "Leave blank to fall back one tier. You may use the same model for every tier, but there will be no contrast.",
  "投稿ごとに一度だけ通し、結果は全員で共有します": "Runs once per post and shares the result with every persona.",
  "宣言した担当だけが呼べます。鍵はここにあり、スキルは持ちません。": "Only personas that declare a tool can call it. Credentials stay here and are never included in skills.",
  "既定は鍵の要らない公開サービスです。自前のインスタンスに向けられます。": "The defaults are public services that need no credentials. You can point these at your own instances.",
  "道路の所要時間です。公共交通は含みません": "Road travel times only; public transit is not included.",
  "Vertex AI の OAuth はデスクトップ前提のため、この形態では選べません": "Vertex AI OAuth requires a desktop flow and is unavailable here.",
  "話し終わりの合図。これで投稿されます。空欄で無効": "A phrase that posts when you finish speaking. Leave blank to disable.",
  "トークンが未設定です。外から届く場所に置くときは KAKERATTA_TOKEN を設定してください。": "No token is configured. Set KAKERATTA_TOKEN before exposing this app externally.",
  "担当領域は本体が読んで振り分けに使います。指示は本人だけが読みます。メモの形式（OKF）は全員に渡るので、指示には「どんな type を使い、いつ統合するか」を書いてください。": "The app reads the domain to route posts; only the persona reads its instructions. Every persona receives the OKF memory format, so describe which types to use and when to consolidate them.",
  "使える道具": "Available tools", "（なし）": "(none)",
  "読み取れませんでした: {0}": "Could not read: {0}", "出典の手がかり: {0}": "Source hint: {0}", "{0}文字": "{0} characters", "読み取り結果": "Reading result",
  "{0}件見つかりました": "Found {0}", "{0}秒": "{0}s",
  "WebView の言語を使用": "Use WebView language", "自動検出": "Auto-detect", "その他（言語コード指定）": "Other (language code)",
  "この環境では読み上げが使えません": "Read-aloud is unavailable in this environment.",
  "日本語の読み上げ音声が端末にありません": "No Japanese text-to-speech voice is installed on this device.",
  "英語の読み上げ音声が端末にありません": "No English text-to-speech voice is installed on this device.",
  "読み上げるものがありません": "There is nothing to read aloud.",
  "このウィンドウはブラウザ音声認識に対応していません。設定で別のサービスを選んでください。": "This window does not support browser speech recognition. Choose another service in Settings.",
  "ネットワークエラーが発生しました。": "A network error occurred.", "マイクの使用が許可されていません。": "Microphone access was denied.",
  "マイクを取得できませんでした。": "Could not access the microphone.", "マイクが見つかりません。": "No microphone was found.",
  "音声が検出されませんでした。": "No speech was detected.", "音声を認識できませんでした。もう一度お試しください。": "Speech could not be recognized. Please try again.",
  "このウィンドウでは録音を利用できません。": "Recording is unavailable in this window.",
  "このウィンドウではライブ音声入力を利用できません。": "Live voice input is unavailable in this window.",
  "中断されました。": "Cancelled.", "無音の検出は利用できません": "Silence detection is unavailable.",
  "録音中にエラーが発生しました。": "An error occurred while recording.", "録音が空です。": "The recording is empty.",
  "録音は5分以内にしてください。": "Recordings must be no longer than five minutes.",
  "保持分を含めた録音は5分以内にしてください。": "The recording, including retained audio, must be no longer than five minutes.",
  "録音サイズの上限 (20MB) を超えました。": "The recording exceeds the 20 MB limit.",
  "言語は auto か BCP-47 (ja / en-US など) で指定してください。": "Use auto or a BCP-47 language code such as ja or en-US.",
};

let language;

function storedLanguage() {
  try {
    const value = globalThis.localStorage?.getItem(STORAGE_KEY);
    return value === "ja" || value === "en" ? value : null;
  } catch { return null; }
}

export function resolveLanguage() {
  const stored = storedLanguage();
  if (stored) return stored;
  const locale = globalThis.navigator?.languages?.[0] || globalThis.navigator?.language || "";
  return String(locale).toLowerCase().split("-")[0] === "ja" ? "ja" : "en";
}

export function getLanguage() { return language || (language = resolveLanguage()); }

export function setLanguage(next, { persist = false } = {}) {
  language = next === "ja" ? "ja" : "en";
  if (persist) {
    try { globalThis.localStorage?.setItem(STORAGE_KEY, language); } catch { /* storage may be unavailable */ }
  }
  return language;
}

export function t(source, ...args) {
  const original = String(source);
  const translated = getLanguage() === "ja" ? original : EN[original] || original;
  return translated.replace(/\{(\d+)\}/g, (token, index) => index < args.length ? String(args[index]) : token);
}
