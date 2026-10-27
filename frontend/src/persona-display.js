// Display text shared by the persona list and the timeline's speaker card.
import { switchControl, el, toast, mountOverlay, unmountOverlay, dialogCloseButton } from "./dom.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { capability, toolLabel } from "./tool_labels.js";

const localZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

// A schedule is shown in the zone it was set in, so the card reads the way the
// persona thinks about it.
export function scheduleWhen(schedule, nextOccurrence = false) {
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
  if (nextOccurrence) return `${parts({ year: "numeric", month: "2-digit", day: "2-digit" })} ${clock}`;
  if (schedule.personEvent?.annual) return `${t("毎年")} ${parts({ month: "2-digit", day: "2-digit" })} ${clock}`;
  return schedule.daily ? `${t("毎日")} ${clock}` : `${parts({ month: "2-digit", day: "2-digit" })} ${clock}`;
}

export function toolLink(name, label = t(toolLabel(name))) {
  const known = ["web_search", "place_search", "timeline_search", "records", "schedule", "weather_forecast"];
  const lang = getLanguage();
  const raw = capability(name);
  const base = raw === "notes" ? "records" : raw;
  const anchor = known.includes(base) ? `${lang}-tool-${base}` : `${lang}-tools`;
  return el("a", { href: `/manual/?lang=${lang}#${anchor}`, target: "_blank", rel: "noopener noreferrer",
    text: label, title: t("{0} の説明を開く（新しいタブ）", label) });
}

// datetime-local is explicitly labelled in the device timezone; the API receives an instant.
export function scheduleInputTime(value) {
 const at = new Date(value);
 const pad = n => String(n).padStart(2, "0");
 return `${at.getFullYear()}-${pad(at.getMonth()+1)}-${pad(at.getDate())}T${pad(at.getHours())}:${pad(at.getMinutes())}`;
}

function scheduleEditor(row, schedule, onChanged) {
 const original = scheduleInputTime(schedule.at);
 const at = el("input", { type: "datetime-local", value: original, required: true });
 const notify = el("input", { type: "checkbox", role: "switch", checked: schedule.notify !== false });
 const save = el("button", { type: "submit", class: "button primary", label: "保存" });
 const previousFocus = document.activeElement;
 const backdrop = el("div", { class: "dialog-backdrop" });
 const close = () => { if (save.disabled) return; unmountOverlay(backdrop); previousFocus?.focus?.(); };
 backdrop.addEventListener("click", event => { if (event.target === backdrop) close(); });
 backdrop.addEventListener("keydown", event => {
  if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); }
  if (event.key === "Tab") {
   const controls = [...backdrop.querySelectorAll("button:not(:disabled), input:not(:disabled)")];
   const first = controls[0], last = controls.at(-1);
   if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
   else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }
 });
 const form = el("form", { class: "schedule-edit-form", onsubmit: async (event) => {
  event.preventDefault();
  if (save.disabled) return;
  const patch = { notify: notify.checked };
  if (at.value !== original) {
   const next = new Date(at.value);
   if (Number.isNaN(next.getTime()) || next.getTime() <= Date.now()) { toast(t("未来の日時を指定してください"), "error"); return; }
   patch.at = next.toISOString();
  }
  save.disabled = at.disabled = notify.disabled = true;
  try { const payload = await api.updateSchedule(row.id, schedule.id, patch); save.disabled = false; close(); onChanged(payload); }
  catch (error) { toast(String(error.message || error), "error"); }
  finally { save.disabled = at.disabled = notify.disabled = false; }
 } },
 el("label", { class: "schedule-date-field" },
  el("span", { class: "schedule-field-label", label: "次の実行日時" }), at,
  el("span", { class: "schedule-zone", text: localZone() })),
 el("label", { class: "schedule-push-field" },
  el("span", { class: "schedule-push-copy" }, el("strong", { label: "Push通知" }),
   el("span", { class: "schedule-push-hint", label: "通知をOFFにしても予定は実行し、タイムラインに投稿します。" })),
  switchControl(notify)),
 schedule.daily || schedule.personEvent?.annual
  ? el("p", { class: "hint", label: "繰り返しの時刻は予定のタイムゾーンで引き継ぎます。年次予定の日付変更は次回だけです。" }) : null,
 el("div", { class: "compose-row" }, save, el("button", { type: "button", class: "button", label: "キャンセル", onclick: close })));
 const dialog = el("div", { class: "dialog schedule-dialog", role: "dialog", "aria-modal": "true", "aria-label": t("日時・通知を編集") },
  dialogCloseButton(close), el("h3", { label: "日時・通知を編集" }),
  el("p", { class: "schedule-dialog-note", text: `${row.name} · ${schedule.note || ""}` }), form);
 backdrop.append(dialog);
 mountOverlay(backdrop);
 at.focus?.();
}

// Both persona views use the same layout and confirmed cancellation operation.
export function notificationSchedules(row, onDeleted, { owner } = {}) {
  if (!row.schedules?.length) return null;
  return el("div", { class: "notification-schedules" },
    owner === undefined ? el("p", { class: "hint notification-schedules-heading", label: "この担当の通知予定" }) : null,
    el("div", { class: "notification-schedules-list" }, ...row.schedules.map((schedule) => {
      const text = `${scheduleWhen(schedule)} · ${schedule.note || ""}`.trim();
      return el("div", { class: "schedule-item" }, el("div", { class: "chip ghost schedule" },
        owner === undefined ? null : el("span", { class: "schedule-owner", text: owner }),
        el("time", { class: "schedule-when", datetime: schedule.at, text: scheduleWhen(schedule, owner !== undefined) }),
        el("span", { class: "schedule-note", text: schedule.note || "" }),
        el("span", { class: "schedule-actions" },
        el("span", { class: "schedule-notify", label: schedule.notify === false ? "通知OFF" : "通知ON" }),
        el("button", { type: "button", class: "schedule-edit-button", html: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m16 3 5 5L8 21H3v-5Z M13 6l5 5"/></svg>', "aria-label": t("日時・通知を編集"), i18nTitle: "日時・通知を編集", onclick: () => scheduleEditor(row, schedule, onDeleted) }),
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
          } }))));
    })));
}
