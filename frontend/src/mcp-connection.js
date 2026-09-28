import { el, clear } from "./dom.js";
import { api } from "./api.js";
import { getLanguage, t } from "./i18n.js";

// Whether the agent is calling in. It is set aside by itself after going quiet
// for the claim wait, and comes back the moment it calls in again.
function agentLine(agent) {
  if (!agent) return null;
  if (agent.paused) return el("p", { class: "hint warn", label: "外部エージェントを使わない設定のため、接続を受け付けていません。" });
  const seen = agent.lastSeen ? new Date(agent.lastSeen) : null;
  const when = seen ? seen.toLocaleString(getLanguage() === "ja" ? "ja-JP" : "en-US", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
  if (agent.away) {
    return el("p", { class: "hint warn", text: seen
      ? t("外部エージェントから応答がありません（最終接続 {0}）。接続があるまで内蔵モデルが考えます。", when)
      : t("外部エージェントからまだ接続がありません。接続があるまで内蔵モデルが考えます。") });
  }
  return el("p", { class: "hint", text: t("外部エージェント接続中（最終接続 {0}）", when) });
}

// The connections are a list to pick one from. Each keeps what an agent needs
// to call in, and how long an ask waits for that agent. A key is known only
// in the session it was issued in, since the server keeps just a hash of it.
export function createMCPConnection({ onChange, onState, controls = null } = {}) {
  const view = el("section", { class: "mcp-connection" });
  let connection = null, busy = false, error = "";
  let adding = false, newName = "";
  const keys = new Map();
  const opened = new Set();
  // The connection whose key was just issued: its setup steps open by themselves.
  let fresh = "";
  const endpoint = () => connection?.path ? new URL(connection.path, location.origin).href : "";

  const act = async (action) => {
    busy = true; error = ""; draw();
    try {
      connection = await action();
      onChange?.(connection);
      onState?.(connection);
    }
    catch (problem) { error = String(problem.message || problem); }
    finally { busy = false; draw(); }
  };
  const issue = (body) => act(async () => {
    const result = await api.issueMCPKey(body);
    const id = result.id || body?.id || "default";
    keys.set(id, result.key);
    opened.add(id);
    fresh = id;
    adding = false; newName = "";
    return result;
  });

  function waitSelect(label, value, options, onChange) {
    const all = options.includes(value) ? options : [...options, value].sort((a, b) => a - b);
    const node = el("select", { disabled: busy, "aria-label": t(label) },
      ...all.map(n => el("option", { value: String(n), text: t("{0}分", n) })));
    node.value = String(value);
    node.addEventListener("change", () => onChange(Number(node.value)));
    return el("label", { class: "field" }, el("span", { label }), node);
  }

  function setupSteps(key, open) {
    const mcpUrl = endpoint();
    const skillUrl = new URL("/integrations/hermes/kakeratta/SKILL.md", location.origin).href;
    const promptText = `Install the kakeratta skill from: ${skillUrl}\nConnect the kakeratta MCP server: ${mcpUrl}\nUse this bearer token: ${key}\nThen show me the kakeratta automation suggestion so I can approve it.\nFinally, test the MCP connection and tell me whether kakeratta is ready.`;
    const nemoCommand = `export KAKERATTA_MCP_TOKEN=${JSON.stringify(key)}\n\nnemohermes hermes-sandbox mcp add kakeratta \\\n  --url ${JSON.stringify(mcpUrl)} \\\n  --env KAKERATTA_MCP_TOKEN\n\nunset KAKERATTA_MCP_TOKEN`;
    const yaml = `mcp_servers:\n  kakeratta:\n    url: ${JSON.stringify(mcpUrl)}\n    headers:\n      Authorization: "Bearer ${key}"\n`;
    const area = (value, rows, label) => {
      const node = el("textarea", { class: "skill-input", rows, readonly: true, "aria-label": t(label) });
      node.value = value;
      return node;
    };
    return el("details", { class: "advanced-category", open },
      el("summary", { label: "Hermesでの設定方法" }),
      el("div", { class: "advanced-category-body" },
        el("p", { label: "1. 通常のHermesをお使いの場合: 以下の指示をHermesにそのまま渡してください（SkillとMCPが自動設定され、毎分の定期実行が提案されます）。" }),
        area(promptText, 5, "Hermesへの指示プロンプト"),
        el("p", { label: "2. NemoClawをお使いの場合: 以下のコマンドでManaged MCPを追加してください。" }),
        area(nemoCommand, 6, "NemoClaw用コマンド"),
        el("p", { label: "3. 手動で ~/.hermes/config.yaml を設定する場合:" }),
        area(yaml, 6, "HermesのMCP接続設定"),
        el("p", { class: "hint", label: "接続キーを含む設定です。第三者に共有しないでください。再発行すると以前のキーは無効になります。" })));
  }

  // What an agent needs to call in through this connection, and how long an
  // ask waits for it.
  function connectionDetails(row) {
    const key = keys.get(row.id) || "";
    const waits = (change) => act(() => api.setMCPConnectionWaits(row.id, change));
    const details = el("details", { class: "advanced-category mcp-connection-details", open: opened.has(row.id),
      ontoggle: (event) => { if (event.currentTarget.open) opened.add(row.id); else opened.delete(row.id); },
    }, el("summary", { label: "接続情報と待ち時間" }));
    details.append(el("div", { class: "advanced-category-body" },
      el("dl", { class: "mcp-connection-values" },
        el("dt", { label: "MCPエンドポイント" }), el("dd", {}, el("code", { text: endpoint() })),
        el("dt", { label: "認証ヘッダー" }), el("dd", {}, el("code", { text: `Authorization: Bearer ${key || "<TOKEN>"}` }))),
      el("p", { class: "hint", label: "MCP Streamable HTTPに対応し、依頼の取得と回答の提出を実行できるエージェントで利用できます。" }),
      key
        ? el("p", { class: "hint warn", label: "キーはこの画面を開いている間だけ表示されます。エージェントに設定してから閉じてください。" })
        : el("p", { class: "hint", label: "キーは発行時のみ表示されます。保存済みのキーを使うか、再発行してください。" }),
      key ? setupSteps(key, fresh === row.id) : null,
      waitSelect("取得を待つ時間", row.claimMinutes || 3, [1, 2, 3, 5, 10, 15, 30], (n) => waits({ claimMinutes: n })),
      waitSelect("取得後、回答を待つ時間", row.planMinutes || 15, [5, 10, 15, 30, 60, 120], (n) => waits({ planMinutes: n })),
      el("p", { class: "hint", label: "時間内に外部エージェントが取得しない、または回答を返さない場合は、内蔵モデルが代わりに答えます。取得を待つ時間のあいだ一度も接続がなければ応答なしとみなし、次に接続があるまで待たずに内蔵モデルが答えます。" }),
      el("div", { class: "compose-row" },
        el("button", { class: "chip ghost", disabled: busy, label: "キーを再発行", onclick: () => {
          if (confirm(t("「{0}」のキーを再発行しますか。以前のキーは使えなくなります。", row.name))) return issue({ id: row.id });
        } }),
        el("button", { class: "chip ghost", disabled: busy, label: "削除", onclick: () => {
          if (confirm(t("「{0}」を削除しますか。このキーで接続しているエージェントは使えなくなります。", row.name))) return act(async () => {
            keys.delete(row.id); opened.delete(row.id);
            return api.revokeMCPKey(row.id);
          });
        } }))));
    return details;
  }

  function connectionRow(row) {
    const radio = el("input", { type: "radio", name: "mcp-connection", value: row.id, disabled: busy, checked: row.active });
    radio.addEventListener("change", () => act(() => api.selectMCPConnection(row.id)));
    return el("div", { class: "mcp-connection-row" + (row.active ? " active" : "") },
      el("label", { class: "check-field" }, radio, el("span", { class: "mcp-connection-name", text: row.name }),
        row.active ? el("span", { class: "chip-note", label: "使用中" }) : null),
      row.active && connection.agent ? agentLine(connection.agent) : null,
      connectionDetails(row));
  }

  function addForm(rows) {
    const name = el("input", { type: "text", maxlength: 80, disabled: busy, "aria-label": t("接続名"), i18nPlaceholder: "例: Hermes、その他のエージェント" });
    name.value = newName;
    const add = el("button", { class: "chip", disabled: busy || !newName.trim(), label: "追加してキーを発行", onclick: () => issue({ name: newName.trim() }) });
    name.addEventListener("input", () => { newName = name.value; add.disabled = busy || !newName.trim(); });
    // A name is short: on a wide screen the field and its buttons share a line.
    return el("div", { class: "mcp-add-connection" },
      el("label", { class: "field" }, el("span", { label: "接続名" }), name),
      el("div", { class: "compose-row" }, add,
        rows.length ? el("button", { class: "chip ghost", disabled: busy, label: "キャンセル", onclick: () => { adding = false; newName = ""; draw(); } }) : null));
  }

  function draw() {
    clear(view);
    view.append(
      el("div", { class: "compose-row" },
        el("h3", { label: "外部エージェント接続" }),
        el("a", { class: "chip ghost help", href: `/manual/?lang=${getLanguage()}#${getLanguage()}-hermes`, target: "_blank", rel: "noopener",
          "aria-label": t("Hermes連携の使い方（別タブで開く）"), i18nTitle: "Hermes連携の使い方（別タブで開く）",
          html: '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M9.5 9a2.5 2.5 0 0 1 5 0c0 2-2.5 2-2.5 4"/><path d="M12 16v1"/></svg>',
        })),
      el("p", { class: "hint", label: "担当画面で「外部エージェントを優先する」をONにした担当は、調べものと回答を接続したエージェントに任せます。記憶・予定・操作はアプリが決めて実行します。" }),
      el("p", { role: "alert", text: error }),
    );
    if (controls) view.append(controls);
    if (!connection) { view.append(el("p", { label: "読み込み中…" })); return; }
    const rows = connection.connections || (connection.configured ? [{ id: "default", name: "Hermes", active: true }] : []);
    if (!rows.length) {
      // The first connection is most likely Hermes, so the name is filled in.
      if (!adding) { adding = true; newName = "Hermes"; }
      view.append(el("p", { class: "hint", label: "まだ接続先がありません。名前を付けて追加すると、接続キーと設定方法が表示されます。" }));
    } else {
      view.append(el("fieldset", { class: "mcp-connections" },
        el("legend", { label: "使用する接続先" }),
        el("p", { class: "hint", label: "外部エージェントを優先する担当すべてに、選んだ接続先を使います。" }),
        ...rows.map(connectionRow)));
    }
    if (adding) view.append(addForm(rows));
    else view.append(el("button", { class: "chip", disabled: busy, label: "＋ 接続先を追加", onclick: () => { adding = true; draw(); } }));
  }

  async function load() {
    try {
      connection = await api.mcpConnection();
      error = ""; onState?.(connection);
    }
    catch (problem) { error = String(problem.message || problem); }
    draw();
  }
  draw();
  return { element: view, load };
}
