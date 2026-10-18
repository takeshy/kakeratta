import { revokeDevicePush } from "./push-client.js";
import { IDENTITY_KEY, localIdentity, clearLocalUser, localList, localRecord } from "./local-store.js";
import { el } from "./dom.js";
import { t } from "./i18n.js";
import { createGoogleLogin, guestLandingPage, showLogin } from "./login.js";

import { reviewLoginURL, showReviewLogin } from "./review-login.js";
import { initializeJudgeLanguage, judgeInviteToken, showJudgeLogin } from "./judge-login.js";

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
  showLogin({ onLogin, error });
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
    // Alexa's linking browser does not support authentication popups.
    if (location.pathname === "/alexa") {
      const result = await authModule.getRedirectResult(firebaseAuth);
      if (result?.user) localStorage.removeItem("kakeratta.signedOut");
    }
    const user = await waitForUser(firebaseAuth, authModule.onAuthStateChanged);
    if (user && new URLSearchParams(location.search).get("review") !== "1" && !judgeInviteToken(location.pathname) && localStorage.getItem("kakeratta.signedOut") !== "1") {
      await establishSession(user);
      return true;
    }
    const invite = judgeInviteToken(location.pathname);
    if (invite) {
      const finish = async user => {
        const firstToken = await user.getIdToken();
        const claimed = await fetch("/api/auth/judge", {
          method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${firstToken}` },
          body: JSON.stringify({ token: invite }),
        });
        if (!claimed.ok) throw new Error("Judge registration is unavailable.");
        await user.getIdToken(true);
        await establishSession(user);
        await verifyIdentity(true, { reloadOnChange: false });
        localStorage.removeItem("kakeratta.signedOut");
        location.replace("/");
      };
      showJudgeLogin({
        register: (email, password) => authModule.createUserWithEmailAndPassword(firebaseAuth, email, password),
        signIn: (email, password) => authModule.signInWithEmailAndPassword(firebaseAuth, email, password),
        finish,
      });
      return false;
    }
    if (new URLSearchParams(location.search).get("review") === "1") {
      showReviewLogin({
        signIn: (email, password) => authModule.signInWithEmailAndPassword(firebaseAuth, email, password),
        establishSession: async user => {
          try { await establishSession(user); }
          catch (error) { await authModule.signOut(firebaseAuth); throw error; }
        },
        onSuccess: () => {
          localStorage.removeItem("kakeratta.signedOut");
          location.replace(reviewLoginURL(location.pathname, location.search, false));
        },
        back: reviewLoginURL(location.pathname, location.search, false),
      });
      return false;
    }
    const landing = guestLandingPage(location.pathname, location.search);
    if (landing) {
      location.replace(landing);
      return false;
    }
    const login = createGoogleLogin({
      signIn: () => location.pathname === "/alexa"
        ? authModule.signInWithRedirect(firebaseAuth, new authModule.GoogleAuthProvider())
        : authModule.signInWithPopup(firebaseAuth, new authModule.GoogleAuthProvider()),
      establishSession,
      onSuccess: () => {
        localStorage.removeItem("kakeratta.signedOut");
        const query = new URLSearchParams(location.search);
        query.delete("login");
        location.replace(location.pathname + (query.size ? `?${query}` : "") + location.hash);
      },
    });
    loginScreen(login);
    if (location.pathname === "/alexa") {
      document.querySelector("#app").append(el("a", {
        href: reviewLoginURL(location.pathname, location.search),
        text: "Review access / 審査用ログイン",
      }));
    }
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
  await revokeDevicePush(localIdentity()?.key).catch(() => {});
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
export function verifyIdentity(force = false, { reloadOnChange = true } = {}) {
  if (!force && verifiedAt > Date.now() - 30000) return Promise.resolve(localIdentity());
  if (verification) return verification;
  verification = (async () => {
    const response = await fetch("/api/auth/identity", { cache: "no-store", signal: AbortSignal.timeout(10000) });
    if (!response.ok) { const error = Error(t("接続とログインを確認してください。")); error.status = response.status; throw error; }
    const next = await response.json();
    if (!next.key) throw Error("Invalid account identity");
    const previous = localIdentity();
    if (previous && previous.key !== next.key) {
      await revokeDevicePush(previous.key).catch(() => {});
      if (reloadOnChange) {
        await localRecord("identity", next, "device");
        localStorage.setItem(IDENTITY_KEY, JSON.stringify(next));
        location.reload();
        throw Error("Account changed");
      }
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
  initializeJudgeLanguage(location.pathname, location.search);
  const cached = localIdentity();
  if (cached && new URLSearchParams(location.search).get("review") !== "1" && !judgeInviteToken(location.pathname)) {
    enabled = cached.enabled;
    return true;
  }
  showLogin({ state: "checking" });
  if (!await authenticate()) return false;
  showLogin({ state: "loading" });
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
