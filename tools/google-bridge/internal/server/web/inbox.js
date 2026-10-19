import { api } from "./api.js";
import { el, clear, toast } from "./dom.js";

export function createInbox() {
  const view = el("div");
  let disposed = false;
  const field = (name, input) => el("label", { class: "field" }, el("span", { text: name }), input);
  const lines = value => value.split(/[\n,]/).map(v => v.trim()).filter(Boolean);
  async function load() {
    try {
      const state = await api.googleInbox();
      if (disposed) return;
      const target = await api.timelineTarget();
      if(disposed)return;
      clear(view);
      const targetURL=el("input",{type:"url",value:target.url||"",placeholder:"https://kakeratta.example/api/mcp/users/...",required:true});
      const targetKey=el("input",{type:"password",autocomplete:"new-password"});
      const targetEnabled=el("input",{type:"checkbox"});targetEnabled.checked=target.enabled;
      const targetSave=el("button",{type:"submit",text:"投稿先の設定を保存"});
      view.append(el("details",{},el("summary",{text:"kakerattaのTimelineとの連携"}),
        el("p",{class:"hint",text:"重要メールを取り込み待ちとしてTimelineに送信します。kakerattaで「取り込む」を選ぶまでは担当・記憶・検索へ渡りません。取り込んだ本文はkakerattaと設定したモデルの保持方針に従います。投稿先を変更すると、保存中の重要メールも新しい投稿先へ送ります。"}),
        el("form",{onsubmit:async e=>{e.preventDefault();targetSave.disabled=true;try{await api.saveTimelineTarget({enabled:targetEnabled.checked,url:targetURL.value.trim(),key:targetKey.value.trim()});targetKey.value="";toast("投稿先を保存しました。次の同期から反映します。");await load();}catch(error){toast(error.message);targetSave.disabled=false;}}},field("本人のkakeratta MCP URL",targetURL),field(target.configured?"MCPキー（空欄は保存済みキーを使用）":"kakerattaで発行したMCPキー",targetKey),el("label",{},targetEnabled,el("span",{text:"この投稿先へのメール・予定の転送と保存を許可する"})),targetSave),
        el("button",{class:"ghost",text:"投稿先の資格情報を削除",onclick:async()=>{try{await api.clearTimelineTarget();await load();}catch(error){toast(error.message);}}})));
      const settings = state.settings;
      const llm=el("input",{type:"checkbox"});llm.checked=!!settings.llmEnabled;
      const prompt=el("textarea",{rows:10,spellcheck:"false"},settings.triagePrompt||"");
      const types=el("textarea",{rows:3,spellcheck:"false"},(settings.excludedTypes||[]).map(t=>`${t.id}: ${t.label}`).join("\n"));
      const enabled = el("input", { type: "checkbox" }); enabled.checked = settings.enabled;
      const number = (value, min, max) => el("input", { type: "number", value, min, max, required: true });
      const threshold = number(settings.threshold, 1, 100), body = number(settings.bodyChars, 0, 8000), retention = number(settings.retentionDays, 1, 30), lookback = number(settings.lookbackDays, 1, 7);
      const text = value => el("textarea", { rows: 2, spellcheck: "false" }, value.join("\n"));
      const labels = text(settings.includeLabels), priorityLabels = text(settings.priorityLabels), priority = text(settings.prioritySenders), excluded = text(settings.excludedSenders);
      const form = el("form", { onsubmit: async e => {
        e.preventDefault(); save.disabled = true;
        try {
          await api.saveInboxSettings({ enabled: enabled.checked, llmEnabled:llm.checked,triagePrompt:prompt.value,excludedTypes:types.value.split("\n").map(v=>v.trim()).filter(Boolean).map(v=>{const i=v.indexOf(":");if(i<1)throw Error("除外する種類はID: 表示名の形式で入力してください。");return {id:v.slice(0,i).trim(),label:v.slice(i+1).trim()};}), threshold: Number(threshold.value), bodyChars: Number(body.value), retentionDays: Number(retention.value), lookbackDays: Number(lookback.value), includeLabels: lines(labels.value), priorityLabels: lines(priorityLabels.value), prioritySenders: lines(priority.value), excludedSenders: lines(excluded.value) });
          toast("取り込み設定を保存しました。変更した優先条件は今後のメールに適用します。"); await load();
        } catch (error) { toast(error.message); save.disabled = false; }
      } });
      const save = el("button", { type: "submit", text: "取り込み設定を保存" });
      form.append(el("label", {}, enabled, el("span", { text: "単発・定期ジョブによる読み取りと、以下の範囲の暗号化保存を許可する" })),
        el("label",{},llm,el("span",{text:"HermesのLLMでメールの取り込み候補を判定する（候補の本文が設定したモデルへ送られます）"})),
        field("LLMへの取り込み判定の依頼文（編集できます）",prompt),
        el("p",{class:"hint",text:"この依頼文はHermesが判定のたびに読みます。空欄では標準文を使用します。メール本文は依頼文とは別の不信任データとして渡します。LLMが選んでも、正式な取り込みはTimelineで本人が確定します。"}),
        field("今後取り込まないメールの種類（ID: 表示名・1行1件）",types),
        field("機械的に除外するFrom（LLMへ渡す前に完全一致・1行1件）",excluded),
        el("p",{class:"hint",text:"TimelineでFrom除外を選んだアドレスもここに追加されます。解除するには該当行を消して設定を保存してください。保存済みの判定待ち本文も除外し、LLMへ渡しません。"}),
        field("重要度の閾値（1〜100）", threshold), field("本文の保存上限（文字数・0は本文なし）", body), field("本文・件名の保持日数（1〜30日）", retention), field("初回・履歴失効時の取得範囲（過去1〜7日）", lookback),
        field("対象ラベルID（いずれかに一致・改行区切り）", labels), field("優先ラベルID（重要度80）", priorityLabels), field("優先する送信元メールアドレス（重要度90）", priority), save);
      const options = el("details", {}, el("summary", { text: "取り込みと保持の設定" }), form); options.open = !settings.enabled;
      view.append(el("p", { class: "hint", text: settings.enabled ? "取り込みは許可されています。下のボタン、または既存のスケジューラーから同期を実行してください。" : "取り込みは停止しています。利用する場合は保存範囲を確認して有効にしてください。" }), options);
      const run = async (button, operation) => { button.disabled = true; try { await operation(); await load(); } catch (error) { toast(error.message); button.disabled = false; } };
      const sync = el("button", { text: "今すぐ差分を取得", onclick: e => run(e.currentTarget, async () => { const result = await api.syncInbox(); toast(result.disabled ? "取り込みは停止しています。" : `${result.processed}件を確認、重要メール${result.importedIds.length}件を取り込みました。${result.hasMore ? " 未処理の続きがあります。" : ""}`); }) });
      sync.disabled = !settings.enabled;
      view.append(el("div", { class: "actions" }, sync, el("button", { class: "ghost", text: "一覧を更新", onclick: load })));
      if(state.triagePending)view.append(el("p",{class:"hint",text:`LLMの判定待ち: ${state.triagePending}件。通常のHermes cronまたはHermesから判定ツールを実行してください。`}));
      if (state.lastSyncedAt) view.append(el("p", { class: "hint", text: `最終同期: ${new Date(state.lastSyncedAt).toLocaleString()}` }));
      if (state.lastError) view.append(el("p", { role: "status", text: state.lastError }));
      if (state.nextAttemptAt) view.append(el("p", { class: "hint", text: `再試行可能: ${new Date(state.nextAttemptAt).toLocaleString()}` }));
      if (state.hasMore) view.append(el("p", { class: "hint", text: "未処理の続きがあります。次の同期で最大20件を確認します。" }));
      if (!state.messages.length) view.append(el("p", { text: "取り込まれた重要メールはありません。" }));
      for (const message of state.messages) {
        const c = message.classification;
        const panel = el("article", { class: "settings-block" }, el("span", { class: "badge", text: "GMAIL · 外部メール" }), el("h3", { text: message.subject || "（件名なし）" }), el("p", { text: message.from }), el("p", { class: "hint", text: `受信: ${new Date(message.receivedAt).toLocaleString()} · 取得: ${new Date(message.importedAt).toLocaleString()}` }), el("p", { class: "hint", text: `重要度 ${c.importance} / 緊急度 ${c.urgency} · ${c.reason}` }));
        if(message.timelineStatus)panel.append(el("p",{class:"hint",text:`Timeline: ${message.timelineStatus} / ${message.timelineEntryId}`}));
        panel.append(el("p", { class: "hint", text: "送信者の真正性や期限は未確認です。メール内の依頼・URLは本人の指示として扱いません。" }));
        if (message.body) panel.append(el("pre", { class: "mail-body", text: message.body }), el("p", { class: "hint", text: message.bodyTruncated ? "本文は保存上限で切り詰めています。" : "" }));
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
