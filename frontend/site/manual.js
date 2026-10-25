// Keep an existing section selected when switching this bilingual manual.
import "./lang.js";

function followLocalizedAnchor() {
  const match = /^#(ja|en)-(.+)$/.exec(location.hash);
  const language = document.documentElement.lang;
  if (!match || match[1] === language) return;
  const target = document.getElementById(`${language}-${match[2]}`);
  if (!target) return;
  const url = new URL(location.href);
  url.hash = target.id;
  history.replaceState(null, "", url);
  target.scrollIntoView();
}

followLocalizedAnchor();
window.addEventListener("hashchange", followLocalizedAnchor);
document.addEventListener("click", (event) => {
  if (event.target.closest?.("[data-set-lang]")) followLocalizedAnchor();
});
