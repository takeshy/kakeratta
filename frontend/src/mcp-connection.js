import { el, clear } from "./dom.js";
import { api } from "./api.js";
import { getLanguage, t } from "./i18n.js";

// Whether the agent is calling in. It is set aside by itself after going quiet
// for the claim wait, and comes back the moment it calls in again.
function agentLine(agent) {
  if (!agent) return null;
  const seen = agent.lastSeen ? new Date(agent.lastSeen) : null;
  const when = seen ? seen.toLocaleString(getLanguage() === "ja" ? "ja-JP" : "en-US", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
  if (agent.away) {
    return el("p", { class: "hint warn", text: seen
      ? t("外部エージェントから応答がありません（最終接続 {0}）。接続があるまで内蔵モデルが考えます。", when)
      : t("外部エージェントからまだ接続がありません。接続があるまで内蔵モデルが考えます。") });
  }
  return el("p", { class: "hint", text: t("外部エージェント接続中（最終接続 {0}）", when) });
}

export function createMCPConnection({ onChange, onState } = {}) {
  const view = el("section");
  let connection = null, key = "", busy = false, error = "";
  const endpoint = () => connection?.path ? new URL(connection.path, location.origin).href : "";
  function draw() {
    clear(view);
    view.append(
      el("div", { class: "compose-row" },
        el("h3", { label: "外部エージェント連携（Hermes）" }),
        el("a", { class: "chip ghost help", href: `/manual/?lang=${getLanguage()}#${getLanguage()}-hermes`, target: "_blank", rel: "noopener",
          "aria-label": t("Hermes連携の使い方（別タブで開く）"), i18nTitle: "Hermes連携の使い方（別タブで開く）",
          html: '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M9.5 9a2.5 2.5 0 0 1 5 0c0 2-2.5 2-2.5 4"/><path d="M12 16v1"/></svg>',
        })),
      el("p", { class: "hint", label: "担当画面で「外部エージェントを優先する」をONにした担当は、文脈の読み取りと workflow づくりを接続したエージェントに任せます。" }),
      el("p", { text: connection ? t(connection.configured ? "接続キー発行済み" : "未設定") : t("読み込み中…") }),
      ...[connection?.configured ? agentLine(connection.agent) : null].filter(Boolean),
      el("p", { role: "alert", text: error }),
    );
    const act = async (action) => {
      busy = true; error = ""; draw();
      try {
        connection = await action(); key = connection.key || "";
        onChange?.(connection);
        onState?.(connection);
      }
      catch (problem) { error = String(problem.message || problem); }
      finally { busy = false; draw(); }
    };
    view.append(el("div", { class: "compose-row" },
      el("button", { class: "chip", disabled: busy, label: connection?.configured ? "接続キーを再発行" : "接続キーを発行", onclick: () => act(api.issueMCPKey) }),
      el("button", { class: "chip ghost", disabled: busy || !connection?.configured, label: "連携を解除", onclick: () => act(api.revokeMCPKey) }),
    ));
    if (key) {
      const skillUrl = new URL("/integrations/hermes/kakeratta/SKILL.md", location.origin).href;
      const mcpUrl = endpoint();
      const promptText = `Install the kakeratta skill from: ${skillUrl}\nConnect the kakeratta MCP server: ${mcpUrl}\nUse this bearer token: ${key}\nThen show me the kakeratta automation suggestion so I can approve it.\nFinally, test the MCP connection and tell me whether kakeratta is ready.`;
      const nemoCommand = `export KAKERATTA_MCP_TOKEN=${JSON.stringify(key)}\n\nnemohermes hermes-sandbox mcp add kakeratta \\\n  --url ${JSON.stringify(mcpUrl)} \\\n  --env KAKERATTA_MCP_TOKEN\n\nunset KAKERATTA_MCP_TOKEN`;
      const yaml = `mcp_servers:\n  kakeratta:\n    url: ${JSON.stringify(mcpUrl)}\n    headers:\n      Authorization: "Bearer ${key}"\n`;

      const promptArea = el("textarea", { class: "skill-input", rows: 5, readonly: true, "aria-label": t("Hermesへの指示プロンプト") });
      promptArea.value = promptText;

      const nemoArea = el("textarea", { class: "skill-input", rows: 6, readonly: true, "aria-label": t("NemoClaw用コマンド") });
      nemoArea.value = nemoCommand;

      const config = el("textarea", { class: "skill-input", rows: 6, readonly: true, "aria-label": t("HermesのMCP接続設定") });
      config.value = yaml;

      view.append(
        el("p", { label: "1. 通常のHermesをお使いの場合: 以下の指示をHermesにそのまま渡してください（SkillとMCPが自動設定され、毎分の定期実行が提案されます）。" }),
        promptArea,
        el("p", { label: "2. NemoClawをお使いの場合: 以下のコマンドでManaged MCPを追加してください。" }),
        nemoArea,
        el("p", { label: "3. 手動で ~/.hermes/config.yaml を設定する場合:" }),
        config,
        el("p", { class: "hint", label: "接続キーを含む設定です。第三者に共有しないでください。再発行すると以前のキーは無効になります。" }),
      );
    }
  }

  async function load() {
    try { connection = await api.mcpConnection(); error = ""; onState?.(connection); }
    catch (problem) { error = String(problem.message || problem); }
    draw();
  }
  draw();
  return { element: view, load };
}
