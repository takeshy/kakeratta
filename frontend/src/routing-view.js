import { avatarNode } from "./avatar.js";
// The router's call about one post, in a sheet of its own.
//
// Who the app chose for a post, whom it passed over and why, are shown apart
// from the timeline rather than inside it: a post and the reasoning about it
// read badly in one column. Each persona still in the room can be told the
// router was wrong about it, which opens the condition dialog.
//
// The sheet can be refreshed while open - the feed does so when the router
// finishes, when the timeline is polled, and when the post is edited - and a
// response that arrives after a newer request is ignored.
import { el, toast, dialogCloseButton, mountOverlay, unmountOverlay } from "./dom.js";
import { api } from "./api.js";
import { t } from "./i18n.js";
import { gloss } from "./builtins.js";

export async function showRouting({ entry, getPersona, onFixCondition, onFixReply, onSelectPersona, onOpenNote }) {
  let record;
  try {
    record = await api.routing(entry.id);
  } catch (error) {
    toast(String(error.message || error), "error");
    return null;
  }
  let open = true;
  let latest = null;
  const backdrop = el("div", { class: "dialog-backdrop" });
  const previousFocus = document.activeElement;
  const close = () => {
    if (!open) return;
    open = false;
    unmountOverlay(backdrop);
    previousFocus?.focus();
  };
  backdrop.addEventListener("click", (event) => { if (event.target === backdrop) close(); });
  backdrop.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); close(); }
    if (event.key !== "Tab") return;
    const focusable = [...backdrop.querySelectorAll("button")].filter((node) => !node.disabled && node.getClientRects().length);
    const first = focusable[0], last = focusable.at(-1);
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  });

  const quote = el("blockquote", { class: "condition-quote" });
  const body = el("div", { class: "routing" });

  function render(current, decision) {
    const text = current.text || "";
    quote.textContent = text.length > 120 ? `${text.slice(0, 120)}…` : text;
    quote.hidden = !text;
    while (body.firstChild) body.firstChild.remove();
    const delivered = decision?.delivered || [];
    const excluded = decision?.excluded || [];
    if (decision?.recorded === false && decision.agent) {
      body.append(el("p", { class: "hint", label: "外部エージェントへの直接の依頼なので、担当には振り分けていません。" }));
      return;
    }
    if (decision?.recorded === false) {
      body.append(el("p", { class: "hint", label: "判断の記録がありません。振り分けの途中なら、終わるとここに出ます。" }));
      return;
    }
    if (!delivered.length && !excluded.length) {
      body.append(el("p", { class: "hint", label: "記録された判断はありません" }));
      return;
    }
    // A persona still in the catalogue can be taught; one that has gone can
    // only be read about.
    const row = (item, verdict, fix) => {
      const persona = getPersona?.(item.id);
      // A built-in persona is named in the display language, as in the feed.
      const name = (persona && gloss(persona).name) || item.name || item.id;
      return el("div", { class: "routing-row" },
        persona && onSelectPersona ? el("button", {
          class: "routing-persona",
          onclick: () => { close(); onSelectPersona(persona); },
        }, avatarNode(persona, { className: "avatar avatar-inline" }), " ", name) : el("span", { class: "who" }, avatarNode(persona || item, { className: "avatar avatar-inline" }), " ", name),
        el("span", { class: "routing-verdict", label: verdict }),
        item.reason ? el("span", { class: "routing-reason", text: item.reason }) : null,
        item.heard ? el("span", { class: "routing-reason", text: t("「{0}」に一致", item.heard) }) : null,
        // A persona the user called was not the router's choice, so it says how.
        item.via ? el("span", { class: "routing-reason", label: item.via === "mention" ? "@で呼ばれました" : "返信で呼ばれました" }) : null,
        // A person has no condition to teach. A post that should have reached
        // one adds keywords; one that should not have offers its words to remove.
        persona && onFixCondition && fix ? el("button", {
          class: "chip ghost routing-fix", label: fix.label,
          onclick: () => { close(); onFixCondition({ persona, entry: current, direction: fix.direction, reason: item.reason || "" }); },
        }) : null);
    };
    // A persona that was called and stayed quiet is the case a person most
    // wants to see: it can be told a reply was wanted.
    for (const item of delivered) {
      // The server says whether the turn has finished and whether it spoke;
      // the page's own timeline may be behind, so it is not consulted.
      const done = item.done === true;
      const said = item.spoke === true;
      const noted = item.noted === true;
      const verdict = !done ? "呼ばれました。返事を考えています…"
        : said && noted ? "呼ばれました。返事とメモあり" : said ? "呼ばれました。返事あり、メモなし"
        : noted ? "呼ばれました。黙ってメモだけ残しました"
        // The persona's own judgement that the post was not its business:
        // "should not have been called" beside it is how the user agrees.
        : item.unrelated === true ? "呼ばれましたが、自分の担当ではないと判断して黙りました"
        : "呼ばれましたが、黙っていました。メモもありません";
      // There is nothing to teach a persona the user called themselves.
      const line = row(item, verdict, item.via ? null : { label: "呼んでほしくなかった", direction: "exclude" });
      const persona = getPersona?.(item.id);
      if (persona && onFixReply && done && !said) {
        line.append(el("button", {
          class: "chip ghost reply-wanted", label: "返事がほしかった",
          onclick: () => { close(); onFixReply({ persona, remark: null, post: current }); },
        }));
      }
      if (persona && onOpenNote) {
        for (const path of item.notes || []) line.append(el("button", {
          class: "chip ghost routing-note", text: `📝 ${path.replace(/\.md$/, "")}`,
          i18nTitle: "この判断で書いたメモを開く",
          onclick: () => { close(); onOpenNote({ persona, path }); },
        }));
      }
      body.append(line);
    }
    for (const item of excluded) body.append(row(item, "呼ばれませんでした", { label: "呼んでほしかった", direction: "include" }));
  }

  // refresh asks again and redraws, unless the sheet has closed or a newer
  // request has been made since.
  async function refresh(current = entry) {
    if (!open) return;
    const request = {};
    latest = request;
    try {
      const decision = await api.routing(entry.id);
      if (!open || latest !== request) return;
      render(current, decision);
    } catch {
      // The sheet stays as it was; a later refresh or reopening tries again.
    }
  }

  render(entry, record);
  const dialog = el("div", { class: "dialog routing-dialog", role: "dialog", "aria-modal": "true", "aria-label": t("担当の判断") },
    dialogCloseButton(close),
    el("h2", { label: "担当の判断" }),
    quote,
    el("p", { class: "hint", label: "この投稿を誰に渡したか、誰に渡さなかったか。@や返信で呼んだ担当は、その理由と一緒に出ます。" }),
    body,
    el("div", { class: "compose-row" }, el("span", { class: "spacer" }), el("button", { class: "button primary", label: "閉じる", onclick: close })));
  backdrop.append(dialog);
  mountOverlay(backdrop);
  dialog.setAttribute("tabindex", "-1");
  dialog.focus?.();
  return { entryId: entry.id, close, refresh, isOpen: () => open };
}
