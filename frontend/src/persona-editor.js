// Editing a persona.
//
// Everything a persona is can be changed here directly - what it covers, how it
// talks, what it keeps, how much thought it gets, what it may reach. Asking the
// AI is part of the same screen rather than a separate one: the request is
// written underneath the persona as it stands, because it is much easier to say
// what should change while looking at what is there. Whatever the AI proposes
// goes into the same form, next to what it replaced, and nothing is saved until
// the user saves it.
import { el, clear, toast } from "./dom.js";
import { createVoiceInput } from "./voice_input.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";

const modelLabels = {
  low: "low — メモと Web 検索と発言だけの軽い担当",
  middle: "middle — 道具を使える、速さと丁寧さのバランス",
  high: "high — 道具を使える、じっくり考える",
};

const toolLabels = {
  web_search: "Webで調べる",
  place_search: "お店や場所を探す",
  route: "行き方を調べる",
  schedule: "自分を起こす予定を置く",
  timeline_search: "過去のかけらを探す",
};

// lowTools mirrors the server's rule: the low tier may hold web search and
// nothing else. The server refuses anything else anyway; saying so here is
// just earlier.
const lowTools = new Set(["web_search"]);

const fields = [
  { key: "icon", label: "アイコン", kind: "input", maxlength: 4 },
  { key: "name", label: "名前", kind: "input", maxlength: 40 },
  { key: "domain", label: "担当すること（振り分けに使います）", kind: "textarea", rows: 4, maxlength: 2000 },
  { key: "tone", label: "話し方", kind: "textarea", rows: 2, maxlength: 2000 },
  { key: "instructions", label: "覚えること・返事をするタイミング", kind: "textarea", rows: 6, maxlength: 8000 },
];

export function showPersonaEditor({ persona, toolNames = [], onSave, getSettings = () => ({}) }) {
  const opener = document.activeElement;
  const original = { ...persona, tools: [...(persona.tools || [])] };
  const inputs = {};
  const previous = {};
  let closed = false;
  let busy = false;

  const backdrop = el("div", { class: "dialog-backdrop" });
  const form = el("div", { class: "persona-form" });
  const error = el("p", { class: "hint", role: "alert" });

  for (const field of fields) {
    const input = field.kind === "textarea"
      ? el("textarea", { class: "skill-input", rows: field.rows, maxlength: field.maxlength })
      : el("input", { class: "skill-input", maxlength: field.maxlength });
    input.value = original[field.key] || "";
    input.addEventListener("input", () => markChanged(field.key));
    inputs[field.key] = input;
    previous[field.key] = el("details", { class: "previous" });
    previous[field.key].hidden = true;
    form.append(el("label", { class: "persona-field" }, el("strong", { label: field.label }), input, previous[field.key]));
  }

  const model = el("select", { class: "skill-input" });
  for (const tier of ["low", "middle", "high"]) {
    model.append(el("option", { value: tier, label: modelLabels[tier] }));
  }
  model.value = original.model || "middle";
  model.addEventListener("change", () => { markChanged("model"); checkTier(); });
  inputs.model = model;
  previous.model = el("details", { class: "previous" });
  previous.model.hidden = true;
  form.append(el("label", { class: "persona-field" }, el("strong", { label: "賢さ" }), model, previous.model));

  const toolBoxes = {};
  const toolList = el("div", { class: "persona-tools" });
  const available = [...new Set([...toolNames, ...original.tools])].filter((name) => name !== "timeline_search");
  for (const name of available) {
    const box = el("input", { type: "checkbox" });
    box.checked = original.tools.includes(name);
    box.addEventListener("change", () => { markChanged("tools"); checkTier(); });
    toolBoxes[name] = box;
    toolList.append(el("label", { class: "switch" }, box, el("span", { text: `${t(toolLabels[name] || name)} (${name})` })));
  }
  previous.tools = el("details", { class: "previous" });
  previous.tools.hidden = true;
  const tierWarning = el("p", { class: "hint warn", role: "status" });
  form.append(el("div", { class: "persona-field" }, el("strong", { label: "使える道具" }), toolList, previous.tools, tierWarning));

  function read() {
    const draft = { ...persona };
    for (const field of fields) draft[field.key] = inputs[field.key].value.trim();
    draft.model = model.value;
    draft.tools = available.filter((name) => toolBoxes[name].checked);
    return draft;
  }

  function shown(key, value) {
    if (key === "model") return t(modelLabels[value] || value || "");
    if (key === "tools") return (value || []).map((name) => t(toolLabels[name] || name)).join(" / ") || t("（なし）");
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

  function checkTier() {
    const draft = read();
    const beyond = draft.model === "low" ? draft.tools.filter((name) => !lowTools.has(name)) : [];
    tierWarning.textContent = beyond.length
      ? t("low の担当は Web 検索以外の道具を使えません（{0}）。middle か high にしてください。", beyond.join(" / "))
      : "";
    save.disabled = busy || beyond.length > 0;
  }

  function apply(draft) {
    for (const field of fields) {
      if (typeof draft[field.key] === "string") inputs[field.key].value = draft[field.key];
      markChanged(field.key);
    }
    if (["low", "middle", "high"].includes(draft.model)) model.value = draft.model;
    markChanged("model");
    if (Array.isArray(draft.tools)) {
      for (const name of available) toolBoxes[name].checked = draft.tools.includes(name);
    }
    markChanged("tools");
    checkTier();
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
    el("p", { class: "hint", label: "上の内容を見ながら、変えたいことを書いてください。AIの案はフォームに反映され、保存するまでは確定しません。" }),
    conversation, request, el("div", { class: "compose-row" }, voice.button, send), voice.activity);
  aiPanel.hidden = true;
  const openAI = el("button", { class: "button ghost", label: "AIで修正", onclick: () => {
    aiPanel.hidden = false;
    openAI.hidden = true;
    request.focus();
  } });

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
      // The AI changes the persona as it stands in the form, edits included.
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
    checkTier();
  }

  function close() {
    closed = true;
    void voice.discard();
    backdrop.remove();
    opener?.focus?.();
  }

  const dialog = el("div", { class: "dialog persona-builder persona-editor", role: "dialog", "aria-modal": "true", "aria-label": t("{0} を修正", persona.name) },
    el("h3", { text: t("{0} を修正", persona.name) }),
    form,
    el("div", { class: "compose-row" }, openAI),
    aiPanel,
    error,
    el("div", { class: "compose-row" },
      el("button", { class: "button ghost", label: "閉じる", onclick: close }),
      el("span", { class: "spacer" }),
      save));
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
  });
  checkTier();
  backdrop.append(dialog);
  document.body.append(backdrop);
  inputs.name.focus?.();
  return { close };
}
