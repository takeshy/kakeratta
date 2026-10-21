// Curated bilingual examples of shipped features, not live model responses.
// Use the real feed/compose modules; no production or external API is called.
import { getLanguage } from "../src/i18n.js";
import { feed, panel, compose, sheet } from "./frame.js";
import { serve } from "./api.js";
import { personas, settings, tools } from "./fixtures.js";
import { createPersonas } from "../src/personas.js";
import { showPersonaBuilder } from "../src/persona-builder.js";
import { openSocial } from "../src/social.js";

export default { title: "Gallery" };
const at = minutes => new Date(Date.now() - minutes * 60000).toISOString();
const text = (ja, en) => getLanguage() === "ja" ? ja : en;
function posts(entries) {
  serve({ settings });
  return panel(feed(entries, personas, { getSettings: () => settings }).element);
}

export const Preparation = {
  name: "出発前に持ち物と帰りの雨を確認",
  render: () => posts([
    { id: "prep-plan", root: "prep-plan", kind: "post", speaker: "user", at: at(120),
      text: text("今日は14時に東京へ出発して、18時ごろ帰る。持ち物は鍵と財布。前回は鍵を忘れた。", "Leaving for Tokyo at 14:00, back around 18:00. I need my keys and wallet. I forgot my keys last time.") },
    { id: "prep-check", root: "prep-check", kind: "post", speaker: "routine", at: at(2), scheduled: true,
      text: text("出発前に、鍵と財布を確認しましょう。今は晴れていますが、18時ごろの東京は雨の予報です。帰りに備えて傘もあるとよさそうです。", "Before you leave, check your keys and wallet. It is sunny now, but the forecast for Tokyo around 18:00 calls for rain. An umbrella could help on the way home.") },
  ]),
};

export const PersonaSuggestion = {
  name: "話題が育ったら新しい担当を提案",
  render: () => posts([
    { id: "suggest-recipe", root: "suggest-recipe", kind: "post", speaker: "gourmet", at: at(5),
      text: text("最近レシピの話が増えてきましたね。作りたい料理を覚える「レシピ担当」を作ってみませんか？", "You have been collecting quite a few recipes. Would you like to create a recipe persona to remember the dishes you want to cook?"),
      suggestion: { topic: text("レシピ", "Recipes"), reason: text("作りたい料理の記録が続いている", "Recipe notes keep coming up"), status: "asked" } },
  ]),
};

// The persona "Create it" just added, as the personas panel shows it: its
// icon, name and the definition drafted for the subject.
const recipePersona = () => ({ id: "recipe", name: text("レシピ", "Recipes"), icon: "🍳", active: true, tools: ["web_search"], wakes: 0,
  domain: text("作りたい料理、集めたレシピ、作ってみた感想の話。", "Dishes you want to cook, the recipes you collect, and how they turned out."),
  tone: text("料理好きの友だちのように、気軽に励ます。", "Like a friend who loves cooking: casual and encouraging."),
  memory: text("作りたい料理と、作ってみた結果やコツを覚える。タグは料理の種類と主な材料。", "Remembers dishes you want to try and how each attempt went, with tips. Tags: dish type and main ingredient.") });
export const SuggestedRecipePersona = { name: "提案から作ったレシピ担当", render: () => recipePanel(false) };
// Its definition opened, every drafted field filled in.
export const SuggestedRecipeDefinition = { name: "提案から作ったレシピ担当の定義", render: () => recipePanel(true) };
function recipePanel(edit) {
  {
    for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
    try { localStorage.removeItem("kakeratta.personas.tab"); localStorage.removeItem("kakeratta.personas.order"); } catch { /* nothing kept */ }
    serve({ personas: [recipePersona(), ...personas], tools });
    const view = createPersonas({ onChange: () => {} });
    void view.load().then(() => {
      [...view.element.querySelectorAll(".persona-tab")][1]?.click();
      if (edit) [...view.element.querySelectorAll('.persona[data-id="recipe"] button')].find(button => /^(修正|Edit)$/.test(button.textContent))?.click();
    });
    return sheet("担当", view.element);
  }
}

// "Create it" under the suggestion made this persona: its definition was
// drafted for the subject, and the recipe facts and records were moved from
// tabe's memory word for word.
export const SuggestedRecipeMemory = {
  name: "提案から作ったレシピ担当の記憶",
  render: () => {
    for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
    const recipe = { id: "recipe", name: text("レシピ", "Recipes"), icon: "🍳", active: true, tools: [], wakes: 0,
      domain: text("作りたい料理、集めたレシピ、作ってみた感想の話。", "Dishes you want to cook, the recipes you collect, and how they turned out.") };
    const memo = text(
      "# Memo\n\n## いまの事実\n\n" +
      "- [f_curry] 週末はスパイスカレーを一から作るのが好き from:sample-curry since:2026-10-04\n" +
      "- [f_gyoza] 羽根つき餃子は片栗粉の水溶きでうまくいった from:sample-gyoza since:2026-10-11\n\n" +
      "## かけらの記録\n\n" +
      "- 2026-10-04 18:20 [sample-curry](/?entry=sample-curry) クミンとコリアンダーでチキンカレーを作った #カレー #スパイス\n" +
      "- 2026-10-11 19:05 [sample-gyoza](/?entry=sample-gyoza) 羽根つき餃子を焼いた #餃子\n" +
      "- 2026-10-18 12:40 [sample-bread](/?entry=sample-bread) 今度フォカッチャを焼いてみたい #パン review:2026-11-08/21d\n",
      "# Memo\n\n## いまの事実\n\n" +
      "- [f_curry] Likes making spice curry from scratch at weekends from:sample-curry since:2026-10-04\n" +
      "- [f_gyoza] Crispy-skirt gyoza worked with a cornstarch slurry from:sample-gyoza since:2026-10-11\n\n" +
      "## かけらの記録\n\n" +
      "- 2026-10-04 18:20 [sample-curry](/?entry=sample-curry) Made chicken curry with cumin and coriander #curry #spices\n" +
      "- 2026-10-11 19:05 [sample-gyoza](/?entry=sample-gyoza) Pan-fried crispy-skirt gyoza #gyoza\n" +
      "- 2026-10-18 12:40 [sample-bread](/?entry=sample-bread) Wants to bake focaccia next #bread review:2026-11-08/21d\n");
    serve({ personas: [recipe], tools, memory: { recipe: { notes: [{ path: "Memo.md", text: memo }] } } });
    const view = createPersonas({ onChange: () => {} });
    void view.load().then(() => {
      [...view.element.querySelectorAll("button")].find(button => /覚えて|remember/i.test(button.textContent))?.click();
    });
    return sheet("担当", view.element);
  },
};

export const ReminderDone = {
  name: "リマインダーを済みにする",
  render: () => posts([
    { id: "gallery-reminder", root: "gallery-reminder", kind: "post", speaker: "routine", scheduled: true, at: at(10), doneAt: at(2),
      text: text("明日の出張に向けて、充電器と社員証をかばんに入れましたか？", "Have you packed your charger and work ID for tomorrow’s trip?") },
  ]),
};

export const VoiceCompose = {
  name: "音声入力と読み上げの入口",
  render: () => {
    serve({ settings });
    return panel(compose({ text: text("明日の帰りに、母の誕生日の花を買いたい。", "I want to pick up flowers for Mum’s birthday on the way home tomorrow."), settings }).element);
  },
};

function intent(recipe) {
  for (const stale of document.querySelectorAll('.dialog-backdrop')) stale.remove();
  serve({ settings });
  showPersonaBuilder({ onSave: () => {}, getSettings: () => settings });
  document.querySelector('.persona-builder form .skill-input').value = recipe
    ? text('作りたい料理の担当。集めたレシピと、作ってみた感想を覚えてほしい。', 'A recipe persona. Remember dishes I want to cook, the recipes I collect and what I thought after making them.')
    : text('飲んだワインの担当。ラベルを読み取り、銘柄と好きだった理由を覚えてほしい。', 'A wine persona. Read labels, identify the wine and remember what I liked about each bottle.');
  return panel(document.createElement('div'));
}
export const WineIntent = { name: '自分の言葉でワイン担当を作る', render: () => intent(false) };
export const RecipeIntent = { name: '提案されたレシピ担当を自分で作る', render: () => intent(true) };

// The Shizuoka thread from the travel example, opened in the shipped share dialog.
export const ShareTrip = {
  name: "静岡の会話を選んでBlueskyに公開",
  render: () => {
    document.querySelectorAll(".social-dialog").forEach(dialog => dialog.close());
    const rows = personas.filter(row => row.id === "gourmet");
    const entries = [
      { id: "share-trip", root: "share-trip", kind: "post", speaker: "user",
        text: text("明日静岡に行く", "I'm going to Shizuoka tomorrow.") },
      { id: "share-tabe", root: "share-trip", replyTo: "share-trip", kind: "remark", speaker: "gourmet",
        text: text("明日静岡に行くなら、さわやかはいかが？以前行きたかったけど時間がなくて行けなかったみたいだから、今度こそぜひ。", "If you're going to Shizuoka tomorrow, how about Sawayaka? You wanted to go last time but ran out of time. Maybe this trip is your chance.") },
      { id: "share-user", root: "share-trip", replyTo: "share-tabe", kind: "reply", speaker: "user",
        text: text("いいね、早めに行って並ぶ。", "Good idea. I'll get there early.") },
    ].map((entry, i) => ({ ...entry, at: at(30 - i * 10) }));
    const posts = entries.map(entry => ({ entryId: entry.id, parent: entry.replyTo || "", text: entry.speaker === "user"
      ? entry.text : `${text("", "Reply from ")}🍜 tabe${text("からの返信", "")}:\n${entry.text}` }));
    serve({ settings, social: { rows, entries, posts, root: "share-trip" }, bluesky: { available: true, connected: true, did: "did:plc:sample", handle: "reader.example.com" } });
    const host = panel(feed(entries, rows, { getSettings: () => settings }).element);
    requestAnimationFrame(async () => {
      if (!host.isConnected) return;
      await openSocial("share-trip", id => rows.find(row => row.id === id));
      const check = document.querySelectorAll('.social-dialog .social-selection input[type="checkbox"]')[1];
      check.checked = true;
      check.dispatchEvent(new Event("change", { bubbles: true }));
    });
    return host;
  },
};
