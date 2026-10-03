import { el, clear } from "./dom.js";
import { api } from "./api.js";
import { t } from "./i18n.js";
import { gloss } from "./builtins.js";
import { fold, matches } from "./personas.js";
import { notificationSchedules } from "./persona-display.js";

export function createSchedules({ onChange } = {}) {
  let rows = [];
  const search = el("input", {
    type: "search", class: "persona-search", maxlength: 200,
    "aria-label": t("予定を検索"), i18nPlaceholder: "担当名・予定の内容で探す",
    oninput: () => draw(),
  });
  const status = el("p", { class: "hint", role: "status", hidden: true });
  const list = el("div", { class: "schedule-list" });
  const element = el("div", { class: "panel-body schedules-panel" }, search, status, list);

  function updateRows(next) {
    rows = next;
    draw();
  }

  function changed(payload) {
    updateRows(payload.personas || rows);
    onChange?.(rows);
  }

  async function load() {
    status.hidden = false;
    status.textContent = t("読み込んでいます…");
    try {
      changed(await api.personas());
      status.hidden = true;
    } catch (error) {
      status.textContent = t(error.message || String(error));
    }
  }

  function draw() {
    clear(list);
    const words = String(search.value || "").split(/[\s　]+/).map(fold).filter(Boolean);
    const schedules = rows.flatMap(row => (row.schedules || []).map(schedule => ({ row, schedule })))
      .filter(({ row, schedule }) => words.every(word => matches(row, word) || fold(schedule.note).includes(word)))
      .sort((a, b) => new Date(a.schedule.at) - new Date(b.schedule.at) || a.schedule.id.localeCompare(b.schedule.id));
    if (!schedules.length) list.append(el("p", { class: "hint", label: words.length ? "一致する予定がありません" : "予定はありません" }));
    for (const { row, schedule } of schedules) {
      list.append(el("div", { class: "persona-schedule-row" },
        notificationSchedules({ ...row, schedules: [schedule] }, changed, { owner: gloss(row).name })));
    }
  }

  return { element, load, updateRows };
}
