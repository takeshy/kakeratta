async function call(method, path, body) {
  const response = await fetch(path, { method, credentials: "same-origin", cache: "no-store", headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || "接続に失敗しました。");
  return result;
}
export const api = {
 calendarInbox: () => call("GET", "/api/google/calendar-inbox"),
 saveCalendarInboxSettings: body => call("PUT", "/api/google/calendar-inbox/settings", body),
 syncCalendarInbox: () => call("POST", "/api/google/calendar-inbox/sync", {}),
 clearCalendarInbox: () => call("DELETE", "/api/google/calendar-inbox"),
  session: () => call("GET", "/api/session"),
  login: (token) => call("POST", "/api/session", { token }),
  logout: () => call("DELETE", "/api/session"),
  googleConnection: () => call("GET", "/api/google/connection"),
  connectGoogle: (body) => call("POST", "/api/google/connect", body),
  finishGoogle: (body) => call("POST", "/api/google/finish", body),
  testGoogle: (service) => call("POST", `/api/google/${encodeURIComponent(service)}/test`, {}),
  disconnectGoogle: (service, revoke = false) => call("DELETE", `/api/google/${encodeURIComponent(service)}/connection${revoke ? "?revoke=true" : ""}`),
  timelineTarget: () => call("GET", "/api/timeline-target"),
  saveTimelineTarget: body => call("PUT", "/api/timeline-target", body),
  clearTimelineTarget: () => call("DELETE", "/api/timeline-target"),
  googleInbox: () => call("GET", "/api/google/inbox"),
  saveInboxSettings: body => call("PUT", "/api/google/inbox/settings", body),
  syncInbox: () => call("POST", "/api/google/inbox/sync", {}),
  inboxFeedback: (messageId, decision) => call("POST", "/api/google/inbox/feedback", { messageId, decision }),
  clearInbox: () => call("DELETE", "/api/google/inbox"),
  googleActions: () => call("GET", "/api/google/actions"),
  decideGoogleAction: (id, payloadHash, approve) => call("POST", `/api/google/actions/${encodeURIComponent(id)}/decision`, { payloadHash, approve }),
  executeGoogleAction: (id, payloadHash) => call("POST", `/api/google/actions/${encodeURIComponent(id)}/execute`, { payloadHash }),
  mcpStatus: () => call("GET", "/api/mcp/credential"),
  issueMCP: () => call("POST", "/api/mcp/credential", {}),
  revokeMCP: () => call("DELETE", "/api/mcp/credential"),
};
