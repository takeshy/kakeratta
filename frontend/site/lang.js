// Japanese and English are both in the page; this picks one. The choice is
// kept under the same key the app uses, so the app, the landing page and the
// manual agree on a language without asking twice. A ?lang= in the URL wins,
// so a link can carry the reader's language across.
const KEY = "kakeratta.language";
const root = document.documentElement;

function fromQuery() {
  const value = new URLSearchParams(location.search).get("lang");
  return value === "ja" || value === "en" ? value : null;
}
function fromStorage() {
  try {
    const value = localStorage.getItem(KEY);
    return value === "ja" || value === "en" ? value : null;
  } catch { return null; }
}
function fromBrowser() {
  const locale = navigator.languages?.[0] || navigator.language || "";
  return String(locale).toLowerCase().split("-")[0] === "ja" ? "ja" : "en";
}

export function resolveLanguage() { return fromQuery() || fromStorage() || fromBrowser(); }

export function applyLanguage(lang) {
  root.lang = lang;
  root.dataset.lang = lang;
  const title = root.dataset[lang === "ja" ? "titleJa" : "titleEn"];
  if (title) document.title = title;
  for (const button of document.querySelectorAll("[data-set-lang]")) {
    button.setAttribute("aria-pressed", String(button.dataset.setLang === lang));
  }
  // Links between these pages carry the language along. The app reads the
  // stored key itself, so its links stay clean.
  for (const link of document.querySelectorAll("a[data-lang-link]")) {
    const url = new URL(link.getAttribute("href"), location.href);
    url.searchParams.set("lang", lang);
    link.setAttribute("href", url.pathname + url.search + url.hash);
  }
}

export function setLanguage(lang) {
  applyLanguage(lang);
  try { localStorage.setItem(KEY, lang); } catch { /* storage may be unavailable */ }
  const url = new URL(location.href);
  url.searchParams.set("lang", lang);
  history.replaceState(null, "", url);
}

applyLanguage(resolveLanguage());
document.addEventListener("click", (event) => {
  const button = event.target.closest?.("[data-set-lang]");
  if (button) setLanguage(button.dataset.setLang);
});
