// Editing a persona.
//
// Direct editing and asking the AI are separate entrances. In AI mode the
// saved persona is initially reference-only; once an AI proposal arrives it
// becomes an editable draft, and nothing is saved until the user saves it.
import { preserveScroll, switchControl, el, clear, toast, dialogCloseButton, mountOverlay, unmountOverlay } from "./dom.js";
import { createAvatarField, userProfile, personaTextDraft } from "./avatar.js";
import { createVoiceInput } from "./voice_input.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { capability, toolLabel, uniqueTools } from "./tool_labels.js";

const fields = [
  { key: "icon", label: "アイコン", kind: "input", maxlength: 4 },
  { key: "name", label: "名前", kind: "input", maxlength: 40 },
  { key: "domain", label: "担当条件（振り分けに使います）", kind: "textarea", rows: 4, maxlength: 2000 },
  { key: "basicInfo", label: "基本情報", kind: "textarea", rows: 4, maxlength: 8000 },
  { key: "tone", label: "話し方", kind: "textarea", rows: 2, maxlength: 2000 },
  { key: "memory", label: "メモの分類（型と、それぞれに書くこと）", kind: "textarea", rows: 3, maxlength: 2000 },
  { key: "instructions", label: "覚えること・返事をするタイミング", kind: "textarea", rows: 6, maxlength: 8000 },
];

// A person's names and keywords are edited as one line of text each, split on
// commas, 読点 and line breaks.
export function splitTerms(text) {
  return String(text || "").split(/[,，、\n]+/).map((term) => term.trim()).filter(Boolean);
}

// joinTerms writes them back the way the language lists things.
export function joinTerms(terms) {
  return (terms || []).join(getLanguage() === "ja" ? "、" : ", ");
}

export function showPersonaEditor({ persona, toolNames = [], onSave, onPosted, getSettings = () => ({}), ai = false, focus = "" }) {
  const opener = document.activeElement;
  const original = { ...persona, tools: [...(persona.tools || [])], kind: persona.kind || "", aliases: [...(persona.aliases || [])], keywords: [...(persona.keywords || [])] };
  const inputs = {};
  const previous = {};
  let closed = false;
  let busy = false;
  let proposalReady = !ai;

  const backdrop = el("div", { class: "dialog-backdrop" });
  const form = el("div", { class: "persona-form" });
  const error = el("p", { class: "hint", role: "alert" });
  const memoryEnabled = el("input", { type: "checkbox", class: "persona-memory-enabled", checked: persona.memoryEnabled !== false, disabled: ai });
  memoryEnabled.checked = persona.memoryEnabled !== false;
  memoryEnabled.addEventListener("change", () => preserveScroll(form, showKind));
  const alwaysUltra = el("input", { type: "checkbox", class: "persona-always-ultra", disabled: ai });
  alwaysUltra.checked = persona.alwaysUltra === true;
  form.append(el("div", { class: "persona-options" },
    el("label", { class: "switch" }, switchControl(memoryEnabled), el("span", { label: "記憶する" })),
    el("label", { class: "switch" }, switchControl(alwaysUltra), el("span", { label: "常に熟考する" }))));

  // A person is reached by its names and keywords instead of the router, so
  // those replace the condition as what decides; the condition stays as what
  // the persona is told it keeps. Whether a persona is a person is settled
  // when it is made and does not change, so there is nothing to switch here.
  const person = { checked: original.kind === "person" };
  const terms = {};
  const termFields = [
    { key: "aliases", label: "呼び名（読点区切り。名前・あだ名・続柄・かな表記）", rows: 2 },
    { key: "keywords", label: "キーワード（読点区切り。類似語や言い換えも並べると届きやすくなります）", rows: 2 },
  ];
  const termBox = el("div");
  for (const field of termFields) {
    const input = el("textarea", { class: "skill-input", rows: field.rows, maxlength: 4000 });
    input.value = joinTerms(original[field.key]);
    input.readOnly = ai;
    input.addEventListener("input", () => markChanged(field.key));
    terms[field.key] = input;
    previous[field.key] = el("details", { class: "previous" });
    previous[field.key].hidden = true;
    termBox.append(el("label", { class: "persona-field" }, el("strong", { label: field.label }), input, previous[field.key]));
  }

  const avatar = createAvatarField({ icon: persona.icon, avatar: persona.avatar, disabled: ai, onBusy: value => setBusy(value) });
  const labels = {};
  const boxes = {};
  for (const field of fields) {
    const input = field.key === "icon" ? avatar.input : field.kind === "textarea"
      ? el("textarea", { class: "skill-input", rows: field.rows, maxlength: field.maxlength })
      : el("input", { class: "skill-input", maxlength: field.maxlength });
    input.value = original[field.key] || "";
    input.readOnly = ai;
    input.addEventListener("input", () => markChanged(field.key));
    inputs[field.key] = input;
    previous[field.key] = el("details", { class: "previous" });
    previous[field.key].hidden = true;
    labels[field.key] = el("strong", { label: field.label });
    boxes[field.key] = field.key === "icon" ? avatar.element : el("label", { class: "persona-field" }, labels[field.key], input, previous[field.key]);
    if (field.key === "icon") { form.append(avatar.element, previous.icon); }
    else form.append(boxes[field.key]);
    if (field.key === "name") form.append(termBox);
  }

  // A person speaks and files its notes the way the app has every person do,
  // so only the relationship and instructions for this one person are asked.
  function showKind() {
    termBox.hidden = !person.checked;
    boxes.basicInfo.hidden = !person.checked;
    boxes.tone.hidden = person.checked;
    boxes.memory.hidden = person.checked || !memoryEnabled.checked;
    labels.domain.textContent = t(person.checked ? "関係性（例：恋人、母、大学時代の友人）" : "担当条件（振り分けに使います）");
    // A relationship is a line, and a person's own instructions a couple; a
    // condition and a subject's instructions are paragraphs.
    inputs.domain.setAttribute("rows", person.checked ? 1 : 4);
    inputs.instructions.setAttribute("rows", person.checked ? 2 : 6);
    labels.instructions.textContent = t(person.checked ? "気を配ること（任意。例：健康の話は控えめに）" : "覚えること・返事をするタイミング");
  }
  showKind();

  const toolBoxes = {};
  const toolList = el("div", { class: "persona-tools" });
  const available = uniqueTools([...new Set([...toolNames, ...original.tools])]).filter((name) => name !== "timeline_search");
  for (const name of available) {
    const box = el("input", { type: "checkbox" });
    box.checked = original.tools.some((tool) => capability(tool) === capability(name));
    box.disabled = ai;
    box.addEventListener("change", () => markChanged("tools"));
    toolBoxes[name] = box;
    toolList.append(el("label", { class: "switch" }, box, el("span", { text: t(toolLabel(name)) })));
  }
  previous.tools = el("details", { class: "previous" });
  previous.tools.hidden = true;
  form.append(el("div", { class: "persona-field" }, el("strong", { label: "使える道具" }), toolList, previous.tools));

  function read() {
    const draft = { ...persona, avatar: avatar.value().avatar, memoryEnabled: memoryEnabled.checked, alwaysUltra: alwaysUltra.checked };
    for (const field of fields) draft[field.key] = inputs[field.key].value.trim();
    if (person.checked) draft.tone = draft.memory = "";
    draft.tools = available.filter((name) => toolBoxes[name].checked);
    draft.kind = person.checked ? "person" : "";
    if (!person.checked) draft.basicInfo = "";
    draft.aliases = person.checked ? splitTerms(terms.aliases.value) : [];
    draft.keywords = person.checked ? splitTerms(terms.keywords.value) : [];
    return draft;
  }

  function shown(key, value) {
    if (key === "tools") return uniqueTools(value || []).map((name) => t(toolLabel(name))).join(" / ") || t("（なし）");
    if (Array.isArray(value)) return joinTerms(value) || t("（なし）");
    return value || "";
  }

  // A field that differs from what was saved shows what it was, so an edit -
  // or an AI proposal - can be read against the original.
  function markChanged(key) {
    const now = read()[key];
    const before = original[key];
    const same = Array.isArray(now) ? now.join(",") === (before || []).join(",") : now === (before || "");
    const node = previous[key];
    node.hidden = same;
    clear(node);
    if (!same) {
      node.append(el("summary", { label: "変更前の内容" }), el("p", { class: "builder-detail", text: shown(key, before) }));
    }
  }

  function updateSave() {
    save.disabled = busy || !proposalReady;
  }

  function setDraftEditable(value) {
    avatar.setDisabled(!value);
    memoryEnabled.disabled = !value;
    alwaysUltra.disabled = !value;
    for (const field of fields) inputs[field.key].readOnly = !value;
    for (const box of Object.values(toolBoxes)) box.disabled = !value;
    for (const input of Object.values(terms)) input.readOnly = !value;
  }

  function apply(draft) {
    proposalReady = true;
    if (typeof draft.memoryEnabled === "boolean") memoryEnabled.checked = draft.memoryEnabled;
    if (typeof draft.alwaysUltra === "boolean") alwaysUltra.checked = draft.alwaysUltra;
    setDraftEditable(true);
    for (const field of fields) {
      if (typeof draft[field.key] === "string") inputs[field.key].value = draft[field.key];
      markChanged(field.key);
    }
    if (Array.isArray(draft.tools)) {
      for (const name of available) toolBoxes[name].checked = draft.tools.some((tool) => capability(tool) === capability(name));
    }
    markChanged("tools");
    for (const field of termFields) {
      if (Array.isArray(draft[field.key])) terms[field.key].value = joinTerms(draft[field.key]);
      markChanged(field.key);
    }
    avatar.set({ icon: inputs.icon.value, avatar: draft.avatar ?? avatar.value().avatar });
    showKind();
    updateSave();
  }

  const save = el("button", { class: "button primary", label: "保存", onclick: async () => {
    if (busy) return;
    setBusy(true);
    error.textContent = "";
    try {
      await voice.discard();
      if (closed) return;
      const saved = await api.savePersonaDraft(read(), persona.id);
      toast(t("{0} を保存しました", saved.name));
      close();
      await onSave?.();
    } catch (e) {
      error.textContent = e.message;
    } finally {
      setBusy(false);
    }
  } });

  // --- asking the AI, underneath the form ---------------------------------
  const messages = [];
  const conversation = el("div", { class: "builder-conversation", role: "log", "aria-live": "polite" });
  const request = el("textarea", {
    class: "skill-input ai-request", rows: 3, maxlength: 12000, "aria-label": t("AIへの依頼"),
    i18nPlaceholder: "例：もっと短く返事してほしい。おすすめより、記録を中心にしてほしい。",
  });
  const send = el("button", { class: "button primary", label: "AIに頼む", type: "button", onclick: () => void ask() });
  const voice = createVoiceInput({
    getSettings,
    getText: () => request.value,
    setText: (value) => { if (!closed && !busy) request.value = value; },
    onSend: () => void ask(),
  });
  const aiPanel = el("div", { class: "ai-panel" },
    el("p", { class: "hint", label: "現在の内容を見ながら、変えたいことを書いてください。AIの案ができたら編集して保存できます。" }),
    conversation, request, el("div", { class: "compose-row" }, voice.button, send), voice.activity);

  function bubble(role, text) {
    conversation.append(el("div", { class: `builder-message ${role}` },
      el("strong", { text: role === "user" ? userProfile(getSettings()).name : "AI" }), el("p", { text })));
    conversation.scrollTop = conversation.scrollHeight;
  }

  async function ask(event) {
    event?.preventDefault();
    const text = request.value.trim();
    if (!text || busy || closed) return;
    setBusy(true);
    error.textContent = "";
    const next = [...messages, { role: "user", content: text }];
    try {
      await voice.discard();
      if (closed) return;
      const result = await api.buildPersona({ id: persona.id, language: getLanguage(), messages: next, current: read() });
      if (closed) return;
      messages.push(next.at(-1), { role: "assistant", content: JSON.stringify({ ...result, draft: personaTextDraft(result.draft) }) });
      bubble("user", text);
      bubble("assistant", result.message);
      request.value = "";
      if (typeof result.placeholder === "string" && result.placeholder.trim()) request.placeholder = result.placeholder.trim();
      if (result.draft) {
        apply(result.draft);
      }
    } catch (e) {
      error.textContent = e.message;
    } finally {
      setBusy(false);
      if (!closed) request.focus();
    }
  }
  request.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing) void ask(event);
  });

  function setBusy(value) {
    busy = value;
    avatar.setDisabled(value || (ai && !proposalReady));
    request.disabled = value;
    send.disabled = value;
    voice.button.disabled = value;
    send.textContent = t(value ? "考えています…" : "AIに頼む");
    updateSave();
  }

  function close() {
    closed = true;
    void voice.discard();
    unmountOverlay(backdrop);
    opener?.focus?.();
  }

  const title = ai ? t("{0} をAIで修正", persona.name) : t("{0} を修正", persona.name);
  const dialog = el("div", { class: "dialog persona-builder persona-editor", role: "dialog", "aria-modal": "true", "aria-label": title },
    dialogCloseButton(close),
    el("h3", { text: title }),
    form,
    ai ? aiPanel : null,
    error,
    el("div", { class: "compose-row" },
      el("button", { class: "button ghost", label: "閉じる", onclick: close }),
      el("span", { class: "spacer" }),
      save));
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
  });
  updateSave();
  backdrop.append(dialog);
  mountOverlay(backdrop);
  // Opened to one field - the card's "edit basic information" - the dialog
  // scrolls to it and starts there rather than at the name.
  const target = !ai && focus && !boxes[focus]?.hidden ? inputs[focus] : null;
  (target || inputs.name).focus?.();
  target?.scrollIntoView?.({ block: "center" });
  return { close };
}
