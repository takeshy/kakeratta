// The timeline.
//
// Posts, remarks and replies in the order they happened. A post nobody reacted
// to looks exactly like one that started a conversation - there is no unread
// count and no "3 AIs are thinking", because most posts are meant to pass
// without anyone saying anything.
import { el, clear, toast, when } from "./dom.js";
import { api, blobURL } from "./api.js";
import { t } from "./i18n.js";
import { createVoiceInput } from "./voice_input.js";

// Silence is the design, but an empty screen cannot say so for itself. The note
// is for the first couple of posts only: once the timeline has a few things in
// it, the user has seen what arriving looks like and the line is just noise.
export const SILENCE_NOTE_UNTIL = 3;
export const showsSilenceNote = (threads) => threads > 0 && threads < SILENCE_NOTE_UNTIL;

export function createFeed({ getPersona, getSettings = () => ({}), onReply }) {
  const view = el("div", { class: "feed" });
  let entries = [];
  let openReply = null;
  let replyEditor = null;
  const drafts = new Map();
  let composing = false;
  let drawPending = false;

  function set(next) {
    entries = next;
    draw();
  }

  // One entry arriving, from the live channel. A late remark lands under the
  // post it belongs to rather than at the bottom.
  function upsert(entry) {
    const index = entries.findIndex((existing) => existing.id === entry.id);
    if (index >= 0) entries[index] = entry;
    else entries.push(entry);
    entries.sort((a, b) => new Date(a.at) - new Date(b.at));
    draw();
  }

  function groups() {
    const byRoot = new Map();
    for (const entry of entries) {
      if (entry.deleted) continue;
      const root = entry.kind === "post" ? entry.id : entry.root;
      if (!root) continue;
      if (!byRoot.has(root)) byRoot.set(root, []);
      byRoot.get(root).push(entry);
    }
    return [...byRoot.values()]
      .filter((thread) => thread.some((entry) => entry.kind === "post"))
      .sort((a, b) => new Date(b[0].at) - new Date(a[0].at));
  }

  function draw() {
    // Detaching a textarea while an IME owns it cancels the conversion. Keep
    // the current DOM until compositionend.
    if (composing) {
      drawPending = true;
      return;
    }
    drawPending = false;
    const active = document.activeElement;
    const selection = active?.classList?.contains("reply-input")
      ? { start: active.selectionStart, end: active.selectionEnd }
      : null;
    const threads = groups();
    // Keep the same field and recognizer across live timeline updates.
    if (replyEditor && (replyEditor.replyTo !== openReply ||
        !threads.some((thread) => thread.some((entry) => entry.id === openReply)))) {
      const previous = replyEditor;
      replyEditor = null;
      void previous.dispose();
    }
    clear(view);
    if (!threads.length) {
      view.append(
        el("div", { class: "empty" },
          el("p", { class: "empty-line", label: "ここにかけらが並びます。" }),
          el("p", { class: "hint", label: "書く、貼る（Ctrl+V）、URL、話す。担当は黙っていることもあり、反応は遅れて届きます。" })),
      );
      return;
    }
    if (showsSilenceNote(threads.length)) {
      view.append(el("p", { class: "note", label: "反応が付かないことがあります。付くときも、遅れて届きます。" }));
    }
    for (const thread of threads) view.append(drawThread(thread));
    if (selection && openReply) {
      const input = [...view.querySelectorAll(".reply-input")]
        .find((candidate) => candidate.dataset.replyTo === openReply);
      input?.focus();
      input?.setSelectionRange(selection.start, selection.end);
    }
  }

  function drawThread(thread) {
    const post = thread.find((entry) => entry.kind === "post");
    const rest = thread.filter((entry) => entry !== post);
    const box = el("article", { class: "thread", "data-id": post.id });
    box.append(drawPost(post));
    for (const entry of rest) box.append(drawSaid(entry, post));
    box.append(drawReplyRow(post));
    return box;
  }

  function drawPost(entry) {
    const media = entry.media;
    const node = el("div", { class: "post" });
    node.append(
      el(
        "div",
        { class: "post-head" },
        el("span", { class: "avatar", text: "🙂" }),
        el("span", { class: "who", label: "あなた" }),
        el("span", { class: "at", text: when(entry.at) }),
        entry.place ? el("span", { class: "place", text: placeLabel(entry.place) }) : null,
        el("span", { class: "spacer" }),
        // Append-only means a delete is recoverable in the log, but nobody
        // wants to go and get it. It stays out of the way until reached for.
        el("button", {
          class: "chip ghost remove",
          i18nTitle: "消す",
          label: "×",
          onclick: async () => {
            if (!confirm(t("この投稿を消しますか"))) return;
            try {
              await api.remove(entry.id);
            } catch (error) {
              toast(String(error.message || error), "error");
            }
          },
        }),
      ),
    );
    if (media?.blobId) {
      node.append(
        el("a", { class: "shot", href: blobURL(media.blobId), target: "_blank" },
          el("img", { src: blobURL(media.blobId), alt: media.describe || t("キャプチャ"), loading: "lazy" })),
      );
    }
    if (entry.text) node.append(el("p", { class: "said", text: entry.text }));
    if (entry.link) {
      node.append(el("a", { class: "link", href: entry.link, target: "_blank", rel: "noreferrer", text: entry.link }));
    }
    if (media) node.append(drawRead(media));
    return node;
  }

  // What the app read out of the capture, folded away. It is what every persona
  // was given, so it is worth being able to check - but it is not the post.
  function drawRead(media) {
    if (media.error) return el("p", { class: "read error", text: t("読み取れませんでした: {0}", media.error) });
    if (!media.readAt) return el("p", { class: "read pending", label: "読み取り中…" });
    const summary = media.describe || (media.text ? t("{0}文字", [...media.text].length) : t("読み取り結果"));
    const body = el("div", { class: "read-body" });
    if (media.origin) body.append(el("p", { class: "origin", text: t("出典の手がかり: {0}", media.origin) }));
    if (media.text) body.append(el("pre", { class: "ocr", text: media.text }));
    if (!body.children.length) body.append(el("p", { class: "hint", label: "文字は読み取れませんでした。" }));
    return el("details", { class: "read" }, el("summary", { text: summary }), body);
  }

  function drawSaid(entry, post) {
    const persona = getPersona(entry.speaker);
    const mine = entry.speaker === "user";
    const node = el("div", { class: `said-row ${mine ? "mine" : ""}` });
    node.append(
      el(
        "div",
        { class: "said-head" },
        el("span", { class: "avatar", text: mine ? "🙂" : persona?.icon || "◎" }),
        el("span", { class: "who", text: mine ? t("あなた") : persona?.name || entry.speaker }),
        el("span", { class: "at", text: when(entry.at) }),
      ),
      el("p", { class: "said", text: entry.text }),
    );
    for (const source of entry.sources || []) {
      node.append(
        el("a", { class: "source", href: source.url, target: "_blank", rel: "noreferrer", text: source.label || source.url }),
      );
    }
    if (!mine) {
      node.append(
        el("button", {
          class: "chip ghost",
          label: "返す",
          onclick: () => {
            openReply = entry.id;
            draw();
          },
        }),
      );
    }
    if (openReply === entry.id) node.append(drawReplyBox(entry.id));
    void post;
    return node;
  }

  function drawReplyRow(post) {
    if (openReply === post.id) return drawReplyBox(post.id);
    // Replying is one of the two things you do here - what you write goes back
    // into the timeline and into what the personas read next.
    return el("button", {
      class: "chip reply-open",
      label: "一言足す",
      onclick: () => {
        openReply = post.id;
        draw();
      },
    });
  }

  // A reply is not a command: it goes back into the timeline and the personas
  // see it next time, the same as anything else.
  function drawReplyBox(replyTo) {
    if (replyEditor?.replyTo === replyTo) return replyEditor.box;
    let disposed = false;
    let sending = false;
    const input = el("textarea", {
      class: "reply-input", rows: "2", i18nPlaceholder: "返事や、@名前 で指名",
      "data-reply-to": replyTo,
    });
    input.value = drafts.get(replyTo) || "";
    input.addEventListener("input", () => drafts.set(replyTo, input.value));
    input.addEventListener("compositionstart", () => { composing = true; });
    input.addEventListener("compositionend", () => {
      composing = false;
      drafts.set(replyTo, input.value);
      if (drawPending) draw();
    });
    const send = async () => {
      const text = input.value.trim();
      if (!text || sending || disposed) return;
      sending = true;
      try {
        await voice.discard();
        if (disposed) return;
        await api.reply(replyTo, text);
        if (!disposed) {
          drafts.delete(replyTo);
          if (openReply === replyTo) openReply = null;
        }
        draw();
        onReply?.();
      } catch (error) {
        toast(String(error.message || error), "error");
      } finally {
        sending = false;
      }
    };
    input.addEventListener("keydown", (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
        event.preventDefault();
        void send();
      }
    });
    const voice = createVoiceInput({
      getSettings,
      getText: () => input.value,
      setText: (value) => {
        if (disposed) return;
        input.value = value;
        drafts.set(replyTo, value);
      },
      onSend: () => void send(),
    });
    const box = el(
      "div",
      { class: "reply" },
      input,
      el(
        "div",
        { class: "compose-row" },
        voice.button,
        el("button", { class: "button send", label: "送る", onclick: () => void send() }),
        el("button", {
          class: "chip ghost",
          label: "やめる",
          onclick: () => {
            drafts.delete(replyTo);
            openReply = null;
            draw();
          },
        }),
      ),
    );
    box.append(voice.activity);
    replyEditor = {
      replyTo, box,
      dispose() {
        disposed = true;
        return voice.discard();
      },
    };
    setTimeout(() => { if (!disposed) input.focus(); }, 0);
    return box;
  }

  function placeLabel(place) {
    if (place.label) return place.label;
    return `${place.lat.toFixed(3)}, ${place.lon.toFixed(3)}`;
  }

  // What the read-aloud mode says: the posts and what was said about them, in
  // order, so a backlog can be listened to while moving.
  function spoken() {
    const lines = [];
    for (const thread of groups().slice().reverse()) {
      const post = thread.find((entry) => entry.kind === "post");
      const said = thread.filter((entry) => entry !== post && entry.speaker !== "user");
      if (!said.length) continue;
      const heading = post.text || post.media?.describe || "投稿";
      lines.push({ id: post.id, text: heading });
      for (const entry of said) {
        const persona = getPersona(entry.speaker);
        lines.push({ id: entry.id, text: `${persona?.name || entry.speaker}。${entry.text}` });
      }
    }
    return lines;
  }

  return { element: view, set, upsert, draw, spoken, entries: () => entries };
}
