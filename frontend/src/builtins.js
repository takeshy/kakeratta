// What the built-in personas are for, in the reader's language.
//
// They are named in romaji - tabe, michi, shiori, sona - so that @tabe can
// be typed anywhere; the name is the same in both interfaces. Their domains are
// written in Japanese because the router and the persona read them, so the
// panel shows this instead.
import { getLanguage } from "./i18n.js";

const GLOSS = {
  gourmet: {
    ja: "食事",
    en: "food",
    domain: "Food, drinks, restaurants and meal photos; menus, preferences and choosing what to eat.",
  },
  move: {
    ja: "移動",
    en: "travel",
    domain: "Plans, wishes or travel to a destination, even just a type of place. The persona researches routes, transport and travel time.",
  },
  book: {
    ja: "読書",
    en: "reading",
    domain: "Books and reading: book images, quotations, impressions and questions about the content, in any subject.",
  },
  routine: {
    ja: "備え",
    en: "preparation",
    domain: "Upcoming plans, everyday belongings, habits and past omissions. It checks what you need before leaving, uses forecasts when weather affects your outing, and gently follows up on small chores.",
  },
};

// gloss(row) → { name, note, domain }: the name, one word for what it covers,
// and the domain as the interface language reads it.
export function gloss(row) {
  const entry = row.builtin && !row.customized ? GLOSS[row.id] : null;
  if (!entry) return { name: row.name, note: "", domain: row.domain };
  const ja = getLanguage() === "ja";
  return { name: row.name, note: ja ? entry.ja : entry.en, domain: ja ? row.domain : entry.domain };
}

// For a sentence: "tabe (food)" / "tabe（食事）".
export function named(row) {
  const { note } = gloss(row);
  if (!note) return row.name;
  return getLanguage() === "ja" ? `${row.name}（${note}）` : `${row.name} (${note})`;
}
