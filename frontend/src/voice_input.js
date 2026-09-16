// Shared speech controls for posts and replies.
import { el, toast } from "./dom.js";
import { createDictation, dictationSupported } from "./dictation.js";
import { t } from "./i18n.js";

export function createVoiceInput({ getSettings, getText, setText, onSend }) {
  const micButton = el("button", { class: "chip voice-button", type: "button", label: "🎙 話す", "aria-pressed": "false" });
  const micTitle = el("strong", { class: "mic-title" });
  const micHint = el("span", { class: "mic-status" });
  const micActivity = el("div", { class: "mic-activity", hidden: true },
    el("span", { class: "mic-copy" }, micTitle, micHint));
  const dictation = createDictation({
    getSettings,
    getText,
    setText,
    onState: (state) => {
      micButton.classList.toggle("active", state.status === "recording");
      micButton.setAttribute("aria-pressed", String(state.status === "recording"));
      const active = state.status !== "idle";
      micActivity.hidden = !active && !state.error;
      micActivity.classList.toggle("processing", active && state.status !== "recording");
      if (state.error) micTitle.textContent = state.error;
      else if (state.status === "recording" && state.backgroundTranscribing) micTitle.textContent = t("無音部分を解析中…（録音は継続）");
      else if (state.status === "recording") micTitle.textContent = t("聞いています…");
      else if (state.status === "starting") micTitle.textContent = t("マイクを準備しています…");
      else if (state.status === "preparing") micTitle.textContent = t("音声を準備しています…");
      else if (state.status === "transcribing") micTitle.textContent = t("音声を解析しています…");
      else micTitle.textContent = "";
      const phrase = getSettings()?.speech?.sendPhrase?.trim();
      micHint.textContent = active
        ? [state.silenceHint, phrase ? t("入力を終えるときは「{0}」と言ってください", phrase) : ""].filter(Boolean).join(" · ")
        : "";
    },
    onSend,
  });
  micButton.addEventListener("click", () => {
    if (!dictationSupported(getSettings())) {
      toast("この環境では音声入力が使えません");
      return;
    }
    void dictation.toggle();
  });

  return {
    button: micButton,
    activity: micActivity,
    async discard() {
      await dictation.discard();
    },
  };
}
