import { userProfile } from "./avatar.js";
import { el, clear, toast, dialogCloseButton } from "./dom.js";
import { createBlueskyConnection } from "./bluesky.js";
import { getLanguage, t } from "./i18n.js";
import { api, blobURL } from "./api.js";
import { renderHtmlDocument } from "./html-document.js";
import { socialImageDocument, imageSource } from "./infographic-document.js";
import { renderHtmlImages } from "./html-image.js";

let socialOpen = false;

// Selection is a snapshot: late persona responses never become public by default.
// The preview shows a post's link facets as they will read on Bluesky. Facet
// ranges are UTF-8 byte offsets, so the text is sliced as bytes.
export function richText(post) {
  const links = (post.links || []).filter(link => link.start >= 0 && link.end > link.start);
  if (!links.length) return [post.text];
  const bytes = new TextEncoder().encode(post.text);
  const decode = (start, end) => new TextDecoder().decode(bytes.slice(start, end));
  const out = [];
  let offset = 0;
  for (const link of links) {
    if (link.start < offset || link.end > bytes.length) continue;
    out.push(decode(offset, link.start));
    out.push(el("a", { href: link.uri, target: "_blank", rel: "noopener noreferrer", text: decode(link.start, link.end) }));
    offset = link.end;
  }
  out.push(decode(offset, bytes.length));
  return out;
}

// Use the same rendered content as the timeline, in an isolated light document.
// Existing HTML/infographic answers retain their layout; other entries can also
// be shared as a picture. Attachments are inlined before the isolated capture.
export async function captureSocialEntry(entry) {
  let source = imageSource(entry);
  if (!source) {
    let media = "";
    if (entry.media?.blobId) {
      const response = await fetch(blobURL(entry.media.blobId));
      if (!response.ok) throw Error(t("画像を読み込めませんでした"));
      media = await new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = () => reject(Error(t("画像を読み込めませんでした")));
        void response.blob().then(blob => reader.readAsDataURL(blob), reject);
      });
    }
    source = socialImageDocument(entry, media);
  }
  return renderHtmlImages(entry.text || "", source);
}

export async function openSocial(root, getPersona, getSettings = () => ({}), { capture = captureSocialEntry } = {}) {
  if (socialOpen) return;
  socialOpen = true;
  let entries;
  try { entries = (await api.thread(root)).entries.filter(entry => !entry.deleted); }
  catch (error) { socialOpen = false; toast(error.message, "error"); return; }
  let plan = null, request = null, sending = false, preparing = false, connected = false, closed = false;
  let generation = 0, completedSelection = null, captureQueue = Promise.resolve();
  const rendered = new Map();
  const selected = new Set([root]);
  const formats = new Map(entries.map(entry => [entry.id, imageSource(entry) ? "image" : "text"]));
  const cards = new Map();
  const selectionKey = () => JSON.stringify([...selected].sort().map(id => [id, formats.get(id)]));
  const isPosted = () => completedSelection === selectionKey();
  const canPublish = () => plan && plan.posts.some(post => post.publication?.state !== "posted") &&
    !plan.posts.some(post => post.publication?.state && !["posted", "retryable"].includes(post.publication.state));
  const dialog = el("dialog", { class: "dialog social-dialog", "aria-labelledby": "social-title" });
  const selection = el("div", { class: "social-selection social-preview" });
  const status = el("p", { role: "status", class: "note", "aria-live": "polite" });
  const error = el("p", { role: "alert", class: "note warn" });
  const publishedLink = el("div", { class: "social-published-link" });
  const publish = el("button", { class: "button primary social-publish", label: "投稿", disabled: true, onclick: send });
  const retry = el("button", { class: "chip ghost social-retry", label: "もう一度読み込む", hidden: true, onclick: refresh });
  function showPublishedLink(url) {
    clear(publishedLink);
    if (url) publishedLink.append(el("a", { href: url, target: "_blank", rel: "noreferrer", label: "公開した投稿を開く" }));
  }
  function controls() {
    for (const input of dialog.querySelectorAll("input, select, button")) input.disabled = sending;
    for (const [id, card] of cards) {
      card.formats.hidden = !selected.has(id);
      for (const [format, button] of card.buttons) {
        button.setAttribute("aria-pressed", String(formats.get(id) === format));
      }
    }
    publish.disabled = sending || preparing || !connected || isPosted() || !canPublish();
    publish.textContent = t(sending ? "投稿しています…" : isPosted() ? "投稿済み" : "投稿");
  }
  for (const entry of entries) {
    const persona = getPersona(entry.speaker);
    const name = entry.speaker === "user" ? userProfile(getSettings?.()).name : t("{0}からの返信", `${persona?.icon || "◎"} ${persona?.name || entry.speaker}`);
    const checkbox = el("input", { type: "checkbox", checked: selected.has(entry.id) });
    checkbox.addEventListener("change", () => {
      if (sending) return;
      if (checkbox.checked) selected.add(entry.id); else selected.delete(entry.id);
      return refresh();
    });
    const buttons = new Map();
    const formatGroup = el("div", { class: "social-formats", role: "group", "aria-label": t("{0}の投稿形式", name), hidden: !selected.has(entry.id) });
    for (const [format, label] of [["text", "テキスト"], ["image", "画像"]]) {
      const button = el("button", { class: "social-format", type: "button", label, "aria-pressed": String(formats.get(entry.id) === format), onclick: () => {
        if (sending || formats.get(entry.id) === format) return;
        formats.set(entry.id, format);
        return refresh();
      } });
      buttons.set(format, button); formatGroup.append(button);
    }
    const content = el("div", { class: "social-entry-preview" });
    cards.set(entry.id, { content, formats: formatGroup, buttons });
    selection.append(el("article", { class: "social-choice" },
      el("div", { class: "social-choice-head" },
        el("label", {}, checkbox, el("strong", { text: entry.id === root ? t("元の投稿 · {0}", name) : name })), formatGroup), content));
  }
  function unselectedContent(entry) {
    const source = imageSource(entry);
    return source ? renderHtmlDocument(source) : el("p", { class: "said", text: entry.text || t("［画像］") });
  }
  async function refresh() {
    const version = ++generation;
    plan = null; request = null; preparing = connected && selected.size > 0;
    error.textContent = ""; retry.hidden = true; clear(publishedLink);
    status.textContent = preparing ? t("公開内容を更新しています…") : t(selected.size ? "Blueskyに接続してください。" : "公開する発言を選んでください。");
    for (const entry of entries) {
      clear(cards.get(entry.id).content).append(selected.has(entry.id)
        ? el("p", { class: "hint", text: preparing ? t("公開内容を更新しています…") : "" }) : unselectedContent(entry));
    }
    controls();
    if (!preparing || closed) return;
    const ids = [...selected];
    const choices = Object.fromEntries(ids.map(id => [id, formats.get(id)]));
    try {
      const htmlImages = {};
      for (const entry of entries) {
        if (!ids.includes(entry.id) || choices[entry.id] !== "image") continue;
        if (version !== generation || closed) return;
        status.textContent = t("回答を画像に変換しています…");
        if (!rendered.has(entry.id)) {
          const draw = captureQueue.catch(() => {}).then(() => closed ? null : capture(entry));
          captureQueue = draw;
          rendered.set(entry.id, draw);
          draw.catch(() => rendered.delete(entry.id));
        }
        htmlImages[entry.id] = await rendered.get(entry.id);
      }
      if (version !== generation || closed) return;
      const body = { root, target: "bluesky", language: getLanguage(), entryIds: ids, formats: choices, htmlImages };
      const next = await api.previewSocial(body);
      if (version !== generation || closed) return;
      plan = next; request = body;
      for (const id of ids) clear(cards.get(id).content);
      const numbers = new Map(plan.posts.map((post, i) => [post.id || post.entryId, i + 1]));
      for (const [i, post] of plan.posts.entries()) {
        cards.get(post.entryId)?.content.append(el("div", { class: "social-preview-post" },
          el("strong", { text: post.parent ? t("{0} · {1}への返信", i + 1, numbers.get(post.parent)) : t(post.entryId === root ? "{0} · 元の投稿" : "{0} · 最初の投稿", i + 1) }),
          post.publication?.state === "posted" ? el("span", { class: "tag", label: "投稿済み" }) : null,
          el("p", { class: "said" }, richText(post)),
          post.image || post.blobId ? el("img", { src: post.image || blobURL(post.blobId), alt: post.imageAlt || t("公開する画像") }) : null));
      }
      showPublishedLink(plan.posts.find(post => !post.parent)?.publication?.url);
      const remaining = plan.posts.filter(post => post.publication?.state !== "posted").length;
      if (!remaining) {
        completedSelection = selectionKey();
        status.textContent = t("選択した会話は投稿済みです。再投稿はできません。");
      } else if (!canPublish()) {
        status.textContent = t("前回の投稿結果を確認できません。Bluesky上の投稿を確認してください。");
      } else {
        status.textContent = t("未投稿の{0}件をBlueskyの {1} から公開します。", remaining, plan.handle || plan.account);
      }
    } catch (reason) {
      if (version !== generation || closed) return;
      error.textContent = t(reason.message); retry.hidden = false;
    } finally {
      if (version === generation && !closed) { preparing = false; controls(); }
    }
  }
  async function send() {
    if (sending || preparing || !connected || isPosted() || !request || !canPublish()) return;
    sending = true; error.textContent = ""; controls();
    try {
      const result = await api.publishSocial({ ...request, digest: plan.digest });
      const url = (result.posts || [])[0]?.publication?.url;
      if (!result.error) {
        completedSelection = selectionKey();
        status.textContent = t("選択した会話を公開しました。");
        plan = null; request = null;
      } else {
        await refresh();
        error.textContent = t(result.error);
      }
      if (url) showPublishedLink(url);
    } catch (reason) {
      await refresh();
      error.textContent = t(reason.message);
    } finally { sending = false; controls(); }
  }
  const connection = createBlueskyConnection({ returnRoot: root, onChange: value => {
    connected = value.connected; completedSelection = null; void refresh();
  } });
  dialog.append(dialogCloseButton(() => { if (!sending) dialog.close(); }), el("h2", { id: "social-title", label: "会話をBlueskyに公開" }),
    el("p", { class: "note", label: "公開する発言と、それぞれの投稿形式を選んでください。表示中の内容を投稿します。担当の返信には個人的な情報が含まれることがあります。" }),
    connection.element, selection, status, error, retry, publishedLink,
    el("div", { class: "compose-row social-actions" },
      el("button", { class: "button social-close", label: "閉じる", onclick: () => { if (!sending) dialog.close(); } }), publish));
  dialog.addEventListener("cancel", event => { if (sending) event.preventDefault(); });
  dialog.addEventListener("close", () => { closed = true; generation++; socialOpen = false; connection.dispose(); dialog.remove(); });
  document.body.append(dialog); dialog.showModal();
  controls();
}
