import { api } from "./api.js";
import { el, clear, toast } from "./dom.js";
import { lang, t } from "./i18n.js";

// Kept apart from i18n.js because the status 却下 and the button 却下 differ in English.
const statusNames = { pending: ["確認待ち", "Awaiting review"], approved: ["承認済み・未実行", "Approved, not run"], succeeded: ["実行済み", "Done"], rejected: ["却下", "Rejected"], expired: ["期限切れ・接続変更", "Expired or connection changed"], failed: ["失敗", "Failed"], conflict: ["予定が変更されました", "The event has changed"], unknown: ["結果不明・Googleで確認してください", "Result unknown; check in Google"], executing: ["実行中", "Running"] };
const typeNames = { gmail_send: ["メールを送信", "Send email"], gmail_draft: ["Gmailに下書きを保存", "Save Gmail draft"], calendar_create: ["予定を作成", "Create event"], calendar_update: ["予定を変更", "Update event"], calendar_delete: ["予定を削除", "Delete event"] };
const named = (names, key) => names[key]?.[lang() === "en" ? 1 : 0] || key;

export function createActions() {
  const view = el("div");
  let disposed = false;
  const field = (name, value) => el("div", { class: "review-field" }, el("strong", { text: name }), el("pre", { text: value || t("（なし）") }));
  async function load() {
    try {
      const { actions } = await api.googleActions();
      if (disposed) return;
      clear(view);
      view.append(el("button", { class: "ghost", text: t("履歴を更新"), onclick: load }));
      if (!actions.length) view.append(el("p", { class: "hint", text: t("Hermesに直接頼んで実行したメール送信・予定変更がここに記録されます。") }));
      for (const action of [...actions].sort((a, b) => b.createdAt.localeCompare(a.createdAt))) {
        const card = el("article", { class: "settings-block action-review" }, el("h3", { text: named(typeNames, action.type) }), el("p", { text: named(statusNames, action.status) }));
        if (action.directRequest) card.append(field(t("あなたの依頼"), action.directRequest));
        card.append(el("p", { class: "hint", text: t("実行: {time}", { time: new Date(action.createdAt).toLocaleString() }) }));
        const p = action.payload;
        if (p?.mail) {
          card.append(field(t("送信元"), p.from), field(t("宛先"), p.mail.to.join("\n")), field("CC", (p.mail.cc || []).join("\n")), field(t("件名"), p.mail.subject), field(t("本文"), p.mail.body));
          if (p.threadId) card.append(field(t("返信先のメッセージ / スレッド"), `${p.mail.replyToMessageId} / ${p.threadId}`), field("In-Reply-To", p.inReplyTo));
          card.append(el("p", { class: "hint", text: action.type === "gmail_draft" ? t("承認後、Googleに下書きを保存します。送信は別の操作案と承認が必要です。") : t("承認後、上記の宛先へメールを送信します。取り消しはできません。") }));
        } else if (p) {
          card.append(field(t("対象カレンダー"), `${p.calendarName} / ${p.calendarId}`), field(t("カレンダーのタイムゾーン"), p.calendarTimeZone));
          if (p.eventId) card.append(field(t("対象の予定"), p.eventId));
          if (p.before) card.append(field(t("変更前"), JSON.stringify(p.before, null, 2)));
          if (p.event) card.append(field(t("作成・変更後"), JSON.stringify(p.event, null, 2)));
          const impact = { none: t("招待の更新通知は要求しません。Googleの仕様により一部の通知が届く場合があります。"), all: t("全参加者への招待・変更・取消通知を要求します。"), externalOnly: t("Google Calendarを使わない参加者への通知を要求します。") }[p.sendUpdates];
          card.append(el("p", { class: "hint", text: t("通知: {impact} 終日予定の終了日は、その日を含みません。{meet}", { impact, meet: p.createMeet ? t(" 新しいGoogle Meetリンクを作成します。") : t(" 会議リンクを新規生成しません。既存リンクは維持します。") }) }));
          if (p.before?.recurringEventId) card.append(el("p", { class: "hint", text: t("繰り返し予定のこの1回だけを操作します。") }));
        }
        card.append(el("details", {}, el("summary", { text: t("操作ID・監査情報") }), el("pre", { text: JSON.stringify({ id: action.id, payloadHash: action.payloadHash, connectionId: action.connectionId, proposedBy: action.proposedBy, decidedBy: action.decidedBy, executedBy: action.executedBy, consumedAt: action.consumedAt, providerId: action.providerId }, null, 2) })));
        const run = async (button, operation) => { button.disabled = true; try { await operation(); await load(); } catch (error) { toast(error.message); button.disabled = false; } };
        if (action.status === "pending") {
          card.append(el("div", { class: "actions" },
            el("button", { text: t("この内容を承認"), onclick: e => run(e.currentTarget, () => api.decideGoogleAction(action.id, action.payloadHash, true)) }),
            el("button", { class: "ghost", text: t("却下"), onclick: e => run(e.currentTarget, () => api.decideGoogleAction(action.id, action.payloadHash, false)) })));
        } else if (action.status === "approved") {
          card.append(el("button", { text: t("承認済みの内容を実行"), onclick: e => run(e.currentTarget, () => api.executeGoogleAction(action.id, action.payloadHash)) }));
        }
        if (action.status === "unknown") card.append(el("p", { class: "secret-note", text: t("自動再送は停止しています。Googleで送信済みメール・下書き・予定を確認してください。同じ操作案の再実行は新たな書き込みを行いません。") }));
        view.append(card);
      }
    } catch (error) { if (!disposed) toast(error.message); }
  }
  void load();
  return { element: view, load, dispose() { disposed = true; clear(view); } };
}
