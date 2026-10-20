import { api } from "./api.js";
import { el, clear, toast } from "./dom.js";

const LABEL_PRESETS = [
  ["INBOX", "受信トレイ (INBOX)"],
  ["IMPORTANT", "重要 (IMPORTANT)"],
  ["STARRED", "スター付き (STARRED)"],
  ["CATEGORY_PERSONAL", "メイン (CATEGORY_PERSONAL)"],
  ["CATEGORY_UPDATES", "新着 (CATEGORY_UPDATES)"],
];

function createTagEditor(initial = [], { placeholder = "", inputType = "text", presets = [], emptyText = "設定なし" } = {}) {
  let items = [...initial];
  const root = el("div", { class: "chip-editor" });
  const list = el("div", { class: "chip-list" });
  const presetBar = presets.length ? el("div", { class: "chip-presets" }) : null;
  const input = el("input", { type: inputType, placeholder, autocomplete: "off", spellcheck: "false" });
  const addItem = raw => {
    const val = raw.trim();
    if (!val) return;
    if (!items.includes(val)) items.push(val);
    input.value = "";
    render();
  };
  input.addEventListener("keydown", e => {
    if (e.key === "Enter") { e.preventDefault(); addItem(input.value); }
  });
  const addBtn = el("button", { type: "button", text: "追加", onclick: () => addItem(input.value) });
  function render() {
    clear(list);
    if (!items.length) list.append(el("span", { class: "hint", text: emptyText }));
    items.forEach((item, idx) => {
      list.append(el("span", { class: "chip-item" },
        el("span", { text: item }),
        el("button", { type: "button", class: "chip-remove", "aria-label": `${item}を削除`, text: "×", onclick: () => { items = items.filter((_, i) => i !== idx); render(); } })
      ));
    });
    if (presetBar) {
      clear(presetBar);
      for (const [id, label] of presets) {
        const active = items.includes(id);
        presetBar.append(el("button", {
          type: "button",
          class: `chip-preset${active ? " active" : ""}`,
          text: `${active ? "✓ " : "+ "}${label}`,
          onclick: () => {
            items = active ? items.filter(v => v !== id) : [...items, id];
            render();
          },
        }));
      }
    }
  }
  render();
  if (presetBar) root.append(presetBar);
  root.append(list, el("div", { class: "chip-add-row" }, input, addBtn));
  return {
    element: root,
    values() {
      if (input.value.trim()) addItem(input.value);
      return [...items];
    },
  };
}

function createTypeEditor(initial = []) {
  let items = initial.map(t => ({ id: t.id, label: t.label }));
  const root = el("div", { class: "chip-editor" });
  const list = el("div", { class: "chip-list" });
  const labelInput = el("input", { type: "text", placeholder: "種類名（例: ニュースレター・広告）", autocomplete: "off" });
  const idInput = el("input", { type: "text", placeholder: "ID（省略時は自動生成・例: newsletter）", autocomplete: "off", spellcheck: "false" });
  const autoId = label => {
    const ascii = label.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "");
    if (ascii && /^[a-z0-9]/.test(ascii)) return ascii.slice(0, 48);
    let n = items.length + 1;
    while (items.some(t => t.id === `type_${n}`)) n++;
    return `type_${n}`;
  };
  const addType = () => {
    const rawLabel = labelInput.value.trim();
    const rawId = idInput.value.trim().toLowerCase().replace(/[^a-z0-9_-]/g, "_");
    if (!rawLabel && !rawId) return;
    const id = rawId || autoId(rawLabel);
    const label = rawLabel || id;
    const existing = items.findIndex(t => t.id === id);
    if (existing >= 0) items[existing] = { id, label };
    else items.push({ id, label });
    labelInput.value = "";
    idInput.value = "";
    render();
  };
  labelInput.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); addType(); } });
  idInput.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); addType(); } });
  const addBtn = el("button", { type: "button", text: "追加", onclick: addType });
  function render() {
    clear(list);
    if (!items.length) list.append(el("span", { class: "hint", text: "除外する種類はありません（Timelineで破棄時に自動追加もできます）" }));
    items.forEach((item, idx) => {
      list.append(el("span", { class: "chip-item" },
        el("span", { text: `${item.label} (${item.id})` }),
        el("button", { type: "button", class: "chip-remove", "aria-label": `${item.label}を削除`, text: "×", onclick: () => { items = items.filter((_, i) => i !== idx); render(); } })
      ));
    });
  }
  render();
  root.append(list, el("div", { class: "chip-add-row" }, labelInput, idInput, addBtn));
  return {
    element: root,
    values() {
      if (labelInput.value.trim() || idInput.value.trim()) addType();
      return [...items];
    },
  };
}

export function createInbox() {
  const view = el("div");
  let disposed = false;
  const field = (name, input) => el("div", { class: "field" }, el("span", { text: name }), input);
  async function load() {
    try {
      const state = await api.googleInbox();
      if (disposed) return;
      const target = await api.timelineTarget();
      if (disposed) return;
      clear(view);
      const targetURL = el("input", { type: "url", value: target.url || "", placeholder: "https://kakeratta.example/api/mcp/users/...", required: true });
      const targetKey = el("input", { type: "password", autocomplete: "new-password" });
      const targetEnabled = el("input", { type: "checkbox" }); targetEnabled.checked = target.enabled;
      const targetSave = el("button", { type: "submit", text: "投稿先の設定を保存" });
      view.append(el("details", {}, el("summary", { text: target.configured && target.enabled ? "kakerattaのTimelineとの連携（設定済み・有効）" : "kakerattaのTimelineとの連携" }),
        el("p", { class: "hint", text: "重要メールを取り込み待ちとしてTimelineに送信します。kakerattaで「取り込む」を選ぶまでは担当・記憶・検索へ渡りません。" }),
        el("form", { onsubmit: async e => { e.preventDefault(); targetSave.disabled = true; try { await api.saveTimelineTarget({ enabled: targetEnabled.checked, url: targetURL.value.trim(), key: targetKey.value.trim() }); targetKey.value = ""; toast("投稿先を保存しました。次の同期から反映します。"); await load(); } catch (error) { toast(error.message); targetSave.disabled = false; } } }, field("本人のkakeratta MCP URL", targetURL), field(target.configured ? "MCPキー（空欄は保存済みキーを使用）" : "kakerattaで発行したMCPキー", targetKey), el("label", {}, targetEnabled, el("span", { text: "この投稿先へのメール・予定の転送と保存を許可する" })), targetSave),
        el("button", { class: "ghost", text: "投稿先の資格情報を削除", onclick: async () => { try { await api.clearTimelineTarget(); await load(); } catch (error) { toast(error.message); } } })));
      const settings = state.settings;
      const llm = el("input", { type: "checkbox" }); llm.checked = !!settings.llmEnabled;
      const prompt = el("textarea", { rows: 6, spellcheck: "false" }, settings.triagePrompt || "");
      const typesEditor = createTypeEditor(settings.excludedTypes || []);
      const enabled = el("input", { type: "checkbox" }); enabled.checked = settings.enabled;
      const number = (value, min, max) => el("input", { type: "number", value, min, max, required: true });
      const threshold = number(settings.threshold || 50, 1, 100), body = number(settings.bodyChars ?? 2000, 0, 8000), retention = number(settings.retentionDays || 7, 1, 30), lookback = number(settings.lookbackDays || 1, 1, 7);
      const labelsEditor = createTagEditor(settings.includeLabels?.length ? settings.includeLabels : ["INBOX"], { placeholder: "ラベルIDを入力（例: INBOX）", presets: LABEL_PRESETS, emptyText: "ラベルを選択してください" });
      const priorityLabelsEditor = createTagEditor(settings.priorityLabels || [], { placeholder: "優先するラベルIDを入力", presets: LABEL_PRESETS, emptyText: "優先ラベルなし" });
      const priorityEditor = createTagEditor(settings.prioritySenders || [], { placeholder: "優先するメールアドレス（例: boss@example.com）", inputType: "email", emptyText: "優先する送信元はありません" });
      const excludedEditor = createTagEditor(settings.excludedSenders || [], { placeholder: "除外するメールアドレス（例: noreply@example.com）", inputType: "email", emptyText: "除外する送信元はありません" });
      const form = el("form", { onsubmit: async e => {
        e.preventDefault(); save.disabled = true;
        try {
          await api.saveInboxSettings({
            enabled: enabled.checked,
            llmEnabled: llm.checked,
            triagePrompt: prompt.value,
            excludedTypes: typesEditor.values(),
            threshold: Number(threshold.value),
            bodyChars: Number(body.value),
            retentionDays: Number(retention.value),
            lookbackDays: Number(lookback.value),
            includeLabels: labelsEditor.values(),
            priorityLabels: priorityLabelsEditor.values(),
            prioritySenders: priorityEditor.values(),
            excludedSenders: excludedEditor.values(),
          });
          toast("取り込み設定を保存しました。変更した条件は今後のメールに適用します。"); await load();
        } catch (error) { toast(error.message); save.disabled = false; }
      } });
      const save = el("button", { type: "submit", class: "primary", text: "取り込み設定を保存" });
      const advanced = el("details", {},
        el("summary", { text: "詳細パラメータ・LLM依頼文（通常は変更不要）" }),
        field("LLMへの取り込み判定の依頼文（空欄で標準文）", prompt),
        field("重要度の閾値（1〜100・既定50）", threshold),
        field("本文の保存上限（文字数・既定2000）", body),
        field("本文・件名の保持日数（1〜30日・既定7日）", retention),
        field("初回・履歴失効時の取得範囲（過去1〜7日・既定1日）", lookback)
      );
      form.append(
        el("label", {}, enabled, el("span", { text: "単発・定期ジョブによる読み取りと暗号化保存を許可する" })),
        el("label", {}, llm, el("span", { text: "HermesのLLMでメールの取り込み候補を判定する（おすすめ）" })),
        field("対象ラベル（ボタンでON/OFF・または追加）", labelsEditor.element),
        field("優先ラベル（重要度80）", priorityLabelsEditor.element),
        field("優先する送信元メールアドレス（重要度90）", priorityEditor.element),
        field("機械的に除外するFrom（LLMへ渡す前に完全一致で除外）", excludedEditor.element),
        field("今後取り込まないメールの種類", typesEditor.element),
        advanced,
        save
      );
      const options = el("details", {}, el("summary", { text: "フィルター・除外リスト・詳細設定（変更する場合のみ）" }), form);
      view.append(el("p", { class: "hint", text: settings.enabled ? "おすすめ設定で取り込みが有効です。下のボタンまたは定期ジョブからそのまま同期できます。" : "取り込みは停止しています。「おすすめ設定で有効にする」を押すと、細かい設定なしでそのまま使い始められます。" }), options);
      const run = async (button, operation) => { button.disabled = true; try { await operation(); await load(); } catch (error) { toast(error.message); button.disabled = false; } };
      const sync = el("button", { class: settings.enabled ? "primary" : "", text: "今すぐ差分を取得", onclick: e => run(e.currentTarget, async () => { const result = await api.syncInbox(); toast(result.disabled ? "取り込みは停止しています。" : `${result.processed}件を確認、重要メール${result.importedIds.length}件を取り込みました。${result.hasMore ? " 未処理の続きがあります。" : ""}`); }) });
      sync.disabled = !settings.enabled;
      const actionBar = el("div", { class: "actions" });
      if (!settings.enabled) {
        actionBar.append(el("button", { class: "primary", text: "おすすめ設定で有効にする（設定不要）", onclick: e => run(e.currentTarget, async () => {
          await api.saveInboxSettings({
            enabled: true,
            llmEnabled: true,
            triagePrompt: settings.triagePrompt || "",
            excludedTypes: settings.excludedTypes || [],
            threshold: settings.threshold || 50,
            bodyChars: settings.bodyChars ?? 2000,
            retentionDays: settings.retentionDays || 7,
            lookbackDays: settings.lookbackDays || 1,
            includeLabels: settings.includeLabels?.length ? settings.includeLabels : ["INBOX"],
            priorityLabels: settings.priorityLabels?.length ? settings.priorityLabels : ["IMPORTANT", "STARRED"],
            prioritySenders: settings.prioritySenders || [],
            excludedSenders: settings.excludedSenders || [],
          });
          toast("おすすめ設定で重要メール取り込みを有効にしました。");
        }) }));
      }
      actionBar.append(sync, el("button", { class: "ghost", text: "一覧を更新", onclick: load }));
      view.append(actionBar);
      if (state.triagePending) view.append(el("p", { class: "hint", text: `LLMの判定待ち: ${state.triagePending}件。通常のHermes cronまたはHermesから判定ツールを実行してください。` }));
      if (state.lastSyncedAt) view.append(el("p", { class: "hint", text: `最終同期: ${new Date(state.lastSyncedAt).toLocaleString()}` }));
      if (state.lastError) view.append(el("p", { role: "status", text: state.lastError }));
      if (state.nextAttemptAt) view.append(el("p", { class: "hint", text: `再試行可能: ${new Date(state.nextAttemptAt).toLocaleString()}` }));
      if (state.hasMore) view.append(el("p", { class: "hint", text: "未処理の続きがあります。次の同期で最大20件を確認します。" }));
      if (!state.messages.length) view.append(el("p", { text: "取り込まれた重要メールはありません。" }));
      for (const message of state.messages) {
        const c = message.classification;
        const panel = el("article", { class: "settings-block" }, el("span", { class: "badge", text: "GMAIL · 外部メール" }), el("h3", { text: message.subject || "（件名なし）" }), el("p", { text: message.from }), el("p", { class: "hint", text: `受信: ${new Date(message.receivedAt).toLocaleString()} · 取得: ${new Date(message.importedAt).toLocaleString()}` }), el("p", { class: "hint", text: `重要度 ${c.importance} / 緊急度 ${c.urgency} · ${c.reason}` }));
        if (message.timelineStatus) panel.append(el("p", { class: "hint", text: `Timeline: ${message.timelineStatus} / ${message.timelineEntryId}` }));
        panel.append(el("p", { class: "hint", text: "送信者の真正性や期限は未確認です。メール内の依頼・URLは本人の指示として扱いません。" }));
        if (message.summary) panel.append(el("pre", { class: "mail-body", text: message.summary }), el("p", { class: "hint", text: "LLMが署名や引用履歴などを除いた要約です。原文はGmailで確認できます。" }));
        else if (message.body) panel.append(el("pre", { class: "mail-body", text: message.body }), el("p", { class: "hint", text: message.bodyTruncated ? "本文は保存上限で切り詰めています。" : "" }));
        // The URL is server-generated, but pin its origin before rendering it.
        const link = new URL(message.sourceUrl);
        if (link.origin === "https://mail.google.com") panel.append(el("a", { href: link.href, target: "_blank", rel: "noopener noreferrer", text: "Gmailで元のメールを確認" }));
        panel.append(el("div", { class: "actions" }, ...[["重要ではない", "unimportant"], ["この送信元を除外", "exclude_sender"], ["この送信元を優先", "prioritize_sender"]].map(([label, decision]) => el("button", { class: "ghost", text: label, onclick: e => run(e.currentTarget, () => api.inboxFeedback(message.messageId, decision)) }))));
        panel.append(el("details", {}, el("summary", { text: "出自・判定情報" }), el("pre", { text: JSON.stringify({ sourceType: message.sourceType, trustLevel: message.trustLevel, connectionId: message.connectionId, messageId: message.messageId, threadId: message.threadId, classification: c }, null, 2) })));
        view.append(panel);
      }
      const confirm = el("input", { type: "checkbox" });
      view.append(el("details", {}, el("summary", { text: "取り込みデータを削除" }), el("p", { class: "hint", text: "取り込みを停止し、保存本文・同期履歴・重複排除データを削除します。Google接続やGmail上のメールは維持します。再び有効にすると同じメールを取り込む場合があります。" }), el("label", {}, confirm, el("span", { text: "削除する内容を確認しました" })), el("button", { class: "ghost", text: "保存データを削除して停止", onclick: e => { if (!confirm.checked) { toast("削除する内容を確認してください。"); return; } return run(e.currentTarget, () => api.clearInbox()); } })));
    } catch (error) { if (!disposed) toast(error.message); }
  }
  void load();
  return { element: view, load, dispose() { disposed = true; clear(view); } };
}

