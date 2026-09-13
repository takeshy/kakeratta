// The panels load themselves from /api. A story has no server, so it hands
// them one: fetch answers from the fixtures, in the shapes the real routes
// use, and writes go back into the same fixtures so a toggle looks toggled.
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

export function serve({ personas = [], tools = [], settings = {}, about = null, memory = {}, models = [], personaDraft = null } = {}) {
  let rows = personas.map((row) => ({ ...row }));
  let current = { ...settings };
  globalThis.fetch = async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.origin);
    const method = (init.method || "GET").toUpperCase();
    const path = url.pathname;
    const body = typeof init.body === "string" ? JSON.parse(init.body) : null;
    if (path === "/api/personas/build" && method === "POST" && personaDraft) return json(personaDraft);
    if (path === "/api/personas" && method === "GET") return json({ personas: rows, tools });
    if (path === "/api/personas" && method === "PUT") {
      rows = rows.map((row) => ({ ...row, active: body.active.includes(row.id) }));
      return json({ personas: rows, tools });
    }
    const note = path.match(/^\/api\/personas\/([^/]+)\/memory$/);
    if (note && method === "GET") return json(memory[decodeURIComponent(note[1])] || { notes: [] });
    if (note && method === "DELETE") return json({ ok: true });
    if (path === "/api/personas/install") return json({ error: "ストーリーの中では追加できません" }, 400);
    if (path === "/api/settings" && method === "GET") return json(current);
    if (path === "/api/settings" && method === "PUT") {
      current = body;
      return json(current);
    }
    if (path === "/api/about") return about ? json(about) : json({ error: "about" }, 404);
    if (path === "/api/models") return json({ models });
    return json({ error: `the story has no ${method} ${path}` }, 404);
  };
}

// The settings panel drives the read-aloud voice through the speaker; here it
// only needs something to list and to take a rate.
export const speaker = {
  voices: () => ["Kyoko", "O-Ren", "Google 日本語"],
  setRate() {},
  setVoice() {},
};
