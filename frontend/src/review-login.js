import { clear, el } from "./dom.js";

// Keep Amazon's state, redirect URI and locale in the same browser throughout.
export function reviewLoginURL(pathname, search, review = true) {
  const query = new URLSearchParams(search);
  if (review) query.set("review", "1");
  else query.delete("review");
  query.set("login", "1");
  return pathname + "?" + query.toString();
}

export function showReviewLogin({ signIn, establishSession, onSuccess, back }) {
  const email = el("input", { type: "email", autocomplete: "username", required: true, placeholder: "Email", "aria-label": "Review account email" });
  const password = el("input", { type: "password", autocomplete: "current-password", required: true, placeholder: "Password", "aria-label": "Review account password" });
  const status = el("p", { role: "status" });
  const button = el("button", { type: "submit", class: "button primary", text: "Sign in" });
  let pending = false;
  const form = el("form", { class: "login" },
    el("h1", { text: "Kakeratta — Review access" }),
    el("p", { text: "Use the test account provided in the skill’s testing instructions. No Google account is needed." }),
    email, password, button, status, el("a", { href: back, text: "Back to Google sign-in" }),
  );
  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (pending) return;
    pending = true;
    button.disabled = true;
    status.textContent = "Signing in…";
    try {
      const result = await signIn(email.value.trim(), password.value);
      await establishSession(result.user);
      onSuccess();
    } catch {
      status.textContent = "Unable to sign in. Check the supplied credentials and review access period, then try again.";
      pending = false;
      button.disabled = false;
    }
  });
  clear(document.querySelector("#app")).append(form);
}
