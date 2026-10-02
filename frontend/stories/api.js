import { adoptFavoriteIDs } from "../src/favorite-state.js";

// The panels load themselves from /api. A story has no server, so it hands
// them one: fetch answers from the fixtures, in the shapes the real routes
// use, and writes go back into the same fixtures so a toggle looks toggled.
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

export function serve({ personas = [], tools = [], settings = {}, about = null, memory = {}, models = [], personaDraft = null, search = null, favorites = null, social = null, routing = {}, routingCheck = { answered: true, delivered: true }, replyPreview = { quiet: true }, bluesky = { available: true, connected: false }, mcp = { configured: false, path: "/api/mcp/users/sample" } } = {}) {
  const saved = new Map((favorites?.items || []).map(item => [item.id, structuredClone(item)]));
  adoptFavoriteIDs([...saved.keys()]);
  const favoriteEntries = () => search?.entries || favorites?.items?.map(item => item.entry) || [];
  let rows = personas.map((row) => ({ ...row }));
  let current = { ...settings };
  let connection = { ...bluesky };
  mcp = structuredClone(mcp);
  mcp.connections ||= mcp.configured ? [{ id: "default", name: "Hermes", active: true }] : [];
  let nextConnection = 1;
  memory = structuredClone(memory);
  globalThis.fetch = async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.origin);
    const method = (init.method || "GET").toUpperCase();
    const path = url.pathname;
    const body = typeof init.body === "string" ? JSON.parse(init.body) : null;
    if (path === "/api/favorites" && method === "GET") {
      if (favorites?.loading) return new Promise(() => {});
      if (favorites?.error) return json({ error: favorites.error }, 500);
      const all = [...saved.values()].sort((a, b) => b.at.localeCompare(a.at) || b.id.localeCompare(a.id));
      const speaker = url.searchParams.get("speaker");
      const shown = all.filter(item => !speaker || item.entry.speaker === speaker);
      const offset = Number(url.searchParams.get("offset") || 0), limit = Number(url.searchParams.get("limit") || 20);
      return json({ items: shown.slice(offset, offset + limit), total: shown.length, ids: [...saved.keys()],
        speakers: Object.fromEntries(all.map(item => [item.entry.speaker, true])) });
    }
    const favorite = path.match(/^\/api\/entry\/([^/]+)\/favorite$/);
    if (favorite && method === "PUT") {
      const id = decodeURIComponent(favorite[1]);
      const entry = favoriteEntries().find(entry => entry.id === id);
      if (!entry) return json({ error: "投稿が見つかりません" }, 404);
      if (!saved.has(id)) saved.set(id, { id, at: new Date().toISOString(), entry });
      return json(saved.get(id));
    }
    if (favorite && method === "DELETE") { saved.delete(decodeURIComponent(favorite[1])); return new Response(null, { status: 204 }); }
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
      const tails = new Map();
      const first = social.posts.find(post => body.entryIds.includes(post.entryId))?.entryId;
      const posts = social.posts.filter(post => body.entryIds.includes(post.entryId)).flatMap(post => {
        const rendered = body.htmlImages?.[post.entryId];
        const parent = post.entryId === first ? "" : tails.get(post.parent) || tails.get(first) || "";
        if (!rendered) { tails.set(post.entryId, post.id || post.entryId); return [{ ...post, parent }]; }
        let tail = parent;
        const pages = rendered.images.map((image, i) => {
          const id = i ? `${post.entryId}:image:${i+1}` : post.entryId;
          const page = { entryId: post.entryId, ...(i ? { id } : {}), parent: tail, text: i ? `つづき:\n［画像 ${i+1}/${rendered.images.length}］` : "［画像］", image, imageAlt: rendered.alt };
          tail = id; return page;
        });
        tails.set(post.entryId, tail);
        return pages;
      });
      return json({ target: "bluesky", root: first, account: connection.did, handle: connection.handle,
        digest: "sample-preview", posts });
    }
    if (path.startsWith("/api/thread/") && social) {
      return json({ entries: social.entries });
    }
    if (path === "/api/timeline/search" && method === "POST" && search) {
      if (search.loading) return new Promise(() => {});
      if (search.error) return json({ error: search.error }, 502);
      let hits = search.hits || [];
      if (body.favoritesOnly) hits = hits.filter(hit => saved.has(hit.entry.id));
      if (body.mode === "keyword") {
        const terms = body.query.normalize("NFKC").toLowerCase().trim().split(/\s+/);
        hits = hits.filter(({ snippet }) => terms.every((term) => snippet.normalize("NFKC").toLowerCase().includes(term)));
      }
      return json({ hits: hits.slice(body.offset || 0, (body.offset || 0) + (body.limit || 20)), total: hits.length });
    }
    const spoken = path.match(/^\/api\/speaker\/([^/]+)$/);
    if (spoken && search) {
      const who = decodeURIComponent(spoken[1]);
      const rows = search.entries.filter((entry) => entry.speaker === who).sort((a, b) => b.at.localeCompare(a.at));
      const offset = Number(url.searchParams.get("offset") || 0), limit = Number(url.searchParams.get("limit") || 20);
      const page = rows.slice(offset, offset + limit);
      const roots = new Set(page.map((entry) => entry.root));
      return json({ hits: page.map((entry) => ({ entry, snippet: entry.text, passage: entry.text })), total: rows.length,
        entries: search.entries.filter((entry) => roots.has(entry.id) || roots.has(entry.root)) });
    }
    if (path.startsWith("/api/thread/") && (search || favorites)) {
      const id = decodeURIComponent(path.slice("/api/thread/".length));
      const entries = favoriteEntries();
      const entry = entries.find((entry) => entry.id === id);
      if (!entry) return json({ error: "投稿が見つかりません" }, 404);
      const root = entry.kind === "post" ? entry.id : entry.root;
      return json({ entries: entries.filter((entry) => entry.id === root || entry.root === root) });
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
    if (path === "/api/mcp/connection" && method === "GET") return json(mcp);
    if (path === "/api/mcp/connection" && method === "POST") {
      const id = body?.id || (body?.name ? `sample-${nextConnection++}` : "default");
      let row = mcp.connections.find(row => row.id === id);
      if (!row) {
        mcp.connections.forEach(row => { row.active = false; });
        row = { id, name: body?.name || "Hermes", active: true };
        mcp.connections.push(row);
      }
      mcp.configured = true;
      mcp.agent = { away: true, lastSeen: null };
      return json({ ...mcp, id, key: `storybook-demo-token-${id}-not-a-real-credential` });
    }
    if (path === "/api/mcp/connection" && method === "PATCH") {
      const row = mcp.connections.find(row => row.id === body.id);
      if (body.claimMinutes) row.claimMinutes = body.claimMinutes;
      if (body.planMinutes) row.planMinutes = body.planMinutes;
      return json(mcp);
    }
    if (path === "/api/mcp/connection" && method === "PUT") {
      mcp.connections.forEach(row => { row.active = row.id === body.id; });
      mcp.agent = { away: true, lastSeen: null };
      return json(mcp);
    }
    if (path === "/api/mcp/connection" && method === "DELETE") {
      mcp.connections = mcp.connections.filter(row => row.id !== (url.searchParams.get("id") || "default"));
      if (mcp.connections.length && !mcp.connections.some(row => row.active)) mcp.connections[0].active = true;
      mcp.configured = mcp.connections.length > 0;
      mcp.agent = { away: true, lastSeen: null };
      return json(mcp);
    }
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
