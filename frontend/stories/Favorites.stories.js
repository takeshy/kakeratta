import { createFavorites } from "../src/favorites.js";
import { getLanguage, t } from "../src/i18n.js";
import { serve } from "./api.js";
import { sheet } from "./frame.js";
import { settings } from "./fixtures.js";

export default { title: "Favorites", parameters: { layout: "fullscreen" } };

function panel({ filtered = false, empty = false, loading = false, error = false, many = false } = {}) {
  const ja = getLanguage() === "ja";
  const personas = [
    { id: "wine", name: ja ? "ぶどう" : "budou", icon: "🍷" },
    { id: "move", name: ja ? "てくてく" : "tekuteku", icon: "👟" },
  ];
  let entries = [
    { id: "wine-post", root: "wine-post", kind: "post", speaker: "user", at: "2026-08-01T10:00:00Z",
      text: ja ? "渋みの少ない赤ワインを探している。" : "I'm looking for a red wine with soft tannins." },
    { id: "wine-answer", root: "wine-post", replyTo: "wine-post", kind: "remark", speaker: "wine", at: "2026-08-01T10:01:00Z",
      text: ja ? "軽やかな**ピノ・ノワール**や**ガメイ**を候補にするとよさそうです。少し冷やすと飲みやすくなります。" : "Try a light **Pinot Noir** or **Gamay**. Serving it slightly chilled can make it even more approachable." },
    { id: "walk-post", root: "walk-post", kind: "post", speaker: "user", at: "2026-09-28T08:00:00Z",
      text: ja ? "昼休みに少し歩きたい。" : "I'd like to walk a little during my lunch break." },
    { id: "walk-answer", root: "walk-post", replyTo: "walk-post", kind: "remark", speaker: "move", at: "2026-09-28T08:01:00Z",
      text: ja ? "まずは10分だけ、公園まで歩いて戻るコースにしましょう。" : "Start with just ten minutes: walk to the park and back." },
  ];
  let items = empty ? [] : [
    { id: "wine-answer", entry: entries[1], at: "2026-10-02T10:00:00Z" },
    { id: "walk-answer", entry: entries[3], at: "2026-10-01T10:00:00Z" },
    { id: "wine-post", entry: entries[0], at: "2026-09-30T10:00:00Z" },
  ];
  if (many) {
    const originals = entries;
    const saved = items;
    entries = [];
    items = [];
    for (let i = 0; i < 15; i++) {
      const copies = originals.map(entry => ({ ...entry, id: `${entry.id}-${i}`, root: `${entry.root}-${i}`,
        replyTo: entry.replyTo ? `${entry.replyTo}-${i}` : undefined,
        text: `${i + 1}. ${entry.text}` }));
      entries.push(...copies);
      items.push(...saved.map((item, offset) => ({ id: `${item.id}-${i}`, entry: copies.find(entry => entry.id === `${item.id}-${i}`),
        at: new Date(Date.parse("2026-10-02T10:00:00Z") - (i * saved.length + offset) * 60000).toISOString() })));
    }
  }
  serve({ settings, personas, search: { entries }, favorites: { items, loading,
    error: error ? t("お気に入りを読み込めませんでした") : null } });
  const view = createFavorites({ getSettings: () => settings, getPersonas: () => personas,
    getPersona: id => personas.find(row => row.id === id) });
  const host = sheet("お気に入り", view.element);
  void view.show().then(() => {
    if (filtered) {
      const select = view.element.querySelector("select");
      select.value = "wine";
      select.dispatchEvent(new Event("change"));
    }
  });
  return host;
}

export const Recent = { name: "最近追加したお気に入り（確認して解除）", render: () => panel() };
export const ByPersona = { name: "担当で絞り込み", render: () => panel({ filtered: true }) };
export const Empty = { name: "お気に入りがない", render: () => panel({ empty: true }) };
export const Loading = { name: "読み込み中", render: () => panel({ loading: true }) };
export const Error = { name: "読み込み失敗", render: () => panel({ error: true }) };

export const Pagination = { name: "45件・スクロールとさらに読み込む", render: () => panel({ many: true }) };
