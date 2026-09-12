// One microphone button, wired to one text field.
//
// The engine behind it is whichever one the settings name; what the reader sees
// is the same button, the same level meter while it listens, and the same note
// when a clip could not be transcribed.
import { el, toast } from "./dom.js";
import { createAudioMeter, createDictation } from "./dictation.js";

export function createMicButton({ api, getSettings, getText, setText, onSend = () => {}, label = "話す" }) {
  const meterHost = el("span", { class: "mic-meter" });
  const meter = createAudioMeter(meterHost);
  const button = el("button", { class: "button", text: label, title: "話してメモを取る" });
  const status = el("span", { class: "hint mic-status" });
  let spoken = "";

  const dictation = createDictation({
    getSettings,
    getText,
    setText: (text) => {
      spoken = text;
      setText(text);
    },
    onSend: (text) => onSend(text),
    onState: (state) => {
      button.classList.toggle("active", state.status === "recording");
      button.textContent = state.status === "recording"
        ? "停止"
        : state.status === "idle"
        ? label
        : "…";
      status.textContent = state.error || state.silenceHint ||
        (state.status === "transcribing" ? "文字にしています…" : "");
      if (state.error) toast(state.error, "error");
      if (state.meterStream) meter.attach(state.meterStream);
      else meter.detach();
      if (state.retainedCount > 0 && state.status === "idle") {
        status.textContent = `${state.retainedCount}件の録音を保持しています`;
      }
    },
  });

  button.addEventListener("click", () => void dictation.toggle());

  return {
    element: el("span", { class: "mic-row" }, button, meterHost, status),
    stop: () => dictation.stop(),
    discard: () => dictation.discard(),
    // The spoken words as the recogniser produced them, before any tidying.
    spoken: () => spoken,
    async tidy() {
      const raw = getText();
      if (!raw.trim()) return { text: raw, spoken: raw };
      try {
        return await api.tidyMemo(raw);
      } catch (error) {
        toast(String(error), "error");
        return { text: raw, spoken: raw };
      }
    },
  };
}
