import { clear, el } from "./dom.js";
import { createAPIRequest } from "./api-request.js";
import { localIdentity } from "./local-store.js";
import { reconnectAuthentication, verifyIdentity } from "./auth.js";
import { t } from "./i18n.js";
import { showLogin } from "./login.js";

const call = createAPIRequest({ identity: localIdentity, verify: verifyIdentity });

export async function startAlexaLink({ reconnect = reconnectAuthentication } = {}) {
  // Cached identity permits offline app startup, but linking needs a live session.
  showLogin({ state: "checking" });
  try {
    await reconnect();
  } catch (error) {
    // On 401, reconnectAuthentication has already shown the sign-in screen.
    // Reloading the current URL retries other failures without losing OAuth state.
    if (error.status !== 401) showLogin({ onLogin: () => location.reload(), error: String(error.message || error) });
    return;
  }
  showAlexaLink();
}

export function showAlexaLink() {
  const query = new URLSearchParams(location.search);
  const status = el("p", { role: "status" });
  const approve = el("button", { class: "button primary", label: "Alexaと連携する", onclick: async () => {
    approve.disabled = true;
    try {
      const result = await call("POST", "/api/alexa/authorize", Object.fromEntries(
        ["client_id", "redirect_uri", "state", "response_type", "scope"].map(key => [key, query.get(key) || ""]),
      ));
      location.replace(result.redirect);
    } catch (error) { status.textContent = t(error.message); approve.disabled = false; }
  } });
  clear(document.querySelector("#app")).append(el("main", { class: "login" },
    el("h1", { label: "Alexaと連携" }),
    el("p", { label: "このアカウントに声でかけらを投稿し、担当の返事と予定をAlexaで読み上げます。" }),
    el("p", { label: "同じAlexaを使う人にも返事や予定が聞こえます。連携は設定の外部連携から解除できます。" }),
    approve, el("a", { href: "/", label: "連携せずに戻る" }), status,
  ));
}

export function createAlexaConnection() {
  const node = el("div", { class: "settings-block" });
  async function load() {
    try {
      const state = await call("GET", "/api/alexa/connection");
      clear(node);
      if (!state.enabled && !state.connected) return;
      node.append(el("h3", { label: "Alexa連携" }), el("p", { label: state.connected
        ? "Alexaと連携しています。『返事を聞いて』『予定を教えて』と話しかけられます。"
        : "Alexaアプリで「かけらった」スキルのアカウント連携を開始してください。" }));
      if (state.connected) node.append(el("button", { class: "button", label: "Alexa連携を解除", onclick: async event => {
        const button = event.currentTarget;
        button.disabled = true;
        try { await call("DELETE", "/api/alexa/connection"); await load(); }
        catch (error) { node.append(el("p", { role: "alert", text: t(error.message) })); button.disabled = false; }
      } }));
    } catch (error) { clear(node).append(el("p", { role: "alert", text: `${t("Alexa連携")}: ${t(error.message)}` })); }
  }
  void load();
  return node;
}
