import { localIdentity, localRecord, localList } from "./local-store.js";

export function createOutbox({ send, verify, changed = () => {}, record = localRecord, list = localList, identity = localIdentity, online = () => navigator.onLine !== false }) {
  let running = null;
  let again = false;
  const entries = async () => (await list("outbox:")).sort((a,b) => a.entry.at.localeCompare(b.entry.at));
  async function enqueue(path, body, parent) {
    const user = identity()?.key;
    if (!user) throw Error("一度オンラインでログインしてください。");
    const id = `local-${crypto.randomUUID()}`;
    const at = new Date().toISOString();
    const entry = { id, at, kind: parent ? "reply" : "post", speaker: "user", text: body.text,
      link: body.link, place: body.place, root: parent?.root || parent?.id || id,
      replyTo: parent?.id, pending: true, localImage: body.image || "" };
    await record(`outbox:${id}`, { path, body: { ...body, clientId: id, clientAt: at }, entry }, user);
    await changed();
    void flush();
    return entry;
  }
  async function flush() {
    if (running) { again = true; return running; }
    if (!online()) return;
    running = (async () => {
      const user = identity()?.key;
      if (!user) return;
      await verify();
      if (identity()?.key !== user) return;
      for (const item of await entries()) {
        if (identity()?.key !== user || !online()) break;
        if (item.error) continue;
        try {
          const saved = await send(item.path, item.body);
          // Persist the acknowledgement before removing the outbox record.
          await record(`ack:${saved.id}`, saved, user);
          await record(`outbox:${item.entry.id}`, null, user);
          await changed(saved);
        } catch (error) {
          if (error.status >= 400 && error.status < 500 && ![401,408,429].includes(error.status)) {
            item.error = error.message;
            item.entry.syncError = error.message;
            await record(`outbox:${item.entry.id}`, item, user);
            await changed();
          }
          break;
        }
      }
    })().catch(() => {}).finally(() => { running = null; if (again) { again = false; void flush(); } });
    return running;
  }
  return { enqueue, flush, entries, async retry() {
    for (const item of await entries()) {
      delete item.error; delete item.entry.syncError;
      await record(`outbox:${item.entry.id}`, item);
    }
    await changed(); return flush();
  } };
}
