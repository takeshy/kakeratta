import { clear, el } from "./dom.js";
import { t } from "./i18n.js";

let firebaseAuth = null;
let firebaseSignOut = null;
let enabled = false;

async function config() {
  const response = await fetch("/api/auth/config", { cache: "no-store" });
  if (!response.ok) return { enabled: false };
  return response.json();
}

function waitForUser(auth, onAuthStateChanged) {
  return new Promise((resolve) => {
    const stop = onAuthStateChanged(auth, (user) => {
      stop();
      resolve(user);
    });
  });
}

async function establishSession(user) {
  const idToken = await user.getIdToken();
  const response = await fetch("/api/auth/session", {
    method: "POST",
    headers: { Authorization: `Bearer ${idToken}` },
  });
  if (!response.ok) throw new Error(t("ログインセッションを開始できませんでした。"));
}

function loginScreen(onLogin, error = "") {
  const root = document.querySelector("#app");
  clear(root);
  root.append(
    el("main", { class: "login" },
      el("h1", { text: "kakeratta" }),
      el("p", { label: "あなた専用の記憶へログインします。" }),
      error ? el("p", { class: "error", text: error }) : null,
      el("button", { class: "button primary", label: "Googleでログイン", onclick: onLogin }),
    ),
  );
}

// Token mode. The URL someone was handed is /?token=...; the page turns it into
// the cookie and then takes it out of the address bar so it is not copied on.
async function claimToken() {
  const url = new URL(location.href);
  const token = url.searchParams.get("token");
  if (token === null) return;
  await fetch(`/api/auth/token?token=${encodeURIComponent(token)}`, { cache: "no-store" }).catch(() => {});
  url.searchParams.delete("token");
  history.replaceState(null, "", url.pathname + (url.search || "") + url.hash);
}

export async function ensureAuthentication() {
  await claimToken();
  let authConfig;
  try {
    authConfig = await config();
  } catch {
    // Older/local servers do not expose auth configuration.
    return true;
  }
  if (!authConfig.enabled) return true;
  enabled = true;

  try {
    const [{ initializeApp }, authModule] = await Promise.all([
      import("https://www.gstatic.com/firebasejs/12.2.1/firebase-app.js"),
      import("https://www.gstatic.com/firebasejs/12.2.1/firebase-auth.js"),
    ]);
    const app = initializeApp({
      apiKey: authConfig.apiKey,
      authDomain: authConfig.authDomain,
      projectId: authConfig.projectId,
    });
    firebaseAuth = authModule.getAuth(app);
    firebaseSignOut = authModule.signOut;
    const user = await waitForUser(firebaseAuth, authModule.onAuthStateChanged);
    if (user) {
      await establishSession(user);
      return true;
    }
    loginScreen(async () => {
      try {
        const result = await authModule.signInWithPopup(firebaseAuth, new authModule.GoogleAuthProvider());
        await establishSession(result.user);
        location.reload();
      } catch (error) {
        loginScreen(() => location.reload(), String(error.message || error));
      }
    });
    return false;
  } catch (error) {
    loginScreen(() => location.reload(), String(error.message || error));
    return false;
  }
}

export async function logout() {
  await fetch("/api/auth/logout", { method: "POST" }).catch(() => {});
  if (firebaseAuth && firebaseSignOut) await firebaseSignOut(firebaseAuth).catch(() => {});
  location.reload();
}

export function authenticationEnabled() { return enabled; }
