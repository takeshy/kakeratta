import { clear, el } from "./dom.js";

// An explicit app link must reach sign-in instead of looping back to the LP.
export function guestLandingPage(pathname, search = "") {
  if (pathname !== "/" && pathname !== "/index.html") return "";
  const query = new URLSearchParams(search);
  if (query.get("login") === "1") return "";
  const language = query.get("lang");
  return language === "ja" || language === "en" ? `/lp/?lang=${language}` : "/lp/";
}

const messages = {
  checking: "ログイン状態を確認しています…",
  authorizing: "Googleの認証を待っています…",
  completing: "ログインを完了しています…",
  loading: "アプリを読み込んでいます…",
};

export function loginView({ state = "idle", onLogin, error = "" } = {}) {
  const busy = state !== "idle";
  return el("main", { class: "login", "aria-busy": busy ? "true" : "false" },
    el("h1", { text: "kakeratta" }),
    busy ? el("div", { class: "login-progress", role: "status", "aria-live": "polite" },
      el("span", { class: "login-spinner", "aria-hidden": "true" }),
      el("p", { label: messages[state] || messages.loading }),
      el("p", { class: "hint", label: "完了すると自動で画面が切り替わります。" }))
      : el("p", { label: "あなた専用の記憶へログインします。" }),
    error ? el("p", { class: "error", role: "alert", text: error }) : null,
    busy ? null : el("button", { class: "button primary", label: "Googleでログイン", onclick: onLogin }),
  );
}

export function showLogin(options) {
  const root = document.querySelector("#app");
  clear(root).append(loginView(options));
}

// Keep progress visible through both the provider popup and server session
// creation. Success reloads the app; only failure offers another login.
export function createGoogleLogin({ signIn, establishSession, onSuccess, show = showLogin }) {
  let busy = false;
  const login = async () => {
    if (busy) return;
    busy = true;
    show({ state: "authorizing" });
    try {
      const result = await signIn();
      show({ state: "completing" });
      await establishSession(result.user);
      await onSuccess();
    } catch (error) {
      busy = false;
      show({ onLogin: login, error: String(error.message || error) });
    }
  };
  return login;
}
