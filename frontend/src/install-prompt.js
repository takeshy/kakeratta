import { el } from "./dom.js";

const DISMISSED_KEY = "kakeratta.install.dismissedUntil";
const WEEK = 7 * 24 * 60 * 60 * 1000;

// Capture the event before authentication and dynamic application loading finish.
export function createInstallPromptController(browser = window, now = Date.now) {
  const nav = browser.navigator;
  const iphone = /iPhone|iPod/.test(nav.userAgent);
  const mobile = nav.userAgentData?.mobile ?? /iPhone|iPod|Android.*Mobile/.test(nav.userAgent);
  const display = browser.matchMedia("(display-mode: standalone)");
  let pending, installed = false, busy = false, listener = () => {};
  let dismissedUntil = 0;
  try { dismissedUntil = Number(browser.localStorage.getItem(DISMISSED_KEY)) || 0; } catch {}
  const state = () => {
    if (!mobile || !browser.isSecureContext || installed || display.matches || nav.standalone || now() < dismissedUntil) return null;
    if (pending) return "install";
    return iphone ? "iphone" : null;
  };
  const changed = () => listener(state());
  const dismiss = () => {
    dismissedUntil = now() + WEEK;
    try { browser.localStorage.setItem(DISMISSED_KEY, String(dismissedUntil)); } catch {}
    changed();
  };
  browser.addEventListener("beforeinstallprompt", event => {
    if (!mobile || !browser.isSecureContext || installed || display.matches || nav.standalone) return;
    event.preventDefault();
    pending = event;
    changed();
  });
  browser.addEventListener("appinstalled", () => { installed = true; pending = null; changed(); });
  display.addEventListener?.("change", changed);
  return {
    subscribe(callback) { listener = callback; changed(); },
    dismiss,
    async install() {
      if (busy || state() !== "install") return;
      const event = pending;
      pending = null;
      busy = true;
      try {
        // Called directly by the button so the browser sees a user gesture.
        await event.prompt();
        const choice = await event.userChoice;
        if (choice.outcome === "accepted") installed = true;
        else dismiss();
      } catch {
        // Prompt events are single-use, including failed attempts.
        dismiss();
      } finally { busy = false; changed(); }
    },
  };
}

export const installPrompt = typeof window === "undefined" ? null : createInstallPromptController();

export function installPromptNode(controller = installPrompt) {
  const node = el("aside", { class: "install-prompt", hidden: true });
  controller.subscribe(mode => {
    node.hidden = !mode;
    node.replaceChildren();
    if (!mode) return;
    node.append(...[
      el("strong", { label: "ホーム画面に追加しませんか？" }),
      el("p", { label: "ホーム画面から、すぐにkakerattaを開けます。" }),
      mode === "iphone" ? el("p", { label: "iPhoneで予定の通知を受け取るには、ホーム画面への追加が必要です。" }) : null,
      mode === "iphone" ? el("p", { label: "Safariでこのページを開き、共有メニューから「ホーム画面に追加」を選んでください。「Webアプリとして開く」が表示されたらオンにして追加します。" }) : null,
      el("p", { label: mode === "iphone"
        ? "追加したアイコンから開き、設定の「この端末」で「Push通知」をオンにして、通知を許可してください。"
        : "予定の通知を受け取るには、設定の「この端末」で「Push通知」をオンにして、通知を許可してください。" }),
      el("div", { class: "install-prompt-actions" },
        mode === "install" ? el("button", { type: "button", class: "button primary", label: "ホーム画面に追加", onclick: async event => {
          event.currentTarget.disabled = true;
          await controller.install();
        } }) : null,
        el("button", { type: "button", class: "button", label: "あとで", onclick: controller.dismiss })),
    ].filter(Boolean));
  });
  return node;
}
