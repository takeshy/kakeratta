// The only place that knows how to reach Go. Keeping it to one file means the
// UI can be exercised in a browser by swapping this module for a fake.
import { Call, Events } from "/wails/runtime.js";

const call = (method, ...args) => Call.ByName(`main.App.${method}`, ...args);

export const api = {
  shelf: () => call("Shelf"),
  importDialog: () => call("ImportDialog"),
  importText: (title, author, text) => call("ImportText", title, author, text),
  importSample: () => call("ImportSample"),
  deleteBook: (id) => call("DeleteBook", id),
  openBook: (id) => call("OpenBook", id),
  // Where the eye is, and what was actually read. They are different questions:
  // going back to re-read a scene moves the first and must not touch the second.
  seek: (bookId, position) => call("Seek", bookId, position),
  markRead: (bookId, span, seconds) => call("MarkRead", bookId, span, seconds),
  setCategory: (bookId, category) => call("SetCategory", bookId, category),
  startReading: (bookId) => call("StartReading", bookId),
  personas: (category) => call("Personas", category),
  addPersona: (bookId, personaId) => call("AddPersona", bookId, personaId),
  removePersona: (bookId, personaId) => call("RemovePersona", bookId, personaId),
  installPersonaDialog: () => call("InstallPersonaDialog"),
  uninstallPersona: (personaId) => call("UninstallPersona", personaId),
  addMemo: (bookId, text, spoken, quote, range) => call("AddMemo", bookId, text, spoken, quote, range),
  updateMemo: (bookId, memoId, text) => call("UpdateMemo", bookId, memoId, text),
  deleteMemo: (bookId, memoId) => call("DeleteMemo", bookId, memoId),
  memos: (bookId) => call("Memos", bookId),
  timeline: (bookId) => call("Timeline", bookId),
  allTimeline: () => call("AllTimeline"),
  calendar: () => call("Calendar"),
  recall: (bookId, query) => call("Recall", bookId, query),
  ask: (bookId, question) => call("Ask", bookId, question),
  say: (bookId, text) => call("Say", bookId, text),
  conversation: (bookId) => call("Conversation", bookId),
  clearConversation: (bookId) => call("ClearConversation", bookId),
  tidyMemo: (spoken) => call("TidyMemo", spoken),
  models: () => call("Models"),
  providers: () => call("Providers"),
  vertexStatus: () => call("GetVertexOAuthStatus"),
  selectVertexClient: () => call("SelectVertexOAuthClient"),
  connectVertex: (clientId, clientSecret, projectId) => call("ConnectVertexOAuth", clientId, clientSecret, projectId),
  disconnectVertex: () => call("DisconnectVertexOAuth"),
  settings: () => call("Settings"),
  saveSettings: (settings) => call("SaveSettings", settings),
  status: (bookId) => call("Status", bookId),
  prepare: (bookId) => call("Prepare", bookId),
  workspace: () => call("Workspace"),
  version: () => call("Version"),
  // A persona speaking up arrives on its own, not as the answer to a call: it
  // is a message from someone reading alongside, and it lands when it lands.
  on: (name, handler) => Events.On(name, handler),
};
