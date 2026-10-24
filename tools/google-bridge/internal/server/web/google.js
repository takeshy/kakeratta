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
      view.append(el("h2", { label: t("Googleアカウントを接続") }));
      if (!result.available) {
        view.append(el("p", { class: "hint", label: t("Google連携は未設定です。サーバー管理者による暗号鍵と公開URLの設定が必要です。") }));
        return;
      }
      const project = el("input", { type: "text", autocomplete: "off", spellcheck: "false" });
      const hasSavedClient = result.services.some(item => item.connectionId);
      const allConnected = result.services.length > 0 && result.services.every(item => item.connected);
      const allScopes = new Set(result.services.flatMap(item => item.scopes || []));
      const targetService = result.services.length > 1 ? "both" : (result.services[0]?.service || "both");
      view.append(createSetupGuide(result.redirectUri, id => {
        if (!project.value) project.value = id;
      }));
      view.append(el("p", { class: "hint", label: t("自分のGoogle CloudプロジェクトとWebアプリ用OAuthクライアントを使います。まず対象APIを有効にし、Google Auth Platformで同意画面とテストユーザーを設定してください。") }),
        el("a", { href: "https://console.cloud.google.com/auth/clients", label: t("Google Auth Platformを開く") }),
        el("p", { label: t("登録するリダイレクトURI") }), el("code", { text: result.redirectUri }),
        el("p", { class: "hint", label: t("Testingでは通常7日で再認証が必要です。個人利用の審査例外がありますが、ユーザー所有プロジェクトでも審査免除は保証されません。") }),
        el("p", { class: "hint", label: t("資格情報はこのアプリのサーバーに暗号化保存します。読み取ったデータはHermesと設定したモデルへ送られ、Hermes側の会話履歴にも残り得ます。秘密情報をチャットに貼り付けないでください。") }));
      const message = el("p", { role: "status", "aria-live": "polite", class: "hint" });
      const recovery = el("p", { class: "hint" });
      const report = error => {
        const text = t(error.message);
        message.textContent = text;
        recovery.textContent = recoveryHint(error.message);
        toast(text);
      };
      const client = el("input", { type: "text", autocomplete: "off", spellcheck: "false" });
      const secret = el("input", { type: "password", autocomplete: "new-password", spellcheck: "false" });
      const send = el("input", { type: "checkbox" });
      send.checked = allScopes.size === 0 || allScopes.has("https://www.googleapis.com/auth/gmail.send");
      const drafts = el("input", { type: "checkbox" });
      drafts.checked = allScopes.size === 0 || allScopes.has("https://www.googleapis.com/auth/gmail.compose");
      const list = el("input", { type: "checkbox" });
      list.checked = allScopes.size === 0 || allScopes.has("https://www.googleapis.com/auth/calendar.calendarlist.readonly");
      const write = el("input", { type: "checkbox" });
      write.checked = allScopes.size === 0 || allScopes.has("https://www.googleapis.com/auth/calendar.events");
      const consent = el("input", { type: "checkbox" });
      const upload = el("input", { type: "file", accept: ".json,application/json", onchange: async () => {
        const file = upload.files?.[0];
        if (!file) return;
        try {
          const config = await readOAuthClient(file, result.redirectUri);
          if (disposed) return;
          project.value = config.projectId;
          client.value = config.clientId;
          secret.value = config.clientSecret;
          message.textContent = t("JSONを確認しました。内容はブラウザーの入力欄だけに保持し、接続時に暗号化保存します。");
          recovery.textContent = "";
          toast(t("JSONを確認し、入力欄に反映しました。"));
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
          const started = await api.connectGoogle({ service: targetService, projectId: project.value.trim(), clientId: client.value.trim(), clientSecret: secret.value, redirectUri: result.redirectUri, modelConsent: true, calendarList: list.checked, gmailSend: send.checked, gmailDrafts: drafts.checked, calendarWrite: write.checked });
          secret.value = "";
          sessionStorage.setItem(FLOW_KEY, JSON.stringify({ service: targetService, browserSecret: started.browserSecret }));
          const redirect = new URL(started.url);
          if (redirect.origin !== "https://accounts.google.com" || redirect.pathname !== "/o/oauth2/v2/auth") throw new Error(t("接続先のURLが不正です"));
          location.assign(redirect.href);
        } catch (error) { report(error); connect.disabled = false; }
      } });
      const connect = el("button", { type: "submit", class: "button secondary", label: hasSavedClient ? t("Googleに再接続（Gmail・Calendar一括）") : t("Googleに接続") });
      const manualInputs = el("details", {},
        el("summary", { text: t("クライアントID・シークレットの手入力欄（JSON選択時は自動で入ります）") }),
        field(t("Google CloudプロジェクトID"), project),
        field(t("OAuthクライアントID"), client),
        field(t("OAuthクライアントシークレット"), secret)
      );
      const scopeOptions = el("details", {},
        el("summary", { text: t("許可する操作の範囲（既定：すべて有効・変更する場合のみ）") }),
        el("label", {}, send, el("span", { text: t("Gmail: メール送信を追加で許可する（操作ごとの承認が必要）") })),
        el("label", {}, drafts, el("span", { text: t("Gmail: 下書き保存・送信を追加で許可する（composeはRestricted権限）") })),
        el("label", {}, list, el("span", { label: t("Calendar: カレンダー一覧の読み取りも許可する") })),
        el("label", {}, write, el("span", { text: t("Calendar: 所有カレンダーの予定変更を許可する（一覧権限も追加・操作ごとの承認が必要）") }))
      );
      form.append(
        manualInputs,
        field(t("GoogleからダウンロードしたWeb用OAuth設定JSONを選択"), upload),
        el("p", { class: "hint", text: t("JSONファイルを選択すると自動でセットされます（再認証時は保存済みクライアントをそのまま使えます）。") }),
        message,
        recovery,
        scopeOptions,
        el("label", {}, consent, el("span", { label: t("GmailとGoogle CalendarのデータをHermesと設定したモデルへ渡すことに同意する") })),
        connect,
      );
      const setup = el("details", { class: "settings-block" }, el("summary", { label: t("Gmail・Google Calendar 一括接続・再認証の設定") }), form);
      setup.open = !allConnected;
      view.append(setup);
      for (const connection of result.services) {
        const name = connection.service === "gmail" ? "Gmail" : "Google Calendar";
        const panel = el("section", { class: "settings-block" }, el("h4", { text: name }));
        const state = { connected: t("接続済み"), disconnected: t("未接続"), needs_reauth: t("再認証が必要") }[connection.status] || connection.status;
        panel.append(el("p", { text: `${t("接続状態")}: ${t(state)}${connection.email ? ` · ${connection.email}` : ""}` }));
        if (connection.lastVerifiedAt) panel.append(el("p", { class: "hint", text: `${t("最終接続確認")}: ${new Date(connection.lastVerifiedAt).toLocaleString()}` }));
        if (connection.scopes.length) panel.append(el("p", { class: "hint", text: `${t("許可済み権限")}: ${connection.scopes.join(", ")}` }));
        if (connection.connectionId) {
          const revoke = el("input", { type: "checkbox" });
          panel.append(el("button", { class: "chip", label: t("接続テスト"), onclick: async (event) => {
            const button = event.currentTarget; button.disabled = true;
            try { await api.testGoogle(connection.service); await load(); toast(t("Google接続を確認しました。")); }
            catch (error) { report(error); }
            finally { button.disabled = false; }
          } }), el("label", {}, revoke, el("span", { label: t("Google側の許可も失効させる（同じプロジェクトの他の連携も再認証が必要）") })),
          el("button", { class: "chip ghost", label: t("接続を解除"), onclick: async (event) => {
            const button = event.currentTarget; button.disabled = true;
            try {
              const disconnected = await api.disconnectGoogle(connection.service, revoke.checked);
              await load();
              toast(disconnected.remoteRevoked ? t("Googleの許可と保存した資格情報を削除しました。") : t("保存した資格情報を削除しました。Google側の許可は残る場合があります。"));
            } catch (error) { report(error); button.disabled = false; }
          } }));
        }
        view.append(panel);
      }
    } catch (error) {
      if (!disposed) clear(view).append(el("p", { text: t(error.message) }), el("button", { class: "chip", label: t("もう一度読み込む"), onclick: () => void load() }));
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
    toast(t("Googleに接続しました。"));
  } catch (error) { toast(error.message, "error"); }
  return true;
}
