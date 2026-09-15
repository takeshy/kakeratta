// Who is in the room.
//
// Each one is a domain, a model size and a set of tools. There is no ceiling on
// how many: the routing is what keeps a post from waking all of them, so adding
// a domain costs one more line in one routing call rather than one more model
// call per post.
import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { t, getLanguage } from "./i18n.js";
import { showPersonaBuilder } from "./persona-builder.js";
import { gloss } from "./builtins.js";
import { createMemoryView } from "./memory.js";

const localZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
// The parts of the day a check-in can wake in, in the order the server knows them.
const DAY_PARTS = [["morning", "朝"], ["noon", "昼"], ["evening", "晩"]];

const tierLabels = {
  low: "low（先に動く）",
  middle: "middle",
  high: "high（最後に動く）",
};

export function createPersonas({ onChange, onSearch, getSettings }) {
  const view = el("div", { class: "panel-body" });
  let rows = [];
  let toolNames = [];

  async function load({ quiet = false } = {}) {
    try {
      const payload = await api.personas();
      rows = payload.personas || [];
      toolNames = payload.tools || [];
      onChange?.(rows);
      draw();
    } catch (error) {
      if (!quiet) toast(String(error.message || error), "error");
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

  function toolLink(name, label = name) {
    const known = ["web_search", "place_search", "route", "timeline_search", "notes"];
    const lang = getLanguage();
    const anchor = known.includes(name) ? `${lang}-tool-${name}` : `${lang}-tools`;
    return el("a", { href: `/manual/?lang=${lang}#${anchor}`, target: "_blank", rel: "noopener noreferrer",
      text: label, title: t("{0} の説明を開く（新しいタブ）", label) });
  }

  // A persona that works by the clock (plana) starts with morning, noon, and
  // evening chosen. Only the browser knows the time zone, so the default is
  // saved from here, once: clearing every part stores the zone too, so the
  // default is never applied again.
  const defaulting = new Set();
  function ensureCheckIns() {
    for (const row of rows) {
      if (!row.checkInByDefault || !row.active || row.dailyCheckIn?.timeZone || defaulting.has(row.id)) continue;
      defaulting.add(row.id);
      api.setDailyCheckIn(row.id, { slots: DAY_PARTS.map(([name]) => name), timeZone: localZone() }).then((payload) => {
        rows = payload.personas || rows;
        onChange?.(rows);
        draw();
      }).catch(() => defaulting.delete(row.id));
    }
  }

  function draw() {
    ensureCheckIns();
    clear(view);
    view.append(
      el("p", {
        class: "hint",
        label: "担当領域ごとに記憶が分かれています。投稿がどれに渡るかは本体が振り分け、" +
          "そのうえで喋るかどうかは各担当が決めます。@名前 で呼べば必ず答えます。",
      }),
    );
    view.append(el("button", { class: "button primary", label: "AIと担当を作る", onclick: () => showPersonaBuilder({ getSettings, onSave: load }) }));
    view.append(el("p", { class: "hint", label: "どの担当も、必要なときに過去のかけらを検索し、元の投稿を参照できます。" }));
    view.append(el("p", { class: "hint" },
      el("span", { label: "すべての担当が使える道具: " }),
      toolLink("timeline_search", t("過去のかけら検索")), " / ",
      toolLink("notes", t("記憶の読み書き"))));
    for (const row of rows) view.append(drawRow(row));
    view.append(drawInstall());
  }

  function drawRow(row) {
    const shown = gloss(row);
    const box = el("section", { class: `persona ${row.active ? "on" : ""}` });
    box.append(
      el(
        "div",
        { class: "persona-head" },
        el("span", { class: "avatar", text: row.icon || "◎" }),
        el("span", { class: "who", text: shown.name }),
        shown.note ? el("span", { class: "note-tag", text: shown.note }) : null,
        el("span", { class: "tag", label: tierLabels[row.model] || row.model }),
        el("span", { class: "spacer" }),
        el("label", { class: "switch" },
          el("input", {
            type: "checkbox",
            checked: row.active,
            onchange: (event) => void setActive(row.id, event.target.checked),
          }),
          el("span", { text: t("有効にする") })),
      ),
      el("p", { class: "domain", text: shown.domain }),
    );
    const schedule = row.dailyCheckIn || {};
    const chosen = schedule.slots || [];
    const zone = schedule.timeZone || localZone();
    // One tap per part of the day; each tap saves, like the active switch.
    const parts = DAY_PARTS.map(([name, label]) => {
      const on = chosen.includes(name);
      return el("button", {
        type: "button", class: `chip${on ? " active" : ""}`, label, "aria-pressed": String(on), disabled: !row.active,
        onclick: async (event) => {
          const slots = DAY_PARTS.map(([part]) => part).filter((part) => part === name ? !on : chosen.includes(part));
          event.currentTarget.disabled = true;
          try {
            const payload = await api.setDailyCheckIn(row.id, { slots, timeZone: zone });
            rows = payload.personas || rows;
            onChange?.(rows);
            draw();
          } catch (error) {
            toast(String(error.message || error), "error");
            draw();
          }
        },
      });
    });
    box.append(el("div", { class: "compose-row", role: "group", "aria-label": t("朝・昼・晩のチェックイン") },
      el("span", { label: "朝・昼・晩のチェックイン" }), ...parts,
      el("span", { class: "hint", text: zone })));
    box.append(el("p", { class: "hint", label: "選んだ時間帯（朝 6〜11時・昼 11〜17時・晩 17〜23時）に起き、伝えることがあるときだけ話します。" }));
    if (row.tools?.length) {
      box.append(el("p", { class: "tools" }, `${t("道具")}: `,
        row.tools.flatMap((name, index) => index ? [" / ", toolLink(name)] : [toolLink(name)])));
    }
    box.append(
      el(
        "div",
        { class: "compose-row" },
        el("span", { class: "hint", text: t("起きた回数 {0}", row.wakes || 0) }),
        el("span", { class: "spacer" }),
        el("button", { class: "chip ghost", label: "AIで修正", onclick: () => showPersonaBuilder({ getSettings, persona: row, onSave: load }) }),
        el("button", { class: "chip ghost", label: "覚えていること", onclick: () => void showMemory(row) }),
        row.builtin
          ? null
          : el("button", {
            class: "chip ghost",
            label: "削除",
            onclick: async () => {
              if (!confirm(t("{0} を削除しますか（メモは残ります）", row.name))) return;
              await api.uninstallPersona(row.id).catch((error) => toast(String(error.message || error), "error"));
              await load();
            },
          }),
      ),
    );
    return box;
  }

  async function showMemory(row) {
    let memory = { notes: [] };
    try {
      memory = await api.memory(row.id);
    } catch (error) {
      toast(String(error.message || error), "error");
      return;
    }
    const memoryHost = el("div");
    const drawMemory = () => memoryHost.replaceChildren(createMemoryView(memory.notes, {
      onSearch: onSearch ? (query) => { close(); onSearch(query); } : undefined,
      onForget: async (path) => {
        await api.forgetNote(row.id, path);
        memory.notes = (memory.notes || []).filter(note => note.path !== path);
        drawMemory();
        memoryHost.focus();
      },
    }));
    memoryHost.setAttribute("tabindex", "-1");
    const backdrop = el("div", { class: "dialog-backdrop" });
    const previousFocus = document.activeElement;
    const close = () => { backdrop.remove(); previousFocus?.focus(); };
    backdrop.addEventListener("click", (event) => { if (event.target === backdrop) close(); });
    backdrop.addEventListener("keydown", (event) => {
      if (event.key === "Escape") { event.preventDefault(); close(); }
      if (event.key !== "Tab") return;
      const focusable = [...backdrop.querySelectorAll("button, a[href], summary")]
        .filter(node => !node.disabled && node.getClientRects().length);
      const first = focusable[0], last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    });
    drawMemory();
    backdrop.append(
      el(
        "div",
        { class: "dialog memory-dialog", role: "dialog", "aria-modal": "true", "aria-label": t("{0} があなたについて覚えていること", gloss(row).name) },
        el("h2", { text: t("{0} があなたについて覚えていること", gloss(row).name) }),
        el("p", { class: "hint", label: "担当が保存したメモです。根拠のリンクから元の投稿や関連する記憶を確認できます。" }),
        memoryHost,
        el(
          "div",
          { class: "compose-row" },
          el("button", {
            class: "chip ghost",
            label: "すべてのメモを忘れさせる",
            onclick: async () => {
              if (!confirm(t("{0} の記憶を消しますか。元に戻せません。", row.name))) return;
              try {
                await api.forget(row.id);
                close();
                await load();
              } catch (error) { toast(String(error.message || error), "error"); }
            },
          }),
          el("span", { class: "spacer" }),
          el("button", { class: "button primary", label: "閉じる", onclick: close }),
        ),
      ),
    );
    document.body.append(backdrop);
    backdrop.querySelector(".memory-dialog").setAttribute("tabindex", "-1");
    backdrop.querySelector(".memory-dialog").focus();
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
          instructions: "概念は type: Wine（銘柄）と type: Preference（好み）。同じ好みが3回出たら Preference に統合する。引用と本人の意見を分け、推測は明記し、統合後も代表例と反例を残す。",
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
      el("summary", { label: "担当を追加する（skill.json）" }),
      el("p", {
        class: "hint",
        text: `${t("担当領域は本体が読んで振り分けに使います。指示は本人だけが読みます。メモの形式（OKF）は全員に渡るので、指示には「どんな type を使い、いつ統合するか」を書いてください。")} ` +
          `${t("使える道具")}: ${toolNames.join(" / ") || t("（なし）")}`,
      }),
      area,
      el(
        "div",
        { class: "compose-row" },
        el("button", {
          class: "button primary",
          label: "追加",
          onclick: async () => {
            try {
              const skill = await api.installPersona(area.value);
              toast(t("{0} を追加しました", skill.name));
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

  return {
    element: view,
    load,
    rows: () => rows,
    // The banner on the main screen can put someone in the room without
    // opening this panel at all.
    activate: (id) => setActive(id, true),
  };
}
