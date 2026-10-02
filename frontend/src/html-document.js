import { el } from "./dom.js";

// A complete document can be displayed; HTML examples inside ordinary prose
// or Markdown remain source text. Accept a single fence around the document.
export function htmlDocument(text) {
  let source = String(text || "").trim();
  const fence = source.match(/^```(?:html)?\s*\n([\s\S]*?)\n```$/i);
  if (fence) source = fence[1].trim();
  return /^(?:<!doctype\s+html\b[^>]*>\s*)?<html\b/i.test(source) && /<\/html>\s*$/i.test(source) ? source : null;
}

export function renderHtmlDocument(source) {
  // No scripts, same-origin access, forms, popups or top-level navigation.
  // The first CSP also prevents external assets from contacting third parties.
  const policy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'";
  return el("iframe", {
    class: "html-document", sandbox: "", referrerpolicy: "no-referrer", i18nTitle: "インフォグラフィック",
    srcdoc: `<!doctype html><meta http-equiv="Content-Security-Policy" content="${policy}"><meta name="viewport" content="width=device-width, initial-scale=1">${source}`,
  });
}
