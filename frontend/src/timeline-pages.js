// Keep paging and live events in one place so a late HTTP response cannot undo
// a reply or deletion that already arrived through the live channel.
export function createTimelinePages({ fetchPage, onChange, onState = () => {} }) {
  let entries = new Map();
  let next = null;
  let pages = 0;
  let pending = null;
  let live = new Map();
  const publish = () => onChange([...entries.values()]);

  function upsert(entry) {
    if (pending) live.set(entry.id, entry);
    entries.set(entry.id, entry);
    publish();
  }

  async function run(refresh) {
    if (pending) return pending;
    if (!refresh && !next) return;
    // Assign the promise before any callbacks can re-enter this loader.
    live = new Map();
    pending = Promise.resolve().then(async () => {
      onState({ loading: true, hasMore: Boolean(next) });
      try {
        const incoming = refresh ? new Map() : new Map(entries);
        let cursor = refresh ? null : next;
        let count = refresh ? 0 : pages;
        const target = refresh ? Math.max(1, pages) : pages + 1;
        do {
          const page = await fetchPage({ limit: 20, before: cursor });
          for (const entry of page.entries || []) incoming.set(entry.id, entry);
          cursor = page.next || null;
          count++;
        } while (refresh && cursor && count < target);
        for (const [id, entry] of live) incoming.set(id, entry);
        entries = incoming;
        next = cursor;
        pages = count;
        publish();
        onState({ loading: false, hasMore: Boolean(next) });
      } catch (error) {
        onState({ loading: false, hasMore: Boolean(next), error, initial: pages === 0 });
      } finally {
        pending = null;
        live.clear();
      }
    });
    return pending;
  }

  return { refresh: () => run(true), more: () => run(false), upsert };
}
