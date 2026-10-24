import { api } from "./api.js";
import { el, clear, toast } from "./dom.js";
import { t } from "./i18n.js";

export function createCalendarInbox() {
  const view = el("div");
  let disposed = false;
  const field = (label, input) => el("label", { class: "field" }, el("span", { text: label }), input);
  const check = (input, label) => el("label", {}, input, el("span", { text: label }));
  async function load() {
    try {
      const state = await api.calendarInbox();
      if (disposed) return;
      clear(view);
      const settings = state.settings;
      const enabled = el("input", { type: "checkbox" }); enabled.checked = settings.enabled;
      const llm = el("input", { type: "checkbox" }); llm.checked = settings.llmEnabled;
      const calendar = el("input", { value: settings.calendarId || "primary", required: true, maxlength: 320 });
      const zone = el("input", { value: settings.timeZone || "Asia/Tokyo", required: true });
      const chars = el("input", { type: "number", min: 0, max: 5000, value: settings.bodyChars ?? 2000, required: true });
      const prompt = el("textarea", { rows: 6 }, settings.triagePrompt || "");
      let exclusions = [...(settings.excludedEvents || [])];
      const rules = el("div");
      function drawRules() {
        clear(rules);
        if (!exclusions.length) rules.append(el("p", { class: "hint", text: t("機械的に除外する予定はありません。") }));
        exclusions.forEach((rule, index) => rules.append(el("div", { class: "calendar-exclusion" },
          el("p", { text: t("{kind}: {label}", { kind: rule.seriesId ? t("定期予定全体") : t("単発予定"), label: rule.label || rule.eventId }) }),
          el("code", { text: `${rule.calendarId} / ${rule.seriesId || rule.eventId}` }),
          el("button", { type: "button", class: "ghost", text: t("この除外を解除"), onclick: () => { exclusions = exclusions.filter((_, i) => i !== index); drawRules(); } }))));
      }
      drawRules();
      const save = el("button", { type: "submit", class: "primary", text: t("予定の取り込み設定を保存") });
      const advanced = el("details", {},
        el("summary", { text: t("詳細パラメータ・LLM依頼文（通常は変更不要）") }),
        field(t("対象カレンダーID（既定: primary）"), calendar),
        field(t("今日・明日を区切るタイムゾーン（既定: Asia/Tokyo）"), zone),
        field(t("説明文の保存上限（文字数・既定2000）"), chars),
        field(t("LLMへの予定取り込み判定の依頼文（編集できます）"), prompt)
      );
      const form = el("form", { onsubmit: async event => {
        event.preventDefault(); save.disabled = true;
        try {
          await api.saveCalendarInboxSettings({ enabled: enabled.checked, llmEnabled: llm.checked, calendarId: calendar.value.trim(), timeZone: zone.value.trim(), triagePrompt: prompt.value, bodyChars: Number(chars.value), excludedEvents: exclusions });
          toast(t("予定の取り込み設定を保存しました。解除は今後の新しい予定に適用します。")); await load();
        } catch (error) { toast(error.message); save.disabled = false; }
      } }, check(enabled, t("今日・明日の予定を読み取り、暗号化保存し、Hermesへ取り込み候補として渡すことを許可する")),
      check(llm, t("HermesのLLMで予定の取り込み候補を選ぶ（おすすめ）")),
      el("h3", { text: t("機械的に除外する予定・定期予定") }), rules,
      el("p", { class: "hint", text: t("Timelineでチェック付き破棄を選んだ予定・定期予定がここに追加されます。") }),
      advanced, save);
      const settingsPanel = el("details", {}, el("summary", { text: t("予定の除外リスト・詳細設定（変更する場合のみ）") }), form);
      view.append(el("p", { class: "hint", text: settings.enabled ? t("おすすめ設定（{calendar} / {zone}）で予定取り込みが有効です。", { calendar: settings.calendarId || "primary", zone: settings.timeZone || "Asia/Tokyo" }) : t("予定の取り込みは停止しています。「おすすめ設定で有効にする」を押すとそのまま使い始められます。") }), settingsPanel);
      const sync = el("button", { class: settings.enabled ? "primary" : "", text: t("今日・明日の予定を今すぐ確認"), onclick: async () => { sync.disabled = true; try { await api.syncCalendarInbox(); await load(); } catch (error) { toast(error.message); sync.disabled = false; } } }); sync.disabled = !settings.enabled;
      const actionBar = el("div", { class: "actions" });
      if (!settings.enabled) {
        actionBar.append(el("button", { class: "primary", text: t("おすすめ設定で有効にする（設定不要）"), onclick: async event => {
          const btn = event.currentTarget; btn.disabled = true;
          try {
            await api.saveCalendarInboxSettings({ enabled: true, llmEnabled: true, calendarId: settings.calendarId || "primary", timeZone: settings.timeZone || "Asia/Tokyo", triagePrompt: settings.triagePrompt || "", bodyChars: settings.bodyChars ?? 2000, excludedEvents: exclusions });
            toast(t("おすすめ設定で予定の取り込みを有効にしました。")); await load();
          } catch (error) { toast(error.message); btn.disabled = false; }
        } }));
      }
      actionBar.append(sync, el("button", { class: "ghost", text: t("予定の状態を更新"), onclick: load }));
      view.append(actionBar);
      if (state.lastSyncedAt) view.append(el("p", { class: "hint", text: t("最終確認: {time}", { time: new Date(state.lastSyncedAt).toLocaleString() }) }));
      if (state.triagePending) view.append(el("p", { class: "hint", text: t("予定のLLM判定待ち: {count}件", { count: state.triagePending }) }));
      if (state.hasMore) view.append(el("p", { class: "hint", text: t("取得の続きがあります。次のジョブまたは確認ボタンで継続します。") }));
      if (state.lastError) view.append(el("p", { role: "status", text: state.lastError }));
      if (state.nextAttemptAt) view.append(el("p", { class: "hint", text: t("再試行可能: {time}", { time: new Date(state.nextAttemptAt).toLocaleString() }) }));
      for (const item of state.events) {
        const panel = el("article", { class: "settings-block" }, el("h3", { text: item.summary || t("（タイトルなし）") }), el("p", { class: "hint", text: t("Hermes経由のTimeline: {status}", { status: item.timelineStatus || t("Hermes配送待ち") }) }));
        if (item.sourceUrl) {
          try {
            const link = new URL(item.sourceUrl);
            if (link.origin === "https://calendar.google.com") {
              panel.append(el("p", { class: "hint" }, el("a", { href: link.href, target: "_blank", rel: "noopener noreferrer", text: t("元の予定（Google カレンダー）を開く ↗") })));
            }
          } catch {}
        }
        panel.append(el("pre", { class: "mail-body", text: item.body }));
        view.append(panel);
      }
      const confirm = el("input", { type: "checkbox" });
      view.append(el("details", {}, el("summary", { text: t("予定の取り込みデータを削除") }), el("p", { class: "hint", text: t("予定の保存内容・ルール・重複排除履歴を削除して停止します。Google接続とGoogle上の予定、既に届けたTimelineカードは維持します。再び有効にすると同じ予定が再提示される場合があります。") }), check(confirm, t("予定の保存データ削除を確認しました")), el("button", { class: "ghost", text: t("予定の保存データを削除して停止"), onclick: async () => { if (!confirm.checked) { toast(t("削除する内容を確認してください。")); return; } try { await api.clearCalendarInbox(); await load(); } catch (error) { toast(error.message); } } })));
    } catch (error) { if (!disposed) toast(error.message); }
  }
  void load();
  return { element: view, load, dispose() { disposed = true; clear(view); } };
}

