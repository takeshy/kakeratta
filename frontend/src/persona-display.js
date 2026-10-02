// Display text shared by the persona list and the timeline's speaker card.
import { el, toast } from "./dom.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { capability, toolLabel } from "./tool_labels.js";

const localZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

// A schedule is shown in the zone it was set in, so the card reads the way the
// persona thinks about it.
export function scheduleWhen(schedule) {
  const at = new Date(schedule.at);
  if (Number.isNaN(at.getTime())) return "";
  const locale = getLanguage() === "ja" ? "ja-JP" : "en-US";
  const parts = (options) => {
    try {
      return new Intl.DateTimeFormat(locale, { timeZone: schedule.timeZone || localZone(), ...options }).format(at);
    } catch {
      return new Intl.DateTimeFormat(locale, options).format(at);
    }
  };
  const clock = parts({ hour: "2-digit", minute: "2-digit" });
  if (schedule.personEvent?.annual) return `${t("毎年")} ${parts({ month: "2-digit", day: "2-digit" })} ${clock}`;
  return schedule.daily ? `${t("毎日")} ${clock}` : `${parts({ month: "2-digit", day: "2-digit" })} ${clock}`;
}

export function toolLink(name, label = t(toolLabel(name))) {
  const known = ["web_search", "place_search", "route", "timeline_search", "notes", "schedule", "weather_forecast"];
  const lang = getLanguage();
  const base = capability(name);
  const anchor = known.includes(base) ? `${lang}-tool-${base}` : `${lang}-tools`;
  return el("a", { href: `/manual/?lang=${lang}#${anchor}`, target: "_blank", rel: "noopener noreferrer",
    text: label, title: t("{0} の説明を開く（新しいタブ）", label) });
}

// Both persona views use the same layout and confirmed cancellation operation.
export function notificationSchedules(row, onDeleted) {
  if (!row.schedules?.length) return null;
  return el("div", { class: "notification-schedules" },
    el("p", { class: "hint notification-schedules-heading", label: "この担当の通知予定" }),
    el("div", { class: "notification-schedules-list" }, ...row.schedules.map((schedule) => {
      const text = `${scheduleWhen(schedule)} · ${schedule.note || ""}`.trim();
      return el("span", { class: "chip ghost schedule", text },
        el("button", { type: "button", class: "chip-remove", text: "×", "aria-label": t("予定「{0}」を消す", text),
          onclick: async (event) => {
            const button = event.currentTarget;
            if (button.disabled || !confirm(t("予定「{0}」を消しますか", text))) return;
            button.disabled = true;
            try {
              const payload = await api.deleteSchedule(row.id, schedule.id);
              onDeleted(payload);
            } catch (error) {
              toast(String(error.message || error), "error");
              button.disabled = false;
            }
          } }));
    })));
}
