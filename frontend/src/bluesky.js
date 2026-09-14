import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { t } from "./i18n.js";
const FLOW_KEY = "kakeratta.bluesky.flow";

export function createBlueskyConnection({ returnRoot = "", onChange = () => {} } = {}) {
  const view = el("div", { class: "bluesky-connection" });
  let disposed = false;
  async function load() {
    try {
      const connection = await api.blueskyConnection();
      if (disposed) return;
      clear(view);
      if (!connection.available) {
        view.append(el("p", { class: "hint", label: "Bluesky連携は準備中です。" }));
      } else if (connection.connected) {
        view.append(el("p", { class: "bluesky-account", text: t("接続先: {0}", connection.handle || connection.did) }),
          el("button", { class: "chip ghost", label: "接続を解除", onclick: async (event) => {
            event.currentTarget.disabled = true;
            try {
              const result = await api.disconnectBluesky();
              if (!result.remoteRevoked) toast("kakerattaの接続を解除しました。Bluesky側の許可はBlueskyの設定から解除してください。");
              await load();
            } catch (error) { toast(error.message, "error"); event.currentTarget.disabled = false; }
          } }));
      } else {
        const handle = el("input", { type: "text", autocapitalize: "none", spellcheck: "false", i18nPlaceholder: "例: alice.bsky.social", "aria-label": t("Blueskyのハンドル"), autocomplete: "off" });
        const status = el("p", { role: "status", class: "hint bluesky-status", "aria-live": "polite" });
        const connect = el("button", { type: "submit", class: "button secondary bluesky-connect-button", label: "Blueskyと接続", onclick: async () => {
          if (!handle.value.trim()) { handle.focus(); return; }
          connect.disabled = true;
          try {
            // Check browser storage before starting a flow that depends on it.
            sessionStorage.setItem(FLOW_KEY, "");
            const result = await api.connectBluesky({ handle: handle.value.trim(), returnRoot });
            sessionStorage.setItem(FLOW_KEY, result.browserSecret);
            const redirect = new URL(result.url);
            if (redirect.protocol !== "https:") throw new Error(t("接続先のURLが不正です"));
            location.assign(redirect.href);
          } catch (error) { status.textContent = t(error.message); connect.disabled = false; }
        } });
        view.append(el("form", { class: "bluesky-connect-form", onsubmit: (event) => event.preventDefault() },
          el("label", { class: "field bluesky-handle" },
            el("span", { label: "Blueskyのハンドル" }), handle), connect),
          el("p", { class: "hint", label: "Blueskyの画面で接続を許可してください。パスワードはBlueskyにだけ入力します。" }), status);
      }
      onChange(connection);
    } catch (error) {
      if (disposed) return;
      clear(view).append(el("p", { class: "hint", text: t(error.message) }),
        el("button", { class: "chip", label: "もう一度読み込む", onclick: () => void load() }));
    }
  }
  void load();
  return { element: view, load, dispose: () => { disposed = true; } };
}

export async function finishBlueskyConnection() {
  const prefix = "#bluesky-callback=";
  if (!location.hash.startsWith(prefix)) return null;
  const hash = location.hash.slice(prefix.length);
  history.replaceState(null, "", location.pathname + location.search);
  try {
    const browserSecret = sessionStorage.getItem(FLOW_KEY) || "";
    sessionStorage.removeItem(FLOW_KEY);
    const result = await api.finishBluesky({ params: decodeURIComponent(hash), browserSecret });
    toast(t("Blueskyに接続しました: {0}", result.handle));
    return result;
  } catch (error) {
    toast(error.message, "error");
    return { returnRoot: "", failed: true };
  }
}
