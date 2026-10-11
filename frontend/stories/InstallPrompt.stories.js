import { createInstallPromptController, installPromptNode } from "../src/install-prompt.js";
import { screen } from "./frame.js";
import { personas } from "./fixtures.js";

export default { title: "InstallPrompt", parameters: { layout: "fullscreen" } };

// Exercise the real controller without native prompts or persistent dismissal
// changing the next story. Smartphone width also works on a desktop canvas.
function invitation({ iphone = false, standalone = false, dismissed = false, desktop = false } = {}) {
  const browser = new EventTarget();
  const display = new EventTarget();
  display.matches = standalone;
  const storage = new Map();
  Object.assign(browser, {
    isSecureContext: true,
    navigator: { userAgent: desktop ? "Macintosh" : iphone ? "iPhone" : "Android Mobile" },
    matchMedia: () => display,
    localStorage: { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value) },
  });
  const controller = createInstallPromptController(browser);
  if (dismissed) controller.dismiss();
  if (!iphone) {
    const event = new Event("beforeinstallprompt", { cancelable: true });
    Object.assign(event, { prompt: async () => {}, userChoice: Promise.resolve({ outcome: "accepted" }) });
    browser.dispatchEvent(event);
  }
  const host = screen({ rows: personas, entries: [] });
  host.style.width = desktop ? "100%" : "390px";
  host.style.maxWidth = "100%";
  host.insertBefore(installPromptNode(controller), host.children[1]);
  return host;
}

export const Android = { name: "スマホ・インストール可能", render: () => invitation() };
export const IPhone = { name: "iPhone・ホーム画面への追加手順", render: () => invitation({ iphone: true }) };
export const ExtraLarge = {
  name: "iPhone・特大文字",
  globals: { textSize: "xlarge" },
  render: () => invitation({ iphone: true }),
};
export const Standalone = { name: "アプリとして起動・案内なし", render: () => invitation({ standalone: true }) };
export const Dismissed = { name: "あとで選択済み・案内なし", render: () => invitation({ iphone: true, dismissed: true }) };
export const Desktop = { name: "PC・案内なし", render: () => invitation({ desktop: true }) };
