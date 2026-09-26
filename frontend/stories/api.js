// The panels load themselves from /api. A story has no server, so it hands
// them one: fetch answers from the fixtures, in the shapes the real routes
// use, and writes go back into the same fixtures so a toggle looks toggled.
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

export function serve({ personas = [], tools = [], settings = {}, about = null, memory = {}, models = [], personaDraft = null, search = null, social = null, routing = {}, routingCheck = { answered: true, delivered: true }, replyPreview = { quiet: true }, bluesky = { available: true, connected: false }, mcp = { configured: false, path: "/api/mcp/users/sample" } } = {}) {
  let rows = personas.map((row) => ({ ...row }));
  let current = { ...settings };
  let connection = { ...bluesky };
  memory = structuredClone(memory);
  globalThis.fetch = async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.origin);
    const method = (init.method || "GET").toUpperCase();
    const path = url.pathname;
    const body = typeof init.body === "string" ? JSON.parse(init.body) : null;
    if (path === "/api/social/bluesky/connection" && method === "GET") return json(connection);
    if (path === "/api/social/bluesky/connection" && method === "DELETE") {
      connection = { available: true, connected: false };
      return json({ disconnected: true, remoteRevoked: true });
    }
    // Stories never contact a real SNS or simulate a successful public post.
    if (path === "/api/social/bluesky/connect" || path === "/api/social/publish") {
      return json({ error: "This sample does not connect or publish to Bluesky." }, 400);
    }
    if (path === "/api/social/preview" && social) {
      return json({ target: "bluesky", root: social.root, account: connection.did, handle: connection.handle,
        digest: "sample-preview", posts: social.posts.filter((post) => body.entryIds.includes(post.entryId)) });
    }
    if (path.startsWith("/api/thread/") && social) {
      return json({ entries: social.entries });
    }
    if (path === "/api/timeline/search" && method === "POST" && search) {
      if (search.loading) return new Promise(() => {});
      if (search.error) return json({ error: search.error }, 502);
      let hits = search.hits || [];
      if (body.mode === "keyword") {
        const terms = body.query.normalize("NFKC").toLowerCase().trim().split(/\s+/);
        hits = hits.filter(({ snippet }) => terms.every((term) => snippet.normalize("NFKC").toLowerCase().includes(term)));
      }
      return json({ hits: hits.slice(body.offset || 0, (body.offset || 0) + (body.limit || 20)), total: hits.length });
    }
    if (path.startsWith("/api/thread/") && search) {
      const id = decodeURIComponent(path.slice("/api/thread/".length));
      const entry = search.entries.find((entry) => entry.id === id);
      if (!entry) return json({ error: "投稿が見つかりません" }, 404);
      const root = entry.kind === "post" ? entry.id : entry.root;
      return json({ entries: search.entries.filter((entry) => entry.id === root || entry.root === root) });
    }
    if (path === "/api/personas/build" && method === "POST" && personaDraft) return json(personaDraft);
    // The router's call about a post comes from the fixtures; a post they say
    // nothing about reads as unrecorded. A trial run always lands as meant.
    const decided = path.match(/^\/api\/entry\/([^/]+)\/routing$/);
    if (decided && method === "GET") return json(routing[decodeURIComponent(decided[1])] || { recorded: false });
    if (/^\/api\/entry\/[^/]+\/routing\/check$/.test(path) && method === "POST") return json(routingCheck);
    if (/^\/api\/entry\/[^/]+\/reply\/preview$/.test(path) && method === "POST") return json(replyPreview);
    if (/^\/api\/personas\/[^/]+$/.test(path) && method === "PUT") {
      rows = rows.map((row) => row.id === body.id ? { ...row, ...body } : row);
      return json(body);
    }
    if (path === "/api/personas" && method === "GET") return json({ personas: rows, tools, mcpConfigured: mcp.configured });
    const planner = path.match(/^\/api\/personas\/([^/]+)\/planner$/);
    if (planner && method === "PUT") {
      rows = rows.map(row => row.id === decodeURIComponent(planner[1]) ? { ...row, planner: body.planner } : row);
      return json({ personas: rows, tools, mcpConfigured: mcp.configured });
    }
    if (path === "/api/personas" && method === "PUT") {
      rows = rows.map((row) => ({ ...row, active: body.active.includes(row.id) }));
      return json({ personas: rows, tools });
    }
    const daily = path.match(/^\/api\/personas\/([^/]+)\/daily$/);
    if (daily && method === "PUT") {
      rows = rows.map(row => row.id === decodeURIComponent(daily[1]) ? { ...row, dailyCheckIn: body } : row);
      return json({ personas: rows, tools });
    }
    const singleNote = path.match(/^\/api\/personas\/([^/]+)\/memory\/note$/);
    if (singleNote && method === "DELETE") {
      const id = decodeURIComponent(singleNote[1]);
      const selected = url.searchParams.get("path");
      if (!selected) return json({ error: "Choose a note." }, 400);
      if (!memory[id]?.notes?.some(note => note.path === selected)) return json({ error: "Note not found." }, 404);
      memory[id].notes = memory[id].notes.filter(note => note.path !== selected);
      return json({ ok: true });
    }
    if (singleNote && method === "GET") {
      const found = memory[decodeURIComponent(singleNote[1])]?.notes?.find(note => note.path === url.searchParams.get("path"));
      return found ? json(found) : json({ error: "Note not found." }, 404);
    }
    const note = path.match(/^\/api\/personas\/([^/]+)\/memory$/);
    if (note && method === "GET") {
      const notes = memory[decodeURIComponent(note[1])]?.notes || [];
      const tag = url.searchParams.get("tag");
      const type = url.searchParams.get("type");
      const shown = notes.filter(item => (!tag || item.text.includes(tag)) && (!type || new RegExp(`^type:\\s*${type}\\b`, "im").test(item.text)));
      return json({ notes: shown, paths: notes.map(item => item.path) });
    }
    if (note && method === "DELETE") { memory[decodeURIComponent(note[1])] = { notes: [] }; return json({ ok: true }); }
    if (path === "/api/personas/install") return json({ error: "ストーリーの中では追加できません" }, 400);
    if (path === "/api/settings" && method === "GET") return json(current);
    if (path === "/api/settings" && method === "PUT") {
      current = body;
      return json(current);
    }
    // The MCP key is what an external agent connects with. A story says
    // whether one exists and never hands out a key.
    if (path === "/api/mcp/connection" && method === "GET") return json(mcp);
    if (path === "/api/about") return about ? json(about) : json({ error: "about" }, 404);
    if (path === "/api/models") return json({ models });
    if (path === "/api/openrouter/models") return json({ models });
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
