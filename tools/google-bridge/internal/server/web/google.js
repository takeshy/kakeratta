import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { t } from "./i18n.js";
import { createSetupGuide, readOAuthClient, recoveryHint } from "./setup.js";

const FLOW_KEY = "google-bridge.oauth.flow";

export function createGoogleConnection() {
  const view = el("div", { class: "google-connection settings-block" });
  let disposed = false;
  async function load() {
    try {
      const result = await api.googleConnection();
      if (disposed) return;
      clear(view);
      view.append(el("h2", { label: "Googleアカウントを接続" }));
      if (!result.available) {
        view.append(el("p", { class: "hint", label: "Google連携は未設定です。サーバー管理者による暗号鍵と公開URLの設定が必要です。" }));
        return;
      }
      const projectInputs = [];
      view.append(createSetupGuide(result.redirectUri, id => {
        for (const input of projectInputs) if (!input.value) input.value = id;
      }));
      view.append(el("p", { class: "hint", label: "自分のGoogle CloudプロジェクトとWebアプリ用OAuthクライアントを使います。まず対象APIを有効にし、Google Auth Platformで同意画面とテストユーザーを設定してください。" }),
        el("a", { href: "https://console.cloud.google.com/auth/clients", label: "Google Auth Platformを開く" }),
        el("p", { label: "登録するリダイレクトURI" }), el("code", { text: result.redirectUri }),
        el("p", { class: "hint", label: "Testingでは通常7日で再認証が必要です。個人利用の審査例外がありますが、ユーザー所有プロジェクトでも審査免除は保証されません。" }),
        el("p", { class: "hint", label: "資格情報はこのアプリのサーバーに暗号化保存します。読み取ったデータはHermesと設定したモデルへ送られ、Hermes側の会話履歴にも残り得ます。秘密情報をチャットに貼り付けないでください。" }));
      for (const connection of result.services) {
        const name = connection.service === "gmail" ? "Gmail" : "Google Calendar";
        const panel = el("section", { class: "settings-block" }, el("h4", { text: name }));
        const state = { connected: "接続済み", disconnected: "未接続", needs_reauth: "再認証が必要" }[connection.status] || connection.status;
        panel.append(el("p", { text: `${t("接続状態")}: ${t(state)}${connection.email ? ` · ${connection.email}` : ""}` }));
        if (connection.lastVerifiedAt) panel.append(el("p", { class: "hint", text: `${t("最終接続確認")}: ${new Date(connection.lastVerifiedAt).toLocaleString()}` }));
        if (connection.scopes.length) panel.append(el("p", { class: "hint", text: `${t("許可済み権限")}: ${connection.scopes.join(", ")}` }));
        const project = el("input", { type: "text", autocomplete: "off", spellcheck: "false" });
        projectInputs.push(project);
        const client = el("input", { type: "text", autocomplete: "off", spellcheck: "false" });
        const secret = el("input", { type: "password", autocomplete: "new-password", spellcheck: "false" });
        const list = el("input", { type: "checkbox" });
        const send = el("input", { type: "checkbox" });
        const drafts = el("input", { type: "checkbox" });
        const write = el("input", { type: "checkbox" });
        const consent = el("input", { type: "checkbox" });
        const message = el("p", { role: "status", "aria-live": "polite", class: "hint" });
        const recovery = el("p", { class: "hint" });
        const report = error => { message.textContent = t(error.message); recovery.textContent = recoveryHint(error.message); };
        const upload = el("input", { type: "file", accept: ".json,application/json", onchange: async () => {
          const file = upload.files?.[0];
          if (!file) return;
          try {
            const config = await readOAuthClient(file, result.redirectUri);
            if (disposed) return;
            project.value = config.projectId; client.value = config.clientId; secret.value = config.clientSecret;
            message.textContent = "JSONを確認しました。内容はブラウザーの入力欄だけに保持し、接続時に暗号化保存します。";
            recovery.textContent = "";
          } catch (error) { if (!disposed) report(error); }
          finally { upload.value = ""; }
        } });
        const field = (label, input) => el("label", { class: "field" }, el("span", { label }), input);
        const form = el("form", { onsubmit: async (event) => {
          event.preventDefault();
          if (!consent.checked) { message.textContent = t("GoogleデータをHermesとモデルへ渡すことに同意してください。"); return; }
          connect.disabled = true;
          try {
            sessionStorage.setItem(FLOW_KEY, "");
            const started = await api.connectGoogle({ service: connection.service, projectId: project.value.trim(), clientId: client.value.trim(), clientSecret: secret.value, redirectUri: result.redirectUri, modelConsent: true, calendarList: list.checked, gmailSend: send.checked, gmailDrafts: drafts.checked, calendarWrite: write.checked });
            secret.value = "";
            sessionStorage.setItem(FLOW_KEY, JSON.stringify({ service: connection.service, browserSecret: started.browserSecret }));
            const redirect = new URL(started.url);
            if (redirect.origin !== "https://accounts.google.com" || redirect.pathname !== "/o/oauth2/v2/auth") throw new Error(t("接続先のURLが不正です"));
            location.assign(redirect.href);
          } catch (error) { report(error); connect.disabled = false; }
        } });
        const connect = el("button", { type: "submit", class: "button secondary", label: connection.connectionId ? "Googleに再接続" : "Googleに接続" });
        form.append(field("Google CloudプロジェクトID", project), field("OAuthクライアントID", client), field("OAuthクライアントシークレット", secret));
        form.append(field("GoogleからダウンロードしたWeb用OAuth設定JSON（任意）", upload), el("p", { class: "hint", text: "ファイルはこのブラウザーで読み取り、クライアント種別とリダイレクトURIを検証します。ファイルそのものは送信しません。" }));
        if (connection.connectionId) form.append(el("p", { class: "hint", label: "再認証では3項目を空欄にすると保存済みクライアントを使います。" }));
        if (connection.service === "calendar") {
          list.checked = connection.scopes.includes("https://www.googleapis.com/auth/calendar.calendarlist.readonly");
          form.append(el("label", {}, list, el("span", { label: "カレンダー一覧の読み取りも許可する" })));
        }
        if (connection.service === "gmail") {
          send.checked = connection.scopes.includes("https://www.googleapis.com/auth/gmail.send");
          drafts.checked = connection.scopes.includes("https://www.googleapis.com/auth/gmail.compose");
          form.append(el("label", {}, send, el("span", { text: "メール送信を追加で許可する（操作ごとの承認が必要）" })), el("label", {}, drafts, el("span", { text: "下書き保存・送信を追加で許可する（composeはRestricted権限）" })));
        } else {
          write.checked = connection.scopes.includes("https://www.googleapis.com/auth/calendar.events");
          form.append(el("label", {}, write, el("span", { text: "所有カレンダーの予定変更を許可する（一覧権限も追加・操作ごとの承認が必要）" })));
        }
        form.append(el("label", {}, consent, el("span", { label: "このサービスのデータをHermesと設定したモデルへ渡すことに同意する" })), connect);
        const setup = el("details", {}, el("summary", { label: "接続・再認証の設定" }), form);
        setup.open = !connection.connected;
        panel.append(setup);
        if (connection.connectionId) {
          const revoke = el("input", { type: "checkbox" });
          panel.append(el("button", { class: "chip", label: "接続テスト", onclick: async (event) => {
            const button = event.currentTarget; button.disabled = true;
            try { await api.testGoogle(connection.service); await load(); toast("Google接続を確認しました。"); }
            catch (error) { report(error); }
            finally { button.disabled = false; }
          } }), el("label", {}, revoke, el("span", { label: "Google側の許可も失効させる（同じプロジェクトの他の連携も再認証が必要）" })),
          el("button", { class: "chip ghost", label: "接続を解除", onclick: async (event) => {
            const button = event.currentTarget; button.disabled = true;
            try {
              const disconnected = await api.disconnectGoogle(connection.service, revoke.checked);
              await load();
              toast(disconnected.remoteRevoked ? "Googleの許可と保存した資格情報を削除しました。" : "保存した資格情報を削除しました。Google側の許可は残る場合があります。");
            } catch (error) { report(error); button.disabled = false; }
          } }));
        }
        panel.append(message, recovery);
        view.append(panel);
      }
    } catch (error) {
      if (!disposed) clear(view).append(el("p", { text: t(error.message) }), el("button", { class: "chip", label: "もう一度読み込む", onclick: () => void load() }));
    }
  }
  void load();
  return { element: view, load, dispose() { disposed = true; clear(view); } };
}

export async function finishGoogleConnection() {
  const prefix = "#google-callback=";
  if (!location.hash.startsWith(prefix)) return false;
  const hash = location.hash.slice(prefix.length);
  history.replaceState(null, "", location.pathname + location.search);
  try {
    const flow = JSON.parse(sessionStorage.getItem(FLOW_KEY) || "null");
    sessionStorage.removeItem(FLOW_KEY);
    const params = new URLSearchParams(decodeURIComponent(hash));
    if (params.has("error")) throw new Error(recoveryHint(params.get("error")));
    if (!flow) throw new Error(t("Googleへの接続が完了しませんでした。もう一度接続してください。"));
    await api.finishGoogle({ service: flow.service, browserSecret: flow.browserSecret, state: params.get("state"), code: params.get("code") });
    toast("Googleに接続しました。");
  } catch (error) { toast(error.message, "error"); }
  return true;
}
