// Display text shared by the persona list and the timeline's speaker card.
import { el } from "./dom.js";
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

export function checkInHint(row) {
  const schedule = row.dailyCheckIn || {};
  const monthly = row.kind === "person" && schedule.every === "monthly";
  return monthly
    ? t(schedule.talk === "minimal"
      ? "毎月{0}日、本体が月のまとめを書いたあとに起き、まとめと名前が出た投稿を読みます。話すのは、記録を忘れてよいかの確認など、あなたの判断が必要なときだけです。"
      : "毎月{0}日、本体が月のまとめを書いたあとに起き、まとめと名前が出た投稿と自分の記憶を見直して、伝える価値があることを見つけたときに話します。", schedule.on || "")
    : t(schedule.talk === "minimal"
      ? "毎日7時〜21時のどこかで起きます。話すのは、記録を忘れてよいかの確認など、あなたの判断が必要なときだけです。"
      : "毎日7時〜21時のどこかで起き、最近の記録と自分の記憶を見直して、伝える価値があることを見つけたときに話します。");
}
