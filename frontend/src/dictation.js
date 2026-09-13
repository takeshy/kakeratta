// Dictation for this app, driving the stack carried over from speech-popup.
//
// It is one of the four ways a post gets in: say it, and the transcript is the
// post. A browser recogniser is created per text field.
import { browserSpeechSupported, createBrowserRecognizer } from "./browser_speech.js";
import { createAudioMeter } from "./meter.js";
import { speechCommandsFor } from "./speech_commands.js";

export function dictationSupported() {
  return browserSpeechSupported();
}

/**
 * Attach dictation to one text field.
 *
 * getSettings must return the app settings; onState reports what the engine is
 * doing so a button can show it. onSend fires when the reader speaks the send
 * phrase, which here means "I am done with this memo".
 */
export function createDictation({ getSettings, getText, setText, onState = () => {}, onSend = () => {} }) {
  let engine = null;
  let state = { status: "idle", error: "", silenceHint: "", retainedCount: 0, meterStream: null };

  // The ported recognisers expect the spoken-punctuation phrases resolved for
  // the current language, the way speech-popup hands them over.
  const speechOf = () => {
    const speech = getSettings()?.speech ?? {};
    return { ...speech, symbolCommands: speechCommandsFor(speech) };
  };

  function build() {
    const options = {
      getSettings: () => speechOf(),
      getBase: () => getText(),
      getContext: () => getText(),
      getText: () => getText(),
      onInput: (text) => setText(text),
      onSend: (text) => {
        setText(text);
        onSend(text);
      },
      onState: (next) => {
        state = next;
        onState(next);
      },
    };
    return createBrowserRecognizer(options);
  }

  return {
    toggle() {
      if (!engine) {
        void engine?.discard?.();
        engine = build();
      }
      return engine.toggle();
    },
    async stop() {
      await engine?.stop?.();
    },
    async discard() {
      await engine?.discard?.();
      engine = null;
    },
    retry: () => engine?.retry?.(),
    state: () => state,
    recording: () => !!engine?.recording?.(),
    busy: () => !!engine?.busy?.(),
  };
}

export { createAudioMeter };
