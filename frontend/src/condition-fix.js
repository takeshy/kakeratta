// Teaching the router from one post.
//
// The router chose, or passed over, a persona for a post, and the person
// disagrees. Rather than rewriting the persona's conditions in the settings,
// they say why here; the AI that builds personas rewrites the condition, the
// person can correct that text by hand. OK saves it and checks the same post;
// a newly selected persona then takes its ordinary turn on that post. If the
// decision is still wrong, the router's reason is shown and they try again.
//
// A person has no condition: it is reached by its words. For a post that
// should have reached one, the same steps add keywords instead - words from
// the post and their near words - and the check is whether the post now
// contains one of them.
//
// A post that should not have reached a person reached it by a word. The
// words of the persona that are in the post are listed, and the person
// chooses which to remove - or none: a word like 母 is also in 母校, and the
// persona already ends a turn that is not about them without a word. The
// persona's own name is not removed here. What was said on the post stays.
import { clear, el, toast, dialogCloseButton, mountOverlay, unmountOverlay } from "./dom.js";
import { api } from "./api.js";
import { getLanguage, t } from "./i18n.js";
import { joinTerms, splitTerms } from "./persona-editor.js";

const EXCERPT = 300;

export function showConditionFix({ persona, entry, direction, reason = "", onSaved }) {
  const include = direction === "include";
  const person = persona.kind === "person" && include;
  const unheard = persona.kind === "person" && !include;
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
    const focusable = [...backdrop.querySelectorAll("button, textarea, input, a[href]")]
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
    if (person) {
      lines.push(t("次の投稿がこの人物の担当に届くよう、キーワードを足してほしい：「{0}」", text));
      lines.push(t("投稿に書かれている語をそのままの形で少なくとも1つ選び、その言い換えや類似語とあわせて keywords に加えてください。"));
      if (note) lines.push(t("本人の補足：「{0}」", note));
      lines.push(t("既存の呼び名とキーワード、他の項目は変えないでください。"));
      return lines.join("\n");
    }
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
      const value = person ? added(result?.draft?.keywords) : String(result?.draft?.domain || "").trim();
      if (!value || (!person && value === base.domain)) {
        error.textContent = [t(person ? "キーワードを足せませんでした" : "条件を変えられませんでした"), result?.message || ""].filter(Boolean).join(" ");
        return null;
      }
      return value;
    } catch (e) {
      error.textContent = e.message;
      return null;
    } finally {
      setBusy(false, button);
    }
  }

  // added is what a proposal adds to the saved keywords, as the text shown.
  function added(keywords) {
    const have = new Set(saved.keywords || []);
    const words = (Array.isArray(keywords) ? keywords : []).map((word) => String(word).trim()).filter((word) => word && !have.has(word));
    return joinTerms(words);
  }

  // current is what the dialog changes, as it stands.
  const current = () => person ? joinTerms(saved.keywords) || t("（なし）") : saved.domain || "";
  const currentLabel = person ? "今のキーワード" : "今の判定条件";

  // 1. Why. The post, what the router said, the condition as it is, and room
  // to say what should have happened.
  function showAsk(notice) {
    controls = [];
    error.textContent = "";
    const noteLabel = person ? "どの語で呼んでほしいか（任意）" : "理由や、どう変えてほしいか";
    const note = el("textarea", {
      class: "skill-input condition-note", rows: 3, maxlength: 2000, "aria-label": t(noteLabel),
      i18nPlaceholder: person ? "例：「釣り」の話は佐藤さんのことが多い。" : include ? "例：飲食店の話でも、場所や移動に関係するときは反応してほしい。" : "例：本のタイトルが出るだけの話には反応しないでほしい。",
    });
    controls.push(note);
    const make = action(person ? "キーワードを作る" : "条件を作る", "button primary condition-build", async () => {
      if (busy) return;
      const domain = await build(request(note.value.trim()), saved, make, "考えています…");
      if (domain) showProposal(domain);
    });
    fill(
      notice ? el("p", { class: "hint warn condition-notice", role: "alert", text: notice }) : null,
      el("blockquote", { class: "condition-quote", text: excerpt }),
      include && reason ? el("p", { class: "hint", text: t("担当の判断：{0}", reason) }) : null,
      field(currentLabel, el("p", { class: "condition-current", text: current() })),
      field(noteLabel, note),
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
    const nextLabel = person ? "足すキーワード（読点区切り）" : "新しい判定条件";
    const next = el("textarea", { class: "skill-input condition-new", rows: 4, maxlength: 2000, "aria-label": t(nextLabel) });
    next.value = domain;
    controls.push(next);
    const ask = el("textarea", {
      class: "skill-input condition-request", rows: 3, maxlength: 2000, "aria-label": t("AIへの依頼"),
      i18nPlaceholder: person ? "例：「キャンプ」も足して。" : "例：飲食店の名前だけでは反応しないようにして。",
    });
    controls.push(ask);
    const askBuild = action(person ? "キーワードを作る" : "条件を作る", "button primary condition-rebuild", async () => {
      if (busy) return;
      const content = ask.value.trim();
      if (!content) return;
      const base = person ? { ...saved, keywords: [...(saved.keywords || []), ...splitTerms(next.value)] } : { ...saved, domain: next.value.trim() };
      const changed = await build(content, base, askBuild, "考えています…");
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
      field(currentLabel, el("p", { class: "condition-current", text: current() })),
      field(nextLabel, next),
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
      if (person) {
        const keywords = [...new Set([...(saved.keywords || []), ...splitTerms(domain)])];
        if (keywords.length !== (saved.keywords || []).length) {
          const stored = await api.savePersonaDraft({ ...saved, keywords }, persona.id);
          saved = { ...saved, ...(stored && typeof stored === "object" ? stored : {}), keywords: stored?.keywords || keywords };
          await onSaved?.();
        }
      } else if (domain !== saved.domain) {
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
		if (include) await api.applyRouting(entry.id, check.token);
		if (closed) return;
		toast(person ? t("キーワードを保存し、「{0}」に一致したので担当を呼びました", check.heard || "")
		  : t(include ? "担当条件を保存し、この投稿に担当を呼びました" : "担当条件を保存しました。この投稿は狙いどおりに振り分けられます"));
        close();
        return;
      }
      failure = person
        ? t("足したキーワードはどれもこの投稿に書かれていないので、{0} に届きませんでした。", persona.name)
        : include
        ? t("保存した条件では、この投稿は {0} に届きませんでした。理由：{1}", persona.name, check.reason || "")
        : t("保存した条件でも、この投稿は {0} に届きました。", persona.name);
      showAsk(failure);
    } catch (e) {
      error.textContent = e.message;
    } finally {
      setBusy(false, button);
    }
  }

  // The words a person was reached by, from a fresh check: the record keeps
  // only the first, and the persona may have changed since.
  async function showWords(notice) {
    controls = [];
    error.textContent = "";
    const quote = el("blockquote", { class: "condition-quote", text: excerpt });
    fill(quote, el("p", { class: "hint", label: "確認しています…" }));
    let check;
    try {
      check = await api.checkRouting(entry.id, persona.id);
    } catch (e) {
      if (!closed) error.textContent = e.message;
      return;
    }
    if (closed) return;
    const words = check?.words?.length ? check.words : check?.heard ? [check.heard] : [];
    if (!check?.answered || !check.delivered || !words.length) {
      fill(quote,
        el("p", { class: "hint", text: t("今の呼び名とキーワードでは、この投稿は {0} に届きません。", persona.name) }),
        el("div", { class: "compose-row" }, el("span", { class: "spacer" }), action("閉じる", "chip ghost", close)));
      return;
    }
    const kindOf = (word) => (saved.keywords || []).includes(word) ? "キーワードの1つ"
      : (saved.aliases || []).includes(word) ? "呼び名の1つ" : "担当の名前";
    const chosen = new Set();
    const remove = action("外す", "button primary condition-remove", () => void removeWords([...chosen], remove));
    remove.disabled = true;
    const rows = words.map((word) => {
      const kind = kindOf(word);
      const box = el("input", { type: "checkbox", disabled: kind === "担当の名前" });
      if (kind !== "担当の名前") controls.push(box);
      box.addEventListener("change", () => {
        if (box.checked) chosen.add(word); else chosen.delete(word);
        remove.disabled = busy || !chosen.size;
      });
      return el("label", { class: "condition-word" }, box,
        el("span", { text: word }), el("span", { class: "routing-verdict", label: kind }));
    });
    const onlyName = words.every((word) => kindOf(word) === "担当の名前");
    fill(
      notice ? el("p", { class: "hint warn condition-notice", role: "alert", text: notice }) : null,
      quote,
      field("この投稿に一致した語", el("div", { class: "condition-words" }, ...rows)),
      el("p", { class: "hint", label: onlyName
        ? "担当の名前で届いたので、ここで外せる語はありません。名前は編集画面で変えられます。"
        : "外すと、その語だけの投稿は今後届かなくなります。呼び名は@での指名にも使われます。残しても、その人と関係のない投稿なら担当は発言もメモもせずに終えます。" }),
      el("div", { class: "compose-row" }, onlyName ? null : remove, el("span", { class: "spacer" }), action(onlyName ? "閉じる" : "残す", "chip ghost", close)),
    );
    rows.find((row) => !row.firstChild.disabled)?.firstChild.focus();
  }

  // Remove the chosen words, then check the post again. Still reaching the
  // person by another word goes back to the list with that said.
  async function removeWords(words, button) {
    if (!words.length || busy) return;
    setBusy(true, button, "保存しています…");
    error.textContent = "";
    try {
      const drop = (terms) => (terms || []).filter((term) => !words.includes(term));
      const next = { ...saved, aliases: drop(saved.aliases), keywords: drop(saved.keywords) };
      const stored = await api.savePersonaDraft(next, persona.id);
      saved = { ...next, ...(stored && typeof stored === "object" ? stored : {}) };
      await onSaved?.();
      if (closed) return;
      const check = await api.checkRouting(entry.id, persona.id);
      if (closed) return;
      if (check?.answered && !check.delivered) {
        toast(t("「{0}」を外しました。この投稿はもう {1} に届きません", joinTerms(words), persona.name));
        close();
        return;
      }
      const still = check?.words?.length ? check.words : check?.heard ? [check.heard] : [];
      setBusy(false, button);
      // The list is drawn again from a fresh check, with its own buttons.
      await showWords(still.length ? t("「{0}」を外しましたが、「{1}」でも一致します。", joinTerms(words), joinTerms(still)) : "");
    } catch (e) {
      error.textContent = e.message;
      setBusy(false, button);
    }
  }

  dialog.append(dialogCloseButton(close), el("h2", { text: title }), body, error);
  backdrop.append(dialog);
  mountOverlay(backdrop);
  dialog.setAttribute("tabindex", "-1");
  if (unheard) void showWords("");
  else showAsk("");
  return { close };
}
