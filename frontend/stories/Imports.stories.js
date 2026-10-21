import { gmailImportCard, showGmailImport } from "../src/gmail-import.js";
import { getLanguage } from "../src/i18n.js";
import { panel } from "./frame.js";
import { serve } from "./api.js";

export default { title: "Imports" };

// Fictional local examples rendered by the shipped review card and dialog.
function example(calendar) {
  const ja = getLanguage() === "ja";
  return {
    id: calendar ? "sample-event" : "sample-mail", kind: "post", speaker: "user",
    at: new Date().toISOString(),
    text: calendar
      ? (ja ? "## 明日の出張\n- 時間：08:30〜09:00\n- 場所：東京駅\n- 持ち物：充電器、社員証、予約の控え" : "## Tomorrow’s business trip\n- Time: 08:30–09:00\n- Place: Tokyo Station\n- Bring: charger, work ID and reservation details")
      : (ja ? "明日19:00、レストラン はなを2名で予約。\n変更する場合は前日までにご連絡ください。" : "Your table for two at Restaurant Hana is confirmed for tomorrow at 19:00.\nPlease contact us by the day before if your plans change."),
    import: {
      type: calendar ? "calendar" : "gmail", status: "pending",
      subject: calendar ? (ja ? "明日の出張の準備" : "Prepare for tomorrow’s trip") : (ja ? "レストランの予約確認" : "Your dinner reservation"),
      from: calendar ? "Google Calendar" : "Restaurant Hana <booking@example.com>",
      sender: calendar ? undefined : "booking@example.com",
      categoryId: "reservations", categoryLabel: ja ? "予約の確認" : "Reservation confirmations",
      eventId: calendar ? "sample-trip" : undefined,
      url: calendar ? "https://calendar.google.com/calendar/event?eid=sample-trip" : "https://mail.google.com/mail/u/0/#inbox/sample-reservation",
    },
  };
}

function review(calendar, open) {
  for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
  serve();
  const entry = example(calendar);
  const card = gmailImportCard(entry, () => {});
  if (open) showGmailImport(entry, () => {});
  return panel(card);
}

export const GmailCard = { name: "メールの確認カード", render: () => review(false, false) };
export const GmailReview = { name: "メールを取り込む前に確認", render: () => review(false, true) };
export const CalendarCard = { name: "カレンダーの確認カード", render: () => review(true, false) };
export const CalendarReview = { name: "予定を取り込む前に確認", render: () => review(true, true) };
