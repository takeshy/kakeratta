import { el, dialogCloseButton, mountOverlay, unmountOverlay, when, toast } from "./dom.js";
import { api } from "./api.js";
import { t } from "./i18n.js";
import { renderMarkdown } from "./markdown.js";

export function gmailImportCard(entry, onResult) {
  const source = entry.import || {};
  const calendar = source.type === "calendar";
  const sourceUrl = source.url || source.sourceUrl;
  return el("section", { class: "gmail-import-review import-card" },
    el("span", { class: "model-badge", label: calendar ? "Calendarからの取り込み待ち" : "Gmailからの取り込み待ち" }),
    el("h3", { text: source.subject || t(calendar ? "予定の確認" : "メールの確認") }),
    el("p", { class: "hint", text: `${source.from || (calendar ? "Google Calendar" : "Gmail")} · ${when(entry.at)}` }),
    sourceUrl ? el("p", { class: "hint" }, el("a", { href: sourceUrl, target: "_blank", rel: "noopener noreferrer", text: calendar ? t("元の予定（Google カレンダー）を開く ↗") : t("元のメール（Gmail）を開く ↗") })) : null,
    el("p", { class: "hint", label: "取り込むまで担当の処理・記憶・検索の対象になりません。" }),
    el("button", { class: "button", label: calendar ? "予定を確認する" : "メールを確認する", onclick: () => showGmailImport(entry, onResult) }));
}

export function showGmailImport(entry, onResult) {
  const calendar = entry.import?.type === "calendar";
  const sourceUrl = entry.import?.url || entry.import?.sourceUrl;
  const previous = document.activeElement;
  const backdrop = el("div", { class: "dialog-backdrop" });
  let saving = false, open = true;
  const close = () => { if (!open || saving) return; open = false; unmountOverlay(backdrop); previous?.focus?.(); };
  const exclude = el("input", { type: "checkbox" });
  const excludeSender = el("input", { type: "checkbox" });
  excludeSender.disabled = !entry.import?.sender;
  exclude.disabled = calendar ? !entry.import?.eventId : !entry.import?.categoryId;
  const type = entry.import?.categoryLabel || t("未分類");
  const errorView = el("p", { role: "alert" });
  const choose = async accept => {
    saving = true; for (const button of dialog.querySelectorAll("button")) button.disabled = true;
    try {
      const result = await api.decideGmailImport(entry.id, accept, !accept && !calendar && exclude.checked, !accept && !calendar && excludeSender.checked, !accept && calendar && exclude.checked);
      saving = false; close(); onResult(result);
    } catch (error) {
      saving = false; errorView.textContent = error.message;
      for (const button of dialog.querySelectorAll("button")) button.disabled = false;
      toast(error.message);
    }
  };
  const dialog = el("section", { class: "dialog gmail-import-dialog", role: "dialog", "aria-modal": "true", "aria-label": t(calendar ? "予定の取り込みを確認" : "メールの取り込みを確認") },
    dialogCloseButton(close), el("h2", { text: entry.import?.subject || t(calendar ? "予定の確認" : "メールの確認") }),
    el("p", { class: "hint", label: calendar ? "予定の内容は外部の情報です。取り込むと通常の投稿として担当が判断します。" : "メール本文は外部の情報です。取り込むと通常の投稿として担当が判断します。" }),
    sourceUrl ? el("p", { class: "hint" }, el("a", { href: sourceUrl, target: "_blank", rel: "noopener noreferrer", text: calendar ? t("元の予定（Google カレンダー）を開く ↗") : t("元のメール（Gmail）を開く ↗") })) : null,
    calendar ? el("div", { class: "gmail-import-body markdown-body" }, renderMarkdown(entry.text)) : el("pre", { class: "gmail-import-body", text: entry.text }),
    el("label", { class: "import-learning" }, exclude, el("span", { text: calendar ? t(entry.import?.seriesId ? "今後この定期予定は取り込まない" : "今後この予定は取り込まない") : t("今後この種類のメールは取り込まない: {0}", type) })),
    calendar ? null : el("label", { class: "import-learning" }, excludeSender, el("span", { text: t("今後このFromからのメールは取り込まない: {0}", entry.import?.sender || t("送信元不明")) })),
    el("p", { class: "hint", label: calendar ? "予定の除外はLLMへ渡す前にIDで適用し、設定画面で解除できます。定期予定は同じシリーズ全体を除外します。" : "送信元の除外はLLMへ渡す前に完全一致で適用し、設定画面で解除できます。" }),
    el("p", { class: "hint", label: "チェックは「取り込まない」を選んだときに保存し、次回以降の判定へ反映します。" }),
    errorView, el("div", { class: "compose-row" }, el("button", { class: "button ghost", label: "取り込まない", onclick: () => choose(false) }), el("button", { class: "button primary", label: "取り込む", onclick: () => choose(true) })));
  backdrop.append(dialog);
  backdrop.addEventListener("click", event => { if (event.target === backdrop) close(); });
  backdrop.addEventListener("keydown", event => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
    if (event.key === "Tab") {
      const focusable = [...dialog.querySelectorAll("button, input")].filter(n => !n.disabled);
      const first = focusable[0], last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    }
  });
  mountOverlay(backdrop); dialog.setAttribute("tabindex", "-1"); dialog.focus?.();
  return { close };
}
