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

// basicInfoBox shows a person's basic information: what the user saved with the
// definition, which is not a note and is never posted. It is on the card and
// above the notes, with a way to edit it from either.
export function basicInfoBox(row, edit, { open = false } = {}) {
  const info = String(row.basicInfo || "").trim();
  return el("details", { class: "basic-info", open },
    el("summary", { label: "基本情報" }),
    el("p", { class: "basic-info-text", text: info || t("まだ登録されていません。") }),
    el("button", { class: "chip ghost", label: "基本情報を編集", onclick: edit }));
}
