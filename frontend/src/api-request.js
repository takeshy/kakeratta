// Shared request handling for JSON endpoints and raw persona definitions.
export function createAPIRequest({ identity, verify, fetch = (...args) => globalThis.fetch(...args) }) {
  return async function call(method, path, body, { raw = false } = {}) {
    const user = identity()?.key;
    if (user) await verify();
    if (identity()?.key !== user) throw Error("Account changed");

    // Publishing, trials, and workspace deletion can take several minutes.
    const slow = path === "/api/social/publish" || path.endsWith("/reply/preview") || path.endsWith("/routing/apply") || path === "/api/workspace";
    const options = { method, headers: {}, signal: AbortSignal.timeout(slow ? 250000 : 45000) };
    if (user) options.headers["X-Kakeratta-Workspace"] = user;
    if (body !== undefined) {
      if (raw) {
        options.body = body;
      } else {
        options.headers["Content-Type"] = "application/json";
        options.body = JSON.stringify(body);
      }
    }
    const response = await fetch(path, options);
    const text = await response.text();
    let payload = null;
    try {
      payload = text ? JSON.parse(text) : null;
    } catch {
      /* Empty or non-JSON responses have no payload. */
    }
    if (!response.ok) {
      const error = new Error(payload?.error || `${response.status} ${response.statusText}`);
      error.status = response.status;
      throw error;
    }
    if (identity()?.key !== user) throw Error("Account changed");
    return payload;
  };
}
