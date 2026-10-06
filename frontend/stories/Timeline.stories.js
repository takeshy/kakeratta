import { sampleAvatar } from "./avatar.js";
import { createEntryFilters, filterConversations } from "../src/entry-filters.js";
import { adoptFavoriteIDs, watchFavorites } from "../src/favorite-state.js";
import { showConditionFix } from "../src/condition-fix.js";
import { showReplyFix } from "../src/reply-fix.js";
import { createSpeaker } from "../src/speaker.js";
import { getLanguage } from "../src/i18n.js";
import { serve } from "./api.js";
import { feed, panel, sheet } from "./frame.js";
import { conditionDraft, keywordDraft, monthSummary, person, personRouting, personTimeline, personWords, personas, replyDraft, replyPreview, routing, settings, timeline, words } from "./fixtures.js";

export default { title: "Timeline" };

const show = (entries, selected = []) => {
  serve({ settings, search: { entries, hits: [] }, favorites: { items: entries.filter(e => selected.includes(e.id)).map(entry => ({ id: entry.id, entry, at: entry.at })) } });
  adoptFavoriteIDs(selected);
  const view = feed(entries, personas);
  let filters = {};
  const draw = () => {
    const shown = filterConversations(entries, filters);
    view.set(shown);
    for (const id of selected) view.reveal(id);
  };
  const controls = createEntryFilters(next => { filters = next; draw(); });
  const host = document.createElement("div"); host.append(controls.element, view.element);
  watchFavorites(() => { if (host.isConnected) draw(); });
  return panel(host);
};

// The dialogs live on <body>, outside the story root, so a story that opens
// one starts by taking down what the previous story left.
const clearDialogs = () => { for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove(); };
const fixing = (args) => showConditionFix({ ...args, onSaved: () => {} });

// A name on the timeline opens what that speaker said, read as the timeline is,
// under the persona's own card from the personas list.
function speakerPanel() {
  const ja = getLanguage() === "ja";
  const sato = person();
  const entries = [
    { id: "wine-1", kind: "post", root: "wine-1", speaker: "user", at: "2026-09-10T10:30:00Z",
      text: ja ? "友人の家で飲んだピノ・ノワール。渋みが少なくて、するっと飲めた。" : "Pinot Noir at a friend's place. Soft tannins, so easy to drink." },
    { id: "wine-2", kind: "post", root: "wine-2", speaker: "user", at: "2026-08-22T11:00:00Z",
      text: ja ? "先週はごちそうになった。" : "Treated to dinner last week." },
    { id: "wine-3", kind: "post", root: "wine-3", speaker: "user", at: "2026-08-03T09:00:00Z",
      text: ja ? "お店で勧めてもらったピノ・ノワール。また頼みたい。" : "The Pinot Noir the restaurant recommended. I'd order it again." },
    { id: "s-1", kind: "remark", root: "wine-1", speaker: sato.id, at: "2026-09-10T10:31:00Z",
      text: ja ? "誕生日が近いですね。好みの赤ワインを一本、贈り物の候補にどうでしょう。" : "Her birthday is close. A red she likes could be a gift." },
    { id: "s-2", kind: "remark", root: "wine-2", speaker: sato.id, at: "2026-08-22T11:02:00Z",
      text: ja ? "先週のお礼をまだ伝えていなければ、一言添えると喜ばれそうです。" : "If you haven't yet thanked her for last week, a word would go down well." },
    { id: "s-3", kind: "remark", root: "wine-3", speaker: sato.id, at: "2026-08-03T09:05:00Z",
      text: ja ? "お店の名前を控えておきましょうか。" : "Shall I note the restaurant's name?" },
  ];
  serve({ settings, search: { entries, hits: [] } });
  const view = createSpeaker({
    getSettings: () => settings, getPersona: (id) => id === sato.id ? sato : null,
    onOpenPersona: () => {}, onOpenMemory: () => {}, onEditPersona: () => {},
  });
  void view.show(sato.id, sato.name).then(() => {
    view.element.querySelectorAll(".basic-info").forEach((details) => { details.open = true; });
  });
  return sheet("発言一覧", view.element);
}

export const Speaker = { name: "名前から発言一覧を開く", render: speakerPanel };

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
// A persona handed to Obsidian's LLM Connect Hub, answering from a file in the
// vault and saying which.
export const ExternalAgentVault = {
  name: "外部エージェント（Obsidian）が答えた",
  render: () => {
    const view = feed(timeline().vault, personas);
    view.element.querySelector(".thread-toggle")?.click();
    return panel(view.element);
  },
};
export const Settled ={ name: "溜まってきた", render: () => show(timeline().many) };
// The router's call about the noodle post: tabe was called, and the reasons
// the other three were not - each with a way to say the router was wrong.
export const RoutingOpened = {
  name: "担当の判断を開いた",
  render: () => {
    clearDialogs();
    serve({ personas, routing: routing() });
    const view = feed(timeline().hero, personas, { onFixCondition: fixing });
    view.element.querySelector('.thread[data-id="p1"] .routing-open')?.click();
    return panel(view.element);
  },
};

const disputed = () => ({
  persona: personas.find((row) => row.id === "move"),
  entry: timeline().hero.find((entry) => entry.id === "p1"),
  direction: "include",
  reason: words().notTravel,
});

// Saying why michi should have been called.
export const ConditionAsk = {
  name: "呼んでほしかった・理由を書く",
  render: () => {
    clearDialogs();
    serve({ personas, routing: routing(), personaDraft: conditionDraft() });
    const host = panel(feed(timeline().hero, personas, { onFixCondition: fixing }).element);
    fixing(disputed());
    document.querySelector(".condition-note").value = words().fixNote;
    return host;
  },
};

// The condition the AI wrote, in the person's hands, with OK to save it and
// have the router try the post again.
export const ConditionProposal = {
  name: "呼んでほしかった・新しい判定条件",
  render: () => {
    clearDialogs();
    serve({ personas, routing: routing(), personaDraft: conditionDraft() });
    const host = panel(feed(timeline().hero, personas, { onFixCondition: fixing }).element);
    fixing(disputed());
    document.querySelector(".condition-note").value = words().fixNote;
    document.querySelector(".condition-build").click();
    return host;
  },
};

// tabe's remark on the noodle post, disputed: how it missed, the tone and
// instructions the AI wrote back, and the reply it would give now.
const disputedReply = () => {
  const entries = timeline().answered;
  return {
    persona: personas.find((row) => row.id === "gourmet"),
    remark: entries.find((entry) => entry.id === "s1"),
    post: entries.find((entry) => entry.id === "p1"),
    onSaved: () => {},
  };
};

function replyFixScene(after) {
  clearDialogs();
  serve({ personas, personaDraft: replyDraft(), replyPreview: replyPreview() });
  const view = feed(timeline().hero, personas, { onFixReply: (args) => showReplyFix({ ...args, onSaved: () => {} }) });
  view.element.querySelector('.thread[data-id="p1"] .thread-toggle')?.click();
  const host = panel(view.element);
  showReplyFix(disputedReply());
  after?.();
  return host;
}

// Waits for a dialog state that arrives after a stubbed fetch, then acts.
const once = (selector, act) => {
  const tick = () => {
    const node = document.querySelector(selector);
    if (node) act(node); else setTimeout(tick, 30);
  };
  tick();
};

export const ReplyFixAsk = {
  name: "返し方を直す・どう違うか書く",
  render: () => replyFixScene(() => { document.querySelector(".reply-note").value = words().replyNote; }),
};

export const ReplyFixProposal = {
  name: "返し方を直す・新しい口調と指示",
  render: () => replyFixScene(() => {
    document.querySelector(".reply-note").value = words().replyNote;
    document.querySelector(".reply-build").click();
  }),
};

export const ReplyFixTrial = {
  name: "返し方を直す・試し書き",
  render: () => replyFixScene(() => {
    document.querySelector(".reply-note").value = words().replyNote;
    document.querySelector(".reply-build").click();
    once(".reply-ok", (ok) => ok.click());
  }),
};

export const Reading = { name: "画像を読み取り中", render: () => show(timeline().reading) };
export const Unreadable = { name: "画像を読めなかった", render: () => show(timeline().unreadable) };

// ---- a person ---------------------------------------------------------------

const people = () => [...personas, person()];

function personScene(open, { draft = null, routingCheck = { answered: true, delivered: true, heard: personWords().added[0], token: "trial_sample" } } = {}) {
  clearDialogs();
  serve({ personas: people(), routing: personRouting(), personaDraft: draft, routingCheck });
  const view = feed(personTimeline(), people(), { onFixCondition: fixing });
  view.element.querySelector(`.thread[data-id="${open}"] .routing-open`)?.click();
  return panel(view.element);
}

// A person is reached by a word, and the sheet says which.
export const PersonRouting = { name: "担当の判断・人物に届いた", render: () => personScene("q1") };

// A post about him in words he is not listed under: asking for him adds
// keywords instead of rewriting a condition.
export const PersonMissed = { name: "担当の判断・人物に届かなかった", render: () => personScene("q2") };

const missedPerson = () => ({
  persona: person(),
  entry: personTimeline().find((entry) => entry.id === "q2"),
  direction: "include",
  reason: personWords().missed,
});

export const PersonKeywordAsk = {
  name: "人物を呼んでほしかった・語を書く",
  render: () => {
    const host = personScene(null, { draft: keywordDraft() });
    fixing(missedPerson());
    document.querySelector(".condition-note").value = personWords().note;
    return host;
  },
};

export const PersonKeywordProposal = {
  name: "人物を呼んでほしかった・足すキーワード",
  render: () => {
    const host = personScene(null, { draft: keywordDraft() });
    fixing(missedPerson());
    document.querySelector(".condition-note").value = personWords().note;
    document.querySelector(".condition-build").click();
    return host;
  },
};

// A post that reached him by a keyword without being about him: the words it
// matched are listed, to remove or keep.
export const PersonWordsRemove = {
  name: "人物を呼んでほしくなかった・外す語",
  render: () => {
    const w = personWords();
    const host = personScene(null, { routingCheck: { answered: true, delivered: true, heard: w.unheard[0], words: w.unheard, token: "trial_sample" } });
    fixing({ persona: person(), entry: personTimeline().find((entry) => entry.id === "q3"), direction: "exclude" });
    return host;
  },
};

// The app's summary of the month, which the user can edit.
export const MonthSummary = { name: "今月のまとめ", render: () => show(monthSummary()) };

export const Filters = { name: "お気に入り・期間で絞り込む", render: () => {
  const entries = timeline().answered;
  const host = show(entries, entries.filter(e => e.kind !== "post").slice(0, 1).map(e => e.id));
  host.querySelector(".entry-filters").open = true;
  return host;
} };

export const FavoritesOnly = { name: "タイムラインのお気に入りのみ", render: () => {
 const entries = timeline().answered;
 const host = show(entries, entries.filter(e => e.kind !== "post").slice(0,1).map(e => e.id));
 const toggle = host.querySelector('input[type="checkbox"]');
 toggle.checked = true; toggle.dispatchEvent(new Event("change"));
 return host;
} };
export const SpeakerFilters = { name: "発言一覧の期間フィルター", render: () => {
 const host = speakerPanel();
 host.querySelector("#sheet .entry-filters").open = true;
 const from = host.querySelector('#sheet input[type="date"]');
 from.value = "2026-08-01"; from.dispatchEvent(new Event("change"));
 return host;
} };

export const ProfilePhotos = {
  name: "表示名と写真アイコン",
  render: () => {
    const current = { ...settings, profile: { name: "佐藤", icon: "🌸", avatar: sampleAvatar("S") } };
    const rows = personas.map(row => ({ ...row, avatar: sampleAvatar(row.name.slice(0, 1).toUpperCase(), "#a88766") }));
    serve({ settings: current });
    return panel(feed(timeline, rows, { getSettings: () => current }).element);
  },
};
