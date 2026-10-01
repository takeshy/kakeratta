// Browser subscription lifecycle; permission is requested only by a user click.
export function pushSupported() {
  return globalThis.isSecureContext === true && !!globalThis.navigator?.serviceWorker &&
    !!globalThis.PushManager && !!globalThis.Notification;
}

export async function pushRegistration() {
  await navigator.serviceWorker.register("/sw.js");
  return navigator.serviceWorker.ready;
}

export async function enablePush(api, publicKey) {
  const permission = await Notification.requestPermission();
  if (permission !== "granted") throw Error("通知の許可をブラウザの設定で確認してください。");
  const registration = await pushRegistration();
  let subscription = await registration.pushManager.getSubscription();
  const key = Uint8Array.from(atob(publicKey.replace(/-/g, "+").replace(/_/g, "/")), ch => ch.charCodeAt(0));
  const previousKey = subscription?.options?.applicationServerKey;
  if (subscription && previousKey && !sameKey(new Uint8Array(previousKey), key)) {
    await api.deletePush({ endpoint: subscription.endpoint });
    if (!await subscription.unsubscribe()) throw Error("通知を解除できませんでした。もう一度お試しください。");
    subscription = null;
  }
  const created = !subscription;
  subscription ||= await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
  try { await api.savePush(subscription.toJSON()); }
  catch (error) { if (created) await subscription.unsubscribe().catch(() => {}); throw error; }
  return subscription;
}

function sameKey(left, right) { return left.length === right.length && left.every((v, i) => v === right[i]); }

export async function disablePush(api) {
  const registration = await navigator.serviceWorker?.getRegistration();
  const subscription = await registration?.pushManager?.getSubscription();
  if (!subscription) return;
  await api.deletePush({ endpoint: subscription.endpoint });
  if (!await subscription.unsubscribe()) throw Error("通知を解除できませんでした。もう一度お試しください。");
}

// Always revoke the browser subscription on logout, including offline logout.
export async function revokeDevicePush(workspace) {
  const registration = await globalThis.navigator?.serviceWorker?.getRegistration();
  const subscription = await registration?.pushManager?.getSubscription();
  if (subscription) {
    await fetch("/api/push", { method: "DELETE", headers: { "Content-Type":"application/json", "X-Kakeratta-Workspace":workspace || "" }, body: JSON.stringify({ endpoint: subscription.endpoint }), signal: AbortSignal.timeout(5000) }).catch(() => {});
    await subscription.unsubscribe();
  }
  for (const notification of await registration?.getNotifications?.() || []) notification.close();
}
