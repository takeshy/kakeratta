// User-scoped IndexedDB snapshots and a durable outbox. Credentials never go here.
export const IDENTITY_KEY = "kakeratta.localIdentity";
export function localIdentity() {
  try { return JSON.parse(localStorage.getItem(IDENTITY_KEY)) || null; } catch { return null; }
}
let opening;
function database() {
  if (!opening) opening = new Promise((resolve, reject) => {
    const request = indexedDB.open("kakeratta-local", 1);
    request.onupgradeneeded = () => request.result.createObjectStore("records");
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => { opening = null; reject(request.error); };
  });
  return opening;
}
export async function localRecord(key, value, user = localIdentity()?.key) {
  if (!user) throw new Error("Local account is unavailable");
  const db = await database();
  return new Promise((resolve, reject) => {
    const tx = db.transaction("records", value === undefined ? "readonly" : "readwrite");
    const store = tx.objectStore("records");
    const request = value === undefined ? store.get(`${user}:${key}`) : value === null ? store.delete(`${user}:${key}`) : store.put(value, `${user}:${key}`);
    tx.oncomplete = () => resolve(request.result);
    tx.onabort = tx.onerror = () => reject(tx.error || Error("Local storage failed"));
  });
}
export async function localList(prefix, user = localIdentity()?.key) {
  if (!user) return [];
  const db = await database();
  return new Promise((resolve, reject) => {
    const tx = db.transaction("records");
    const start = `${user}:${prefix}`;
    const request = tx.objectStore("records").getAll(IDBKeyRange.bound(start, start + "\uffff"));
    tx.oncomplete = () => resolve(request.result);
    tx.onabort = tx.onerror = () => reject(tx.error);
  });
}
export async function clearLocalUser(user = localIdentity()?.key) {
  if (!user) return;
  const db = await database();
  await new Promise((resolve, reject) => {
    const tx = db.transaction("records", "readwrite");
    tx.objectStore("records").delete(IDBKeyRange.bound(`${user}:`, `${user}:\uffff`));
    tx.oncomplete = resolve;
    tx.onabort = tx.onerror = () => reject(tx.error);
  });
}
export function recentEntries(entries, limit = 20) {
  const roots = new Set(entries.filter(e => e.kind === "post" && !e.deleted && !e.pending)
    .sort((a,b) => new Date(b.at)-new Date(a.at)).slice(0,limit).map(e => e.id));
  return entries.filter(e => !e.pending && roots.has(e.root || e.id));
}
