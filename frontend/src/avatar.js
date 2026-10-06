import { el, clear } from "./dom.js";
import { t } from "./i18n.js";

const PREFIX = "data:image/jpeg;base64,";
const MAX_DATA_LENGTH = PREFIX.length + 4 * Math.ceil(32768 / 3);
export function avatarSource(value) {
  if (typeof value !== "string" || !value.startsWith(PREFIX) || value.length > MAX_DATA_LENGTH) return "";
  const data = value.slice(PREFIX.length);
  if (!data || data.length % 4 || !/^[A-Za-z0-9+/]+={0,2}$/.test(data)) return "";
  const padding = data.endsWith("==") ? 2 : data.endsWith("=") ? 1 : 0;
  return data.length / 4 * 3 - padding <= 32768 ? value : "";
}
export const userProfile = settings => ({ name: settings?.profile?.name?.trim() || t("あなた"), icon: settings?.profile?.icon || "🙂", avatar: settings?.profile?.avatar || "" });

export function avatarNode(profile = {}, { className = "avatar", fallback = "◎" } = {}) {
  const source = avatarSource(profile.avatar);
  if (!source) return el("span", { class: className, "aria-hidden": "true", text: profile.icon || fallback });
  return el("span", { class: `${className} avatar-photo`, "aria-hidden": "true" },
    el("img", { src: source, alt: "", width: 128, height: 128, decoding: "async" }));
}

export async function compressAvatar(file) {
  if (!file || file.size > 20 * 1024 * 1024) throw Error(t("写真は20MB以下の画像を選んでください。"));
  const url = URL.createObjectURL(file);
  const image = new Image();
  try {
    image.src = url;
    await image.decode();
    const width = image.naturalWidth, height = image.naturalHeight;
    if (!width || !height) throw Error("invalid image");
    const edge = Math.min(width, height);
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 128;
    const context = canvas.getContext("2d");
    context.fillStyle = "#fff";
    context.fillRect(0, 0, 128, 128);
    context.drawImage(image, (width - edge) / 2, (height - edge) / 2, edge, edge, 0, 0, 128, 128);
    for (const quality of [0.82, 0.65, 0.5, 0.35]) {
      const result = canvas.toDataURL("image/jpeg", quality);
      if (avatarSource(result)) return result;
    }
    throw Error("too large");
  } catch {
    throw Error(t("写真を読み込めませんでした。JPEGやPNGの画像を選んでください。"));
  } finally { URL.revokeObjectURL(url); }
}

// The selected photo is always compressed before a draft or save sees it.
export function createAvatarField({ icon = "", avatar = "", onChange = () => {}, onBusy = () => {}, disabled = false } = {}) {
  let value = { icon, avatar }, pending = false, locked = disabled;
  const input = el("input", { class: "avatar-icon-input", type: "text", maxlength: 8, "aria-label": t("アイコン") });
  input.value = icon;
  const photo = el("input", { type: "file", accept: "image/*", hidden: true, "aria-label": t("写真を選ぶ") });
  const preview = el("span", { class: "avatar-preview" });
  const error = el("p", { class: "hint", role: "alert", hidden: true });
  const choose = el("button", { class: "chip", type: "button", label: "写真を選ぶ", onclick: () => photo.click() });
  const remove = el("button", { class: "chip", type: "button", label: "写真を外す", onclick: () => change({ ...value, avatar: "" }) });
  const render = () => {
    clear(preview).append(avatarNode(value));
    remove.hidden = !value.avatar;
    for (const node of [input, photo, choose, remove]) node.disabled = locked || pending;
  };
  async function change(next) {
    if (pending || locked) return;
    pending = true; error.hidden = true; render(); onBusy(true);
    try {
      if (await onChange(next) === false) throw Error(t("保存できませんでした。もう一度お試しください。"));
      value = next;
    } catch (problem) { error.textContent = problem.message; error.hidden = false; }
    finally { pending = false; input.value = value.icon; onBusy(false); render(); }
  }
  input.addEventListener("change", () => change({ icon: input.value.trim(), avatar: "" }));
  photo.addEventListener("change", async () => {
    const file = photo.files?.[0];
    if (!file || pending || locked) return;
    pending = true; error.hidden = true; render(); onBusy(true);
    try {
      const compressed = await compressAvatar(file);
      const next = { ...value, avatar: compressed };
      if (await onChange(next) === false) throw Error(t("保存できませんでした。もう一度お試しください。"));
      value = next;
    } catch (problem) { error.textContent = problem.message; error.hidden = false; }
    finally { pending = false; input.value = value.icon; photo.value = ""; onBusy(false); render(); }
  });
  const element = el("div", { class: "avatar-field" },
    el("label", { class: "field" }, el("span", { label: "アイコン" }), input),
    el("div", { class: "compose-row" }, preview, choose, remove, photo), error);
  render();
  return { element, input, value: () => ({ ...value }), busy: () => pending,
    set: next => { value = { ...value, ...next }; input.value = value.icon; render(); },
    setDisabled: next => { locked = next; render(); } };
}

export const personaTextDraft = draft => draft ? { ...draft, avatar: undefined } : draft;
