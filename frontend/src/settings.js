// Where the models run, the web search key, which tools are wired up, and how dictation is set up.
//
// None of the keys here ever reach the page: they are stored by the relay and
// come back as "set or not". A page that does not have a key cannot leak one.
import { el, clear, toast } from "./dom.js";
import { createBlueskyConnection } from "./bluesky.js";
import { createMCPConnection } from "./mcp-connection.js";
import { api } from "./api.js";
import { speechLanguageOptions } from "./speech_languages.js";
import { createAudioMeter } from "./meter.js";
import { getLanguage, setLanguage, t } from "./i18n.js";
import { TEXT_SIZES, getTextSize, setTextSize } from "./text-size.js";

export function aiConfigured(settings) {
  if (!settings) return false;
  if (settings.openRouterMode) return Boolean(settings.openRouterApiKey?.trim());
  return Boolean(settings.baseUrl?.trim() && settings.lowModel?.trim() && settings.apiKey?.trim());
}

export function createSettings({ speaker, onSettings, onMCPChange, closeAccount = null }) {
  const view = el("div", { class: "panel-body settings-panel" });
  let settings = null;
  let revealOpenRouter = () => {};
  let setupHint = null;
  let about = null;
  let bluesky = null;
  let openRouterModels = [];
  let openRouterVerified = false;
  let stopMicCheck = () => {};
  let exportRow = null;
  let exporting = false;
  let exportLink = null;
  let stopExportWatch = () => {};

  async function load() {
    settings = await api.settings();
    about = about || (await api.about().catch(() => null));
    onSettings?.(settings);
    draw();
  }

  async function patch(change) {
    const next = { ...settings, ...change };
    try {
      settings = await api.saveSettings(next);
      if (setupHint) setupHint.hidden = aiConfigured(settings);
      onSettings?.(settings);
      return true;
    } catch (error) {
      toast(String(error.message || error), "error");
      return false;
    }
  }

  const patchSpeech = (change) => patch({ speech: { ...settings.speech, ...change } });

  function field(label, value, onChange, { type = "text", hint = "" } = {}) {
    const input = el("input", { type, value: value ?? "" });
    input.addEventListener("change", () => void onChange(input.value.trim()));
    return el("label", { class: "field" },
      el("span", { text: t(label) }), input, hint ? el("span", { class: "hint", text: t(hint) }) : null);
  }

  function select(label, value, options, onChange, hint = "") {
    const node = el("select", {}, options.map(([id, text]) => el("option", { value: id, text: t(text), selected: id === value })));
    node.addEventListener("change", () => void onChange(node.value));
    return el("label", { class: "field" },
      el("span", { text: t(label) }), node, hint ? el("span", { class: "hint", text: t(hint) }) : null);
  }

  function modelPicker(label, value, models, onChange) {
    let selected = value || "";
    let choices = models;
    const current = el("span", { class: "model-picker-current", text: selected });
    const search = el("input", { type: "search", i18nPlaceholder: "モデルを検索" });
    const list = el("div", { class: "model-picker-list", role: "listbox" });
    const details = el("details", { class: "model-picker" },
      el("summary", {}, current), search, list);
    const render = () => {
      const query = search.value.trim().toLowerCase();
      const matching = [...new Set([selected, ...choices].filter(Boolean))]
        .filter((id) => !query || id.toLowerCase().includes(query))
        .slice(0, 100);
      list.replaceChildren(...matching.map((id) => el("button", {
        type: "button",
        class: id === selected ? "selected" : "",
        role: "option",
        "aria-selected": id === selected ? "true" : "false",
        text: id,
        onclick: () => {
          selected = id;
          current.textContent = id;
          details.open = false;
          search.value = "";
          render();
          void onChange(id);
        },
      })));
      if (!matching.length) list.append(el("p", { class: "hint", label: "一致するモデルがありません" }));
    };
    search.addEventListener("input", render);
    details.addEventListener("toggle", () => {
      if (details.open) {
        render();
        search.focus();
      }
    });
    render();
    const field = el("div", { class: "field" }, el("span", { text: t(label) }), details);
    field.setModels = (next) => {
      choices = next;
      render();
    };
    return field;
  }

  // Models are not chosen here; the tiers keep their defaults (see the manual).
  // Listing the endpoint's models is still the check that the key works.
  function connectionCheck(saveKey, { fetchModels = () => api.models(), label = "接続確認", onModels } = {}) {
    const check = el("button", {
      class: "chip",
      label,
      onclick: async () => {
        check.disabled = true;
        check.textContent = t("接続中…");
        try {
          if (!(await saveKey())) return;
          const result = await fetchModels();
          onModels?.(result.models ?? []);
          toast(t("接続できました"));
        } catch (error) {
          toast(String(error.message || error), "error");
        } finally {
          check.disabled = false;
          check.textContent = t(label);
        }
      },
    });
    return el("div", { class: "compose-row" }, check);
  }

  function drawAI() {
    const apiKey = field("APIキー", settings.apiKey, (value) => patch({ apiKey: value }), {
      type: "password",
      hint: "サーバー側にだけ保存され、指定した接続先以外へは送りません",
    });
    view.append(el("h3", { label: "モデル" }));
    view.append(
      el("p", { label: "Nebius Token FactoryのNVIDIA Nemotronモデルを使います。Token FactoryのAPIキーを入力してください。" }),
      el("a", { href: "https://tokenfactory.nebius.com", target: "_blank", rel: "noopener noreferrer", text: t("APIキーを取得") }),
    );
    setupHint = el("p", { class: "hint warn", hidden: aiConfigured(settings) },
      el("span", { label: "最初にAI接続を設定してください。設定が完了するまでこの画面は閉じられません。" }),
      " ",
      el("a", {
        href: "#openrouter-settings", label: "OpenRouterを使うことも可能です。",
        onclick: (event) => { event.preventDefault(); revealOpenRouter(); },
      }),
    );
    view.append(setupHint);
    view.append(
      apiKey,
      connectionCheck(() => patch({ apiKey: apiKey.querySelector("input").value.trim() })),
    );
  }

  // OpenRouter is the other way to run the models: Jev for routing alone, or
  // every tier moved across. It sits under Advanced because what almost
  // everyone runs is the default.
  function openRouterSection() {
    const jevKey = field("OpenRouter APIキー", settings.openRouterApiKey,
      (value) => patch({ openRouterApiKey: value }), {
        type: "password",
        hint: "JevまたはOpenRouter専用モードに使います。サーバー側だけに保存します",
      });
    const jevEnabled = el("input", { type: "checkbox", checked: !!settings.jevEnabled });
    const openRouterMode = el("input", { type: "checkbox", checked: !!settings.openRouterMode });
    // A new opt-in still requires a successful connection check. An existing
    // opt-in must remain clickable so it can always be turned off after the
    // page is reopened, when verification state starts fresh.
    jevEnabled.disabled = !openRouterVerified && !jevEnabled.checked;
    openRouterMode.disabled = !openRouterVerified && !openRouterMode.checked;
    const routerPicker = modelPicker("Routerモデル", settings.openRouterRouterModel, openRouterModels, (value) => patch({ openRouterRouterModel: value }));
    const jevRouter = el("div", { class: "field fixed-model" },
      el("span", { label: "Routerモデル" }),
      el("strong", { text: "Jev (jev-latest)" }),
      el("span", { class: "hint", label: "Jevが有効な間はRouterに固定されます" }));
    const pickers = [
      routerPicker,
      modelPicker("Highモデル", settings.openRouterHighModel, openRouterModels, (value) => patch({ openRouterHighModel: value })),
      modelPicker("Middleモデル", settings.openRouterMiddleModel, openRouterModels, (value) => patch({ openRouterMiddleModel: value })),
      modelPicker("Lowモデル", settings.openRouterLowModel, openRouterModels, (value) => patch({ openRouterLowModel: value })),
    ];
    const syncRouterModel = () => {
      routerPicker.hidden = jevEnabled.checked;
      jevRouter.hidden = !jevEnabled.checked;
    };
    jevEnabled.addEventListener("change", () => {
      syncRouterModel();
      void patch({ jevEnabled: jevEnabled.checked });
    });
    const refreshPickers = (models) => {
      openRouterModels = models;
      for (const picker of pickers) picker.setModels(models);
    };
    const pickerGroup = el("div", { class: "openrouter-models" }, ...pickers,
      el("p", { class: "hint", label: "Lowはthinking無効、最大1,024トークン、temperature 0.1で呼び出します" }));
    syncRouterModel();
    pickerGroup.hidden = !openRouterMode.checked;
    openRouterMode.addEventListener("change", () => {
      pickerGroup.hidden = !openRouterMode.checked;
      void patch({ openRouterMode: openRouterMode.checked });
    });
    const openRouterCheck = connectionCheck(
      () => patch({ openRouterApiKey: jevKey.querySelector("input").value.trim() }),
      { fetchModels: () => api.openRouterModels(), label: "OpenRouterの接続確認", onModels: (models) => {
        refreshPickers(models);
        openRouterVerified = true;
        jevEnabled.disabled = false;
        openRouterMode.disabled = false;
      } },
    );
    return el("div", { class: "settings-block", id: "openrouter-settings" },
      el("h3", { label: "OpenRouterを使う" }),
      jevKey,
      openRouterCheck,
      el("p", { class: "hint", label: "接続確認が成功すると、JevとOpenRouter専用モードを選べます。" }),
      el("label", { class: "field check-field" }, jevEnabled, el("span", { label: "Jevを有効にする" })),
      el("p", { class: "hint", label: "Jevは正確で速く、費用が最も安い振り分け専用モデルです。判定理由は表示されません。" }),
      jevRouter,
      el("label", { class: "field check-field" }, openRouterMode, el("span", { label: "OpenRouter専用モード" })),
      el("p", { class: "hint", label: "ONにするとHigh・Middle・LowもOpenRouter経由になります。ツール呼び出しはExactoを使います。" }),
      pickerGroup,
    );
  }

  // Web search is the one tool with a key of the user's own. Everything else a
  // persona can reach - map endpoints, MCP servers - is the operator's
  // configuration, the same for everyone, and not shown here.
  function drawTools() {
    const search = settings.search ?? {};
    const apiKey = field("Tavily APIキー", search.apiKey,
      (value) => patch({ search: { apiKey: value } }), {
        type: "password",
        hint: "Web検索に使います。AIモデルのAPIキーとは別です。Web検索を使用しない場合は空欄にしてください",
      });
    const check = el("button", {
      class: "chip",
      label: "Web検索の接続確認",
      onclick: async () => {
        check.disabled = true;
        check.textContent = t("接続中…");
        try {
          const saved = await patch({ search: { apiKey: apiKey.querySelector("input").value.trim() } });
          if (!saved) return;
          await api.checkSearch();
          toast(t("Web検索に接続できました"));
        } catch (error) {
          toast(String(error.message || error), "error");
        } finally {
          check.disabled = false;
          check.textContent = t("Web検索の接続確認");
        }
      },
    });
    view.append(
      el("h3", { label: "Web検索" }),
      apiKey,
      el("div", { class: "compose-row" }, check),
    );
  }

  // Markdown in your own words is opt-in: a persona's reply is rendered
  // because nobody edits those, but what you write is yours, and writing
  // Markdown is a decision, not a default. Editing stays what it has always
  // been - the source, written directly.
  function drawInput(into) {
    const markdown = el("input", { type: "checkbox", checked: !!settings.markdownInput });
    markdown.addEventListener("change", () => void patch({ markdownInput: markdown.checked }));
    into.append(
      el("label", { class: "field check-field" }, markdown, el("span", { label: "入力内容をマークダウンとして扱う" })),
      el("p", {
        class: "hint",
        label: "ONにすると、あなたの投稿と返信もマークダウンとして表示します。編集はマークダウンをそのまま書きます。担当の返事は常にマークダウンで表示します。",
      }),
    );
  }

  // Whether the external agent is used at all. Pausing keeps every persona's
  // planner choice for when it is switched back on. How long an ask waits is
  // each connection's own.
  function drawExternalAgent(into, onPause) {
    const agent = settings.externalAgent || {};
    const save = async (change) => {
      const saved = await patch({ externalAgent: { ...(settings.externalAgent || {}), ...change } });
      if (saved) onMCPChange?.();
      return saved;
    };
    const enabled = el("input", { type: "checkbox", checked: !agent.paused });
    enabled.addEventListener("change", async () => {
      enabled.disabled = true;
      if (await save({ paused: !enabled.checked })) onPause?.();
      else enabled.checked = !enabled.checked;
      enabled.disabled = false;
    });
    into.append(
      el("label", { class: "field check-field" }, enabled, el("span", { label: "外部エージェントを使う" })),
      el("p", { class: "hint", label: "OFFにすると、外部エージェントからの接続を受け付けず、外部エージェントを優先する担当も内蔵モデルで考えます。担当ごとの設定はそのまま残ります。" }),
    );
  }

  // The level meter opens a microphone of its own, so it runs only while the
  // reader asks for it here and never beside dictation.
  function micCheck() {
    const meterNode = el("span", { class: "mic-meter", ariaHidden: "true" });
    const meter = createAudioMeter(meterNode);
    const button = el("button", { class: "chip", type: "button", label: "マイク確認" });
    let stream = null;
    let request = 0;
    const stop = () => {
      request++;
      stream?.getTracks().forEach((track) => track.stop());
      stream = null;
      meter.detach();
      button.textContent = t("マイク確認");
    };
    button.addEventListener("click", () => {
      if (stream) {
        stop();
        return;
      }
      const current = ++request;
      button.textContent = t("マイクを準備しています…");
      Promise.resolve().then(() => navigator.mediaDevices.getUserMedia({ audio: true })).then((opened) => {
        if (current !== request) {
          opened.getTracks().forEach((track) => track.stop());
          return;
        }
        stream = opened;
        button.textContent = t("確認を終える");
        void meter.attach(opened);
      }).catch(() => {
        if (current !== request) return;
        stop();
        toast(t("マイクを取得できませんでした。"), "error");
      });
    });
    stopMicCheck = stop;
    return el("div", { class: "compose-row" }, button, meterNode);
  }

  function drawSpeech(into) {
    const speech = settings.speech ?? {};
    const languages = speechLanguageOptions(speech.language || "auto");
    into.append(
      el("h3", { label: "音声入力" }),
      el("p", { class: "hint", label: "ブラウザ音声認識" }),
      select("自動送信前にAIで文章を整える", String(!!speech.formatBeforeSend),
        [["false", "OFF"], ["true", "ON"]],
        (next) => patchSpeech({ formatBeforeSend: next === "true" }),
        "終了フレーズで送信するときだけ、lowモデルが意味を変えずに句読点や言いよどみを整えます"),
      select("言語", speech.language || "auto", languages.map((item) => [item.value, item.label]),
        (next) => patchSpeech({ language: next })),
      select("無音で自動停止", String(speech.silenceSeconds ?? 3),
        [["0", "しない"], ...Array.from({ length: 10 }, (_, i) => [String(i + 1), t("{0}秒", i + 1)])],
        (next) => patchSpeech({ silenceSeconds: Number(next) })),
      field("送信の合図", speech.sendPhrase, (value) => patchSpeech({ sendPhrase: value }), {
        hint: "話し終わりの合図。これで投稿されます。空欄で無効",
      }),
      micCheck(),
    );
  }

  function drawReadAloud(into) {
    into.append(el("h3", { label: "読み上げ" }));
    const rate = el("input", {
      type: "range", min: "0.5", max: "2", step: "0.1", value: String(settings.readAloudRate || 1.2),
    });
    const rateValue = el("span", { class: "hint", text: `${Number(settings.readAloudRate || 1.2).toFixed(1)}×` });
    rate.addEventListener("input", () => {
      rateValue.textContent = `${Number(rate.value).toFixed(1)}×`;
      speaker.setRate(Number(rate.value));
    });
    rate.addEventListener("change", () => void patch({ readAloudRate: Number(rate.value) }));
    const voices = speaker.voices();
    const voice = el("select", {},
      el("option", { value: "", label: "既定の声" }),
      voices.map((name) => el("option", { value: name, text: name, selected: name === settings.readAloudVoice })));
    voice.addEventListener("change", () => {
      speaker.setVoice(voice.value);
      void patch({ readAloudVoice: voice.value });
    });
    into.append(
      el("label", { class: "field" }, el("span", { label: "速さ" }), rate, rateValue),
      el("label", { class: "field" }, el("span", { label: "声" }), voice),
    );
  }

  // The backup is the user's own data as the files a development kakeratta
  // reads. Where it comes from depends on the deployment: a hosted workspace
  // writes it to storage and answers with a link over the event stream, a
  // laptop streams it down the response. The page treats both the same - it
  // waits for a link, then follows it.
  function startExport() {
    exporting = true;
    exportLink = null;
    drawExportRow();
    api.startExport().then((started) => {
      if (started?.state === "ready" && started.url) finishExport(started);
      else watchExport();
    }).catch((error) => {
      exporting = false;
      drawExportRow();
      toast(String(error.message || error), "error");
    });
  }

  // A big workspace takes minutes to write out, so the answer comes later and
  // over the same channel a reaction would.
  function watchExport() {
    stopExportWatch();
    stopExportWatch = api.on((name, data) => {
      if (name !== "export") return;
      if (data?.state === "ready" && data.url) return finishExport(data);
      if (data?.state !== "failed") return;
      stopWatching();
      exporting = false;
      drawExportRow();
      toast(t("書き出しできませんでした"), "error");
    });
  }

  function stopWatching() {
    stopExportWatch();
    stopExportWatch = () => {};
  }

  function finishExport(ready) {
    stopWatching();
    exporting = false;
    exportLink = { url: ready.url, name: ready.name || "" };
    drawExportRow();
    // The download starts on its own and stays on the page; the link remains
    // for a browser that would rather be clicked.
    try {
      globalThis.location.assign(exportLink.url);
    } catch {
      /* the link is there to follow */
    }
  }

  function drawExportRow() {
    if (!exportRow) return;
    clear(exportRow);
    const start = el("button", {
      class: "chip",
      label: exporting ? "書き出し中…" : "zipに書き出す",
      onclick: () => startExport(),
    });
    if (exporting) start.setAttribute("disabled", "");
    exportRow.append(start);
    if (exportLink) {
      exportRow.append(el("a", {
        class: "chip", href: exportLink.url, download: exportLink.name, label: "ダウンロード",
      }));
    }
  }

  function drawBackup(into) {
    exportRow = el("div", { class: "compose-row" });
    into.append(
      el("p", {
        class: "hint",
        label: "投稿・担当・メモ・画像を、開発環境でそのまま動く構成のままzipに書き出します。APIキーやBlueskyなどの接続は入らないので、復元先では入れ直してください。",
      }),
      exportRow,
    );
    drawExportRow();
  }

  // Leaving is the one thing here that takes away rather than adds, and the
  // whole of it at once: everything the account holds, and the account. A
  // button that erases years of notes should not be a slip of the thumb from
  // the backup button above it, so the word is asked for, and the backup is
  // pointed at first.
  let closingRow = null;
  let closing = false;
  function drawClosingRow() {
    if (!closingRow) return;
    clear(closingRow);
    const button = el("button", {
      class: "chip danger",
      label: closing ? "削除しています…" : "すべて削除して利用を停止する",
      onclick: () => void closeAccountNow(),
    });
    if (closing) button.setAttribute("disabled", "");
    closingRow.append(button);
  }

  async function closeAccountNow() {
    if (closing) return;
    const word = t("削除");
    const typed = globalThis.prompt?.(t("投稿・担当・メモ・画像・設定・接続をすべて削除して、利用を停止します。元に戻せません。続けるには「{0}」と入力してください。", word));
    if ((typed ?? "").trim().toLowerCase() !== word.toLowerCase()) return;
    closing = true;
    drawClosingRow();
    try {
      await api.closeAccount();
      await closeAccount();
    } catch (error) {
      closing = false;
      drawClosingRow();
      toast(String(error?.message || t("削除できませんでした。もう一度お試しください")), "error");
    }
  }

  function drawClosing(into) {
    closingRow = el("div", { class: "compose-row" });
    into.append(
      el("p", { class: "hint", label: "このアカウントのデータをすべて消して、利用を停止します。必要なものは先に上のバックアップで書き出してください。" }),
      closingRow,
    );
    drawClosingRow();
  }

  // Show the deployed version alongside device preferences.
  function drawDevice() {
    view.append(
      el("h3", { label: "この端末" }),
      select("表示言語", getLanguage(), [["en", "English"], ["ja", "日本語"]], (next) => {
        setLanguage(next, { persist: true });
        location.reload();
      }),
      select("文字サイズ", getTextSize(), TEXT_SIZES.map(([id, label]) => [id, label]), setTextSize),
    );
    if (!about) return;
    view.append(el("p", { class: "hint", text: `kakeratta ${about.version}` }));
  }

  // Everything below the line is something most people never open: another
  // provider for the models, how dictation and reading aloud behave, an
  // external agent, and posting to Bluesky. The defaults are the answer for
  // all of them, so the section stays shut and is built the first time it is
  // opened - which is also when the external agent and Bluesky are asked about.
  function drawAdvanced() {
    const body = el("div", { class: "advanced-body" });
    const section = el("details", { class: "settings-advanced" },
      el("summary", { label: "上級オプション" }), body);
    let built = false;
    let routerSection = null;
    let modelCategory = null;
    const category = (label) => {
      const content = el("div", { class: "advanced-category-body" });
      const group = el("details", { class: "advanced-category", "data-category": label },
        el("summary", { label }), content);
      body.append(group);
      return { group, content };
    };
    const build = () => {
      if (!section.open || built) return;
      built = true;
      routerSection = openRouterSection();
      modelCategory = category("モデル");
      modelCategory.content.append(routerSection);
      drawInput(category("入力").content);
      const voice = category("音声");
      drawSpeech(voice.content);
      drawReadAloud(voice.content);
      const integrations = category("外部連携");
      // The switch only matters once an agent is connected; until then it
      // stays out of sight, like the planner choice on the persona cards.
      const controls = el("div", { class: "settings-block external-agent", hidden: true });
      // The connection says whether it is let in, so it is asked again.
      drawExternalAgent(controls, () => void mcp.load());
      const mcp = createMCPConnection({ controls, onChange: onMCPChange, onState: (connection) => {
        controls.hidden = !connection?.configured;
      } });
      integrations.content.append(mcp.element);
      void mcp.load();
      bluesky?.dispose();
      bluesky = createBlueskyConnection();
      integrations.content.append(el("h3", { label: "Bluesky連携" }), bluesky.element);
      drawBackup(category("バックアップ").content);
      if (closeAccount) drawClosing(category("利用停止").content);
    };
    section.addEventListener("toggle", build);
    revealOpenRouter = () => {
      section.open = true;
      build();
      modelCategory.group.open = true;
      routerSection.scrollIntoView({ block: "start", behavior: "smooth" });
      routerSection.querySelector("input").focus({ preventScroll: true });
    };
    view.append(section, el("p", { class: "hint", label: "入力の書式、モデルの切り替え、音声入力・読み上げ、外部エージェント・Bluesky連携、バックアップができます。" }));
  }

  // The screen leads with the two keys that decide whether anything works at
  // all, then what you came to do with your own data. The rest is behind the
  // line.
  function draw() {
    if (!settings) return;
    stopMicCheck();
    bluesky?.dispose();
    bluesky = null;
    clear(view);
    drawAI();
    drawTools();
    drawDevice();
    drawAdvanced();
  }

  return { element: view, load, draw, settings: () => settings, stopMicCheck: () => stopMicCheck() };
}
