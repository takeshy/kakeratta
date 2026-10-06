// What a tool is called on screen.
//
// One capability can reach the app under more than one name: the built-in one
// ("place_search") and a server-qualified one ("geo.place_search") when an MCP
// server offers the same thing. For the reader that is one choice, so the list
// shows a single entry per capability, under the built-in name - the name that
// also falls back to the built-in call when the server cannot be reached.
const labels = {
  kakeratta_help: "kakerattaの仕様を読む",
  web_search: "Webで調べる",
  place_search: "お店や場所を探す",
  weather_forecast: "天気予報を調べる",
  schedule: "自分を起こす予定を置く",
  timeline_search: "過去のかけらを探す",
};

// capability is the tool's name without its server prefix. The first dot
// separates, the way the server splits a qualified name; server names have none.
export function capability(name) {
  const text = String(name);
  const dot = text.indexOf(".");
  return dot === -1 ? text : text.slice(dot + 1);
}

// toolLabel is the one line shown for a tool, in the current language.
export function toolLabel(name) {
  return labels[capability(name)] || name;
}

// uniqueTools omits removed built-in tools and folds a server-qualified twin into the built-in name beside it,
// so "geo.place_search" never appears next to "place_search". A tool that only
// a server offers stays under its qualified name.
export function uniqueTools(names) {
  const list = (names || []).filter(name => name !== "route");
  const plain = new Set(list.filter((name) => capability(name) === name));
  const out = [];
  for (const name of list) {
    const base = capability(name);
    if (name !== base && plain.has(base)) continue;
    if (!out.includes(name)) out.push(name);
  }
  return out;
}
