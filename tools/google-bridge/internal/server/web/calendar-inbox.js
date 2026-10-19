import { api } from "./api.js";
import { el, clear, toast } from "./dom.js";

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
      const calendar = el("input", { value: settings.calendarId, required: true, maxlength: 320 });
      const zone = el("input", { value: settings.timeZone, required: true });
      const chars = el("input", { type: "number", min: 0, max: 5000, value: settings.bodyChars, required: true });
      const prompt = el("textarea", { rows: 7 }, settings.triagePrompt);
      let exclusions = [...(settings.excludedEvents || [])];
      const rules = el("div");
      function drawRules() {
        clear(rules);
        if (!exclusions.length) rules.append(el("p", { class: "hint", text: "機械的に除外する予定はありません。" }));
        exclusions.forEach((rule, index) => rules.append(el("div", { class: "calendar-exclusion" },
          el("p", { text: `${rule.seriesId ? "定期予定全体" : "単発予定"}: ${rule.label || rule.eventId}` }),
          el("code", { text: `${rule.calendarId} / ${rule.seriesId || rule.eventId}` }),
          el("button", { type: "button", class: "ghost", text: "この除外を解除", onclick: () => { exclusions = exclusions.filter((_, i) => i !== index); drawRules(); } }))));
      }
      drawRules();
      const save = el("button", { type: "submit", text: "予定の取り込み設定を保存" });
      const form = el("form", { onsubmit: async event => {
        event.preventDefault(); save.disabled = true;
        try {
          await api.saveCalendarInboxSettings({ enabled: enabled.checked, llmEnabled: llm.checked, calendarId: calendar.value.trim(), timeZone: zone.value.trim(), triagePrompt: prompt.value, bodyChars: Number(chars.value), excludedEvents: exclusions });
          toast("予定の取り込み設定を保存しました。解除は今後の新しい予定に適用します。"); await load();
        } catch (error) { toast(error.message); save.disabled = false; }
      } }, check(enabled, "今日・明日の予定を読み取り、暗号化保存し、設定したkakerattaへ取り込み候補として送ることを許可する"),
      field("対象カレンダーID（通常はprimary）", calendar), field("今日・明日を区切るタイムゾーン（例: Asia/Tokyo）", zone),
      field("説明文の保存上限（文字数・0でも日時とタイトルは保持）", chars),
      check(llm, "HermesのLLMで予定の取り込み候補を選ぶ（有効時は内容が設定したモデルへ送られます）"),
      field("LLMへの予定取り込み判定の依頼文（編集できます）", prompt),
      el("h3", { text: "機械的に除外する予定・定期予定" }), rules,
      el("p", { class: "hint", text: "Timelineのチェック付き破棄を次の同期で反映します。予定IDまたは定期予定の元IDで、LLMより先に除外します。解除ボタンを押した後、この設定を保存してください。既に処理済みの回は再表示しません。" }), save);
      const settingsPanel = el("details", {}, el("summary", { text: "予定の取り込みとLLMルール" }), form); settingsPanel.open = !settings.enabled;
      view.append(el("p", { class: "hint", text: "今日0時から明後日0時までを対象に、定期予定は各回へ展開します。同じ予定は翌日も重複表示しません。投稿先は「04 重要メール」のTimeline連携設定を共用します。Googleへの予定登録・更新・削除は行いません。" }), settingsPanel);
      const sync = el("button", { text: "今日・明日の予定を今すぐ確認", onclick: async () => { sync.disabled = true; try { await api.syncCalendarInbox(); await load(); } catch (error) { toast(error.message); sync.disabled = false; } } }); sync.disabled = !settings.enabled;
      view.append(el("div", { class: "actions" }, sync, el("button", { class: "ghost", text: "予定の状態を更新", onclick: load })));
      if (state.lastSyncedAt) view.append(el("p", { class: "hint", text: `最終確認: ${new Date(state.lastSyncedAt).toLocaleString()}` }));
      if (state.triagePending) view.append(el("p", { class: "hint", text: `予定のLLM判定待ち: ${state.triagePending}件` }));
      if (state.hasMore) view.append(el("p", { class: "hint", text: "取得の続きがあります。次のジョブまたは確認ボタンで継続します。" }));
      if (state.lastError) view.append(el("p", { role: "status", text: state.lastError }));
      if (state.nextAttemptAt) view.append(el("p", { class: "hint", text: `再試行可能: ${new Date(state.nextAttemptAt).toLocaleString()}` }));
      for (const item of state.events) view.append(el("article", { class: "settings-block" }, el("h3", { text: item.summary || "（タイトルなし）" }), el("p", { class: "hint", text: `Timeline: ${item.timelineStatus || "未配信"}` }), el("pre", { class: "mail-body", text: item.body })));
      const confirm = el("input", { type: "checkbox" });
      view.append(el("details", {}, el("summary", { text: "予定の取り込みデータを削除" }), el("p", { class: "hint", text: "予定の保存内容・ルール・重複排除履歴を削除して停止します。Google接続とGoogle上の予定、既に届けたTimelineカードは維持します。再び有効にすると同じ予定が再提示される場合があります。" }), check(confirm, "予定の保存データ削除を確認しました"), el("button", { class: "ghost", text: "予定の保存データを削除して停止", onclick: async () => { if (!confirm.checked) { toast("削除する内容を確認してください。"); return; } try { await api.clearCalendarInbox(); await load(); } catch (error) { toast(error.message); } } })));
    } catch (error) { if (!disposed) toast(error.message); }
  }
  void load();
  return { element: view, load, dispose() { disposed = true; clear(view); } };
}
