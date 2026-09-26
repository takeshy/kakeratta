import { showConditionFix } from "../src/condition-fix.js";
import { showReplyFix } from "../src/reply-fix.js";
import { serve } from "./api.js";
import { feed, panel } from "./frame.js";
import { conditionDraft, keywordDraft, person, personRouting, personTimeline, personWords, personas, replyDraft, replyPreview, routing, timeline, words } from "./fixtures.js";

export default { title: "Timeline" };

const show = (entries) => panel(feed(entries, personas).element);

// The dialogs live on <body>, outside the story root, so a story that opens
// one starts by taking down what the previous story left.
const clearDialogs = () => { for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove(); };
const fixing = (args) => showConditionFix({ ...args, onSaved: () => {} });

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

function personScene(open, { draft = null } = {}) {
  clearDialogs();
  serve({ personas: people(), routing: personRouting(), personaDraft: draft,
    routingCheck: { answered: true, delivered: true, heard: personWords().added[0], token: "trial_sample" } });
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
