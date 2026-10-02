// What the built-in personas are for, in the reader's language.
//
// They are named in romaji - tabe, shiori, sona, support - so that @tabe can
// be typed anywhere; the name is the same in both interfaces. Their domains are
// written in Japanese because the router and the persona read them, so the
// panel shows this instead.
import { getLanguage } from "./i18n.js";

const GLOSS = {
  infographic: {
    ja: "図解",
    en: "infographic",
    name: "Infographic",
    domain: "Explicit requests to turn a post, expanded article, interpreted image or conversation into an HTML infographic, in the current display language. Does not keep memory or check in.",
  },
  gourmet: {
    ja: "食事",
    en: "food",
    domain: "Food, drinks, restaurants and meal photos; menus, preferences and choosing what to eat.",
  },
  support: {
    ja: "サポート",
    en: "support",
    domain: "Questions and problems about kakeratta: usage, specifications, settings, personas, memory, notifications, search and integrations. Answers from the shipped specification.",
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
  return { name: ja ? row.name : entry.name || row.name, note: ja ? entry.ja : entry.en, domain: ja ? row.domain : entry.domain };
}

// For a sentence: "tabe (food)" / "tabe（食事）".
export function named(row) {
  const { name, note } = gloss(row);
  if (!note) return name;
  return getLanguage() === "ja" ? `${name}（${note}）` : `${name} (${note})`;
}
