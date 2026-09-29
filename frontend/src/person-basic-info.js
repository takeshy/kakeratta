// User-editable person information saved with the persona definition.
import { el } from "./dom.js";
import { t } from "./i18n.js";

export function basicInfoField() {
  const box = el("textarea", { class: "skill-input person-basic-info", rows: 4, maxlength: 8000, "aria-label": t("基本情報") });
  const node = el("label", { class: "persona-field person-basic-info-field" },
    el("strong", { label: "基本情報" }), box,
    el("p", { class: "hint", label: "誕生日や好みなどを担当の基本情報として保存します。あとから編集できます。" }));
  node.hidden = true;
  return { node, box, set(draft) {
    box.value = draft?.kind === "person" ? String(draft.basicInfo || "") : "";
    node.hidden = draft?.kind !== "person";
  } };
}
