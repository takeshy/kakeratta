import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";

export function showPersonaBuilder({ persona, onSave }) {
  const opener = document.activeElement;
  const messages = [];
  let draft = null;
  let busy = false;
  const backdrop = el("div", { class: "dialog-backdrop" });
  const conversation = el("div", { class: "builder-conversation", role: "log", "aria-live": "polite" });
  const preview = el("div");
  const error = el("p", { class: "hint", role: "alert" });
  const input = el("textarea", { class: "skill-input", rows: 3, maxlength: 12000, "aria-label": t("回答や変更したいこと"), i18nPlaceholder: persona ? "例：もっと短く返事してほしい。おすすめより、記録を中心にしてほしい。" : "例：美味しかったワインのラベルを送るので、銘柄やブドウ品種を教えてほしい。" });
  const send = el("button", { class: "button primary", label: "送る", type: "submit" });
  const save = el("button", { class: "button primary", label: "この内容で保存", hidden: true, onclick: async () => {
    if (busy || !draft) return;
    setBusy(true);
    error.textContent = "";
    try {
      const saved = await api.savePersonaDraft(draft, persona?.id);
      toast(t("{0} を保存しました", saved.name));
      close();
      await onSave?.();
    } catch (e) { error.textContent = e.message; }
    finally { setBusy(false); }
  }});
  function close() { backdrop.remove(); opener?.focus(); }
  function setBusy(value) {
    busy = value;
    input.disabled = value;
    send.disabled = value;
    save.disabled = value;
    send.textContent = t(value ? "考えています…" : "送る");
  }
  function bubble(role, text) {
    conversation.append(el("div", { class: `builder-message ${role}` }, el("strong", { text: role === "user" ? t("あなた") : "AI" }), el("p", { text })));
    conversation.scrollTop = conversation.scrollHeight;
  }
  bubble("assistant", t(persona ? "どんなところを変えたい？" : "どんなことの担当を作りたい？"));
  async function submit(event) {
    event.preventDefault();
    const answer = input.value.trim();
    if (!answer || busy) return;
    setBusy(true);
    error.textContent = "";
    const next = [...messages, { role: "user", content: answer }];
    try {
      const result = await api.buildPersona({ id: persona?.id, language: getLanguage(), messages: next });
      if (!backdrop.isConnected) return;
      messages.push(next.at(-1), { role: "assistant", content: JSON.stringify(result) });
      bubble("user", answer);
      bubble("assistant", result.message);
      input.value = "";
      draft = result.draft;
      input.placeholder = typeof result.placeholder === "string" && result.placeholder.trim()
        ? result.placeholder.trim()
        : t(draft
          ? "例：もう少し親しみやすい話し方にしてほしい。"
          : "上の質問に答えてください。迷うところは「おまかせ」でも大丈夫です。");
      clear(preview);
      save.hidden = !draft;
      if (draft) {
        preview.append(el("h4", { text: `${draft.icon || "◎"} ${draft.name}` }));
        const modelLabels = { low: "すばやく短く答える", middle: "速さと丁寧さのバランス", high: "じっくり考えて答える" };
        const toolLabels = { web_search: "Webで調べる", place_search: "お店や場所を探す", route: "行き方を調べる" };
        for (const [label, value] of [["担当すること", draft.domain], ["話し方", draft.tone], ["覚えること・返事をするタイミング", draft.instructions], ["考え方", t(modelLabels[draft.model])], ["できること", draft.tools?.map(tool => t(toolLabels[tool] || tool)).join(" / ") || t("会話と記憶")]]) {
          preview.append(el("strong", { label }), el("p", { class: "builder-detail", text: value }));
        }
        preview.append(el("p", { class: "hint", label: "まずはこの内容で使ってみましょう。使ったあとでも「AIで修正」で変えられます。" }));
      }
    } catch (e) { error.textContent = e.message; }
    finally { setBusy(false); if (backdrop.isConnected) input.focus(); }
  }
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing) void submit(event);
  });
  const dialog = el("div", { class: "dialog persona-builder", role: "dialog", "aria-modal": "true", "aria-label": t(persona ? "AIで修正" : "AIと担当を作る") },
    el("h3", { text: persona ? t("{0} を修正", persona.name) : t("AIと担当を作る") }),
    conversation, preview, error,
    el("form", { onsubmit: submit }, input, el("div", { class: "compose-row" }, send)),
    el("div", { class: "compose-row" }, el("button", { class: "button ghost", label: "閉じる", onclick: close }), el("span", { class: "spacer" }), save));
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
    if (event.key === "Tab") {
      const focusable = [...dialog.querySelectorAll("button, textarea")].filter(node => !node.disabled && !node.hidden);
      const first = focusable[0], last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    }
  });
  backdrop.append(dialog);
  document.body.append(backdrop);
  input.focus();
}
