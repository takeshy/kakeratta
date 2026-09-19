import { el, clear } from "./dom.js";
import { api } from "./api.js";
import { t } from "./i18n.js";

export function createMCPConnection() {
  const view = el("section");
  let connection = null, key = "", busy = false, error = "";
  const endpoint = () => connection?.path ? new URL(connection.path, location.origin).href : "";
  function draw() {
    clear(view);
    view.append(
      el("h3", { label: "MCP連携（Hermesなど）" }),
      el("p", { class: "hint", label: "あなたのHermesからkakerattaに接続します。HermesのAPI公開は不要です。" }),
      el("p", { class: "hint", label: "このキーで、あなたの投稿・担当のメモ・Hermesへの依頼を読み、Hermesのメモと報告を書けます。モデル設定や他のユーザーにはアクセスできません。" }),
      el("p", { class: "hint", label: "依頼はHermesが取得するまで待機します。自動対応にはHermes側の定期実行を設定してください。" }),
      el("p", { text: connection ? t(connection.configured ? "MCPキー発行済み" : "MCPキー未発行") : t("読み込み中…") }),
      el("p", { class: "hint", label: "Hermes担当をOFFにするとMCPからの読み書きを停止します。MCP接続を解除するとキーが失効します。" }),
      el("p", { role: "alert", text: error }),
    );
    const act = async (action) => {
      busy = true; error = ""; draw();
      try { connection = await action(); key = connection.key || ""; }
      catch (problem) { error = String(problem.message || problem); }
      finally { busy = false; draw(); }
    };
    view.append(el("div", { class: "compose-row" },
      el("button", { class: "chip", disabled: busy, label: connection?.configured ? "MCPキーを再発行" : "MCPキーを発行", onclick: () => act(api.issueMCPKey) }),
      el("button", { class: "chip ghost", disabled: busy || !connection?.configured, label: "MCP接続を解除", onclick: () => act(api.revokeMCPKey) }),
    ));
    view.append(el("p", { class: "hint", label: "再発行すると以前のキーは無効になります。発行したキーはこの画面でだけ表示します。" }));
    if (connection?.path) view.append(el("p", { text: endpoint() }));
    if (key) {
      const yaml = `mcp_servers:\n  kakeratta:\n    url: ${JSON.stringify(endpoint())}\n    headers:\n      Authorization: "Bearer \${KAKERATTA_MCP_TOKEN}"\n`;
      const config = el("textarea", { class: "skill-input", rows: 7, readonly: true, "aria-label": t("HermesのMCP接続設定") });
      config.value = yaml;
      const env = el("textarea", { class: "skill-input", rows: 2, readonly: true, "aria-label": t("HermesのMCP環境変数") });
      env.value = `KAKERATTA_MCP_TOKEN=${key}\n`;
      view.append(el("p", { label: "次の設定を自分のHermesの ~/.hermes/config.yaml に追加してください。" }),
        config,
        el("p", { label: "次のキーを ~/.hermes/.env に追加してください。再発行時はこの値を更新します。" }),
        env,
        el("p", { class: "hint", label: "Hermesを再起動し、「kakerattaの依頼を確認して」と話しかけてください。" }));
    }
  }
  async function load() {
    try { connection = await api.mcpConnection(); error = ""; }
    catch (problem) { error = String(problem.message || problem); }
    draw();
  }
  draw();
  return { element: view, load };
}
