// Who is in the room.
//
// Each one is a domain, a model size and a set of tools. There is no ceiling on
// how many: the routing is what keeps a post from waking all of them, so adding
// a domain costs one more line in one routing call rather than one more model
// call per post.
import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";

const tierLabels = {
  low: "low（先に動く）",
  middle: "middle",
  high: "high（最後に動く）",
};

export function createPersonas({ onChange }) {
  const view = el("div", { class: "panel-body" });
  let rows = [];
  let toolNames = [];

  async function load() {
    try {
      const payload = await api.personas();
      rows = payload.personas || [];
      toolNames = payload.tools || [];
      onChange?.(rows);
      draw();
    } catch (error) {
      toast(String(error.message || error), "error");
    }
  }

  async function setActive(id, on) {
    const active = rows.filter((row) => (row.id === id ? on : row.active)).map((row) => row.id);
    try {
      const payload = await api.setActive(active);
      rows = payload.personas || rows;
      onChange?.(rows);
      draw();
    } catch (error) {
      toast(String(error.message || error), "error");
    }
  }

  function draw() {
    clear(view);
    view.append(
      el("p", {
        class: "hint",
        text: "担当領域ごとに記憶が分かれています。投稿がどれに渡るかは本体が振り分け、" +
          "そのうえで喋るかどうかは各担当が決めます。@名前 で呼べば必ず答えます。",
      }),
    );
    for (const row of rows) view.append(drawRow(row));
    view.append(drawInstall());
  }

  function drawRow(row) {
    const box = el("section", { class: `persona ${row.active ? "on" : ""}` });
    box.append(
      el(
        "div",
        { class: "persona-head" },
        el("span", { class: "avatar", text: row.icon || "◎" }),
        el("span", { class: "who", text: row.name }),
        el("span", { class: "tag", text: tierLabels[row.model] || row.model }),
        el("span", { class: "spacer" }),
        el("label", { class: "switch" },
          el("input", {
            type: "checkbox",
            checked: row.active,
            onchange: (event) => void setActive(row.id, event.target.checked),
          }),
          el("span", { text: row.active ? "いる" : "いない" })),
      ),
      el("p", { class: "domain", text: row.domain }),
    );
    if (row.tools?.length) {
      box.append(el("p", { class: "tools", text: `道具: ${row.tools.join(" / ")}` }));
    }
    box.append(
      el(
        "div",
        { class: "compose-row" },
        el("span", { class: "hint", text: `起きた回数 ${row.wakes || 0}` }),
        el("span", { class: "spacer" }),
        el("button", { class: "chip ghost", text: "メモを見る", onclick: () => void showMemory(row) }),
        row.builtin
          ? null
          : el("button", {
            class: "chip ghost",
            text: "削除",
            onclick: async () => {
              if (!confirm(`${row.name} を削除しますか（メモは残ります）`)) return;
              await api.uninstallPersona(row.id).catch((error) => toast(String(error.message || error), "error"));
              await load();
            },
          }),
      ),
    );
    return box;
  }

  // The notes are the persona's own text, written in whatever shape its skill
  // told it to use. Reading them is the only way to see what it decided was
  // worth keeping - and the only way to notice when it is keeping the wrong
  // things.
  async function showMemory(row) {
    let memory = { notes: [] };
    try {
      memory = await api.memory(row.id);
    } catch (error) {
      toast(String(error.message || error), "error");
      return;
    }
    const backdrop = el("div", { class: "dialog-backdrop" });
    const close = () => backdrop.remove();
    backdrop.append(
      el(
        "div",
        { class: "dialog" },
        el("h3", { text: `${row.name} のメモ` }),
        el("p", {
          class: "hint",
          text: `${(memory.notes || []).length}件。何を残し何を捨てるかは本人の判断で、本体は中身を読みません。`,
        }),
        (memory.notes || []).length
          ? el("div", { class: "notes" }, (memory.notes || []).map((note) =>
            el("details", { class: "note" },
              el("summary", { text: note.path }),
              el("pre", { class: "memo", text: note.text }))))
          : el("p", { class: "hint", text: "（まだ何もありません）" }),
        el(
          "div",
          { class: "compose-row" },
          el("button", {
            class: "chip ghost",
            text: "忘れさせる",
            onclick: async () => {
              if (!confirm(`${row.name} の記憶を消しますか。元に戻せません。`)) return;
              await api.forget(row.id).catch((error) => toast(String(error.message || error), "error"));
              close();
              await load();
            },
          }),
          el("span", { class: "spacer" }),
          el("button", { class: "button primary", text: "閉じる", onclick: close }),
        ),
      ),
    );
    document.body.append(backdrop);
  }

  function drawInstall() {
    const area = el("textarea", {
      class: "skill-input",
      rows: "6",
      placeholder: JSON.stringify(
        {
          id: "wine",
          name: "ぶどう",
          icon: "🍷",
          domain: "ワイン。銘柄、産地、ラベルの写真、飲んだ記録。食事全般や技術の話には反応しない。",
          tone: "控えめ。おすすめを押しつけない。",
          instructions: "概念は type: Wine（銘柄）と type: Preference（好み）。同じ好みが3回出たら Preference に統合し、根拠の銘柄は消す。",
          model: "high",
          tools: toolNames.slice(0, 2),
        },
        null,
        2,
      ),
    });
    return el(
      "details",
      { class: "install" },
      el("summary", { text: "担当を追加する（skill.json）" }),
      el("p", {
        class: "hint",
        text: "担当領域は本体が読んで振り分けに使います。指示は本人だけが読みます。" +
          "メモの形式（OKF）は全員に渡るので、指示には「どんな type を使い、いつ統合するか」を書いてください。" +
          `使える道具: ${toolNames.join(" / ") || "（なし）"}`,
      }),
      area,
      el(
        "div",
        { class: "compose-row" },
        el("button", {
          class: "button primary",
          text: "追加",
          onclick: async () => {
            try {
              const skill = await api.installPersona(area.value);
              toast(`${skill.name} を追加しました`);
              area.value = "";
              await load();
            } catch (error) {
              toast(String(error.message || error), "error");
            }
          },
        }),
      ),
    );
  }

  return { element: view, load, rows: () => rows };
}
