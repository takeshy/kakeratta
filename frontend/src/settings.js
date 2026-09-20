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

export function aiConfigured(settings) {
  if (!settings) return false;
  return Boolean(settings.baseUrl?.trim() && settings.lowModel?.trim() && settings.apiKey?.trim());
}

export function createSettings({ speaker, onSettings }) {
  const view = el("div", { class: "panel-body" });
  let settings = null;
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
    if (!aiConfigured(settings)) view.append(el("p", {
      class: "hint warn",
      label: "最初にAI接続を設定してください。設定が完了するまでこの画面は閉じられません。",
    }));
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
    jevEnabled.disabled = !openRouterVerified;
    openRouterMode.disabled = !openRouterVerified;
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
    return el("div", { class: "settings-block" },
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

  function drawBackup() {
    exportRow = el("div", { class: "compose-row" });
    view.append(
      el("h3", { label: "バックアップ" }),
      el("p", {
        class: "hint",
        label: "投稿・担当・メモ・設定・画像を、開発環境でそのまま動く構成のままzipに書き出します。設定にはAPIキーが含まれるため、書き出したzipは他人に渡さないでください。",
      }),
      exportRow,
    );
    drawExportRow();
  }

  // One line about the copy of kakeratta in front of you. The workspace path
  // comes back only from a server that keeps its files where you can open
  // them; a hosted one has none to name.
  function drawDevice() {
    view.append(
      el("h3", { label: "この端末" }),
      select("表示言語", getLanguage(), [["en", "English"], ["ja", "日本語"]], (next) => {
        setLanguage(next, { persist: true });
        location.reload();
      }),
    );
    if (!about) return;
    view.append(el("p", { class: "hint", text: `kakeratta ${about.version}` }));
    if (about.workspace) view.append(el("p", { class: "hint", text: t("保存先 {0}", about.workspace) }));
  }

  // Everything below the line is something most people never open: another
  // provider for the models, how dictation and reading aloud behave, an
  // external agent, and posting to Bluesky. The defaults are the answer for
  // all of them, so the section stays shut and is built the first time it is
  // opened - which is also when Hermes and Bluesky are asked about.
  function drawAdvanced() {
    const body = el("div", { class: "advanced-body" });
    const section = el("details", { class: "settings-advanced" },
      el("summary", { label: "上級オプション" }), body);
    let built = false;
    section.addEventListener("toggle", () => {
      if (!section.open || built) return;
      built = true;
      body.append(openRouterSection());
      drawSpeech(body);
      drawReadAloud(body);
      const mcp = createMCPConnection();
      body.append(mcp.element);
      void mcp.load();
      bluesky?.dispose();
      bluesky = createBlueskyConnection();
      body.append(el("h3", { label: "Bluesky連携" }), bluesky.element);
    });
    view.append(section);
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
    drawBackup();
    drawDevice();
    drawAdvanced();
  }

  return { element: view, load, draw, settings: () => settings, stopMicCheck: () => stopMicCheck() };
}
