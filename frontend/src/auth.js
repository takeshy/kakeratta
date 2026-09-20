import { IDENTITY_KEY, localIdentity, clearLocalUser, localList, localRecord } from "./local-store.js";
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

async function authenticate() {
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
    if (user && localStorage.getItem("kakeratta.signedOut") !== "1") {
      await establishSession(user);
      return true;
    }
    loginScreen(async () => {
      try {
        const result = await authModule.signInWithPopup(firebaseAuth, new authModule.GoogleAuthProvider());
        await establishSession(result.user);
        localStorage.removeItem("kakeratta.signedOut");
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

// Leaving with confirmUnsent off is the account already closed on the server:
// there is nowhere for an unsent post to go, so nothing to ask about.
export async function logout({ confirmUnsent = true } = {}) {
  if (confirmUnsent && (await localList("outbox:").catch(() => [])).length && !confirm(t("未送信の投稿もこの端末から削除してログアウトしますか？"))) return;
  if (enabled && !firebaseAuth && navigator.onLine !== false) {
    const authConfig = await config();
    const [appModule, authModule] = await Promise.all([import("https://www.gstatic.com/firebasejs/12.2.1/firebase-app.js"), import("https://www.gstatic.com/firebasejs/12.2.1/firebase-auth.js")]);
    firebaseAuth = authModule.getAuth(appModule.getApps()[0] || appModule.initializeApp(authConfig));
    firebaseSignOut = authModule.signOut;
  }
  await localRecord("identity", null, "device");
  await clearLocalUser();
  localStorage.setItem("kakeratta.signedOut", "1");
  localStorage.removeItem(IDENTITY_KEY);
  await fetch("/api/auth/logout", { method: "POST" }).catch(() => {});
  if (firebaseAuth && firebaseSignOut) await firebaseSignOut(firebaseAuth).catch(() => {});
  location.reload();
}

export function authenticationEnabled() { return enabled; }

let verification = null;
let verifiedAt = 0;
export function verifyIdentity(force = false) {
  if (!force && verifiedAt > Date.now() - 30000) return Promise.resolve(localIdentity());
  if (verification) return verification;
  verification = (async () => {
    const response = await fetch("/api/auth/identity", { cache: "no-store", signal: AbortSignal.timeout(10000) });
    if (!response.ok) { const error = Error(t("接続とログインを確認してください。")); error.status = response.status; throw error; }
    const next = await response.json();
    if (!next.key) throw Error("Invalid account identity");
    const previous = localIdentity();
    if (previous && previous.key !== next.key) {
      await localRecord("identity", next, "device");
      localStorage.setItem(IDENTITY_KEY, JSON.stringify(next));
      location.reload();
      throw Error("Account changed");
    }
    localStorage.setItem(IDENTITY_KEY, JSON.stringify(next));
    await localRecord("identity", next, "device").catch(() => {});
    verifiedAt = Date.now();
    enabled = next.enabled;
    return next;
  })().finally(() => { verification = null; });
  return verification;
}
export async function ensureAuthentication() {
  const cached = localIdentity();
  if (cached) {
    enabled = cached.enabled;
    return true;
  }
  if (!await authenticate()) return false;
  try { await verifyIdentity(); } catch (error) {
    loginScreen(() => location.reload(), t("一度オンラインでログインしてください。"));
    return false;
  }
  return true;
}
export async function reconnectAuthentication() {
  try { return await verifyIdentity(true); } catch (error) {
    if (error.status === 401 && await authenticate()) return verifyIdentity();
    throw error;
  }
}
globalThis.addEventListener?.("storage", event => {
  if (event.key === IDENTITY_KEY && event.oldValue !== event.newValue) location.reload();
});
