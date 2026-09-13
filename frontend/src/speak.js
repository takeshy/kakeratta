// Reading the backlog out loud.
//
// Reactions arrive while you are doing something else, so there has to be a way
// to take them in without looking. It reads the post and then what was said
// about it, in order, and stops when it runs out.
import { toast } from "./dom.js";
import { getLanguage } from "./i18n.js";

// Chromium stops a long utterance after about fifteen seconds unless the
// synthesiser is nudged, so a paused/resumed tick runs while speaking.
const KEEP_ALIVE_MS = 10000;
const VOICES_WAIT_MS = 1500;

const languageOf = (voice) => voice.lang?.replace("_", "-").toLowerCase() || "";

export function chooseVoice(voices, name, language) {
  const prefix = `${language.toLowerCase()}-`;
  const matching = voices.filter((voice) =>
    languageOf(voice) === language.toLowerCase() || languageOf(voice).startsWith(prefix));
  const selected = name ? matching.find((voice) => voice.name === name) : null;
  if (selected) return selected;
  return matching.find((voice) => voice.default) || matching[0] || null;
}

export function createSpeaker() {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : null;
  const supported = Boolean(synth && typeof window.SpeechSynthesisUtterance === "function");

  const listeners = new Set();
  let speaking = false;
  let queue = [];
  let rate = 1.2;
  let voiceName = "";
  let keepAlive = null;
  let onLine = () => {};

  function emit(next) {
    if (speaking === next) return;
    speaking = next;
    for (const listener of listeners) listener(speaking);
  }

  function voiceFor(language) {
    return chooseVoice(synth.getVoices() || [], voiceName, language);
  }

  async function waitForVoices() {
    if ((synth.getVoices() || []).length) return;
    await new Promise((resolve) => {
      const done = () => {
        clearTimeout(timer);
        synth.removeEventListener?.("voiceschanged", done);
        resolve();
      };
      const timer = setTimeout(done, VOICES_WAIT_MS);
      synth.addEventListener?.("voiceschanged", done, { once: true });
    });
  }

  function step() {
    if (!queue.length) {
      stop();
      return;
    }
    const line = queue.shift();
    onLine(line);
    const utterance = new SpeechSynthesisUtterance(line.text);
    utterance.rate = rate;
    const language = getLanguage() === "ja" ? "ja" : "en";
    utterance.lang = language === "ja" ? "ja-JP" : "en-US";
    const voice = voiceFor(language);
    if (voice) {
      utterance.voice = voice;
      utterance.lang = voice.lang;
    } else if ((synth.getVoices() || []).length) {
      toast(language === "ja" ? "日本語の読み上げ音声が端末にありません" : "英語の読み上げ音声が端末にありません", "error");
      stop();
      return;
    }
    console.log("[kakeratta read aloud]", {
      text: line.text,
      language: utterance.lang,
      voice: utterance.voice?.name || "browser default",
      rate: utterance.rate,
    });
    utterance.onend = () => step();
    utterance.onerror = () => step();
    synth.speak(utterance);
  }

  async function start(lines) {
    if (!supported) {
      toast("この環境では読み上げが使えません");
      return;
    }
    if (!lines.length) {
      toast("読み上げるものがありません");
      return;
    }
    synth.cancel();
    queue = [...lines];
    emit(true);
    clearInterval(keepAlive);
    keepAlive = setInterval(() => {
      if (!synth.speaking) return;
      synth.pause();
      synth.resume();
    }, KEEP_ALIVE_MS);
    await waitForVoices();
    if (!speaking) return;
    step();
  }

  function stop() {
    clearInterval(keepAlive);
    keepAlive = null;
    queue = [];
    synth?.cancel();
    emit(false);
  }

  return {
    supported,
    start,
    stop,
    toggle(lines) {
      if (speaking) stop();
      else void start(lines);
    },
    speaking: () => speaking,
    onState: (listener) => listeners.add(listener),
    onLine: (handler) => {
      onLine = handler;
    },
    setRate: (value) => {
      rate = value || 1.2;
    },
    setVoice: (name) => {
      voiceName = name || "";
    },
    voices: () => (supported ? (synth.getVoices() || []).map((voice) => voice.name) : []),
  };
}
