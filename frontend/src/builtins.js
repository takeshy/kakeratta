// What the built-in personas are for, in the reader's language.
//
// They are named in romaji - tabe, michi, shiori, plana - so that @tabe can
// be typed anywhere; the name is the same in both interfaces. Their domains are
// written in Japanese because the router and the persona read them, so the
// panel shows this instead.
import { getLanguage } from "./i18n.js";

const GLOSS = {
  gourmet: {
    ja: "食事",
    en: "food",
    domain: "Food: places, dishes, drinks, photos of meals and menus, places to try, \"what should I eat\".",
  },
  move: {
    ja: "移動",
    en: "travel",
    domain: "Getting there: destinations, routes, travel time, meeting times, itineraries, tickets.",
  },
  book: {
    ja: "読書",
    en: "reading",
    domain: "Books and reading: e-book captures, quoted passages, titles and authors, thoughts on a read.",
  },
  routine: {
    ja: "繰り返し",
    en: "routines",
    domain: "Small repeated chores and whether they were done: medicine taken, pets fed, laundry, trash day. Too small for a calendar.",
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
