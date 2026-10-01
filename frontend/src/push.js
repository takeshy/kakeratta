import { el, toast } from "./dom.js";
import { t } from "./i18n.js";
import { api } from "./api.js";
import { pushSupported, pushRegistration, enablePush, disablePush } from "./push-client.js";

export function createPushSettings() {
  const hint = el("p", { class: "hint" });
  const button = el("button", { type: "button", disabled: true, label: "通知を有効にする" });
  const view = el("div", {}, el("h3", { label: "リマインダーのpush通知" }), hint, button);
  let config;
  let enabled = false;
  const render = () => {
    button.textContent = t(enabled ? "通知を無効にする" : "通知を有効にする");
    hint.textContent = t(enabled ? "この端末に誕生日や予定のリマインダーを届けます。" : "誕生日や予定の通知を、ブラウザを閉じていても受け取れます。");
  };
  async function load() {
    if (!pushSupported()) { hint.textContent = t("このブラウザではpush通知を利用できません。iPhone・iPadではホーム画面に追加して開いてください。"); return; }
    try {
      const registration = await pushRegistration();
      const subscription = await registration.pushManager.getSubscription();
      config = await api.pushConfig(subscription?.endpoint);
      if (!config.available) { hint.textContent = t("push通知はサーバーで設定されていません。"); return; }
      enabled = !!subscription && config.subscribed && Notification.permission === "granted";
      render();
      button.disabled = false;
    } catch (error) { hint.textContent = t("通知の設定を読み込めませんでした。"); }
  }
  button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      if (enabled) await disablePush(api);
      else await enablePush(api, config.publicKey);
      enabled = !enabled;
      render();
    } catch (error) { toast(t(error.message || String(error)), "error"); }
    finally { button.disabled = false; }
  });
  return { element: view, load };
}
