// What was told about a person while making or changing its persona.
//
// Birthdays, tastes and what happened are not settings: the builder hands them
// back apart from the draft. The user can choose to post them as a fragment
// naming the person, so the persona can make notes from that post.
import { el, toast } from "./dom.js";
import { api } from "./api.js";
import { t } from "./i18n.js";

// factsPost is the text posted for a saved persona, or "" when there is
// nothing to post. The persona is named with @ so it is sure to take it; a
// name with a space cannot follow an @, and the id stands in for it.
export function factsPost(skill, facts) {
  const text = String(facts || "").trim();
  if (!text || skill?.kind !== "person") return "";
  const name = /\s/.test(skill.name || "") ? skill.id : skill.name;
  return `@${name} ${text}`;
}

export async function postFacts(skill, facts, onPosted) {
  const text = factsPost(skill, facts);
  if (!text) return null;
  try {
    const entry = await api.post({ text });
    onPosted?.(entry);
    return entry;
  } catch (error) {
    toast(t("担当は保存しましたが、覚えることを投稿できませんでした：{0}", String(error.message || error)), "error");
    return null;
  }
}

// factsField shows the proposed fragment and asks separately whether to post it.
// It is hidden until a person's draft brings facts.
export function factsField() {
  const box = el("textarea", { class: "skill-input person-facts", rows: 4, maxlength: 2000, "aria-label": t("会話で伝えたこと") });
  const post = el("input", { type: "checkbox" });
  const node = el("div", { class: "persona-field person-facts-field" },
    el("strong", { label: "会話で伝えたこと" }),
    box,
    el("label", { class: "switch" }, post, el("span", { label: "保存時にこの内容をかけらとして投稿する" })),
    el("p", { class: "hint", label: "選ぶと担当宛てのかけらを作り、担当がそこからメモを作ります。選ばない場合、この内容は残りません。" }));
  node.hidden = true;
  return {
    node,
    box,
    post,
    set(draft, facts) {
      const shown = draft?.kind === "person" && !!String(facts || "").trim();
      box.value = shown ? String(facts).trim() : "";
      post.checked = false;
      node.hidden = !shown;
    },
  };
}
