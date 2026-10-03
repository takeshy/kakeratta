import { createSchedules } from "../src/schedules.js";
import { serve } from "./api.js";
import { sheet } from "./frame.js";
import { personas, person, tools } from "./fixtures.js";

export default { title: "Schedules", parameters: { layout: "fullscreen" } };

function panelOf(rows, after) {
  for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
  serve({ personas: rows, tools });
  const panel = createSchedules();
  void panel.load().then(() => after?.(panel.element));
  return sheet("予定一覧", panel.element);
}

// Interleaved owners make the chronological ordering visible; edits stay in the fixture API.
function scheduleRows() {
  const at = hours => new Date(Date.now() + hours * 3600000).toISOString();
  return [
    { ...personas[0], schedules: [
      { id: "dinner", at: at(30), timeZone: "Asia/Tokyo", note: "週末の夕食のお店を探す", notify: false },
      { id: "lunch", at: at(2), timeZone: "Asia/Tokyo", note: "ランチの予約を確認する", notify: true },
    ] },
    { ...person(), schedules: [
      { id: "present", at: at(5), timeZone: "Asia/Tokyo", note: "誕生日のプレゼントを選ぶ", notify: true },
    ] },
  ];
}

export const Overview = { name: "予定一覧・日時順", render: () => panelOf(scheduleRows()) };
export const Empty = { name: "予定なし", render: () => panelOf([]) };
export const Edit = { name: "予定の編集・通知ON", render: () => panelOf(scheduleRows(), element => element.querySelector(".schedule-edit-button").click()) };
export const EditMuted = { name: "予定の編集・通知OFF", render: () => panelOf(scheduleRows(), element => [...element.querySelectorAll(".schedule-edit-button")].at(-1).click()) };
