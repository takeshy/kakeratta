// What a persona answer is published as when it is published as pictures: a
// complete HTML answer as it stands, or the infographic persona's or external
// agent's Markdown as the infographic the timeline lays out, with its diagrams. Text cannot carry
// a laid-out infographic, so it becomes a standalone document for capture.
import { renderMarkdown } from "./markdown.js";
import { el } from "./dom.js";
import { htmlDocument } from "./html-document.js";
import { laidOutAsInfographic, renderInfographic } from "./infographic.js";

export function imageSource(entry) {
  if (!entry || entry.speaker === "user") return null;
  const html = htmlDocument(entry.text);
  if (html) return html;
  return laidOutAsInfographic(entry.speaker, entry.text) ? infographicDocument(entry.text) : null;
}

// The app's own rules, without the dark theme: a published picture always
// uses the light one, whatever the viewer's device prefers.
function pageCSS(sheets = globalThis.document?.styleSheets || []) {
  const out = [];
  const collect = (rules) => {
    for (const rule of rules) {
      const media = rule.media?.mediaText || rule.conditionText || "";
      if (rule.cssRules && /prefers-color-scheme:\s*dark/.test(media)) continue;
      if (/^@import\b/.test(rule.cssText)) continue;
      out.push(rule.cssText);
    }
  };
  for (const sheet of sheets) {
    try { collect(sheet.cssRules || []); } catch { /* another origin's sheet */ }
  }
  return out.join("\n");
}

export function infographicDocument(text, css = pageCSS()) {
  const view = el("div", { class: "said said-markdown said-infographic" }, renderInfographic(text));
  const lang = globalThis.document?.documentElement?.lang || "ja";
  return `<!doctype html><html lang="${lang === "en" ? "en" : "ja"}"><head><meta charset="utf-8"><style>${css.replace(/<\/style/gi, "<\\/style")}</style>` +
    "<style>body{margin:0;padding:16px;background:var(--bg);color:var(--ink)}.markdown-mermaid{overflow:visible}</style></head>" +
    `<body>${view.outerHTML}</body></html>`;
}

// Ordinary timeline content can be explicitly shared as a picture too.
export function socialImageDocument(entry, media = "", css = pageCSS()) {
  const view = el("div", { class: "said said-markdown" },
    entry.speaker === "user" ? el("p", { style: "white-space:pre-wrap", text: entry.text || "" }) : renderMarkdown(entry.text || ""),
    media ? el("img", { src: media, alt: "", style: "display:block;max-width:100%;height:auto" }) : null);
  const lang = globalThis.document?.documentElement?.lang === "en" ? "en" : "ja";
  return `<!doctype html><html lang="${lang}"><head><meta charset="utf-8"><style>${css.replace(/<\/style/gi, "<\\/style")}</style>` +
    '<style>body{margin:0;padding:24px;background:var(--bg,#fff);color:var(--ink,#222)}.markdown-mermaid{overflow:visible}</style></head>' +
    `<body>${view.outerHTML}</body></html>`;
}
