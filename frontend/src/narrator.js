// Reading aloud is the same reading, out loud. The narrator owns no position
// of its own: `onboundary` reports where the voice is, and that becomes the
// reader's current position, exactly as scrolling does in the reading view.
import { toast } from "./dom.js";

// Chromium stops a long utterance after about fifteen seconds unless the
// synthesiser is nudged, so a paused/resumed tick runs while speaking.
const KEEP_ALIVE_MS = 10000;

export function createNarrator({ reader, onPosition }) {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : null;
  const supported = Boolean(synth && typeof window.SpeechSynthesisUtterance === "function");

  const listeners = [];
  let speaking = false;
  let rate = 1;
  let voiceName = "";
  let voiceList = [];
  let queue = [];
  let block = null;
  let offset = 0;
  let utterance = null;
  let keepAlive = null;

  if (supported) {
    const loadVoices = () => {
      voiceList = synth.getVoices() || [];
    };
    loadVoices();
    synth.addEventListener?.("voiceschanged", loadVoices);
  }

  const emit = (next) => {
    if (speaking === next) return;
    speaking = next;
    for (const listener of listeners) listener(speaking);
  };

  // Blocks in reading order. Images have nothing to say and are skipped.
  function blocksOf(opened) {
    const all = [];
    for (const section of opened?.book?.sections || []) {
      for (const b of section.blocks || []) {
        if (b.kind === "image" || !(b.plain || "").trim()) continue;
        all.push(b);
      }
    }
    return all;
  }

  function positionIn(target, charOffset) {
    return {
      sectionId: target.range.start.sectionId,
      blockId: target.id,
      charOffset,
      absolute: target.range.start.absolute + charOffset,
    };
  }

  function highlight(target) {
    for (const node of reader.element.querySelectorAll(".block.speaking")) {
      node.classList.remove("speaking");
    }
    if (!target) return;
    const node = reader.blockNode(target.id);
    if (!node) return;
    node.classList.add("speaking");
    node.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }

  function cancelUtterance() {
    if (!utterance) return;
    utterance.onboundary = null;
    utterance.onend = null;
    utterance.onerror = null;
    utterance = null;
  }

  function speakCurrent() {
    if (!block) {
      finish();
      return;
    }
    const text = (block.plain || "").slice(offset);
    if (!text.trim()) {
      advance();
      return;
    }
    cancelUtterance();
    synth.cancel();
    const next = new SpeechSynthesisUtterance(text);
    next.rate = rate;
    next.lang = "ja-JP";
    const voice = voiceList.find((candidate) => candidate.name === voiceName);
    if (voice) next.voice = voice;
    const base = offset;
    const spoken = block;
    next.onboundary = (event) => {
      if (spoken !== block) return;
      offset = base + (event.charIndex || 0);
      const current = positionIn(spoken, offset);
      onPosition(current, current);
    };
    next.onend = () => {
      if (spoken !== block || utterance !== next) return;
      onPosition(positionIn(spoken, spoken.plain.length), spoken.range.end);
      advance();
    };
    next.onerror = (event) => {
      if (event.error === "interrupted" || event.error === "canceled") return;
      toast(`読み上げできません: ${event.error}`, "error");
      stop();
    };
    utterance = next;
    highlight(block);
    synth.speak(next);
  }

  function advance() {
    block = queue.shift() || null;
    offset = 0;
    if (!block) {
      finish();
      return;
    }
    speakCurrent();
  }

  function finish() {
    cancelUtterance();
    synth?.cancel();
    clearInterval(keepAlive);
    keepAlive = null;
    highlight(null);
    block = null;
    queue = [];
    offset = 0;
    emit(false);
  }

  function start() {
    const opened = reader.opened;
    if (!opened) return;
    const all = blocksOf(opened);
    if (!all.length) return;
    const from = opened.state.current.absolute || 0;
    // The voice starts at the block the eye is on, not at the character: a
    // sentence cut in half is worse than a few words heard twice.
    let index = all.findIndex((candidate) => candidate.range.end.absolute > from);
    if (index < 0) index = 0;
    queue = all.slice(index + 1);
    block = all[index];
    offset = 0;
    emit(true);
    clearInterval(keepAlive);
    keepAlive = setInterval(() => {
      if (!speaking) return;
      synth.pause();
      synth.resume();
    }, KEEP_ALIVE_MS);
    speakCurrent();
  }

  function stop() {
    if (!supported) return;
    finish();
  }

  // A new rate or voice applies to what is being said now, picked up from the
  // character the voice has already reached.
  function restart() {
    if (!speaking || !block) return;
    speakCurrent();
  }

  return {
    supported,
    toggle() {
      if (!supported) return;
      if (speaking) stop();
      else start();
    },
    stop,
    onState(listener) {
      listeners.push(listener);
    },
    setRate(value) {
      const next = Number(value);
      if (!next || next === rate) return;
      rate = next;
      restart();
    },
    setVoice(name) {
      if (name === voiceName) return;
      voiceName = name || "";
      restart();
    },
    voices() {
      return voiceList.map((voice) => voice.name);
    },
    get speaking() {
      return speaking;
    },
  };
}
