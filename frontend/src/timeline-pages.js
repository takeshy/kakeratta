// Keep paging and live events in one place so a late HTTP response cannot undo
// a reply or deletion that already arrived through the live channel.
export function createTimelinePages({ fetchPage, onChange, onState = () => {} }) {
  let filters = {};
  let generation = 0;
  let entries = new Map();
  let next = null;
  let pages = 0;
  let pending = null;
  let live = new Map();
  const publish = (state) => onChange([...entries.values()], state);

  function upsert(entry) {
    if (pending) live.set(entry.id, entry);
    if (!Object.keys(filters).length || entries.has(entry.id) || [...entries.values()].some(e => (e.root || e.id) === (entry.root || entry.id))) entries.set(entry.id, entry);
    publish();
  }

  // A quiet refresh is a background re-read: it says nothing while loading or
  // on failure, and redraws only when something actually changed.
  async function run(refresh, quiet = false) {
    if (pending) return pending;
    if (!refresh && !next) return;
    // Assign the promise before any callbacks can re-enter this loader.
    live = new Map();
    const current = generation;
    pending = Promise.resolve().then(async () => {
      if (!quiet) onState({ loading: true, hasMore: Boolean(next) });
      try {
        const incoming = refresh ? new Map() : new Map(entries);
        let cursor = refresh ? null : next;
        let count = refresh ? 0 : pages;
        const target = refresh ? Math.max(1, pages) : pages + 1;
        do {
          const page = await fetchPage({ limit: 20, before: cursor, ...filters });
          if (current !== generation) return;
          for (const entry of page.entries || []) incoming.set(entry.id, entry);
          cursor = page.next || null;
          count++;
        } while (refresh && cursor && count < target);
        for (const [id, entry] of live) {
          if (!Object.keys(filters).length || incoming.has(id) || [...incoming.values()].some(e => (e.root || e.id) === (entry.root || entry.id))) incoming.set(id, entry);
        }
        const changed = !quiet || !sameEntries(entries, incoming);
        entries = incoming;
        next = cursor;
        pages = count;
        if (changed) publish();
        onState({ loading: false, hasMore: Boolean(next) });
      } catch (error) {
        if (current === generation && !quiet) onState({ loading: false, hasMore: Boolean(next), error, initial: pages === 0 });
      } finally {
        if (current === generation) { pending = null; live.clear(); }
      }
    });
    return pending;
  }

  function restore(snapshot) {
    if (pages || entries.size) return;
    entries = new Map(snapshot.map(entry => [entry.id, entry]));
    publish();
  }
  return { restore, setFilters: (nextFilters) => {
    filters = { ...nextFilters }; generation++; pending = null; live = new Map();
    entries = new Map(); next = null; pages = 0; publish({ reset: true });
    return run(true);
  }, refresh: () => run(true), poll: () => run(true, true), more: () => run(false), upsert };
}

function sameEntries(a, b) {
  if (a.size !== b.size) return false;
  for (const [id, entry] of b) {
    if (!a.has(id) || JSON.stringify(a.get(id)) !== JSON.stringify(entry)) return false;
  }
  return true;
}
