import { el, clear, toast } from "./dom.js";
import { createBlueskyConnection } from "./bluesky.js";
import { t } from "./i18n.js";
import { api, blobURL } from "./api.js";

// Selection is a snapshot: late persona responses never become public by default.
export async function openSocial(root, getPersona) {
  let entries;
  try { entries = (await api.thread(root)).entries; }
  catch (error) { toast(error.message, "error"); return; }
  let plan = null;
  let busy = false;
  const selected = new Set([root]);
  const dialog = el("dialog", { class: "dialog social-dialog", "aria-labelledby": "social-title" });
  let connected = false;
  const connection = createBlueskyConnection({ returnRoot: root, onChange: (value) => {
    connected = value.connected;
    invalidate();
    previewButton.disabled = busy || !connected;
  } });
  const preview = el("div", { class: "social-preview", "aria-live": "polite" });
  const status = el("p", { role: "status", class: "note" });
  const publish = el("button", { class: "button", label: "選択した会話を投稿", disabled: true, onclick: send });
  const selection = el("div", { class: "social-selection" });
  function invalidate() { plan = null; publish.disabled = true; clear(preview); status.textContent = ""; }
  for (const entry of entries) {
    if (entry.deleted) continue;
    const persona = getPersona(entry.speaker);
    const name = entry.speaker === "user" ? t("あなた") : t("{0}からの返信", `${persona?.icon || "◎"} ${persona?.name || entry.speaker}`);
    const checkbox = el("input", { type: "checkbox", checked: entry.id === root, disabled: entry.id === root });
    checkbox.addEventListener("change", () => {
      if (checkbox.checked) selected.add(entry.id); else selected.delete(entry.id);
      invalidate();
    });
    selection.append(el("label", { class: "social-choice" }, checkbox,
      el("span", {}, el("strong", { text: entry.id === root ? t("元の投稿 · {0}", name) : name }),
        el("span", { class: "said", text: entry.text || t("［画像］") }))));
  }
  const body = () => ({ root, target: "bluesky", entryIds: [...selected] });
  const previewButton = el("button", { class: "button secondary", label: "プレビュー", disabled: true, onclick: async () => {
    if (busy) return;
    setBusy(true); invalidate();
    try {
      plan = await api.previewSocial(body());
      const numbers = new Map(plan.posts.map((post, i) => [post.id || post.entryId, i + 1]));
      for (const [i, post] of plan.posts.entries()) {
        preview.append(el("article", { class: "social-preview-post" },
          el("strong", { text: post.parent ? t("{0} · {1}への返信", i + 1, numbers.get(post.parent)) : t("{0} · 元の投稿", i + 1) }),
          el("p", { class: "said", text: post.text }),
          post.blobId ? el("img", { src: blobURL(post.blobId), alt: t("公開する画像") }) : null));
      }
      status.textContent = t("{0}件をBlueskyの {1} から公開します。", plan.posts.length, plan.handle || plan.account);
    } catch (error) { status.textContent = t(error.message); }
    finally { setBusy(false); }
  } });
  function setBusy(value) {
    busy = value;
    for (const input of dialog.querySelectorAll("input, select, button")) input.disabled = value;
    selection.querySelector("input").disabled = true;
    publish.disabled = value || !plan;
    previewButton.disabled = value || !connected;
  }
  async function send() {
    if (busy || !plan) return;
    setBusy(true); status.textContent = t("投稿しています…");
    try {
      const result = await api.publishSocial({ ...body(), digest: plan.digest });
      status.textContent = t(result.error || "選択した会話を公開しました。");
      for (const post of result.posts || []) {
        if (post.publication.url) preview.append(el("a", { href: post.publication.url, target: "_blank", rel: "noreferrer", label: "公開した投稿を開く" }));
      }
      // A new preview is needed before any retry or extension of the thread.
      plan = null;
    } catch (error) { status.textContent = t("{0}。再操作するときはプレビューから確認してください。", t(error.message)); plan = null; }
    finally { setBusy(false); }
  }
  dialog.append(el("h2", { id: "social-title", label: "会話をBlueskyに公開" }),
    el("p", { class: "note", label: "公開したい発言だけ選択してください。Personaの返信には個人的な情報が含まれることがあります。返信を選ぶときは、その返信先も選んでください。" }),
    connection.element, selection,
    previewButton, preview, status,
    el("div", { class: "compose-row" }, publish,
      el("button", { class: "chip ghost", label: "閉じる", onclick: () => dialog.close() })));
  dialog.addEventListener("cancel", (event) => { if (busy) event.preventDefault(); });
  dialog.addEventListener("close", () => { connection.dispose(); dialog.remove(); });
  document.body.append(dialog); dialog.showModal();
}
