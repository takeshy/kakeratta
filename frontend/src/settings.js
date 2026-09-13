// Where the models run, what the tools can reach, and how dictation is set up.
//
// None of the keys here ever reach the page: they are stored by the relay and
// come back as "set or not". A page that does not have a key cannot leak one.
import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { ENDPOINT_TYPES, endpointPreset, isGoogleEndpoint } from "./speech.js";
import { speechLanguageOptions } from "./speech_languages.js";
import { getLanguage, setLanguage, t } from "./i18n.js";

const providerLabels = {
  nebius: "Nebius Token Factory",
  "openai-compatible": "OpenAI 互換",
};

const endpointLabels = {
  openai: "OpenAI",
  custom: "OpenAI 互換（自前）",
  "gemini-transcribe": "Gemini API",
  "vertex-transcribe": "Vertex AI",
  "azure-mai-transcribe": "Azure",
};

export function aiConfigured(settings) {
  if (!settings) return false;
  return Boolean(settings.baseUrl?.trim() && settings.lowModel?.trim() && settings.apiKey?.trim());
}

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

  function field(label, value, onChange, { type = "text", hint = "", list = null } = {}) {
    const input = el("input", { type, value: value ?? "" });
    if (list) input.setAttribute("list", list);
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

  function connectionCheck(saveKey) {
    const datalist = el("datalist", { id: "model-list" }, models.map((id) => el("option", { value: id })));
    const check = el("button", {
      class: "chip",
      label: "接続確認",
      onclick: async () => {
        check.disabled = true;
        check.textContent = t("接続中…");
        try {
          if (!(await saveKey())) return;
          models = (await api.models()).models || [];
          toast(t("接続できました"));
        } catch (error) {
          toast(String(error.message || error), "error");
        } finally {
          check.disabled = false;
          check.textContent = t("接続確認");
        }
      },
    });
    return [datalist, el("div", { class: "compose-row" }, check)];
  }

  function drawAI() {
    const provider = settings.provider || "nebius";
    const apiKey = field("APIキー", settings.apiKey, (value) => patch({ apiKey: value }), {
      type: "password",
      hint: "サーバー側にだけ保存され、指定した接続先以外へは送りません",
    });
    const advanced = el("details", { class: "install" },
      el("summary", { label: "モデルの上級者向け設定" }),
      el("p", {
        class: "hint",
        label: "担当ごとに low / middle / high を選びます。low が先に動き、その発言を見て上の段が突っ込めます。" +
          "どのモデルをどの段に置くかが、そのまま体験の差になります。",
      }),
      select("実行場所", provider, Object.entries(providerLabels), async (next) => {
        const entry = (about?.providers ?? []).find((item) => item.id === next);
        await patch({ provider: next, baseUrl: entry?.endpoint ?? settings.baseUrl });
        draw();
      }),
      field("エンドポイント", settings.baseUrl, (value) => patch({ baseUrl: value })),
      field("low（先に動く。浅く速い）", settings.lowModel, (value) => patch({ lowModel: value }), { list: "model-list" }),
      field("middle", settings.middleModel, (value) => patch({ middleModel: value }), { list: "model-list" }),
      field("high（最後に動く。下の発言を見て突っ込める）", settings.highModel, (value) => patch({ highModel: value }), {
        list: "model-list",
        hint: "空にすると1つ下の段に落ちます。全部同じにしてもいいですが、対比は無くなります",
      }),
      field("画像を読むモデル（vision）", settings.visionModel, (value) => patch({ visionModel: value }), {
        list: "model-list",
        hint: "画像対応モデルを選んでください。Nebiusでは空欄にすると画像用の標準モデルを使います。",
      }),
      field("振り分けに使うモデル", settings.routeModel, (value) => patch({ routeModel: value }), { list: "model-list" }),
    );
    view.append(el("h3", { label: "モデル" }));
    if (provider === "nebius") view.append(el("p", { class: "hint" },
      t("AIにはNebius Token Factoryを使用します。"), " ",
      el("a", {
        href: "https://tokenfactory.nebius.com",
        target: "_blank",
        rel: "noopener noreferrer",
        text: t("APIキーを取得"),
      }),
    ));
    if (!aiConfigured(settings)) view.append(el("p", {
      class: "hint warn",
      label: "最初にAI接続を設定してください。設定が完了するまでこの画面は閉じられません。",
    }));
    view.append(
      apiKey,
      ...connectionCheck(() => patch({ apiKey: apiKey.querySelector("input").value.trim() })),
      advanced,
    );
  }

  function drawTools() {
    const search = settings.search ?? {};
    const providers = about?.search ?? {};
    const provider = search.provider ?? "tavily";
    const providerSelect = select("種別", provider,
      [["", "なし"], ...Object.entries(providers).map(([id, entry]) => [id, entry.label])],
      async (next) => {
        await patch({ search: { ...search, provider: next, baseUrl: providers[next]?.baseUrl ?? "", apiKey: "" } });
        draw();
      });
    if (!provider) {
      view.append(el("h3", { label: "Web検索" }), providerSelect);
    } else {
      const apiKey = field(provider === "brave" ? "Brave Search APIキー" : "Tavily APIキー", search.apiKey,
      (value) => patch({ search: { ...search, provider, baseUrl: search.baseUrl || providers[provider]?.baseUrl || "", apiKey: value } }), {
        type: "password",
        hint: "Web検索に使います。AIモデルのAPIキーとは別です",
      });
      const advanced = el("details", { class: "install" },
      el("summary", { label: "Web検索の上級者向け設定" }),
      field("エンドポイント", search.baseUrl || providers[provider]?.baseUrl,
        (value) => patch({ search: { ...search, provider, baseUrl: value } })),
    );
      const check = el("button", {
      class: "chip",
      label: "Web検索の接続確認",
      onclick: async () => {
        check.disabled = true;
        check.textContent = t("接続中…");
        try {
          const saved = await patch({ search: {
            ...search,
            provider,
            baseUrl: search.baseUrl || providers[provider]?.baseUrl || "",
            apiKey: apiKey.querySelector("input").value.trim(),
          } });
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
        providerSelect,
        apiKey,
        el("div", { class: "compose-row" }, check),
        advanced,
      );
    }
    const places = settings.places ?? {};
    view.append(
      el("details", { class: "install" },
        el("summary", { label: "店舗検索・経路検索の接続先" }),
        el("p", { class: "hint", label: "既定は鍵の要らない公開サービスです。自前のインスタンスに向けられます。" }),
        field("地名検索 (Nominatim)", places.nominatimUrl, (value) => patch({ places: { ...places, nominatimUrl: value } })),
        field("周辺検索 (Overpass)", places.overpassUrl, (value) => patch({ places: { ...places, overpassUrl: value } })),
        field("経路 (OSRM)", places.routeUrl, (value) => patch({ places: { ...places, routeUrl: value } }), {
          hint: "道路の所要時間です。公共交通は含みません",
        })),
    );
  }

  function drawSpeech() {
    const speech = settings.speech ?? {};
    const mode = speech.provider || "browser";
    const modeLabels = {
      browser: "ブラウザ音声認識",
      "openai-compatible": "音声認識API（録音して送る）",
      live: "ライブ文字起こし",
    };
    const advanced = el("details", { class: "install" },
      el("summary", { label: "音声入力の上級者向け設定" }),
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
      advanced.append(
        select("サービス", speech.endpointType, endpoints.map((id) => [id, endpointLabels[id] ?? id]), async (next) => {
          const preset = endpointPreset(next);
          await patchSpeech({ endpointType: next, baseUrl: preset.baseUrl, model: preset.model, apiKey: "" });
          draw();
        }, "Vertex AI の OAuth はデスクトップ前提のため、この形態では選べません"),
      );
      if (!isGoogleEndpoint(speech.endpointType)) {
        advanced.append(
          field("Base URL", speech.baseUrl, (value) => patchSpeech({ baseUrl: value })),
          field("モデル", speech.model, (value) => patchSpeech({ model: value })),
        );
      }
      advanced.append(field("APIキー", speech.apiKey, (value) => patchSpeech({ apiKey: value }), { type: "password" }));
    }
    const languages = speechLanguageOptions(speech.provider, speech.endpointType, speech.model, speech.language || "auto");
    advanced.append(
      select("言語", speech.language || "auto", languages.map((item) => [item.value, item.label]),
        (next) => patchSpeech({ language: next })),
      select("無音で自動停止", String(speech.silenceSeconds ?? 3),
        [["0", "しない"], ...Array.from({ length: 10 }, (_, i) => [String(i + 1), t("{0}秒", i + 1)])],
        (next) => patchSpeech({ silenceSeconds: Number(next) })),
      field("送信の合図", speech.sendPhrase, (value) => patchSpeech({ sendPhrase: value }), {
        hint: "話し終わりの合図。これで投稿されます。空欄で無効",
      }),
    );
    view.append(
      el("h3", { label: "音声入力" }),
      el("p", { class: "hint", label: modeLabels[mode] ?? mode }),
      advanced,
    );
  }

  function drawReadAloud() {
    view.append(el("h3", { label: "読み上げ" }));
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
    view.append(
      el("label", { class: "field" }, el("span", { label: "速さ" }), rate, rateValue),
      el("label", { class: "field" }, el("span", { label: "声" }), voice),
    );
  }

  function drawAbout() {
    if (!about) return;
    view.append(
      el("h3", { label: "この端末" }),
      el("p", { class: "hint", text: `kakeratta ${about.version}` }),
      el("p", { class: "hint", text: t("保存先 {0}", about.workspace) }),
    );
    if (!about.needsToken) {
      view.append(el("p", {
        class: "hint warn",
        label: "トークンが未設定です。外から届く場所に置くときは KAKERATTA_TOKEN を設定してください。",
      }));
    }
  }

  function drawLanguage() {
    view.append(
      el("h3", { label: "表示言語" }),
      select("言語", getLanguage(), [["en", "English"], ["ja", "日本語"]], (next) => {
        setLanguage(next, { persist: true });
        location.reload();
      }),
    );
  }

  function draw() {
    if (!settings) return;
    clear(view);
    drawLanguage();
    drawAI();
    drawTools();
    drawSpeech();
    drawReadAloud();
    drawAbout();
  }

  return { element: view, load, draw, settings: () => settings };
}
