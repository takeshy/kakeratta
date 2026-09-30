// How large the text is, kept on this device like the display language: a
// phone and a desktop want different answers. The page is sized in rem, so
// scaling the root scales all of it.
const STORAGE_KEY = "kakeratta.textSize";

export const TEXT_SIZES = [["standard", "標準", 100], ["large", "大", 115], ["xlarge", "特大", 130]];

export function getTextSize() {
  try {
    const value = globalThis.localStorage?.getItem(STORAGE_KEY);
    return TEXT_SIZES.some(([id]) => id === value) ? value : "standard";
  } catch { return "standard"; }
}

export function setTextSize(next) {
  const id = TEXT_SIZES.some(([size]) => size === next) ? next : "standard";
  try { globalThis.localStorage?.setItem(STORAGE_KEY, id); } catch { /* storage may be unavailable */ }
  applyTextSize(id);
  return id;
}

export function applyTextSize(id = getTextSize()) {
  const percent = TEXT_SIZES.find(([size]) => size === id)?.[2] ?? 100;
  globalThis.document?.documentElement.style.setProperty("font-size", `${percent}%`);
}
