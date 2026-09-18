import { showConditionFix } from "../src/condition-fix.js";
import { serve } from "./api.js";
import { feed, panel } from "./frame.js";
import { conditionDraft, personas, routing, timeline, words } from "./fixtures.js";

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

export const Reading = { name: "画像を読み取り中", render: () => show(timeline().reading) };
export const Unreadable = { name: "画像を読めなかった", render: () => show(timeline().unreadable) };
