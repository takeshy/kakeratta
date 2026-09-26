// What was told about a person while making or changing its persona.
//
// Birthdays, tastes and what happened are not settings: the builder hands them
// back apart from the draft, and saving posts them as a fragment naming the
// person. The persona then takes its ordinary turn on that post, so each fact
// becomes a note that leads back to where it was said, like any other.
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

// factsField is the box that shows them, editable, and says what saving does.
// It is hidden until a person's draft brings facts.
export function factsField() {
  const box = el("textarea", { class: "skill-input person-facts", rows: 4, maxlength: 2000, "aria-label": t("かけらとして残すこと") });
  const node = el("div", { class: "persona-field person-facts-field" },
    el("strong", { label: "かけらとして残すこと" }),
    box,
    el("p", { class: "hint", label: "保存すると、この内容を担当宛てのかけらとして投稿します。担当はそこからメモを作ります。空にすると投稿しません。" }));
  node.hidden = true;
  return {
    node,
    box,
    set(draft, facts) {
      const shown = draft?.kind === "person" && !!String(facts || "").trim();
      box.value = shown ? String(facts).trim() : "";
      node.hidden = !shown;
    },
  };
}
