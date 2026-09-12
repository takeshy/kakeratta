// Where the models run, and how dictation is set up.
//
// Reading, listening and memos work with none of this filled in; only the AI
// features wait for it. The default is Nebius Token Factory with NVIDIA's
// Nemotron, which is what this app is built to run on.
import { clear, el, toast } from "./dom.js";
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

export function createSettingsPanel({ api, narrator, onSettings = () => {} }) {
  const body = el("div");
  let settings = null;
  let catalogue = null;
  let models = [];
  let opened = null;

  async function load() {
    settings = await api.settings();
    catalogue = catalogue || (await api.providers().catch(() => null));
    onSettings(settings);
    draw(opened);
  }

  async function patch(change) {
    settings = { ...settings, ...change };
    onSettings(settings);
    try {
      await api.saveSettings(settings);
    } catch (error) {
      toast(String(error), "error");
    }
  }

  async function patchSpeech(change) {
    await patch({ speech: { ...settings.speech, ...change } });
  }

  function field(label, value, onChange, { type = "text", hint = "", list = null } = {}) {
    const input = el("input", { type, value: value ?? "" });
    if (list) input.setAttribute("list", list);
    input.addEventListener("change", () => void onChange(input.value.trim()));
    return el(
      "label",
      { class: "field" },
      el("span", { text: label }),
      input,
      hint ? el("span", { class: "hint", text: hint }) : null,
    );
  }

  function select(label, value, options, onChange, hint = "") {
    const node = el(
      "select",
      {},
      options.map(([id, text]) => el("option", { value: id, text, selected: id === value })),
    );
    node.addEventListener("change", () => void onChange(node.value));
    return el(
      "label",
      { class: "field" },
      el("span", { text: label }),
      node,
      hint ? el("span", { class: "hint", text: hint }) : null,
    );
  }

  function drawModels() {
    const datalist = el("datalist", { id: "model-list" }, models.map((id) => el("option", { value: id })));
    const refresh = el("button", {
      class: "button",
      text: "モデル一覧を取得",
      onclick: async () => {
        try {
          models = (await api.models()) || [];
          toast(models.length ? `${models.length}件のモデルが見つかりました` : "モデル一覧を取得できませんでした");
          draw(opened);
        } catch (error) {
          toast(String(error), "error");
        }
      },
    });
    return [datalist, el("div", { class: "row" }, refresh)];
  }

  function drawAI() {
    const provider = settings.provider || "nebius";
    const frameworks = catalogue?.frameworks ?? {};
    body.append(
      el("h3", { text: "AIモデル" }),
      select(
        "実行場所",
        provider,
        Object.entries(providerLabels),
        async (next) => {
          const entry = (catalogue?.providers ?? []).find((item) => item.id === next);
          await patch({
            provider: next,
            baseUrl: entry?.endpoint ?? settings.baseUrl,
            ...(next === "nebius"
              ? { chatModel: catalogue?.nemotron ?? settings.chatModel, heavyModel: catalogue?.nemotron ?? settings.heavyModel }
              : {}),
          });
          draw(opened);
        },
        provider === "nebius"
          ? "NVIDIA Nemotron を Nebius Token Factory で実行します（既定）"
          : provider === "local"
          ? "手元のサーバーで実行します。鍵も本文も端末から出ません"
          : "OpenAI 互換のエンドポイントなら何でも使えます",
      ),
    );
    if (provider === "local") {
      body.append(
        select(
          "サーバー",
          settings.localFramework || "ollama",
          Object.entries(frameworks).map(([id, entry]) => [id, entry.label]),
          async (next) => {
            await patch({ localFramework: next, baseUrl: frameworks[next]?.endpoint ?? settings.baseUrl });
            draw(opened);
          },
          settings.localFramework === "opencode"
            ? "OpenCode はセッションAPIで話します。モデルは <providerID>/<modelID> の形式です"
            : "",
        ),
      );
    }
    body.append(
      field("エンドポイント", settings.baseUrl, (value) => patch({ baseUrl: value })),
      field("APIキー", settings.apiKey, (value) => patch({ apiKey: value }), {
        type: "password",
        hint: "この端末にだけ保存し、指定した接続先以外へは送りません",
      }),
      ...drawModels(),
      field("回答に使うモデル", settings.chatModel, (value) => patch({ chatModel: value }), { list: "model-list" }),
      field("ページ要約・章要約・メモ整形（軽いモデル）", settings.lightModel, (value) => patch({ lightModel: value }), { list: "model-list" }),
      field("読了後の総括（重いモデル）", settings.heavyModel, (value) => patch({ heavyModel: value }), { list: "model-list" }),
      el(
        "label",
        { class: "field" },
        el("span", { text: "技術書はPDFのまま渡す" }),
        el("input", {
          type: "checkbox",
          checked: settings.sendBookAsFile !== false,
          onchange: (event) => void patch({ sendBookAsFile: event.target.checked }),
        }),
        el("span", { class: "hint", text: "コード・表・図が保たれます。受け付けない接続先なら自動でテキストに落とします" }),
      ),
    );
  }

  // Web search is the one way out to the open web, and the app makes the call.
  // A persona declares that it needs it; it never holds the key itself.
  function drawSearch() {
    const search = settings.search ?? {};
    const providers = catalogue?.search ?? {};
    body.append(
      el("h3", { text: "Web検索（技術書）" }),
      select(
        "検索サービス",
        search.provider || "",
        [["", "使わない"], ...Object.entries(providers).map(([id, entry]) => [id, entry.label])],
        async (next) => {
          await patch({ search: { ...search, provider: next, baseUrl: providers[next]?.baseUrl ?? "" } });
          draw(opened);
        },
        "本が書かれた後に変わったことを調べるのに使います。小説では使いません",
      ),
    );
    if (!search.provider) return;
    body.append(
      field("エンドポイント", search.baseUrl, (value) => patch({ search: { ...search, baseUrl: value } })),
      field("APIキー", search.apiKey, (value) => patch({ search: { ...search, apiKey: value } }), {
        type: "password",
        hint: providers[search.provider]?.needKey ? "この接続先には鍵が要ります" : "自前のSearXNGなら不要です",
      }),
      el("p", { class: "hint", text: "検索結果からのみ固有名詞とバージョンを出し、出典のリンクを必ず添えます。" }),
    );
  }

  function drawSpeech() {
    const speech = settings.speech ?? {};
    body.append(
      el("h3", { text: "音声入力" }),
      select(
        "方式",
        speech.provider || "browser",
        [
          ["browser", "ブラウザ音声認識"],
          ["openai-compatible", "音声認識API（録音して送る）"],
          ["live", "ライブ文字起こし"],
        ],
        async (next) => {
          await patchSpeech({ provider: next });
          draw(opened);
        },
        speech.provider === "live"
          ? "話している途中から文字になります（OpenAI / Gemini API / Vertex AI）"
          : speech.provider === "browser"
          ? "この環境のWebViewが対応している場合のみ使えます"
          : "停止後にまとめて文字にします。whisper.cpp などローカルも使えます",
      ),
    );
    if (speech.provider !== "browser") {
      const endpoints = ENDPOINT_TYPES.filter((id) => speech.provider !== "live" || isGoogleEndpoint(id) || id === "openai");
      body.append(
        select("サービス", speech.endpointType, endpoints.map((id) => [id, endpointLabels[id] ?? id]), async (next) => {
          const preset = endpointPreset(next);
          await patchSpeech({
            endpointType: next,
            baseUrl: preset.baseUrl,
            model: preset.model,
            apiKey: "",
          });
          draw(opened);
        }),
      );
      if (!isGoogleEndpoint(speech.endpointType)) {
        body.append(field("Base URL", speech.baseUrl, (value) => patchSpeech({ baseUrl: value })));
        body.append(field("モデル", speech.model, (value) => patchSpeech({ model: value })));
      }
      if (speech.endpointType !== "vertex-transcribe" && speech.endpointType !== "whisper-cpp") {
        body.append(field("APIキー", speech.apiKey, (value) => patchSpeech({ apiKey: value }), { type: "password" }));
      }
      if (speech.endpointType === "vertex-transcribe") {
        body.append(drawVertex(speech));
      }
    }
    const languages = speechLanguageOptions(speech.provider, speech.endpointType, speech.model, speech.language || "auto");
    body.append(
      select("言語", speech.language || "auto", languages.map((item) => [item.value, item.label]), (next) =>
        patchSpeech({ language: next })),
      select(
        "無音で自動停止",
        String(speech.silenceSeconds ?? 3),
        [["0", "しない"], ...Array.from({ length: 10 }, (_, i) => [String(i + 1), `${i + 1}秒`])],
        (next) => patchSpeech({ silenceSeconds: Number(next) }),
      ),
      field("送信の合図", speech.sendPhrase, (value) => patchSpeech({ sendPhrase: value }), {
        hint: "話し終わりの合図。メモなら保存、質問なら送信します。空欄で無効",
      }),
    );
    const replacements = el("textarea", { rows: "3", placeholder: "日記書いて => /daily" });
    replacements.value = speech.replacements || "";
    replacements.addEventListener("change", () => void patchSpeech({ replacements: replacements.value }));
    const commandField = (label, key, placeholder) => {
      const phrases = settings.speech?.[key] ?? {};
      const input = el("input", { type: "text", value: phrases.ja ?? "", placeholder });
      input.addEventListener("change", () =>
        void patchSpeech({ [key]: { ...phrases, ja: input.value.trim() } }));
      return el("label", { class: "field" }, el("span", { text: label }), input);
    };
    body.append(
      commandField("「？」にする言葉", "questionPhrases", "クエスチョン, クエスチョンマーク"),
      commandField("改行にする言葉", "newlinePhrases", "エンター"),
      commandField("「！」にする言葉", "exclamationPhrases", "びっくり"),
      el(
        "label",
        { class: "field" },
        el("span", { text: "置換ルール" }),
        replacements,
        el("span", { class: "hint", text: "1行1件。話した言葉を別の表記に置き換えます" }),
      ),
      el(
        "label",
        { class: "field" },
        el("span", { text: "話し言葉を整えてから保存する" }),
        el("input", {
          type: "checkbox",
          checked: speech.tidyWithModel !== false,
          onchange: (event) => void patchSpeech({ tidyWithModel: event.target.checked }),
        }),
        el("span", { class: "hint", text: "整える前の発話も一緒に保存します" }),
      ),
    );
  }

  function drawVertex(speech) {
    const row = el("div", { class: "field" });
    const status = el("span", { class: "hint", text: "確認中…" });
    api.vertexStatus().then((value) => {
      status.textContent = value?.connected
        ? `接続済み（${value.projectId || "プロジェクト未設定"}）`
        : "未接続";
    }).catch(() => {
      status.textContent = "未接続";
    });
    row.append(
      el("span", { text: "Vertex AI" }),
      status,
      el(
        "div",
        { class: "row" },
        el("button", {
          class: "button",
          text: "OAuthクライアントを選ぶ",
          onclick: async () => {
            try {
              const client = await api.selectVertexClient();
              if (!client) return;
              const result = await api.connectVertex(client.clientId, client.clientSecret, client.projectId || speech.vertexProjectId);
              await patchSpeech({ vertexProjectId: result?.projectId || client.projectId || "" });
              toast("Vertex AI に接続しました");
              draw(opened);
            } catch (error) {
              toast(String(error), "error");
            }
          },
        }),
        el("button", {
          class: "button ghost",
          text: "切断",
          onclick: async () => {
            await api.disconnectVertex();
            draw(opened);
          },
        }),
      ),
      field("プロジェクトID", speech.vertexProjectId, (value) => patchSpeech({ vertexProjectId: value })),
    );
    return row;
  }

  function drawReadAloud() {
    body.append(el("h3", { text: "読み上げ" }));
    const rate = el("input", {
      type: "range", min: "0.5", max: "2.5", step: "0.1",
      value: String(settings.readAloudRate || 1),
    });
    const rateValue = el("span", { class: "hint", text: `${Number(settings.readAloudRate || 1).toFixed(1)}×` });
    rate.addEventListener("input", () => {
      rateValue.textContent = `${Number(rate.value).toFixed(1)}×`;
      narrator.setRate(Number(rate.value));
    });
    rate.addEventListener("change", () => void patch({ readAloudRate: Number(rate.value) }));
    const voices = narrator.voices();
    const voice = el(
      "select",
      {},
      el("option", { value: "", text: "既定の声" }),
      voices.map((name) => el("option", { value: name, text: name, selected: name === settings.readAloudVoice })),
    );
    voice.addEventListener("change", () => {
      narrator.setVoice(voice.value);
      void patch({ readAloudVoice: voice.value });
    });
    body.append(
      el("label", { class: "field" }, el("span", { text: "速さ" }), rate, rateValue),
      el("label", { class: "field" }, el("span", { text: "声" }), voice),
    );
  }

  function drawIngest() {
    body.append(el("h3", { text: "取り込み" }));
    const status = opened?.status;
    if (status) {
      const label = {
        pending: "未処理", indexing: "索引を作成中", sections: "章を要約中",
        following: "読んだところまでを要約しています", uncategorised: "カテゴリ未選択",
        ready: "準備完了", partial: "一部失敗", error: "失敗",
        "no-model": "接続先が未設定のため保留中",
      }[status.stage] || status.stage;
      body.append(el("p", {
        class: "status",
        text: status.total ? `${label}（${status.done}/${status.total}章）` : label,
      }));
      if (status.message) body.append(el("p", { class: "hint", text: status.message }));
      if (status.error) body.append(el("p", { class: "hint", text: status.error }));
    }
    body.append(
      el("div", { class: "row" }, el("button", {
        class: "button",
        text: "要約をやり直す",
        onclick: async () => {
          try {
            await api.prepare(opened.book.id);
            toast("要約を開始しました。できている章はそのまま使います。");
          } catch (error) {
            toast(String(error), "error");
          }
        },
      })),
      el("p", {
        class: "hint",
        text: opened?.book?.category === "technical"
          ? "取り込み時に一度だけ通します。処理中も本文とメモは使えます。"
          : "小説は読み進むごとに要約します。先に全文を処理することはありません。",
      }),
    );
    api.workspace().then((path) => {
      body.append(el("h3", { text: "保存先" }), el("p", { class: "hint", text: path }));
    });
  }

  function draw(next) {
    opened = next || opened;
    if (!settings) return;
    clear(body);
    drawAI();
    drawSearch();
    drawSpeech();
    drawReadAloud();
    if (opened) drawIngest();
  }

  return { element: body, draw, load };
}
