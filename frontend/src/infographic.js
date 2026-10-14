// The infographic persona and the external agent write Markdown; this lays it
// out as an infographic. The conversion is mechanical: the first top-level heading and
// the prose under it become the title banner, each heading at the shallowest
// remaining level opens a coloured section card, and everything inside a card
// (deeper headings, lists, tables, quotations, Mermaid fences) is drawn by the
// ordinary Markdown renderer. Like that renderer it only builds DOM nodes and
// never interprets model-written text as markup.
import { el } from "./dom.js";
import { renderMarkdown } from "./markdown.js";

const HEADING = /^(#{1,6})\s+(.+?)\s*#*\s*$/;
// One leading emoji (with its modifiers, joiners and keycaps) shown as a badge.
const EMOJI = /^((?:\p{Extended_Pictographic}|\p{Regional_Indicator}|[#*0-9]️?⃣)(?:[️‍⃣]|\p{Emoji_Modifier}|\p{Extended_Pictographic}|\p{Regional_Indicator})*)\s*(.*)$/u;
const TONES = 6;

// Heading lines outside code fences, so a "#" comment in code stays code.
function outline(text) {
  const lines = String(text || "").replace(/\r\n?/g, "\n").split("\n");
  const headings = [];
  let fenced = false;
  lines.forEach((line, index) => {
    if (/^```/.test(line)) fenced = !fenced;
    const match = !fenced && line.match(HEADING);
    if (match) headings.push({ index, level: match[1].length, title: match[2] });
  });
  return { lines, headings };
}

// An answer is laid out as an infographic when it has structure to lay out.
// A clarifying question in plain prose stays an ordinary reply.
export function isInfographic(text) {
  return outline(text).headings.length > 0;
}

// The infographic persona and the connected external agent both have their
// headed Markdown laid out this way.
export function laidOutAsInfographic(speaker, text) {
  return (speaker === "infographic" || speaker === "agent") && isInfographic(text);
}

function title(tag, className, text) {
  const match = text.match(EMOJI);
  const label = match?.[2] ? match[2] : text;
  return el(tag, { class: className },
    match?.[2] ? el("span", { class: "infographic-emoji", "aria-hidden": "true", text: match[1] }) : null,
    el("span", { text: label.replace(/\*\*([^*\n]+)\*\*/g, "$1") }));
}

const body = (lines) => {
  const source = lines.join("\n").trim();
  return source ? renderMarkdown(source) : null;
};

export function renderInfographic(text) {
  const { lines, headings } = outline(text);
  const root = el("div", { class: "infographic" });
  // A lone "#" heading above everything else is the banner.
  const hero = headings[0]?.level === 1 && headings.slice(1).every(heading => heading.level > 1) ? headings[0] : null;
  const rest = hero ? headings.slice(1) : headings;
  const level = Math.min(...rest.map(heading => heading.level));
  const sections = rest.filter(heading => heading.level === level);
  // Whatever precedes the first card is the banner's lead or a free lead.
  const first = sections[0]?.index ?? lines.length;
  if (hero) {
    const preface = body(lines.slice(0, hero.index));
    if (preface) root.append(preface);
    root.append(el("header", { class: "infographic-hero" },
      title("h3", "infographic-title", hero.title), body(lines.slice(hero.index + 1, first))));
  } else {
    const lead = body(lines.slice(0, first));
    if (lead) root.append(el("div", { class: "infographic-lead" }, lead));
  }
  sections.forEach((heading, number) => {
    const end = sections[number + 1]?.index ?? lines.length;
    root.append(el("section", { class: "infographic-section", "data-tone": String(number % TONES) },
      title("h4", "infographic-heading", heading.title), body(lines.slice(heading.index + 1, end))));
  });
  return root;
}
