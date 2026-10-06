import { switchControl, el, clear, toast, dialogCloseButton, mountOverlay, unmountOverlay } from "./dom.js";
import { createVoiceInput } from "./voice_input.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { toolLabel, uniqueTools } from "./tool_labels.js";
import { basicInfoField } from "./person-basic-info.js";

// A new persona starts active, and saying so is what keeps someone from
// thinking it was made while it still waits for a switch.
export function addedToast(saved) {
  if (saved.active) toast(t("✓ 「{0}」を追加しました。この担当は現在有効です", saved.name));
  else toast(t("「{0}」を追加しましたが、有効にできませんでした。カードのスイッチをオンにしてください", saved.name), "error");
}

export function showPersonaBuilder({ persona, kind = "subject", onSave, onPosted, getSettings = () => ({}) }) {
  const opener = document.activeElement;
  const person = (persona?.kind || kind) === "person";
  const opening = t(persona ? "どんなところを変えたい？" : person
    ? "誰のことを覚える担当を作りたい？名前や呼び名、あなたとの関係を教えてください。"
    : "どんなことの担当を作りたい？");
  const messages = [{ role: "assistant", content: opening }];
  let draft = null;
  // How often a person is looked back on, as the AI chose from how close they are.
  let checkIn = "";
  let busy = false;
  let closed = false;
  const backdrop = el("div", { class: "dialog-backdrop" });
  const conversation = el("div", { class: "builder-conversation", role: "log", "aria-live": "polite" });
  // Once there is a draft, the draft is what to read: the exchange that led to
  // it folds away and the box for more changes waits behind a button, so the
  // screen asks one thing - add this or not.
  const log = el("details", { class: "builder-log", open: true, hidden: !persona }, el("summary", { label: "AIとのやりとり", hidden: true }), conversation);
  const notice = el("p", { class: "builder-notice", role: "status", hidden: true, label: persona
    ? "まだ保存されていません。下記の内容で問題なければ「この内容で保存」を押してください。"
    : "まだ追加されていません。下記の内容で問題なければ「この担当を追加」を押してください。追加するとすぐに有効になります。" });
  const preview = el("div");
  const basicInfo = basicInfoField();
  const memoryEnabled = el("input", { type: "checkbox", class: "persona-memory-enabled", checked: persona?.memoryEnabled !== false });
  memoryEnabled.checked = persona?.memoryEnabled !== false;
  let memoryChoiceChanged = false;
  const memoryOption = el("div", { class: "persona-options" },
    el("label", { class: "switch" }, switchControl(memoryEnabled), el("span", { label: "記憶する" })));
  memoryEnabled.addEventListener("change", () => {
    memoryChoiceChanged = true;
    if (draft) draft.memoryEnabled = memoryEnabled.checked;
  });
  const alwaysUltra = el("input", { type: "checkbox", class: "persona-always-ultra" });
  alwaysUltra.checked = persona?.alwaysUltra === true;
  let ultraChoiceChanged = false;
  memoryOption.append(el("label", { class: "switch" }, switchControl(alwaysUltra), el("span", { label: "常に熟考する" })));
  alwaysUltra.addEventListener("change", () => {
    ultraChoiceChanged = true;
    if (draft) draft.alwaysUltra = alwaysUltra.checked;
  });

  const error = el("p", { class: "hint", role: "alert" });
  const input = el("textarea", { class: "skill-input", rows: 3, maxlength: 12000, "aria-label": t("回答や変更したいこと"), i18nPlaceholder: persona ? "例：もっと短く返事してほしい。おすすめより、記録を中心にしてほしい。" : person ? "例：大学時代の友人の佐藤さん。さっちゃんと呼んでいて、たまに一緒に食事をします。" : "例：美味しかったワインのラベルを送るので、銘柄やブドウ品種を教えてほしい。" });
  const send = el("button", { class: "button primary", label: "送る", type: "button", onclick: () => void submit() });
  const revise = el("button", { class: "button ghost", label: "さらに修正する", hidden: true, onclick: () => {
    form.hidden = false;
    send.hidden = voice.button.hidden = false;
    revise.hidden = true;
    input.focus();
  }});
  // Creating says what the button does: nothing exists until it is pressed.
  const save = el("button", { class: "button primary builder-save", label: persona ? "この内容で保存" : "この担当を追加", hidden: true, onclick: async () => {
    if (busy || !draft) return;
    setBusy(true);
    error.textContent = "";
    try {
      await voice.discard();
      if (closed) return;
      const saved = await api.savePersonaDraft({ ...draft, memoryEnabled: memoryEnabled.checked, alwaysUltra: alwaysUltra.checked, basicInfo: draft.kind === "person" ? basicInfo.box.value.trim() : "" }, persona?.id);
      if (memoryEnabled.checked && !persona && saved.kind === "person" && checkIn === "monthly") {
        try { await api.setDailyCheckIn(saved.id, { talk: "share", every: "monthly" }); }
        catch (e) { toast(String(e.message || e), "error"); }
      }
      if (persona) toast(t("{0} を保存しました", saved.name));
      else addedToast(saved);
      close();
      await onSave?.(saved);
    } catch (e) { error.textContent = e.message; }
    finally { setBusy(false); }
  }});
  const voice = createVoiceInput({
    getSettings,
    getText: () => input.value,
    setText: (value) => { if (!closed && !busy) input.value = value; },
    onSend: () => void submit(),
  });
  function close() {
    closed = true;
    void voice.discard();
    unmountOverlay(backdrop);
    opener?.focus();
  }
  function setBusy(value) {
    busy = value;
    input.disabled = value;
    send.disabled = value;
    save.disabled = value;
    revise.disabled = value;
    basicInfo.box.disabled = value;
    memoryEnabled.disabled = value;
    alwaysUltra.disabled = value;
    voice.button.disabled = value;
    send.textContent = t(value ? "考えています…" : "送る");
  }
  function bubble(role, text) {
    conversation.append(el("div", { class: `builder-message ${role}` }, el("strong", { text: role === "user" ? t("あなた") : "AI" }), el("p", { text })));
    conversation.scrollTop = conversation.scrollHeight;
  }
  if (persona) bubble("assistant", opening);
  async function submit(event) {
    event?.preventDefault();
    const answer = input.value.trim();
    if (!answer || busy || closed) return;
    setBusy(true);
    error.textContent = "";
    const next = [...messages, { role: "user", content: answer }];
    try {
      await voice.discard();
      if (closed) return;
      const result = await api.buildPersona({ id: persona?.id, language: getLanguage(), messages: next, current: draft ? { ...draft, memoryEnabled: memoryEnabled.checked, alwaysUltra: alwaysUltra.checked, basicInfo: basicInfo.box.value.trim() } : persona ? { ...persona, memoryEnabled: memoryEnabled.checked, alwaysUltra: alwaysUltra.checked } : undefined });
      if (!backdrop.isConnected) return;
      messages.push(next.at(-1), { role: "assistant", content: JSON.stringify(result) });
      log.hidden = false;
      bubble("user", answer);
      bubble("assistant", result.message);
      input.value = "";
      draft = result.draft;
      if (draft) {
        if (!memoryChoiceChanged && typeof draft.memoryEnabled === "boolean") memoryEnabled.checked = draft.memoryEnabled;
        draft.memoryEnabled = memoryEnabled.checked;
        if (!ultraChoiceChanged && typeof draft.alwaysUltra === "boolean") alwaysUltra.checked = draft.alwaysUltra;
        draft.alwaysUltra = alwaysUltra.checked;
      }
      checkIn = draft?.kind === "person" ? (result.checkIn === "monthly" ? "monthly" : "daily") : "";
      basicInfo.set(draft);
      input.placeholder = typeof result.placeholder === "string" && result.placeholder.trim()
        ? result.placeholder.trim()
        : t(draft
          ? draft.kind === "person"
            ? "例：呼び名に「さっちゃん」も加えてほしい。"
            : "例：もう少し親しみやすい話し方にしてほしい。"
          : "上の質問に答えてください。迷うところは「おまかせ」でも大丈夫です。");
      clear(preview);
      save.hidden = notice.hidden = revise.hidden = !draft;
      form.hidden = !!draft;
      send.hidden = voice.button.hidden = !!draft;
      log.open = !draft;
      log.querySelector("summary").hidden = !draft;
      if (draft) {
        preview.append(el("h4", { text: `${draft.icon || "◎"} ${draft.name}` }));
        const words = draft.kind === "person"
          ? [["呼び名", [draft.name, ...(draft.aliases || [])].join(" / ")], ["キーワード", (draft.keywords || []).join(" / ") || t("（なし）")]]
          : [];
        // A person's tone and kinds of note are the app's; only instructions
        // for this one person are its own, and there may be none.
        const about = draft.kind === "person"
          ? [["関係性", draft.domain], ...(String(draft.instructions || "").trim() ? [["気を配ること", draft.instructions]] : []),
            ...(persona || !memoryEnabled.checked ? [] : [["振り返り", t(checkIn === "monthly" ? "月1回（月のまとめを読む）" : "毎日")]])]
          : [["担当条件", draft.domain], ["話し方", draft.tone], ...(memoryEnabled.checked ? [["メモの分類", draft.memory]] : []), ["覚えること・返事をするタイミング", draft.instructions]];
        for (const [label, value] of [...words, ...about, ["できること", uniqueTools(draft.tools).map(tool => t(toolLabel(tool))).join(" / ") || t("会話と記憶")]]) {
          preview.append(el("strong", { label }), el("p", { class: "builder-detail", text: value }));
        }
        preview.append(el("p", { class: "hint", label: "まずはこの内容で使ってみましょう。使ったあとでも「AIで修正」で変えられます。" }));
      }
    } catch (e) { error.textContent = e.message; }
    finally {
      setBusy(false);
      if (backdrop.isConnected) (draft ? save : input).focus();
    }
  }
  const form = el("form", { onsubmit: submit }, input, voice.activity);
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing) void submit(event);
  });
  const dialog = el("div", { class: "dialog persona-builder", role: "dialog", "aria-modal": "true", "aria-label": t(persona ? "AIで修正" : "AIと担当を作る") },
    dialogCloseButton(close),
    el("h3", { text: persona ? t("{0} を修正", persona.name) : t("AIと担当を作る") }),
    notice, preview, basicInfo.node, memoryOption, log, error, form,
    el("div", { class: "compose-row builder-actions" }, voice.button, send, el("span", { class: "spacer" }), revise, save));
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
    if (event.key === "Tab") {
      const focusable = [...dialog.querySelectorAll("button, textarea, summary")].filter(node => !node.disabled && node.getClientRects().length);
      const first = focusable[0], last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    }
  });
  backdrop.append(dialog);
  mountOverlay(backdrop);
  input.focus();
}
