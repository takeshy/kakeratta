import { el, clear, toast, dialogCloseButton } from "./dom.js";
import { createBlueskyConnection } from "./bluesky.js";
import { t } from "./i18n.js";
import { api, blobURL } from "./api.js";
import { htmlDocument, renderHtmlDocument } from "./html-document.js";
import { renderHtmlImages } from "./html-image.js";

let socialOpen = false;

// Selection is a snapshot: late persona responses never become public by default.
export async function openSocial(root, getPersona) {
  if (socialOpen) return;
  socialOpen = true;
  let entries;
  try { entries = (await api.thread(root)).entries; }
  catch (error) { socialOpen = false; toast(error.message, "error"); return; }
  let plan = null;
  let busy = false;
  let completedSelection = null;
  const rendered = new Map();
  const selectionKey = () => [...selected].sort().join("\n");
  const isPosted = () => completedSelection === selectionKey();
  const canPublish = () => plan && plan.posts.some(post => post.publication?.state !== "posted") && !plan.posts.some(post => post.publication?.state && !["posted", "retryable"].includes(post.publication.state));
  const selected = new Set([root]);
  const checkboxes = new Map();
  const dialog = el("dialog", { class: "dialog social-dialog", "aria-labelledby": "social-title" });
  let connected = false;
  const connection = createBlueskyConnection({ returnRoot: root, onChange: (value) => {
    connected = value.connected;
    completedSelection = null;
    invalidate();
    previewButton.disabled = busy || !connected || !selected.size || isPosted();
  } });
  const preview = el("div", { class: "social-preview", "aria-live": "polite" });
  const publishedLink = el("div", { class: "social-published-link" });
  function showPublishedLink(url) {
    clear(publishedLink);
    if (url) publishedLink.append(el("a", { href: url, target: "_blank", rel: "noreferrer", label: "公開した投稿を開く" }));
  }
  const status = el("p", { role: "status", class: "note" });
  const publish = el("button", { class: "button", label: "選択した会話を投稿", disabled: true, onclick: send });
  publish.hidden = true;
  const selection = el("div", { class: "social-selection" });
  function invalidate() {
    plan = null; publish.disabled = true; publish.hidden = true; clear(preview); clear(publishedLink); status.textContent = "";
    publish.textContent = t(isPosted() ? "投稿済み" : "選択した会話を投稿");
    previewButton.disabled = busy || !connected || !selected.size || isPosted();
  }
  for (const entry of entries) {
    if (entry.deleted) continue;
    const persona = getPersona(entry.speaker);
    const html = entry.speaker !== "user" && htmlDocument(entry.text);
    const name = entry.speaker === "user" ? t("あなた") : t("{0}からの返信", `${persona?.icon || "◎"} ${persona?.name || entry.speaker}`);
    const checkbox = el("input", { type: "checkbox", checked: entry.id === root });
    checkboxes.set(entry.id, checkbox);
    checkbox.addEventListener("change", () => {
      if (checkbox.checked) selected.add(entry.id); else selected.delete(entry.id);
      invalidate();
    });
    selection.append(el("label", { class: "social-choice" }, checkbox,
      el("span", {}, el("strong", { text: entry.id === root ? t("元の投稿 · {0}", name) : name }),
        html ? el("span", { class: "said" }, renderHtmlDocument(html), el("span", { class: "hint", label: "HTMLの回答は画像として公開します。" }))
          : el("span", { class: "said", text: entry.text || t("［画像］") }))));
  }
  const body = () => ({ root, target: "bluesky", entryIds: [...selected],
    htmlImages: Object.fromEntries([...rendered].filter(([id]) => selected.has(id))) });
  const previewButton = el("button", { class: "button secondary", label: "プレビュー", disabled: true, onclick: async () => {
    if (busy || isPosted() || !selected.size) return;
    setBusy(true); invalidate();
    try {
      for (const entry of entries) {
        if (!selected.has(entry.id) || entry.speaker === "user" || !htmlDocument(entry.text) || rendered.has(entry.id)) continue;
        status.textContent = t("HTMLを画像に変換しています…");
        rendered.set(entry.id, await renderHtmlImages(entry.text));
      }
      plan = await api.previewSocial(body());
      const numbers = new Map(plan.posts.map((post, i) => [post.id || post.entryId, i + 1]));
      for (const [i, post] of plan.posts.entries()) {
        preview.append(el("article", { class: "social-preview-post" },
          el("strong", { text: post.parent ? t("{0} · {1}への返信", i + 1, numbers.get(post.parent)) : t(post.entryId === root ? "{0} · 元の投稿" : "{0} · 最初の投稿", i + 1) }),
          post.publication?.state === "posted" ? el("span", { class: "tag", label: "投稿済み" }) : null,
          el("p", { class: "said", text: post.text }),
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
    } catch (error) { status.textContent = t(error.message); }
    finally { setBusy(false); }
  } });
  function setBusy(value) {
    busy = value;
    for (const input of dialog.querySelectorAll("input, select, button")) input.disabled = value;
    for (const input of checkboxes.values()) input.disabled = value;
    publish.disabled = value || isPosted() || !canPublish();
    publish.hidden = !canPublish() && !isPosted();
    publish.textContent = t(value ? "処理中…" : isPosted() ? "投稿済み" : "選択した会話を投稿");
    previewButton.disabled = value || !connected || !selected.size || isPosted();
  }
  async function send() {
    if (busy || isPosted() || !canPublish()) return;
    setBusy(true); publish.textContent = t("投稿しています…"); status.textContent = t("投稿しています…");
    try {
      const result = await api.publishSocial({ ...body(), digest: plan.digest });
      if (!result.error) completedSelection = selectionKey();
      status.textContent = t(result.error || "選択した会話を公開しました。");
      const parentPost = (result.posts || [])[0];
      if (parentPost?.publication?.url) showPublishedLink(parentPost.publication.url);
      // A new preview is needed before any retry or extension of the thread.
      plan = null;
    } catch (error) { status.textContent = t("{0}。再操作するときはプレビューから確認してください。", t(error.message)); plan = null; }
    finally { setBusy(false); }
  }
  dialog.append(dialogCloseButton(() => dialog.close()), el("h2", { id: "social-title", label: "会話をBlueskyに公開" }),
    el("p", { class: "note", label: "元の投稿も外せます。回答だけ公開したい場合は、その回答だけ選択してください。Personaの返信には個人的な情報が含まれることがあります。返信先を選ばない発言は、選択した最初の発言への返信として公開します。プレビューで公開内容を確認してください。" }),
    connection.element, selection,
    previewButton, preview, status, publishedLink,
    el("div", { class: "compose-row" }, publish,
      el("button", { class: "chip ghost", label: "閉じる", onclick: () => dialog.close() })));
  dialog.addEventListener("cancel", (event) => { if (busy) event.preventDefault(); });
  dialog.addEventListener("close", () => { socialOpen = false; connection.dispose(); dialog.remove(); });
  document.body.append(dialog); dialog.showModal();
}
