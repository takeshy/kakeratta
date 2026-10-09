import { el, clear } from "./dom.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";

export function costText(counts) {
  const known = (counts.estimatedUsd || 0) + (counts.reportedUsd || 0);
  const amount = `US$${known.toFixed(4)}`;
  if (!counts.unpriced) return amount;
  return counts.unpriced === counts.calls ? t("未算出（{0}件）", counts.unpriced)
    : `${amount} + ${t("未算出（{0}件）", counts.unpriced)}`;
}

export function usageTotals(rows) {
  return rows.reduce((total, row) => {
    for (const key of ["calls", "failures", "estimatedUsd", "reportedUsd", "unpriced", "searches", "searchFailures"]) total[key] = (total[key] || 0) + (row[key] || 0);
    return total;
  }, {});
}

export function createUsage({ persona = "" } = {}) {
  const body = el("div", { class: "usage-body" });
  const section = el("details", { class: "settings-usage" }, el("summary", { label: "AI利用状況" }), body);
  const month = el("input", { type: "month", "aria-label": t("集計月") });
  const now = new Date();
  month.value = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`;
  const refresh = el("button", { class: "chip", type: "button", label: "更新" });
  const results = el("div", { "aria-live": "polite" });
  body.append(el("div", { class: "usage-controls" }, el("label", { class: "field" }, el("span", { label: "集計月" }), month), refresh), results,
    el("p", { class: "hint", label: "AI料金は米ドルの概算です。OpenRouterはAPIが返した料金、Nebiusは記録時の公開単価で集計します。Nebiusの割引・無料枠は反映しません。Web検索料金・外部エージェント側のAI料金は含みません。" }),
    el("p", { class: "hint", label: "返答・定期実行には調査、記憶更新、再試行も含みます。定期実行はチェックインとリマインダーです。Web検索は実際のAPIリクエスト数（失敗を含む）です。過去のログは集計対象に含みません。" }));
  if (!persona) body.append(el("p", { class: "hint", label: "全体の合計は振り分け・各担当の利用分・その他の処理を含みます。その他にはEmbeddingや日まとめ・月まとめなどを含みます。担当ごとの内訳は担当画面をご覧ください。" }));
  let request = 0;
  let loaded = false;
  async function load() {
    const current = ++request;
    refresh.disabled = true;
    clear(results).append(el("p", { class: "hint", label: "読み込み中…" }));
    try {
      const { usage } = await api.usage(month.value, persona);
      if (current !== request) return;
      month.value = usage.month;
      clear(results);
      if (!usage.rows.length) {
        results.append(el("p", { class: "hint", label: "この月の利用記録はありません" }));
        return;
      }
      const total = usageTotals(usage.rows);
      results.append(el("p", { text: t("合計：{0} · Web検索 {1}回", costText(total), total.searches) }));
      const labels = { reply: "返答", scheduled: "定期実行", other: "その他" };
      const displayRows = persona ? usage.rows : [
        ["routing", "振り分け"], ["persona", "担当合計"], ["other", "その他"],
      ].map(([component, label]) => ({ ...usageTotals(usage.rows.filter(row => (row.component === component || (component === "other" && row.component === "embedding")))), label }));
      const table = el("table", { class: "usage-table" },
        el("thead", {}, el("tr", {}, ...["用途", "AI料金", "Web検索"].map(label => el("th", { scope: "col", label })))),
        el("tbody", {}, ...displayRows.map(row => el("tr", {},
          el("th", { scope: "row", text: t(row.label || labels[row.category] || "その他") }),
          el("td", { text: costText(row) }),
          el("td", { text: t("{0}回", row.searches || 0) })))));
      results.append(el("div", { class: "usage-table-scroll" }, table));
      if (total.failures || total.searchFailures) results.append(el("p", { class: "hint", text: t("失敗したリクエスト：AI {0}件、Web検索 {1}件", total.failures, total.searchFailures) }));
      if (usage.first) results.append(el("p", { class: "hint", text: t("この月の記録開始：{0}", new Date(usage.first).toLocaleString(getLanguage())) }));
    } catch (error) {
      if (current !== request) return;
      clear(results).append(el("p", { class: "hint", text: String(error.message || error) }));
    } finally {
      if (current === request) refresh.disabled = false;
    }
  }
  section.addEventListener("toggle", () => { if (section.open && !loaded) { loaded = true; void load(); } });
  refresh.addEventListener("click", () => void load());
  month.addEventListener("change", () => void load());
  return { element: section, load };
}
