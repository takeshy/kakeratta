import { htmlDocument } from "./html-document.js";

const WIDTH = 720;
const PAGE_HEIGHT = 1440;
const MAX_HEIGHT = PAGE_HEIGHT * 10;
const POLICY = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'";

// Keep capture as isolated as the timeline frame. The parent can read this
// frame for layout, but generated content cannot run scripts or load resources.
export function imageDocument(source) {
  const doc = new DOMParser().parseFromString(source, "text/html");
  doc.querySelectorAll("script, noscript, template, iframe, object, embed, link, meta, base, audio, video, animate, animateTransform, set").forEach(node => node.remove());
  for (const node of doc.querySelectorAll("*")) {
    for (const attr of [...node.attributes]) {
      if (/^on/i.test(attr.name) || ["srcset", "action", "formaction", "autofocus"].includes(attr.name)) node.removeAttribute(attr.name);
      if (["src", "href", "xlink:href"].includes(attr.name) && !/^(?:#|data:image\/(?:png|jpe?g|gif|webp|svg\+xml|avif)(?:;|,))/i.test(attr.value)) node.removeAttribute(attr.name);
    }
  }
  const meta = doc.createElement("meta");
  meta.setAttribute("http-equiv", "Content-Security-Policy");
  meta.setAttribute("content", POLICY);
  doc.head.prepend(meta);
  const viewport = doc.createElement("meta");
  viewport.setAttribute("name", "viewport");
  viewport.setAttribute("content", "width=device-width, initial-scale=1");
  doc.head.append(viewport);
  return doc;
}

function loadImage(src) {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(Error("HTMLを画像に変換できませんでした。もう一度プレビューしてください。"));
    image.src = src;
  });
}

export async function renderHtmlImages(text) {
  const source = htmlDocument(text);
  if (!source) throw Error("HTMLの回答が見つかりません");
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  const sourceHash = [...new Uint8Array(hash)].map(value => value.toString(16).padStart(2, "0")).join("");
  const safe = imageDocument(source);
  const frame = document.createElement("iframe");
  frame.setAttribute("sandbox", "allow-same-origin");
  frame.setAttribute("referrerpolicy", "no-referrer");
  frame.setAttribute("aria-hidden", "true");
  frame.style.cssText = `position:fixed;left:-10000px;top:0;width:${WIDTH}px;height:1px;border:0;pointer-events:none;`;
  // Mount inside the topmost modal so capture has an actual layout there too.
  const host = document.querySelector("dialog[open]") || document.body;
  try {
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(Error("HTMLを画像に変換できませんでした。もう一度プレビューしてください。")), 10000);
      frame.onload = () => { clearTimeout(timeout); resolve(); };
      frame.srcdoc = "<!doctype html>" + safe.documentElement.outerHTML;
      host.append(frame);
    });
    const doc = frame.contentDocument;
    await doc.fonts.ready;
    await Promise.all([...doc.images].map(image => image.complete ? null : new Promise(resolve => { image.onload = image.onerror = resolve; })));
    const width = Math.max(WIDTH, doc.documentElement.scrollWidth, doc.body.scrollWidth);
    const height = Math.max(1, doc.documentElement.scrollHeight, doc.body.scrollHeight);
    if (height > MAX_HEIGHT || width > 2400) throw Error("HTMLが大きすぎます。内容を短くしてからプレビューしてください。");
    const alt = doc.body.innerText.replace(/\s+/g, " ").trim().slice(0, 1000);
    doc.querySelectorAll("meta").forEach(node => node.remove());
    const xml = new XMLSerializer().serializeToString(doc.documentElement);
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><foreignObject width="100%" height="100%">${xml}</foreignObject></svg>`;
    const image = await loadImage("data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg));
    const images = [];
    for (let top = 0; top < height; top += PAGE_HEIGHT) {
      const pageHeight = Math.min(PAGE_HEIGHT, height - top);
      const canvas = document.createElement("canvas");
      canvas.width = width * 2; canvas.height = pageHeight * 2;
      const context = canvas.getContext("2d");
      if (!context) throw Error("HTMLを画像に変換できませんでした。もう一度プレビューしてください。");
      context.fillStyle = "#fff"; context.fillRect(0, 0, canvas.width, canvas.height);
      context.drawImage(image, 0, top, width, pageHeight, 0, 0, canvas.width, canvas.height);
      images.push(canvas.toDataURL("image/jpeg", .9));
    }
    return { sourceHash, images, alt };
  } finally { frame.remove(); }
}
