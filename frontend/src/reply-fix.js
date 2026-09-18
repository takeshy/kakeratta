// Teaching a persona from one of its remarks - or from its silence.
//
// The persona said something, and it was not what the person wanted; or it
// was given a post and said nothing when it should have. Rather
// than rewriting its tone and instructions in the settings, they say here how
// it missed; the AI that builds personas rewrites the tone and the
// instructions - nothing else - and the person can correct both by hand. OK
// saves them and then has the persona take the same turn again, without any
// of it landing anywhere, so the difference can be read on the same post. If
// it still misses, they say how, and it goes round again.
import { clear, el } from "./dom.js";
import { api } from "./api.js";
import { getLanguage, t } from "./i18n.js";

const EXCERPT = 300;
const cut = (text) => (text || "").length > EXCERPT ? `${text.slice(0, EXCERPT)}…` : text || "";

export function showReplyFix({ persona, remark = null, post, onSaved }) {
  // Without a remark, the dispute is about the persona's silence on the post,
  // and the post is where its turn is tried again.
  const target = remark || post;
  const title = remark ? t("{0} の返事を直す", persona.name) : t("{0} に返事してほしかった", persona.name);
  const postText = cut(post?.text || "");
  // said is the remark being disputed: the original one, and then each trial
  // turn the person said was still not it. Silence is said as such.
  let said = remark ? remark.text || "" : t("（何も言いませんでした）");
  // saved is the persona as it stands on the server; it moves when OK saves.
  let saved = { ...persona };
  // failure is what the person said about the last trial, told to the AI.
  let failure = "";
  const messages = [];
  let closed = false;
  let busy = false;
  let controls = [];

  const backdrop = el("div", { class: "dialog-backdrop" });
  const dialog = el("div", { class: "dialog condition-fix reply-fix", role: "dialog", "aria-modal": "true", "aria-label": title });
  const body = el("div", { class: "condition-body" });
  const error = el("p", { class: "hint warn", role: "alert" });
  const previousFocus = document.activeElement;
  const close = () => { closed = true; backdrop.remove(); previousFocus?.focus(); };
  backdrop.addEventListener("click", (event) => { if (event.target === backdrop) close(); });
  backdrop.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
    if (event.key !== "Tab") return;
    const focusable = [...backdrop.querySelectorAll("button, textarea, a[href], summary")]
      .filter((node) => !node.disabled && node.getClientRects().length);
    const first = focusable[0], last = focusable.at(-1);
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  });

  function setBusy(value, button, label) {
    busy = value;
    for (const control of controls) control.disabled = value;
    if (button) button.textContent = t(value && label ? label : button.dataset.label);
  }

  function action(label, cls, onclick) {
    const button = el("button", { class: cls, label, type: "button", "data-label": label, onclick });
    button.dataset.label = label;
    controls.push(button);
    return button;
  }

  function fill(...nodes) {
    clear(body);
    body.append(...nodes.filter(Boolean));
  }

  function field(label, node) {
    return el("label", { class: "persona-field" }, el("strong", { label }), node);
  }

  function quote(text) {
    return el("blockquote", { class: "condition-quote" },
      el("span", { class: "avatar", text: persona.icon || "◎" }), el("span", { class: "who", text: ` ${persona.name}: ` }),
      el("span", { text }));
  }

  // The request is written for the persona builder, which already knows to
  // change only what it is asked to.
  function request(note) {
    const lines = [];
    if (failure) lines.push(failure);
    if (post?.text) lines.push(t("次の投稿：「{0}」", post.text));
    if (!remark && !failure) lines.push(t("に、この担当は何も言わなかったが、返事がほしかった。"));
    else lines.push(t("への、この担当の返事が期待と違った：「{0}」", said));
    if (note) lines.push(t("本人の説明：「{0}」", note));
    lines.push(t("口調（tone）と指示（instructions）だけを直し、担当条件（domain）や他の項目は変えないでください。"));
    return lines.join("\n");
  }

  // build asks the AI, working from base: what is saved, or what the person
  // has typed into the boxes since. It returns the new tone and instructions,
  // or nothing when the AI left both as they were.
  async function build(content, base, button, label) {
    setBusy(true, button, label);
    error.textContent = "";
    const next = [...messages, { role: "user", content }];
    try {
      const result = await api.buildPersona({ id: persona.id, entryId: target.id, language: getLanguage(), messages: next, current: base });
      if (closed) return null;
      messages.splice(0, messages.length, ...next, { role: "assistant", content: JSON.stringify(result) });
      const tone = String(result?.draft?.tone ?? base.tone ?? "").trim();
      const instructions = String(result?.draft?.instructions ?? base.instructions ?? "").trim();
      if (tone === (base.tone || "").trim() && instructions === (base.instructions || "").trim()) {
        error.textContent = [t("口調と指示を変えられませんでした"), result?.message || ""].filter(Boolean).join(" ");
        return null;
      }
      return { tone, instructions };
    } catch (e) {
      error.textContent = e.message;
      return null;
    } finally {
      setBusy(false, button);
    }
  }

  // 1. How it missed. The post, what the persona said, and room to say what
  // was wanted instead.
  function showAsk(notice) {
    controls = [];
    error.textContent = "";
    const note = el("textarea", {
      class: "skill-input reply-note", rows: 3, maxlength: 2000, "aria-label": t("期待とどう違うか"),
      i18nPlaceholder: remark ? "例：おすすめの店を並べるより、前に行った店との違いを一言で言ってほしい。" : "例：ラベルの写真を送ったら、銘柄と品種を一言で教えてほしい。",
    });
    controls.push(note);
    const make = action("直し方を作る", "button primary reply-build", async () => {
      if (busy) return;
      const changed = await build(request(note.value.trim()), saved, make, "考えています…");
      if (changed) showProposal(changed);
    });
    fill(
      notice ? el("p", { class: "hint warn condition-notice", role: "alert", text: notice }) : null,
      postText ? el("p", { class: "hint", text: t("投稿：{0}", postText) }) : null,
      quote(said),
      field("期待とどう違うか", note),
      el("div", { class: "compose-row" }, make, el("span", { class: "spacer" }), action("キャンセル", "chip ghost", close)),
    );
    note.focus();
  }

  // 3. The proposal. The tone and the instructions are the persona's own, in
  // the person's hands; the AI can be asked to change them again, and OK saves
  // them and has the persona take the turn again.
  function showProposal({ tone, instructions }) {
    controls = [];
    error.textContent = "";
    const toneBox = el("textarea", { class: "skill-input reply-tone", rows: 3, maxlength: 2000, "aria-label": t("新しい口調") });
    toneBox.value = tone;
    const instructionsBox = el("textarea", { class: "skill-input reply-instructions", rows: 8, maxlength: 8000, "aria-label": t("新しい指示") });
    instructionsBox.value = instructions;
    controls.push(toneBox, instructionsBox);
    const ask = el("textarea", {
      class: "skill-input condition-request", rows: 3, maxlength: 2000, "aria-label": t("AIへの依頼"),
      i18nPlaceholder: "例：もっと短く。理由は一つだけ挙げて。",
    });
    controls.push(ask);
    const typed = () => ({ ...saved, tone: toneBox.value.trim(), instructions: instructionsBox.value.trim() });
    const askBuild = action("直し方を作る", "button primary reply-rebuild", async () => {
      if (busy) return;
      const content = ask.value.trim();
      if (!content) return;
      const changed = await build(content, typed(), askBuild, "考えています…");
      if (changed) {
        toneBox.value = changed.tone;
        instructionsBox.value = changed.instructions;
        ask.value = "";
        askBox.hidden = true;
      }
    });
    const askBox = el("div", { class: "condition-ask" }, field("AIへの依頼", ask), el("div", { class: "compose-row" }, askBuild));
    askBox.hidden = true;
    const ok = action("OK", "button primary reply-ok", () => void confirm(typed(), ok));
    const askAI = action("AIに変更を依頼", "chip ghost condition-ask-ai", () => {
      askBox.hidden = !askBox.hidden;
      if (!askBox.hidden) ask.focus();
    });
    fill(
      el("details", { class: "reply-current" },
        el("summary", { label: "今の口調と指示を見る" }),
        field("今の口調", el("p", { class: "condition-current", text: saved.tone || "" })),
        field("今の指示", el("p", { class: "condition-current", text: saved.instructions || "" }))),
      field("新しい口調", toneBox),
      field("新しい指示", instructionsBox),
      askBox,
      el("div", { class: "compose-row" }, ok, askAI, el("span", { class: "spacer" }), action("キャンセル", "chip ghost", close)),
    );
    toneBox.focus();
  }

  // 4. The trial. What the persona would say now on the same post; the person
  // is the judge. Good closes; not yet goes back to saying how it missed,
  // with this trial as the remark being disputed.
  function showTrial(preview) {
    controls = [];
    error.textContent = "";
    const trial = preview.quiet ? t("（何も言いませんでした）") : (preview.spoke || "");
    fill(
      el("p", { class: "hint", label: "保存しました。同じ投稿に、いまの口調と指示で返事を試し書きしました。" }),
      postText ? el("p", { class: "hint", text: t("投稿：{0}", postText) }) : null,
      quote(trial),
      preview.error ? el("p", { class: "hint warn", text: preview.error }) : null,
      el("div", { class: "compose-row" },
        action("これでよい", "button primary reply-accept", close),
        action("まだ違う", "chip ghost reply-again", () => {
          failure = t("直した口調と指示での返事「{0}」も、まだ期待と違った。", trial);
          said = trial;
          showAsk(t("直した口調と指示での返事は上のとおりでした。どう違うか、もう一度書いてください。"));
        }),
        el("span", { class: "spacer" }),
        action("キャンセル", "chip ghost", close)),
    );
  }

  // OK: save, then have the persona take the same turn again with what was
  // saved. What was saved stays saved whatever the trial says.
  async function confirm(next, button) {
    if (busy) return;
    setBusy(true, button, "試し書きしています…");
    error.textContent = "";
    let savedSuccessfully = next.tone === (saved.tone || "") && next.instructions === (saved.instructions || "");
    try {
      if (!savedSuccessfully) {
        const stored = await api.savePersonaDraft(next, persona.id);
        saved = { ...next, ...(stored && typeof stored === "object" ? stored : {}), tone: next.tone, instructions: next.instructions };
        savedSuccessfully = true;
        await onSaved?.();
      }
      const preview = await api.previewReply(target.id, persona.id);
      if (closed) return;
      showTrial(preview || { quiet: true });
    } catch (e) {
      error.textContent = [t(savedSuccessfully ? "保存しましたが、返事を試せませんでした" : "口調と指示を保存できませんでした"), e.message].filter(Boolean).join(" ");
    } finally {
      setBusy(false, button);
    }
  }

  dialog.append(el("h2", { text: title }), body, error);
  backdrop.append(dialog);
  document.body.append(backdrop);
  dialog.setAttribute("tabindex", "-1");
  showAsk("");
  return { close };
}
