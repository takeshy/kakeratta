// Dictation for this app, driving the stack carried over from speech-popup.
//
// It is one of the four ways a post gets in: say it, and the transcript is the
// post. The recogniser is created per text field, and every engine (browser,
// recorded, live streaming) behaves the same from the caller's side.
import { api } from "./api.js";
import { createRecorder, speechSupported } from "./recorder.js";
import { browserSpeechSupported, createBrowserRecognizer } from "./browser_speech.js";
import { createLiveRecognizer, liveSpeechSupported } from "./live_recognizer.js";
import { createAudioMeter } from "./meter.js";
import { speechCommandsFor } from "./speech_commands.js";

// Every request goes through the relay: the browser never sees the key, and
// there is no CORS to negotiate with a local whisper.cpp. Vertex has its own
// route, the only place the stored OAuth token is used.
async function speechTransport(request) {
  if (new URL(request.url).hostname === "aiplatform.googleapis.com") {
    return await api.speechVertex(request);
  }
  return await api.speechRequest(request);
}

const liveHandlers = new Set();
api.on((name, data) => {
  if (name !== "speech:live") return;
  for (const handler of liveHandlers) handler(data);
});

const liveTransport = {
  start: async () => (await api.liveStart()).sessionId,
  send: (sessionId, audio) => api.liveAudio(sessionId, audio),
  finish: (sessionId) => api.liveFinish(sessionId),
  stop: () => api.liveStop(),
  onEvent: (handler) => {
    liveHandlers.add(handler);
    return () => liveHandlers.delete(handler);
  },
};

export function dictationSupported(settings) {
  const speech = settings?.speech ?? {};
  if (speech.provider === "browser") return browserSpeechSupported();
  if (speech.provider === "live") return liveSpeechSupported();
  return speechSupported();
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
  let provider = "";
  let state = { status: "idle", error: "", silenceHint: "", retainedCount: 0, meterStream: null };

  // The ported recognisers expect the spoken-punctuation phrases resolved for
  // the current language, the way speech-popup hands them over.
  const speechOf = () => {
    const speech = getSettings()?.speech ?? {};
    return { ...speech, symbolCommands: speechCommandsFor(speech) };
  };

  function build() {
    const settings = speechOf();
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
    provider = settings.provider || "browser";
    if (provider === "browser") return createBrowserRecognizer(options);
    if (provider === "live") return createLiveRecognizer({ ...options, liveTransport });
    return createRecorder({ ...options, transport: speechTransport });
  }

  return {
    toggle() {
      if (!engine || provider !== (speechOf().provider || "browser")) {
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
