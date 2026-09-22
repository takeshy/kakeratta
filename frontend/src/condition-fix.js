// Teaching the router from one post.
//
// The router chose, or passed over, a persona for a post, and the person
// disagrees. Rather than rewriting the persona's conditions in the settings,
// they say why here; the AI that builds personas rewrites the condition, the
// person can correct that text by hand, and OK saves it and then has the
// router try the same post again. If the post still does not go where it was
// meant to, they are told - with the router's reason - and asked again.
import { clear, el, toast, dialogCloseButton, mountOverlay, unmountOverlay } from "./dom.js";
import { api } from "./api.js";
import { getLanguage, t } from "./i18n.js";

const EXCERPT = 300;

export function showConditionFix({ persona, entry, direction, reason = "", onSaved }) {
  const include = direction === "include";
  const title = include ? t("{0} を呼んでほしかった", persona.name) : t("{0} を呼んでほしくなかった", persona.name);
  const text = entry.text || "";
  const excerpt = text.length > EXCERPT ? `${text.slice(0, EXCERPT)}…` : text;
  // saved is the persona as it stands on the server; it moves when OK saves.
  let saved = { ...persona };
  // failure is what the last trial run said, told to the AI next time round.
  let failure = "";
  const messages = [];
  let closed = false;
  let busy = false;
  let controls = [];

  const backdrop = el("div", { class: "dialog-backdrop" });
  const dialog = el("div", { class: "dialog condition-fix", role: "dialog", "aria-modal": "true", "aria-label": title });
  const body = el("div", { class: "condition-body" });
  const error = el("p", { class: "hint warn", role: "alert" });
  const previousFocus = document.activeElement;
  const close = () => { closed = true; unmountOverlay(backdrop); previousFocus?.focus(); };
  backdrop.addEventListener("click", (event) => { if (event.target === backdrop) close(); });
  backdrop.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
    if (event.key !== "Tab") return;
    const focusable = [...backdrop.querySelectorAll("button, textarea, a[href]")]
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

  // fill puts a state's nodes into the body; a null is a part not shown.
  function fill(...nodes) {
    clear(body);
    body.append(...nodes.filter(Boolean));
  }

  function field(label, node) {
    return el("label", { class: "persona-field" }, el("strong", { label }), node);
  }

  // The request is written for the persona builder, which already knows to
  // change only what it is asked to and to keep a condition short.
  function request(note) {
    const lines = [];
    if (failure) lines.push(failure);
    if (include) {
      lines.push(t("次の投稿をこの担当の対象にしてほしい：「{0}」", text));
      if (reason) lines.push(t("今の担当条件では「{0}」と判断され対象外になった。", reason));
    } else {
      lines.push(t("次の投稿はこの担当の対象にしないでほしい：「{0}」", text));
      lines.push(t("今の担当条件では対象と判断された。"));
    }
    if (note) lines.push(t("本人の補足：「{0}」", note));
    lines.push(t("担当条件（domain）だけを最小限に直し、他の項目は変えないでください。"));
    return lines.join("\n");
  }

  // build asks the AI for a condition, working from base: what is saved, or
  // what the person has typed into the new condition since. It returns the
  // new text, or nothing when the AI left the condition as it was.
  async function build(content, base, button, label) {
    setBusy(true, button, label);
    error.textContent = "";
    const next = [...messages, { role: "user", content }];
    try {
      const result = await api.buildPersona({ id: persona.id, entryId: entry.id, language: getLanguage(), messages: next, current: base });
      if (closed) return null;
      messages.splice(0, messages.length, ...next, { role: "assistant", content: JSON.stringify(result) });
      const domain = String(result?.draft?.domain || "").trim();
      if (!domain || domain === base.domain) {
        error.textContent = [t("条件を変えられませんでした"), result?.message || ""].filter(Boolean).join(" ");
        return null;
      }
      return domain;
    } catch (e) {
      error.textContent = e.message;
      return null;
    } finally {
      setBusy(false, button);
    }
  }

  // 1. Why. The post, what the router said, the condition as it is, and room
  // to say what should have happened.
  function showAsk(notice) {
    controls = [];
    error.textContent = "";
    const note = el("textarea", {
      class: "skill-input condition-note", rows: 3, maxlength: 2000, "aria-label": t("理由や、どう変えてほしいか"),
      i18nPlaceholder: include ? "例：飲食店の話でも、場所や移動に関係するときは反応してほしい。" : "例：本のタイトルが出るだけの話には反応しないでほしい。",
    });
    controls.push(note);
    const make = action("条件を作る", "button primary condition-build", async () => {
      if (busy) return;
      const domain = await build(request(note.value.trim()), saved, make, "考えています…");
      if (domain) showProposal(domain);
    });
    fill(
      notice ? el("p", { class: "hint warn condition-notice", role: "alert", text: notice }) : null,
      el("blockquote", { class: "condition-quote", text: excerpt }),
      include && reason ? el("p", { class: "hint", text: t("担当の判断：{0}", reason) }) : null,
      field("今の判定条件", el("p", { class: "condition-current", text: saved.domain || "" })),
      field("理由や、どう変えてほしいか", note),
      el("div", { class: "compose-row" }, make, el("span", { class: "spacer" }), action("キャンセル", "chip ghost", close)),
    );
    note.focus();
  }

  // 3. The proposal. The new condition is the condition itself, in the
  // person's hands; the AI can be asked to change it again, and OK saves it
  // and has the router try the post.
  function showProposal(domain) {
    controls = [];
    error.textContent = "";
    const next = el("textarea", { class: "skill-input condition-new", rows: 4, maxlength: 2000, "aria-label": t("新しい判定条件") });
    next.value = domain;
    controls.push(next);
    const ask = el("textarea", {
      class: "skill-input condition-request", rows: 3, maxlength: 2000, "aria-label": t("AIへの依頼"),
      i18nPlaceholder: "例：飲食店の名前だけでは反応しないようにして。",
    });
    controls.push(ask);
    const askBuild = action("条件を作る", "button primary condition-rebuild", async () => {
      if (busy) return;
      const content = ask.value.trim();
      if (!content) return;
      const changed = await build(content, { ...saved, domain: next.value.trim() }, askBuild, "考えています…");
      if (changed) {
        next.value = changed;
        ask.value = "";
        askBox.hidden = true;
      }
    });
    const askBox = el("div", { class: "condition-ask" }, field("AIへの依頼", ask), el("div", { class: "compose-row" }, askBuild));
    askBox.hidden = true;
    const ok = action("OK", "button primary condition-ok", () => void confirm(next.value.trim(), ok));
    const askAI = action("AIに変更を依頼", "chip ghost condition-ask-ai", () => {
      askBox.hidden = !askBox.hidden;
      if (!askBox.hidden) ask.focus();
    });
    next.addEventListener("input", () => { ok.disabled = busy || !next.value.trim(); });
    fill(
      field("今の判定条件", el("p", { class: "condition-current", text: saved.domain || "" })),
      field("新しい判定条件", next),
      askBox,
      el("div", { class: "compose-row" }, ok, askAI, el("span", { class: "spacer" }), action("キャンセル", "chip ghost", close)),
    );
    next.focus();
  }

  // OK: save, then have the router try this post with the saved condition.
  // Landing as meant ends it; anything else is said, and the person is asked
  // again from the start. What was saved stays saved either way.
  async function confirm(domain, button) {
    if (!domain || busy) return;
    setBusy(true, button, "確認しています…");
    error.textContent = "";
    try {
      if (domain !== saved.domain) {
        const stored = await api.savePersonaDraft({ ...saved, domain }, persona.id);
        saved = { ...saved, ...(stored && typeof stored === "object" ? stored : {}), domain };
        await onSaved?.();
      }
      const check = await api.checkRouting(entry.id, persona.id);
      if (closed) return;
      if (!check?.answered) {
        error.textContent = [t("保存しましたが、振り分けを確認できませんでした"), check?.error || ""].filter(Boolean).join(" ");
        return;
      }
      if (include ? check.delivered : !check.delivered) {
        toast(t("担当条件を保存しました。この投稿は狙いどおりに振り分けられます"));
        close();
        return;
      }
      failure = include
        ? t("保存した条件では、この投稿は {0} に届きませんでした。理由：{1}", persona.name, check.reason || "")
        : t("保存した条件でも、この投稿は {0} に届きました。", persona.name);
      showAsk(failure);
    } catch (e) {
      error.textContent = e.message;
    } finally {
      setBusy(false, button);
    }
  }

  dialog.append(dialogCloseButton(close), el("h2", { text: title }), body, error);
  backdrop.append(dialog);
  mountOverlay(backdrop);
  dialog.setAttribute("tabindex", "-1");
  showAsk("");
  return { close };
}
