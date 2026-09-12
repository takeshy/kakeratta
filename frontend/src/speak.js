// Reading the backlog out loud.
//
// Reactions arrive while you are doing something else, so there has to be a way
// to take them in without looking. It reads the post and then what was said
// about it, in order, and stops when it runs out.
import { toast } from "./dom.js";

// Chromium stops a long utterance after about fifteen seconds unless the
// synthesiser is nudged, so a paused/resumed tick runs while speaking.
const KEEP_ALIVE_MS = 10000;

export function createSpeaker() {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : null;
  const supported = Boolean(synth && typeof window.SpeechSynthesisUtterance === "function");

  const listeners = new Set();
  let speaking = false;
  let queue = [];
  let rate = 1;
  let voiceName = "";
  let keepAlive = null;
  let onLine = () => {};

  function emit(next) {
    if (speaking === next) return;
    speaking = next;
    for (const listener of listeners) listener(speaking);
  }

  function voiceFor() {
    if (!voiceName) return null;
    return (synth.getVoices() || []).find((voice) => voice.name === voiceName) || null;
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
    utterance.lang = "ja-JP";
    const voice = voiceFor();
    if (voice) utterance.voice = voice;
    utterance.onend = () => step();
    utterance.onerror = () => step();
    synth.speak(utterance);
  }

  function start(lines) {
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
      else start(lines);
    },
    speaking: () => speaking,
    onState: (listener) => listeners.add(listener),
    onLine: (handler) => {
      onLine = handler;
    },
    setRate: (value) => {
      rate = value || 1;
    },
    setVoice: (name) => {
      voiceName = name || "";
    },
    voices: () => (supported ? (synth.getVoices() || []).map((voice) => voice.name) : []),
  };
}
