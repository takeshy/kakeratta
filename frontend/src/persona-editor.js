// Editing a persona.
//
// Direct editing and asking the AI are separate entrances. In AI mode the
// saved persona is initially reference-only; once an AI proposal arrives it
// becomes an editable draft, and nothing is saved until the user saves it.
import { el, clear, toast } from "./dom.js";
import { createVoiceInput } from "./voice_input.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { capability, toolLabel, uniqueTools } from "./tool_labels.js";

const fields = [
  { key: "icon", label: "アイコン", kind: "input", maxlength: 4 },
  { key: "name", label: "名前", kind: "input", maxlength: 40 },
  { key: "domain", label: "担当条件（振り分けに使います）", kind: "textarea", rows: 4, maxlength: 2000 },
  { key: "tone", label: "話し方", kind: "textarea", rows: 2, maxlength: 2000 },
  { key: "memory", label: "メモの分類（型と、それぞれに書くこと）", kind: "textarea", rows: 3, maxlength: 2000 },
  { key: "instructions", label: "覚えること・返事をするタイミング", kind: "textarea", rows: 6, maxlength: 8000 },
];

export function showPersonaEditor({ persona, toolNames = [], onSave, getSettings = () => ({}), ai = false }) {
  const opener = document.activeElement;
  const original = { ...persona, tools: [...(persona.tools || [])] };
  const inputs = {};
  const previous = {};
  let closed = false;
  let busy = false;
  let proposalReady = !ai;

  const backdrop = el("div", { class: "dialog-backdrop" });
  const form = el("div", { class: "persona-form" });
  const error = el("p", { class: "hint", role: "alert" });

  for (const field of fields) {
    const input = field.kind === "textarea"
      ? el("textarea", { class: "skill-input", rows: field.rows, maxlength: field.maxlength })
      : el("input", { class: "skill-input", maxlength: field.maxlength });
    input.value = original[field.key] || "";
    input.readOnly = ai;
    input.addEventListener("input", () => markChanged(field.key));
    inputs[field.key] = input;
    previous[field.key] = el("details", { class: "previous" });
    previous[field.key].hidden = true;
    form.append(el("label", { class: "persona-field" }, el("strong", { label: field.label }), input, previous[field.key]));
  }

  const toolBoxes = {};
  const toolList = el("div", { class: "persona-tools" });
  const available = persona.id === "hermes" ? [] : uniqueTools([...new Set([...toolNames, ...original.tools])]).filter((name) => name !== "timeline_search");
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
  if (persona.id !== "hermes") form.append(el("div", { class: "persona-field" }, el("strong", { label: "使える道具" }), toolList, previous.tools));

  function read() {
    const draft = { ...persona };
    for (const field of fields) draft[field.key] = inputs[field.key].value.trim();
    draft.tools = available.filter((name) => toolBoxes[name].checked);
    return draft;
  }

  function shown(key, value) {
    if (key === "tools") return uniqueTools(value || []).map((name) => t(toolLabel(name))).join(" / ") || t("（なし）");
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
    for (const field of fields) inputs[field.key].readOnly = !value;
    for (const box of Object.values(toolBoxes)) box.disabled = !value;
  }

  function apply(draft) {
    proposalReady = true;
    setDraftEditable(true);
    for (const field of fields) {
      if (typeof draft[field.key] === "string") inputs[field.key].value = draft[field.key];
      markChanged(field.key);
    }
    if (Array.isArray(draft.tools)) {
      for (const name of available) toolBoxes[name].checked = draft.tools.some((tool) => capability(tool) === capability(name));
    }
    markChanged("tools");
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
      el("strong", { text: role === "user" ? t("あなた") : "AI" }), el("p", { text })));
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
      messages.push(next.at(-1), { role: "assistant", content: JSON.stringify(result) });
      bubble("user", text);
      bubble("assistant", result.message);
      request.value = "";
      if (typeof result.placeholder === "string" && result.placeholder.trim()) request.placeholder = result.placeholder.trim();
      if (result.draft) apply(result.draft);
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
    request.disabled = value;
    send.disabled = value;
    voice.button.disabled = value;
    send.textContent = t(value ? "考えています…" : "AIに頼む");
    updateSave();
  }

  function close() {
    closed = true;
    void voice.discard();
    backdrop.remove();
    opener?.focus?.();
  }

  const title = ai ? t("{0} をAIで修正", persona.name) : t("{0} を修正", persona.name);
  const dialog = el("div", { class: "dialog persona-builder persona-editor", role: "dialog", "aria-modal": "true", "aria-label": title },
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
  document.body.append(backdrop);
  inputs.name.focus?.();
  return { close };
}
