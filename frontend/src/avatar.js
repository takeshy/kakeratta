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

// Emoji are picked from groups rather than typed: most keyboards make them
// hard to reach. The text box stays, folded under the groups, for anything
// the groups do not have.
const split = text => text.match(/(?:\p{Extended_Pictographic}|◎)\uFE0F?/gu);
export const EMOJI_GROUPS = [
  { label: "顔", icon: "🙂", emoji: split("🙂😀😄😊😉😍🥰😎🤓🧐🤔😌😴🥳🤗😇🙃😺🤖👻👽🎃🦸🧙") },
  { label: "人", icon: "🧑", emoji: ["🧑", "👩", "👨", "🧒", "👧", "👦", "👵", "👴", "🧑‍🍳", "🧑‍🏫", "🧑‍⚕️", "🧑‍💻", "🧑‍🎨", "🧑‍🔬", "🧑‍🌾", "🧑‍🚀", "🕵️", "💁", "🙋", "🧘", "🏃", "👋", "👍", "🙌"] },
  { label: "動物", icon: "🐶", emoji: split("🐶🐱🐭🐹🐰🦊🐻🐼🐨🐯🦁🐮🐷🐸🐵🐔🐧🐦🦉🦄🐝🦋🐢🐬") },
  { label: "自然", icon: "🌸", emoji: split("🌸🌻🌷🌹🍀🌿🌱🌲🍁🍄🌵🌙⭐🌞🌈☁️⛄🔥💧🌊⚡🌍🪐🌋") },
  { label: "食べ物", icon: "🍙", emoji: split("🍙🍣🍜🍛🍱🍔🍕🍞🥐🍰🍩🍪🍫🍎🍓🍑🍌🍇🍉☕🍵🍺🍷🥗") },
  { label: "趣味", icon: "⚽", emoji: split("⚽⚾🏀🎾🏓🎯🎮🎲🧩🎨🎵🎸🎹🎤🎬📷📚✏️🧶🏕️🎣🚴🏊🎭") },
  { label: "乗り物・場所", icon: "🚃", emoji: split("🚃🚄🚗🚌🚲✈️🚀⛵🗺️🧭🏠🏢🏫🏥🏯⛩️🗼🗻🏝️🏖️🎡⛲🌃🏞️") },
  { label: "もの", icon: "💡", emoji: split("💡📱💻⌚📖📝📌📎🔑🔍🧪💊🩺💰🎁🛒🧳☂️🕯️🔔📅⏰📊🧸") },
  { label: "記号", icon: "❤️", emoji: split("❤️🧡💛💚💙💜🤍✨💫🌟✅❗❓💬💭♻️⚙️🔆💯🏳️🚩🆗🔰◎") },
];

// The selected photo is always compressed before a draft or save sees it.
export function createAvatarField({ icon = "", avatar = "", onChange = () => {}, onBusy = () => {}, disabled = false } = {}) {
  let value = { icon, avatar }, pending = false, locked = disabled, group = 0;
  const input = el("input", { class: "avatar-icon-input", type: "text", maxlength: 8, "aria-label": t("絵文字を直接入力") });
  input.value = icon;
  const photo = el("input", { type: "file", accept: "image/*", hidden: true, "aria-label": t("写真を選ぶ") });
  const preview = el("span", { class: "avatar-preview" });
  const status = el("span", { class: "hint avatar-status" });
  const error = el("p", { class: "hint", role: "alert", hidden: true });
  const choose = el("button", { class: "chip", type: "button", onclick: () => photo.click() });
  const remove = el("button", { class: "chip ghost", type: "button", label: "写真を外す", onclick: () => change({ ...value, avatar: "" }) });
  const tabs = el("div", { class: "avatar-emoji-tabs", role: "tablist", "aria-label": t("絵文字のグループ") });
  const grid = el("div", { class: "avatar-emoji-grid", role: "group" });
  const other = el("details", { class: "avatar-emoji-other" },
    el("summary", { label: "その他の絵文字を入力" }), input);
  const picker = el("div", { class: "avatar-emoji-picker" }, tabs, grid, other);
  let buttons = [], tabButtons = [];
  // Picking an emoji is the same as typing it: the photo gives way to it.
  function pick(emoji) {
    if (pending || locked) return;
    input.value = emoji;
    if (input.dispatchEvent && typeof Event === "function") input.dispatchEvent(new Event("input", { bubbles: true }));
    change({ icon: emoji, avatar: "" });
  }
  function drawGrid() {
    clear(grid);
    grid.setAttribute("aria-label", t(EMOJI_GROUPS[group].label));
    buttons = EMOJI_GROUPS[group].emoji.map(emoji => el("button", {
      class: "avatar-emoji", type: "button", "aria-label": emoji, text: emoji, onclick: () => pick(emoji),
    }));
    grid.append(...buttons);
    tabButtons.forEach((tab, index) => {
      tab.setAttribute("aria-selected", String(index === group));
      tab.className = `avatar-emoji-tab${index === group ? " active" : ""}`;
    });
  }
  tabButtons = EMOJI_GROUPS.map((entry, index) => el("button", {
    class: "avatar-emoji-tab", type: "button", role: "tab", title: t(entry.label), "aria-label": t(entry.label),
    onclick: () => { group = index; drawGrid(); render(); },
  }, el("span", { "aria-hidden": "true", text: entry.icon }), el("span", { class: "avatar-emoji-tab-label", label: entry.label })));
  tabs.append(...tabButtons);
  const render = () => {
    clear(preview).append(avatarNode(value, { fallback: "🙂" }));
    remove.hidden = !value.avatar;
    choose.textContent = t(value.avatar ? "写真を変更" : "写真を選ぶ");
    status.textContent = t(value.avatar ? "写真を表示中" : "絵文字を表示中");
    for (const node of [input, photo, choose, remove, ...buttons, ...tabButtons]) node.disabled = locked || pending;
    for (const button of buttons) {
      const current = !value.avatar && button.textContent === value.icon;
      button.setAttribute("aria-pressed", String(current));
      button.className = `avatar-emoji${current ? " selected" : ""}`;
    }
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
  // Open on the group that holds the current emoji, if any does.
  const found = EMOJI_GROUPS.findIndex(entry => entry.emoji.includes(icon));
  if (found >= 0) group = found;
  const element = el("div", { class: "avatar-field" },
    el("strong", { class: "avatar-field-title", label: "アイコン" }),
    el("div", { class: "avatar-summary" }, preview,
      el("div", { class: "avatar-summary-body" }, status,
        el("div", { class: "avatar-actions" }, choose, remove, photo),
        el("span", { class: "hint", label: "写真を選ぶと絵文字より優先して表示します。" }))),
    picker, error);
  drawGrid();
  render();
  return { element, input, value: () => ({ ...value }), busy: () => pending,
    set: next => { value = { ...value, ...next }; input.value = value.icon; render(); },
    setDisabled: next => { locked = next; render(); } };
}

export const personaTextDraft = draft => draft ? { ...draft, avatar: undefined } : draft;
