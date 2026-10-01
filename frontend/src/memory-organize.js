import { el } from "./dom.js";
import { t } from "./i18n.js";

// A detached panel stops polling. No request in this component applies a
// proposal except the explicit approval button.
export function createMemoryOrganization({ api, id, externalAvailable, onApplied }) {
  const host = el("details", { class: "memory-organization" }, el("summary", { label: "メモを整理する" }));
  const request = el("textarea", { class: "skill-input", rows: 3, maxlength: 4000,
    "aria-label": t("どのように整理したいですか"), placeholder: t("例: 同じ話題のメモをまとめ、好みとお店に分けて整理して") });
  const planner = el("select", { "aria-label": t("調査するAI") },
    el("option", { value: "builtin", label: "内蔵AI" }),
    externalAvailable ? el("option", { value: "external", label: "外部エージェント" }) : null);
  const status = el("p", { class: "hint", role: "status" });
  const result = el("div");
  let generation = 0;
  let preview = null;
  const fail = (error) => { status.textContent = t(error.message || String(error)); };
  const approve = el("button", { class: "button primary", label: "OK、この方針で実行", hidden: true, onclick: async () => {
    if (!preview || approve.disabled) return;
    approve.disabled = true;
    investigate.disabled = true;
    try {
      await api.applyMemoryOrganization(id, preview.token);
      preview = null;
      approve.hidden = true;
      result.replaceChildren();
      status.textContent = t("メモを整理しました");
      await onApplied?.();
    } catch (error) { fail(error); }
    finally { approve.disabled = false; investigate.disabled = false; }
  } });
  const render = (proposal) => {
    preview = proposal;
    if (proposal.status === "failed") { status.textContent = t(proposal.error); investigate.disabled = false; return; }
    if (proposal.status === "researching") { status.textContent = t("外部エージェントの調査を待っています。承認するまでメモは変更されません。"); return; }
    investigate.disabled = false;
    if (proposal.status !== "ready") return;
    status.textContent = t("整理方針と変更内容を確認してください。OKで実行します。");
    result.replaceChildren(el("p", { text: proposal.policy, style: "white-space: pre-wrap" }),
      ...(proposal.changes || []).map(change => el("details", {},
        el("summary", { text: `${t(change.delete ? "削除" : "保存")}: ${change.path}` }),
        change.delete ? null : el("pre", { text: change.text }))));
    approve.hidden = !(proposal.changes || []).length;
  };
  const poll = async (token, active) => {
    if (active !== generation || !host.isConnected) return;
    try {
      const proposal = await api.memoryOrganization(id, token);
      if (active !== generation || !host.isConnected) return;
      render(proposal);
      if (proposal.status === "researching") setTimeout(() => poll(token, active), 3000);
    } catch (error) { fail(error); investigate.disabled = false; }
  };
  const investigate = el("button", { class: "button", label: "調べて整理方針を出す", onclick: async () => {
    if (investigate.disabled || !request.value.trim()) { request.focus(); return; }
    const active = ++generation;
    investigate.disabled = true;
    approve.hidden = true;
    preview = null;
    result.replaceChildren();
    status.textContent = t("メモを調べています。承認するまでメモは変更されません。");
    try {
      const proposal = await api.organizeMemory(id, request.value, planner.value);
      if (active !== generation || !host.isConnected) return;
      render(proposal);
      if (proposal.status === "researching") setTimeout(() => poll(proposal.token, active), 3000);
    } catch (error) { fail(error); investigate.disabled = false; }
  } });
  const invalidate = () => {
    generation++;
    preview = null;
    approve.hidden = true;
    investigate.disabled = false;
    result.replaceChildren();
    status.textContent = "";
  };
  request.addEventListener("input", invalidate);
  planner.addEventListener("change", invalidate);
  host.append(el("p", { class: "hint", label: "整理の希望を伝えると、AIがメモを調べて方針を提案します。確認してOKを押すと実行します。" }),
    request, el("label", {}, t("調査するAI"), planner), investigate, status, result, approve);
  return host;
}
