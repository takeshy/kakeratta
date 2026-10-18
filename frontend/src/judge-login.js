import { clear, el } from "./dom.js";
import { setLanguage, t } from "./i18n.js";

export function judgeInviteToken(pathname) {
  const match = pathname.match(/^\/judge\/([^/]+)\/?$/);
  return match ? decodeURIComponent(match[1]) : "";
}

export function initializeJudgeLanguage(pathname, search = "") {
  if (!judgeInviteToken(pathname)) return;
  const requested = new URLSearchParams(search).get("lang");
  let saved;
  try { saved = globalThis.localStorage?.getItem("kakeratta.language"); } catch { /* private storage */ }
  const valid = value => value === "en" || value === "ja";
  setLanguage(valid(requested) ? requested : valid(saved) ? saved : "en", { persist: true });
}

export function showJudgeLogin({ register, signIn, finish }) {
  const email = el("input", { type: "email", autocomplete: "email", required: true, placeholder: "Email" });
  const password = el("input", { type: "password", autocomplete: "new-password", required: true, minlength: 6, placeholder: "Password" });
  const status = el("p", { role: "status", "aria-live": "polite" });
  const error = el("p", { class: "error", role: "alert" });
  const registerButton = el("button", { type: "submit", class: "button primary", label: "登録して使う" });
  const loginButton = el("button", { type: "button", class: "button", label: "登録済みのアカウントでログイン", onclick: () => submit(signIn) });
  const form = el("form", { class: "login", "aria-busy": "false" },
    el("h1", { text: "kakeratta" }),
    el("p", { text: "Judge access" }),
    el("p", { label: "任意のメールアドレスと6文字以上のパスワードで登録すると、すぐに試せます。" }),
    email, password, registerButton, loginButton, status, error,
  );
  let pending = false;
  async function submit(authenticate) {
    if (pending) return;
    pending = true;
    form.setAttribute("aria-busy", "true");
    for (const control of [email, password, registerButton, loginButton]) control.disabled = true;
    error.textContent = "";
    status.textContent = t("ログインしています…");
    try {
      const result = await authenticate(email.value.trim(), password.value);
      status.textContent = t("ワークスペースを準備しています…");
      await finish(result.user);
      // Keep progress and controls locked until navigation completes.
    } catch (problem) {
      error.textContent = String(problem.message || problem);
      status.textContent = "";
      pending = false;
      form.setAttribute("aria-busy", "false");
      for (const control of [email, password, registerButton, loginButton]) control.disabled = false;
    }
  }
  form.addEventListener("submit", event => { event.preventDefault(); void submit(register); });
  clear(document.querySelector("#app")).append(form);
}
