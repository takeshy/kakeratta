import { api } from "./api.js";
import { createGoogleConnection, finishGoogleConnection } from "./google.js";
import { el, clear, toast } from "./dom.js";

import { createActions } from "./actions.js";

import { createInbox } from "./inbox.js";

import { createCalendarInbox } from "./calendar-inbox.js";

let google, actions, inbox, calendarInbox;
function signedOut() {
 calendarInbox?.dispose();calendarInbox=null;
 clear(document.querySelector("#calendar-inbox-list"));
  inbox?.dispose();
  inbox = null;
  actions?.dispose();
  actions = null;
  google?.dispose();
  google = null;
  clear(document.querySelector("#google"));
  clear(document.querySelector("#mcp"));
  clear(document.querySelector("#action-list"));
  clear(document.querySelector("#inbox-list"));
  document.querySelector("#login").hidden = false;
  document.querySelector("#dashboard").hidden = true;
  document.querySelector("#logout").hidden = true;
}
async function mcp() {
  const status = await api.mcpStatus();
  const view = clear(document.querySelector("#mcp"));
  view.append(el("p", { text: status.configured ? "MCPキーは発行済みです。再発行すると以前のキーは使えなくなります。" : "MCPキーを発行してHermesに登録してください。" }));
  view.append(el("p", {}, el("code", { text: status.url })));
  const output = el("div");
  view.append(el("div", { class: "actions" },
    el("button", { text: status.configured ? "MCPキーを再発行" : "MCPキーを発行", onclick: async event => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        const result = await api.issueMCP();
        clear(output).append(el("p", { class: "secret-note", text: "このキーは一度だけ表示します。自分のHermes設定へ保存してください。チャットや公開リポジトリへ貼り付けないでください。" }),
          el("pre", { text: `mcp_servers:\n  google_bridge:\n    url: ${JSON.stringify(result.url)}\n    headers:\n      Authorization: ${JSON.stringify(`Bearer ${result.key}`)}\n    tools:\n      prompts: false\n      resources: false` }),
          el("p", {text:"定期同期にHermes cronを使う場合は、下のJSONをcheck_google_bridge.pyと同じディレクトリのgoogle_bridge.jsonへ保存し、権限を0600にしてください。"}),
          el("pre", {text:JSON.stringify({url:result.url,key:result.key},null,2)}));
      } catch (error) { toast(error.message); }
      finally { button.disabled = false; }
    } }),
    el("button", { class: "ghost", text: "MCPキーを無効化", onclick: async () => { try { await api.revokeMCP(); await mcp(); toast("Hermesのアクセスを停止しました。"); } catch (error) { toast(error.message); } } })), output,
    el("p", { class: "hint", text: "Hermesが別の端末・コンテナにある場合、127.0.0.1はHermes自身を指します。SSH転送や自分のHTTPSサーバーを使い、Hermesから到達できるURLを設定してください。" }));
}
async function dashboard() {
  await api.session();
  document.querySelector("#login").hidden = true;
  document.querySelector("#dashboard").hidden = false;
  document.querySelector("#logout").hidden = false;
  await finishGoogleConnection();
  google?.dispose();
  google = createGoogleConnection();
  clear(document.querySelector("#google")).append(google.element);
  await mcp();
  actions?.dispose();
  actions = createActions();
  clear(document.querySelector("#action-list")).append(actions.element);
  inbox?.dispose();
  inbox = createInbox();
  clear(document.querySelector("#inbox-list")).append(inbox.element);
 calendarInbox?.dispose(); calendarInbox=createCalendarInbox();clear(document.querySelector("#calendar-inbox-list")).append(calendarInbox.element);
}
document.querySelector("#login-form").addEventListener("submit", async event => {
  event.preventDefault();
  const input = document.querySelector("#admin-token");
  const button = event.currentTarget.querySelector("button");
  button.disabled = true;
  try { await api.login(input.value); input.value = ""; await dashboard(); }
  catch (error) { document.querySelector("#login-error").textContent = error.message; }
  finally { button.disabled = false; }
});
document.querySelector("#logout").addEventListener("click", async () => {
  try { await api.logout(); clear(document.querySelector("#mcp")); signedOut(); }
  catch (error) { toast(error.message); }
});
dashboard().catch(signedOut);
