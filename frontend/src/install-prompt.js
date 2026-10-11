import { el, toast } from "./dom.js";
import { t } from "./i18n.js";

const DISMISSED_KEY = "kakeratta.install.dismissedUntil";
const WEEK = 7 * 24 * 60 * 60 * 1000;
// Links shared through messengers and social apps open in their own WebView,
// where neither the native prompt nor Add to Home Screen is available.
const IN_APP = /\bLine\/|FBAN|FBAV|FB_IAB|Instagram|MicroMessenger|\bTwitter/;
const SHARE_ICON = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 15V3m0 0L8 7m4-4 4 4M7 10H5v11h14V10h-2"/></svg>';

// Capture the event before authentication and dynamic application loading finish.
export function createInstallPromptController(browser = window, now = Date.now) {
  const nav = browser.navigator;
  const ios = /iPhone|iPod/.test(nav.userAgent);
  const inApp = IN_APP.test(nav.userAgent);
  const line = /\bLine\//.test(nav.userAgent);
  const mobile = nav.userAgentData?.mobile ?? /iPhone|iPod|Android.*Mobile/.test(nav.userAgent);
  const display = browser.matchMedia("(display-mode: standalone)");
  let pending, installed = false, busy = false;
  const listeners = new Set();
  let dismissedUntil = 0;
  try { dismissedUntil = Number(browser.localStorage.getItem(DISMISSED_KEY)) || 0; } catch {}
  const eligible = () => mobile && browser.isSecureContext && !installed && !display.matches && !nav.standalone;
  // The mode ignores "Later", so Settings can still offer installation.
  const mode = () => {
    if (!eligible()) return null;
    if (inApp) return "in-app";
    if (pending) return "install";
    return ios ? "ios" : null;
  };
  const banner = () => (now() < dismissedUntil ? null : mode());
  const changed = () => { for (const listener of listeners) listener(banner()); };
  const dismiss = () => {
    dismissedUntil = now() + WEEK;
    try { browser.localStorage.setItem(DISMISSED_KEY, String(dismissedUntil)); } catch {}
    changed();
  };
  browser.addEventListener("beforeinstallprompt", event => {
    if (!eligible()) return;
    event.preventDefault();
    pending = event;
    changed();
  });
  browser.addEventListener("appinstalled", () => { installed = true; pending = null; changed(); });
  display.addEventListener?.("change", changed);
  return {
    mode,
    subscribe(callback) {
      listeners.add(callback);
      callback(banner());
      return () => listeners.delete(callback);
    },
    dismiss,
    // LINE honours this parameter by handing the page to the default browser.
    externalURL() {
      if (!line) return null;
      const url = new URL(browser.location.href);
      url.searchParams.set("openExternalBrowser", "1");
      return url.href;
    },
    async install() {
      if (busy || mode() !== "install") return;
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

const steps = (...items) => el("ol", { class: "install-steps" }, ...items.map(item => el("li", {}, ...item)));

// Shared by the banner and Settings; actions differ only in the "Later" button.
function guide(controller, mode, later, inSettings = false) {
  const install = el("button", { type: "button", class: "button primary", label: "ホーム画面に追加", onclick: async event => {
    event.currentTarget.disabled = true;
    await controller.install();
  } });
  const external = controller.externalURL();
  const body = {
    install: [el("p", { label: "ホーム画面から、すぐにkakerattaを開けます。" })],
    ios: [
      el("p", { label: "予定の通知を受け取るには、ホーム画面への追加が必要です。" }),
      steps(
        [el("span", { class: "install-share-icon", html: SHARE_ICON }), el("span", { label: "共有ボタンをタップ（見当たらないときは「…」の中）" })],
        [el("span", { label: "「ホーム画面に追加」を選ぶ（なければ「もっと見る」の中）" })],
        [el("span", { label: "「Webアプリとして開く」をオンにして「追加」" })]),
    ],
    "in-app": [el("p", { label: external
      ? "LINEの中ではホーム画面に追加できません。ブラウザで開き直してください。"
      : "アプリ内のブラウザではホーム画面に追加できません。メニューの「ブラウザで開く」から開き直してください。" })],
  }[mode];
  const note = { install: "予定の通知を受け取るには、設定の「この端末」で「Push通知」をオンにして、通知を許可してください。",
    ios: "追加したアイコンから開き、設定の「この端末」で「Push通知」をオンにして、通知を許可してください。" }[mode];
  // Settings already shows the switch directly below this guidance.
  const settingsNote = { install: "予定の通知を受け取るには、下の「Push通知」をオンにして、通知を許可してください。",
    ios: "追加したアイコンから開き、この画面の「Push通知」をオンにして、通知を許可してください。" }[mode];
  return [
    ...body,
    note ? el("p", { class: "install-note", label: inSettings ? settingsNote : note }) : null,
    el("div", { class: "install-prompt-actions" },
      mode === "install" ? install : null,
      mode === "in-app" && external ? el("a", { class: "button primary", href: external, label: "ブラウザで開く" }) : null,
      later),
  ].filter(Boolean);
}

export function installPromptNode(controller = installPrompt) {
  const node = el("aside", { class: "install-prompt", hidden: true, "aria-label": t("ホーム画面に追加") });
  const later = () => {
    controller.dismiss();
    toast("設定の「この端末」から、いつでもホーム画面に追加できます。");
  };
  controller.subscribe(mode => {
    node.hidden = !mode;
    node.replaceChildren();
    if (!mode) return;
    node.append(el("strong", { label: "ホーム画面に追加しませんか？" }),
      ...guide(controller, mode, el("button", { type: "button", class: "button", label: "あとで", onclick: later })));
  });
  return node;
}

// Settings keeps the way back after "Later"; it is absent once installed.
export function installSettingsNode(controller = installPrompt) {
  const node = el("div", { class: "install-settings" });
  let shown = false, unsubscribe = () => {};
  const draw = () => {
    // Settings is rebuilt on every visit, so a detached copy stops listening.
    if (node.isConnected) shown = true;
    else if (shown) return unsubscribe();
    const mode = controller?.mode();
    node.hidden = !mode;
    node.replaceChildren();
    if (mode) node.append(el("h3", { label: "ホーム画面に追加" }), ...guide(controller, mode, null, true));
  };
  if (controller) unsubscribe = controller.subscribe(draw);
  else draw();
  return node;
}
