// Putting something in.
//
// Four ways, one passage: a line of text, a pasted image, a URL, or something
// said out loud. Writing is the quickest for a thought; an image is the only
// one that does not care whether what was on screen sat behind a login, a DRM
// reader or a paywall, which is why it works for Kindle when nothing else does.
//
// It starts one line tall and grows with what is in it. The timeline is what
// the screen is for; this is the way through to it.
import { el, clear, toast } from "./dom.js";
import { api } from "./api.js";
import { createDictation, dictationSupported } from "./dictation.js";
import { t } from "./i18n.js";

const LINK = /^https?:\/\/\S+$/i;

export function createCompose({ getSettings, onPosted }) {
  const view = el("form", { class: "compose" });
  const text = el("textarea", {
    class: "compose-text",
    rows: "1",
    i18nPlaceholder: "かけらを放り込む",
  });
  const preview = el("div", { class: "compose-preview", hidden: true });
  const status = el("span", { class: "compose-status" });
  let image = null;
  let place = null;
  let sending = false;

  // One line until there is more than one line of it.
  function grow() {
    text.style.height = "auto";
    const ceiling = Math.round((globalThis.innerHeight || 800) * 0.4);
    text.style.height = `${Math.min(text.scrollHeight, ceiling)}px`;
  }

  // The position is only ever the current one. Where the user is going and when
  // they want to be there is in what they wrote, and the personas read that.
  async function locate() {
    if (!navigator.geolocation) return null;
    return new Promise((resolve) => {
      const done = (value) => resolve(value);
      navigator.geolocation.getCurrentPosition(
        (position) => done({ lat: position.coords.latitude, lon: position.coords.longitude }),
        () => done(null),
        { enableHighAccuracy: false, timeout: 4000, maximumAge: 300000 },
      );
    });
  }

  function showImage(dataURL, name) {
    image = dataURL;
    clear(preview).append(
      el("img", { src: dataURL, alt: name || t("キャプチャ") }),
      el("button", {
        class: "chip",
        type: "button",
        label: "取り消す",
        onclick: () => {
          image = null;
          preview.hidden = true;
          clear(preview);
        },
      }),
    );
    preview.hidden = false;
  }

  function readFile(file) {
    if (!file || !file.type.startsWith("image/")) return;
    const reader = new FileReader();
    reader.onload = () => showImage(String(reader.result), file.name);
    reader.readAsDataURL(file);
  }

  // Pasting is the whole desktop story: the OS already put the screenshot on
  // the clipboard, so there is nothing to install.
  view.addEventListener("paste", (event) => {
    for (const item of event.clipboardData?.items || []) {
      if (item.type.startsWith("image/")) {
        event.preventDefault();
        readFile(item.getAsFile());
        return;
      }
    }
  });
  view.addEventListener("dragover", (event) => event.preventDefault());
  view.addEventListener("drop", (event) => {
    event.preventDefault();
    readFile(event.dataTransfer?.files?.[0]);
  });

  const picker = el("input", {
    type: "file",
    accept: "image/*",
    hidden: true,
    onchange: () => readFile(picker.files?.[0]),
  });

  async function send() {
    if (sending) return;
    const written = text.value.trim();
    if (!written && !image) {
      toast("何か書くか、画像を貼ってください");
      return;
    }
    sending = true;
    status.textContent = t("送っています…");
    try {
      const body = { text: written, image: image || "" };
      // A line that is only a URL is a link, not a comment on one.
      if (LINK.test(written) && !image) {
        body.link = written;
        body.text = "";
        status.textContent = t("ページを読んでいます…");
      }
      body.place = place || (await locate());
      const entry = await api.post(body);
      text.value = "";
      grow();
      image = null;
      preview.hidden = true;
      clear(preview);
      onPosted?.(entry);
      status.textContent = "";
    } catch (error) {
      toast(String(error.message || error), "error");
      status.textContent = "";
    } finally {
      sending = false;
    }
  }

  const micButton = el("button", { class: "chip", type: "button", label: "🎙 話す" });
  const dictation = createDictation({
    getSettings,
    getText: () => text.value,
    setText: (value) => {
      text.value = value;
      grow();
    },
    onState: (state) => {
      micButton.classList.toggle("active", state.status === "recording");
      status.textContent = state.error || (state.status === "recording" ? t("聞いています…") : "");
    },
    // Saying the send phrase means "that is the post".
    onSend: () => void send(),
  });
  micButton.addEventListener("click", () => {
    if (!dictationSupported(getSettings())) {
      toast("この環境では音声入力が使えません");
      return;
    }
    void dictation.toggle();
  });

  text.addEventListener("input", grow);
  text.addEventListener("keydown", (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      event.preventDefault();
      void send();
    }
  });

  view.addEventListener("submit", (event) => {
    event.preventDefault();
    void send();
  });

  view.append(
    text,
    preview,
    el(
      "div",
      { class: "compose-row" },
      el("button", {
        class: "chip",
        type: "button",
        label: "📷 画像",
        onclick: () => picker.click(),
      }),
      micButton,
      picker,
      status,
      el("span", { class: "spacer" }),
      el("button", { class: "button send", type: "submit", label: "送る", i18nTitle: "Ctrl（⌘）+ Enter" }),
    ),
  );

  return {
    element: view,
    focus: () => text.focus(),
    // The share target lands on the page with a file already in hand.
    accept(file, note) {
      readFile(file);
      if (note) text.value = note;
      grow();
      text.focus();
    },
    // Naming someone is a promise of an answer, so it goes in the text where
    // the personas read it - not into a hidden "to:" field.
    mention(name) {
      const tag = `@${name}`;
      if (!text.value.includes(tag)) {
        text.value = text.value.trim() ? `${text.value.replace(/\s*$/, "")} ${tag} ` : `${tag} `;
      }
      grow();
      text.focus();
      text.setSelectionRange(text.value.length, text.value.length);
    },
    setPlace(next) {
      place = next;
    },
  };
}
