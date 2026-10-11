import { createInstallPromptController, installPromptNode } from "../src/install-prompt.js";
import { createSettings } from "../src/settings.js";
import { serve, speaker } from "./api.js";
import { screen, sheet } from "./frame.js";
import { about, personas, settings as settingsFixture } from "./fixtures.js";

export default { title: "InstallPrompt", parameters: { layout: "fullscreen" } };

// Exercise the real controller without native prompts or persistent dismissal
// changing the next story. Smartphone width also works on a desktop canvas.
function controllerFor({ iphone = false, line = false, standalone = false, dismissed = false, desktop = false } = {}) {
  const browser = new EventTarget();
  const display = new EventTarget();
  display.matches = standalone;
  const storage = new Map();
  Object.assign(browser, {
    isSecureContext: true,
    navigator: { userAgent: desktop ? "Macintosh" : line ? "iPhone Line/14.0.0" : iphone ? "iPhone" : "Android Mobile" },
    location: { href: "https://kakeratta.example/" },
    matchMedia: () => display,
    localStorage: { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value) },
  });
  const controller = createInstallPromptController(browser);
  if (dismissed) controller.dismiss();
  if (!iphone && !line) {
    const event = new Event("beforeinstallprompt", { cancelable: true });
    Object.assign(event, { prompt: async () => {}, userChoice: Promise.resolve({ outcome: "accepted" }) });
    browser.dispatchEvent(event);
  }
  return controller;
}

const phone = (host, desktop = false) => {
  host.style.width = desktop ? "100%" : "390px";
  host.style.maxWidth = "100%";
  return host;
};

function invitation(options = {}) {
  const host = phone(screen({ rows: personas, entries: [] }), options.desktop);
  host.insertBefore(installPromptNode(controllerFor(options)), host.children[1]);
  return host;
}

// The real Settings panel, scrolled to This device where the guidance stays.
function settingsPanel(options) {
  serve({ settings: settingsFixture, about });
  const panel = createSettings({ speaker, onSettings: () => {}, installPrompt: controllerFor(options) });
  void panel.load().then(() => {
    let heading = panel.element.querySelector(".install-settings");
    while (heading && heading.tagName !== "H3") heading = heading.previousElementSibling;
    if (heading) heading.style.scrollMarginTop = "72px";
    heading?.scrollIntoView({ block: "start" });
  });
  return phone(sheet("設定", panel.element));
}

export const Android = { name: "スマホ・インストール可能", render: () => invitation() };
export const IPhone = { name: "iPhone・ホーム画面への追加手順", render: () => invitation({ iphone: true }) };
export const ExtraLarge = {
  name: "iPhone・特大文字",
  globals: { textSize: "xlarge" },
  render: () => invitation({ iphone: true }),
};
export const Line = { name: "LINE内ブラウザ・ブラウザで開き直す", render: () => invitation({ line: true }) };
export const SettingsAfterLater = {
  name: "設定・この端末（あとで選択後）",
  render: () => settingsPanel({ iphone: true, dismissed: true }),
};
export const Standalone = { name: "アプリとして起動・案内なし", render: () => invitation({ standalone: true }) };
export const Dismissed = { name: "あとで選択済み・案内なし", render: () => invitation({ iphone: true, dismissed: true }) };
export const Desktop = { name: "PC・案内なし", render: () => invitation({ desktop: true }) };
