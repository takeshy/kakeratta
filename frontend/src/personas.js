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
import { showPersonaEditor } from "./persona-editor.js";
import { gloss } from "./builtins.js";
import { createMemoryView } from "./memory.js";
import { capability, toolLabel, uniqueTools } from "./tool_labels.js";

const localZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

// A schedule is shown in the zone it was set in, so the card reads the way the
// persona thinks about it.
function scheduleWhen(schedule) {
  const at = new Date(schedule.at);
  if (Number.isNaN(at.getTime())) return "";
  const locale = getLanguage() === "ja" ? "ja-JP" : "en-US";
  const parts = (options) => {
    try {
      return new Intl.DateTimeFormat(locale, { timeZone: schedule.timeZone || localZone(), ...options }).format(at);
    } catch {
      return new Intl.DateTimeFormat(locale, options).format(at);
    }
  };
  const clock = parts({ hour: "2-digit", minute: "2-digit" });
  return schedule.daily ? `${t("毎日")} ${clock}` : `${parts({ month: "2-digit", day: "2-digit" })} ${clock}`;
}

const tierLabels = {
  low: "low（先に動く）",
  middle: "middle",
  high: "high（最後に動く）",
};

export function createPersonas({ onChange, onSearch, onAsk, getSettings }) {
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

  function toolLink(name, label = t(toolLabel(name))) {
    const known = ["web_search", "place_search", "route", "timeline_search", "notes", "schedule"];
    const lang = getLanguage();
    const base = capability(name);
    const anchor = known.includes(base) ? `${lang}-tool-${base}` : `${lang}-tools`;
    return el("a", { href: `/manual/?lang=${lang}#${anchor}`, target: "_blank", rel: "noopener noreferrer",
      text: label, title: t("{0} の説明を開く（新しいタブ）", label) });
  }

  // A name tapped on the main screen opens this panel, which reloads as it
  // opens: the card to scroll to is remembered until there is one to scroll to.
  let pending = null;
  function revealPending() {
    if (!pending) return;
    const card = view.querySelector(`[data-id="${pending}"]`);
    if (!card) return;
    pending = null;
    card.scrollIntoView({ block: "center", behavior: "smooth" });
    card.classList.add("revealed");
    setTimeout(() => card.classList.remove("revealed"), 2000);
  }

  function draw() {
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
    revealPending();
  }

  function drawRow(row) {
    const shown = gloss(row);
    const box = el("section", { class: `persona ${row.active ? "on" : ""}`, "data-id": row.id });
    box.append(
      el(
        "div",
        { class: "persona-head" },
        el("span", { class: "avatar", text: row.icon || "◎" }),
        el("span", { class: "who", text: shown.name }),
        shown.note ? el("span", { class: "note-tag", text: shown.note }) : null,
        el("span", { class: "tag", label: row.id === "hermes" ? "外部エージェント" : tierLabels[row.model] || row.model }),
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
    if (row.id === "hermes") {
      box.append(el("p", { class: "hint", label: "あなたのHermesがMCPで依頼を取得し、結果を返します。接続は設定画面から行えます。" }));
      box.append(el("p", { class: "hint", text: t("未処理の依頼 {0}件", row.pendingTasks || 0) }));
      box.append(el("button", { class: "button primary", label: "Hermesに聞く", disabled: !row.active,
        onclick: () => onAsk?.(row) }));
    }
    const schedule = row.dailyCheckIn || {};
    const enabled = el("input", { type: "checkbox", checked: !!schedule.enabled, disabled: !row.active });
    const time = el("input", { type: "time", value: schedule.time || "08:00", disabled: !row.active, "aria-label": t("希望時刻") });
    const zone = schedule.timeZone || localZone();
    const saveDaily = async () => {
      const next = { enabled: enabled.checked, time: time.value || "08:00", timeZone: zone };
      enabled.disabled = time.disabled = true;
      try {
        const payload = await api.setDailyCheckIn(row.id, next);
        rows = payload.personas || rows;
        onChange?.(rows);
        draw();
      } catch (error) {
        toast(String(error.message || error), "error");
        draw();
      }
    };
    enabled.addEventListener("change", saveDaily);
    time.addEventListener("change", saveDaily);
    box.append(el("div", { class: "compose-row" },
      el("label", { class: "switch" }, enabled, el("span", { label: "1日1回のチェックイン" })),
      time, el("span", { class: "hint", text: zone })));
    box.append(el("p", { class: "hint", label: row.id === "hermes" ? "希望時刻に確認依頼を保存します。Hermes側で取得すると実行されます。" : "毎日一度だけ起き、伝えることがある日だけ話します。" }));
    if (row.schedules?.length) {
      box.append(el("div", { class: "compose-row" },
        el("span", { class: "hint", label: "この担当が預けた予定" }),
        ...row.schedules.map((schedule) => el("span", { class: "chip ghost schedule",
          text: `${scheduleWhen(schedule)} · ${schedule.note || ""}`.trim() })),
      ));
      box.append(el("p", { class: "hint", label: "予定は担当が置きます。メモを消すと担当が見直します。" }));
    }
    if (row.tools?.length) {
      box.append(el("p", { class: "tools" }, `${t("道具")}: `,
        uniqueTools(row.tools).flatMap((name, index) => index ? [" / ", toolLink(name)] : [toolLink(name)])));
    }
    box.append(
      el(
        "div",
        { class: "compose-row" },
        el("span", { class: "hint", text: t("起きた回数 {0}", row.wakes || 0) }),
        el("span", { class: "spacer" }),
        el("button", { class: "chip ghost", label: "修正", onclick: () => showPersonaEditor({ getSettings, persona: row, toolNames, onSave: load }) }),
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

  // focusPath: a note to open and scroll to first - the one a remark on the
  // timeline said it wrote.
  async function showMemory(row, focusPath = "") {
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
      onEdit: async (path, text) => {
        const updated = await api.editNote(row.id, path, text);
        memory.notes = (memory.notes || []).map(note => note.path === path ? { ...note, ...updated } : note);
        drawMemory();
        toast(t("メモを保存しました"));
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
      const focusable = [...backdrop.querySelectorAll("button, a[href], summary, textarea")]
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
    if (focusPath) {
      const card = [...backdrop.querySelectorAll("[data-path]")].find((node) => node.getAttribute("data-path") === focusPath);
      if (card) {
        if (card.tagName === "DETAILS") card.open = true;
        card.scrollIntoView?.({ block: "start" });
        card.focus?.({ preventScroll: true });
      }
    }
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
          memory: "型は Wine（銘柄。産地・品種・感想）と Preference（好みの傾向）の2つ。同じ好みが3回出たら Preference に統合する。",
          instructions: "引用と本人の意見を分け、推測は明記し、統合後も代表例と反例を残す。",
          model: "high",
          tools: uniqueTools(toolNames).slice(0, 2),
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
          `${t("使える道具")}: ${uniqueTools(toolNames).join(" / ") || t("（なし）")}`,
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
    reveal: (id) => { pending = id; revealPending(); },
    // A remark on the timeline leads to the note written with it.
    showMemoryFor: (row, path) => showMemory(row, path),
  };
}
