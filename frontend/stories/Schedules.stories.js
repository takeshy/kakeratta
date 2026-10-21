import { createSchedules } from "../src/schedules.js";
import { serve } from "./api.js";
import { sheet } from "./frame.js";
import { personas, person, tools } from "./fixtures.js";
import { getLanguage, t } from "../src/i18n.js";

export default { title: "Schedules", parameters: { layout: "fullscreen" } };

function panelOf(rows, after) {
  for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
  serve({ personas: rows, tools });
  const panel = createSchedules();
  void panel.load().then(() => after?.(panel.element));
  return sheet(t("予定一覧"), panel.element);
}

// Interleaved owners make the chronological ordering visible; edits stay in the fixture API.
function scheduleRows() {
  const ja = getLanguage() === "ja";
  const at = hours => new Date(Date.now() + hours * 3600000).toISOString();
  return [
    { ...personas.find(row => row.id === "gourmet"), schedules: [
      { id: "dinner", at: at(30), timeZone: "Asia/Tokyo", note: ja ? "週末の夕食のお店を探す" : "Find a restaurant for the weekend", notify: false },
      { id: "lunch", at: at(2), timeZone: "Asia/Tokyo", note: ja ? "ランチの予約を確認する" : "Check the lunch reservation", notify: true },
    ] },
    { ...person(), schedules: [
      { id: "present", at: at(5), timeZone: "Asia/Tokyo", note: ja ? "誕生日のプレゼントを選ぶ" : "Choose a birthday present", notify: true },
    ] },
  ];
}

export const Overview = { name: "予定一覧・日時順", render: () => panelOf(scheduleRows()) };
export const Empty = { name: "予定なし", render: () => panelOf([]) };
export const Edit = { name: "予定の編集・通知ON", render: () => panelOf(scheduleRows(), element => element.querySelector(".schedule-edit-button").click()) };
export const EditMuted = { name: "予定の編集・通知OFF", render: () => panelOf(scheduleRows(), element => [...element.querySelectorAll(".schedule-edit-button")].at(-1).click()) };
