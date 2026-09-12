// Where the models run, what the tools can reach, and how dictation is set up.
//
// None of the keys here ever reach the page: they are stored by the relay and
// come back as "set or not". A page that does not have a key cannot leak one.
import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { ENDPOINT_TYPES, endpointPreset, isGoogleEndpoint } from "./speech.js";
import { speechLanguageOptions } from "./speech_languages.js";

const providerLabels = {
  nebius: "Nebius Token Factory",
  "openai-compatible": "OpenAI 互換",
  local: "ローカルLLM",
};

const endpointLabels = {
  openai: "OpenAI",
  "whisper-cpp": "whisper.cpp",
  custom: "OpenAI 互換（自前）",
  "gemini-transcribe": "Gemini API",
  "vertex-transcribe": "Vertex AI",
  "azure-mai-transcribe": "Azure",
};

export function createSettings({ speaker, onSettings }) {
  const view = el("div", { class: "panel-body" });
  let settings = null;
  let about = null;
  let models = [];

  async function load() {
    settings = await api.settings();
    about = about || (await api.about().catch(() => null));
    onSettings?.(settings);
    draw();
  }

  async function patch(change) {
    settings = { ...settings, ...change };
    onSettings?.(settings);
    try {
      settings = await api.saveSettings(settings);
      onSettings?.(settings);
    } catch (error) {
      toast(String(error.message || error), "error");
    }
  }

  const patchSpeech = (change) => patch({ speech: { ...settings.speech, ...change } });

  function field(label, value, onChange, { type = "text", hint = "", list = null } = {}) {
    const input = el("input", { type, value: value ?? "" });
    if (list) input.setAttribute("list", list);
    input.addEventListener("change", () => void onChange(input.value.trim()));
    return el("label", { class: "field" },
      el("span", { text: label }), input, hint ? el("span", { class: "hint", text: hint }) : null);
  }

  function select(label, value, options, onChange, hint = "") {
    const node = el("select", {}, options.map(([id, text]) => el("option", { value: id, text, selected: id === value })));
    node.addEventListener("change", () => void onChange(node.value));
    return el("label", { class: "field" },
      el("span", { text: label }), node, hint ? el("span", { class: "hint", text: hint }) : null);
  }

  function toggle(label, value, onChange, hint = "") {
    return el("label", { class: "field row-field" },
      el("input", { type: "checkbox", checked: value, onchange: (event) => void onChange(event.target.checked) }),
      el("span", { text: label }), hint ? el("span", { class: "hint", text: hint }) : null);
  }

  function drawModels() {
    const datalist = el("datalist", { id: "model-list" }, models.map((id) => el("option", { value: id })));
    const refresh = el("button", {
      class: "chip",
      text: "モデル一覧を取得",
      onclick: async () => {
        try {
          models = (await api.models()).models || [];
          toast(models.length ? `${models.length}件見つかりました` : "取得できませんでした");
          draw();
        } catch (error) {
          toast(String(error.message || error), "error");
        }
      },
    });
    return [datalist, el("div", { class: "compose-row" }, refresh)];
  }

  function drawAI() {
    const provider = settings.provider || "nebius";
    const frameworks = about?.frameworks ?? {};
    view.append(
      el("h3", { text: "モデル" }),
      el("p", {
        class: "hint",
        text: "サイズの使い分けはコストの話ではありません。浅い反応と深い反応が混ざることそのものが体験です。",
      }),
      select("実行場所", provider, Object.entries(providerLabels), async (next) => {
        const entry = (about?.providers ?? []).find((item) => item.id === next);
        await patch({ provider: next, baseUrl: entry?.endpoint ?? settings.baseUrl });
        draw();
      }),
    );
    if (provider === "local") {
      view.append(
        select("サーバー", settings.localFramework || "ollama",
          Object.entries(frameworks).map(([id, entry]) => [id, entry.label]),
          async (next) => {
            await patch({ localFramework: next, baseUrl: frameworks[next]?.endpoint ?? settings.baseUrl });
            draw();
          }),
      );
    }
    view.append(
      field("エンドポイント", settings.baseUrl, (value) => patch({ baseUrl: value })),
      field("APIキー", settings.apiKey, (value) => patch({ apiKey: value }), {
        type: "password",
        hint: "サーバー側にだけ保存され、指定した接続先以外へは送りません",
      }),
      ...drawModels(),
      field("軽いモデル（浅い反応・振り分け）", settings.lightModel, (value) => patch({ lightModel: value }), { list: "model-list" }),
      field("重いモデル（深い反応）", settings.heavyModel, (value) => patch({ heavyModel: value }), { list: "model-list" }),
      field("画像を読むモデル（vision）", settings.visionModel, (value) => patch({ visionModel: value }), {
        list: "model-list",
        hint: "投稿ごとに一度だけ通し、結果は全員で共有します",
      }),
      field("振り分けに使うモデル", settings.routeModel, (value) => patch({ routeModel: value }), { list: "model-list" }),
    );
  }

  function drawTools() {
    const search = settings.search ?? {};
    const providers = about?.search ?? {};
    view.append(
      el("h3", { text: "道具" }),
      el("p", { class: "hint", text: "宣言した担当だけが呼べます。鍵はここにあり、スキルは持ちません。" }),
      select("Web検索", search.provider || "",
        [["", "使わない"], ...Object.entries(providers).map(([id, entry]) => [id, entry.label])],
        async (next) => {
          await patch({ search: { ...search, provider: next, baseUrl: providers[next]?.baseUrl ?? "" } });
          draw();
        }),
    );
    if (search.provider) {
      view.append(
        field("エンドポイント", search.baseUrl, (value) => patch({ search: { ...search, baseUrl: value } })),
        field("APIキー", search.apiKey, (value) => patch({ search: { ...search, apiKey: value } }), { type: "password" }),
      );
    }
    const places = settings.places ?? {};
    view.append(
      el("details", { class: "install" },
        el("summary", { text: "店舗検索・経路検索の接続先" }),
        el("p", { class: "hint", text: "既定は鍵の要らない公開サービスです。自前のインスタンスに向けられます。" }),
        field("地名検索 (Nominatim)", places.nominatimUrl, (value) => patch({ places: { ...places, nominatimUrl: value } })),
        field("周辺検索 (Overpass)", places.overpassUrl, (value) => patch({ places: { ...places, overpassUrl: value } })),
        field("経路 (OSRM)", places.routeUrl, (value) => patch({ places: { ...places, routeUrl: value } }), {
          hint: "道路の所要時間です。公共交通は含みません",
        })),
    );
  }

  function drawPrivacy() {
    view.append(
      el("h3", { text: "位置" }),
      toggle("投稿に位置を残す", settings.keepPlace !== false, (value) => patch({ keepPlace: value }),
        "反応のために今いる場所が要ることと、過去の投稿に位置を残すことは別です。切ると、反応には使われますが記録には残りません。"),
    );
  }

  function drawSpeech() {
    const speech = settings.speech ?? {};
    view.append(
      el("h3", { text: "音声入力" }),
      select("方式", speech.provider || "browser",
        [["browser", "ブラウザ音声認識"], ["openai-compatible", "音声認識API（録音して送る）"], ["live", "ライブ文字起こし"]],
        async (next) => {
          await patchSpeech({ provider: next });
          draw();
        }),
    );
    if (speech.provider !== "browser") {
      const endpoints = ENDPOINT_TYPES.filter(
        (id) => (speech.provider !== "live" || isGoogleEndpoint(id) || id === "openai") && id !== "vertex-transcribe",
      );
      view.append(
        select("サービス", speech.endpointType, endpoints.map((id) => [id, endpointLabels[id] ?? id]), async (next) => {
          const preset = endpointPreset(next);
          await patchSpeech({ endpointType: next, baseUrl: preset.baseUrl, model: preset.model, apiKey: "" });
          draw();
        }, "Vertex AI の OAuth はデスクトップ前提のため、この形態では選べません"),
      );
      if (!isGoogleEndpoint(speech.endpointType)) {
        view.append(
          field("Base URL", speech.baseUrl, (value) => patchSpeech({ baseUrl: value })),
          field("モデル", speech.model, (value) => patchSpeech({ model: value })),
        );
      }
      if (speech.endpointType !== "whisper-cpp") {
        view.append(field("APIキー", speech.apiKey, (value) => patchSpeech({ apiKey: value }), { type: "password" }));
      }
    }
    const languages = speechLanguageOptions(speech.provider, speech.endpointType, speech.model, speech.language || "auto");
    view.append(
      select("言語", speech.language || "auto", languages.map((item) => [item.value, item.label]),
        (next) => patchSpeech({ language: next })),
      select("無音で自動停止", String(speech.silenceSeconds ?? 3),
        [["0", "しない"], ...Array.from({ length: 10 }, (_, i) => [String(i + 1), `${i + 1}秒`])],
        (next) => patchSpeech({ silenceSeconds: Number(next) })),
      field("送信の合図", speech.sendPhrase, (value) => patchSpeech({ sendPhrase: value }), {
        hint: "話し終わりの合図。これで投稿されます。空欄で無効",
      }),
    );
  }

  function drawReadAloud() {
    view.append(el("h3", { text: "読み上げ" }));
    const rate = el("input", {
      type: "range", min: "0.5", max: "2.5", step: "0.1", value: String(settings.readAloudRate || 1),
    });
    const rateValue = el("span", { class: "hint", text: `${Number(settings.readAloudRate || 1).toFixed(1)}×` });
    rate.addEventListener("input", () => {
      rateValue.textContent = `${Number(rate.value).toFixed(1)}×`;
      speaker.setRate(Number(rate.value));
    });
    rate.addEventListener("change", () => void patch({ readAloudRate: Number(rate.value) }));
    const voices = speaker.voices();
    const voice = el("select", {},
      el("option", { value: "", text: "既定の声" }),
      voices.map((name) => el("option", { value: name, text: name, selected: name === settings.readAloudVoice })));
    voice.addEventListener("change", () => {
      speaker.setVoice(voice.value);
      void patch({ readAloudVoice: voice.value });
    });
    view.append(
      el("label", { class: "field" }, el("span", { text: "速さ" }), rate, rateValue),
      el("label", { class: "field" }, el("span", { text: "声" }), voice),
    );
  }

  function drawAbout() {
    if (!about) return;
    view.append(
      el("h3", { text: "この端末" }),
      el("p", { class: "hint", text: `dokking ${about.version}` }),
      el("p", { class: "hint", text: `保存先 ${about.workspace}` }),
      about.needsToken ? null : el("p", {
        class: "hint warn",
        text: "トークンが未設定です。外から届く場所に置くときは DOKKING_TOKEN を設定してください。",
      }),
    );
  }

  function draw() {
    if (!settings) return;
    clear(view);
    drawAI();
    drawTools();
    drawPrivacy();
    drawSpeech();
    drawReadAloud();
    drawAbout();
  }

  return { element: view, load, draw, settings: () => settings };
}
