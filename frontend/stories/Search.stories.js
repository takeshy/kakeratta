import { createSearch } from "../src/search.js";
import { getLanguage, t } from "../src/i18n.js";
import { serve } from "./api.js";
import { sheet } from "./frame.js";
import { settings } from "./fixtures.js";

export default { title: "Search", parameters: { layout: "fullscreen" } };

// Sample search responses only; the story never calls an embedding provider.
function fixtures() {
  const ja = getLanguage() === "ja";
  const wine = { id: "wine", name: ja ? "ぶどう" : "budou", icon: "🍷" };
  const entries = [
    { id: "wine-1", kind: "post", root: "wine-1", speaker: "user", at: "2026-09-10T10:30:00Z",
      text: ja ? "友人の家で飲んだピノ・ノワール。渋みが少なくて、するっと飲めた。これ好き。" : "Pinot Noir at a friend's place. Soft tannins and so easy to drink. I really liked this one." },
    { id: "wine-reply", kind: "remark", root: "wine-1", speaker: "wine", at: "2026-09-10T10:31:00Z",
      text: ja ? "渋みが穏やかで軽やかな味わいが好みなんですね。次に選ぶときの手がかりとして覚えておきます。" : "You seem to enjoy a lighter wine with gentle tannins. I'll remember that for the next time you choose a bottle." },
    { id: "wine-2", kind: "post", root: "wine-2", speaker: "user", at: "2026-08-22T11:00:00Z",
      text: ja ? "週末のガメイ。赤い果実の香りで重たくない。少し冷やすとおいしかった。" : "Gamay over the weekend. Red fruit aromas without much weight. Lovely slightly chilled." },
    { id: "wine-3", kind: "post", root: "wine-3", speaker: "user", at: "2026-08-03T09:00:00Z",
      text: ja ? "お店で勧めてもらったピノ・ノワール。料理の味を邪魔しなくて、また頼みたい。" : "The Pinot Noir the restaurant recommended complemented the food beautifully. I'd order it again." },
  ];
  return { wine, entries, hits: entries.filter((entry) => entry.kind === "post").map((entry, i) => ({ entry, snippet: entry.text, score: 0.85-i*0.1 })) };
}

function panel(mode, { empty = false, loading = false, error = false, thread = false } = {}) {
  const sample = fixtures();
  serve({ settings, search: {
    entries: sample.entries, hits: empty ? [] : sample.hits, loading,
    error: error ? t("意味検索に接続できませんでした。もう一度検索してください") : null,
  } });
  const view = createSearch({ getSettings: () => settings, getPersona: (id) => id === "wine" ? sample.wine : null });
  const form = view.element.querySelector("form");
  form.querySelector("input").value = mode === "keyword"
    ? (getLanguage() === "ja" ? "ピノ" : "Pinot")
    : (getLanguage() === "ja" ? "軽くて飲みやすかった赤ワイン" : "A light red wine I enjoyed");
  if (thread) {
    const observer = new MutationObserver(() => {
      const hit = view.element.querySelector(".search-hit");
      if (hit) { observer.disconnect(); hit.click(); }
    });
    observer.observe(view.element, { childList: true, subtree: true });
  }
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  return sheet("検索", view.element);
}

export const Keyword = { name: "名前で探す", render: () => panel("keyword") };
export const Semantic = { name: "覚えていることから探す", render: () => panel("semantic") };
export const Thread = { name: "検索結果から元の会話を開く", render: () => panel("semantic", { thread: true }) };
export const Loading = { name: "初回の索引作成中", render: () => panel("semantic", { loading: true }) };
export const Empty = { name: "一致する投稿がない", render: () => panel("keyword", { empty: true }) };
export const Error = { name: "失敗・再検索できる", render: () => panel("semantic", { error: true }) };
