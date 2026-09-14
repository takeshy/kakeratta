// The timeline.
//
// Posts, remarks and replies in the order they happened. A post nobody reacted
// to looks exactly like one that started a conversation - there is no unread
// count and no "3 AIs are thinking", because most posts are meant to pass
// without anyone saying anything.
import { el, clear, toast, when } from "./dom.js";
import { api, blobURL } from "./api.js";
import { t } from "./i18n.js";
import { createMentionPicker } from "./mention-picker.js";
import { openSocial } from "./social.js";
import { createVoiceInput } from "./voice_input.js";

// Silence is the design, but an empty screen cannot say so for itself. The note
// is for the first couple of posts only: once the timeline has a few things in
// it, the user has seen what arriving looks like and the line is just noise.
export const SILENCE_NOTE_UNTIL = 3;
export const showsSilenceNote = (threads) => threads > 0 && threads < SILENCE_NOTE_UNTIL;

export function createFeed({ getPersona, getPersonas, getSettings = () => ({}), onReply, onEntryChange, onSpeak, showIntro = true }) {
  const view = el("div", { class: "feed" });
  let entries = [];
  let speakingThread = null;
  let openReply = null;
  let replyEditor = null;
  let postEditor = null;
  const expanded = new Set();
  const drafts = new Map();
  let composing = false;
  let drawPending = false;
  const offline = () => globalThis.navigator?.onLine === false;
  const updateEditAvailability = () => {
    for (const button of view.querySelectorAll(".edit-post")) {
      button.disabled = offline() || !!postEditor || button.dataset.pending === "true";
    }
    postEditor?.updateAvailability();
  };
  globalThis.addEventListener?.("online", updateEditAvailability);
  globalThis.addEventListener?.("offline", updateEditAvailability);

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
    if (postEditor && !threads.some((thread) => thread.some((entry) => entry.id === postEditor.id))) postEditor = null;
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
    if (showIntro && showsSilenceNote(threads.length)) {
      view.append(el("p", { class: "note", label: "反応が付かないことがあります。付くときも、遅れて届きます。" }));
    }
    for (const thread of threads) view.append(drawThread(thread));
    if (selection && active === postEditor?.input) {
      active.focus();
      active.setSelectionRange(selection.start, selection.end);
    }
    if (selection && openReply && active !== postEditor?.input) {
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
    // A conversation stays folded under its post until asked for, so the
    // timeline reads as the things you dropped in. Replying opens it: you
    // should see what you are answering.
    const open = expanded.has(post.id) || thread.some((entry) => entry.id === openReply);
    box.append(el("div", { class: "thread-actions" },
      onSpeak ? el("button", {
        class: "chip ghost thread-speak", "data-thread": post.id,
        text: t(speakingThread === post.id ? "■ 停止" : "🔈 読み上げ"),
        "aria-pressed": String(speakingThread === post.id),
        i18nTitle: "このスレッドを読み上げます",
        onclick: () => onSpeak(post.id, spoken(post.id)),
      }) : null,
      rest.length ? drawToggle(thread, post, rest.length, open) : null,
      el("button", { class: "chip ghost", label: "Blueskyに投稿", disabled: !!post.pending, onclick: () => void openSocial(post.id, getPersona) }),
      openReply === post.id ? null : drawReplyOpen(post)));
    if (open) for (const entry of rest) {
      box.append(drawSaid(entry, post));
      if (entry.pending) box.append(el("p", { class: "hint", text: entry.syncError ? t("送信できませんでした: {0}", entry.syncError) : t("端末に保存済み・送信待ち") }));
    }
    if (openReply === post.id) box.append(drawReplyBox(post.id));
    return box;
  }

  function drawToggle(thread, post, count, open) {
    return el("button", {
      class: "chip ghost thread-toggle",
      "aria-expanded": String(open),
      text: `${open ? "▾" : "▸"} ${t("返信 {0}件", count)}`,
      onclick: () => {
        if (open) {
          expanded.delete(post.id);
          // The draft stays in drafts; only the open box folds away with it.
          if (thread.some((entry) => entry.id === openReply)) openReply = null;
        } else {
          expanded.add(post.id);
        }
        draw();
      },
    });
  }

  function drawPost(entry) {
    const media = entry.media;
    const mine = entry.speaker === "user";
    const persona = getPersona(entry.speaker);
    const node = el("div", { class: "post" });
    node.append(
      el(
        "div",
        { class: "post-head" },
        el("span", { class: "avatar", text: mine ? "🙂" : persona?.icon || "◎" }),
        el("span", { class: "who", text: mine ? t("あなた") : persona?.name || entry.speaker }),
        el("span", { class: "at", text: when(entry.at) }),
        el("span", { class: "spacer" }),
        mine ? el("button", {
          class: "chip ghost edit-post", label: "編集", disabled: offline() || !!entry.pending || !!postEditor,
          "data-pending": String(!!entry.pending), i18nTitle: "編集はオンライン時に利用できます",
          onclick: () => {
            if (offline() || entry.pending || postEditor) return;
            postEditor = createPostEditor(entry); draw(); postEditor.input.focus();
          },
        }) : null,
        // Append-only means a delete is recoverable in the log, but nobody
        // wants to go and get it. It stays out of the way until reached for.
        el("button", {
          class: "chip ghost remove",
          i18nTitle: "消す",
          disabled: !!entry.pending,
          label: "×",
          onclick: async (event) => {
            if (!confirm(t("この投稿を消しますか"))) return;
            const button = event.currentTarget;
            button.disabled = true;
            try {
              const removed = await api.remove(entry.id);
              const deleted = { ...entry, ...removed, deleted: true };
              // Apply the successful response even if SSE is delayed or disconnected.
              if (onEntryChange) onEntryChange(deleted);
              else upsert(deleted);
            } catch (error) {
              toast(String(error.message || error), "error");
            } finally {
              button.disabled = false;
            }
          },
        }),
      ),
    );
    if (entry.pending) node.append(el("p", { class: "hint", text: entry.syncError ? t("送信できませんでした: {0}", entry.syncError) : t("端末に保存済み・送信待ち") }));
    if (entry.localImage) node.append(el("img", { class: "local-shot", src: entry.localImage, alt: t("キャプチャ") }));
    if (media?.blobId) {
      node.append(
        el("a", { class: "shot", href: blobURL(media.blobId), target: "_blank" },
          el("img", { src: blobURL(media.blobId), alt: media.describe || t("キャプチャ"), loading: "lazy" })),
      );
    }
    if (postEditor?.id === entry.id) node.append(postEditor.box);
    else if (entry.text) node.append(el("p", { class: "said", text: entry.text }));
    if (entry.link) {
      node.append(el("a", { class: "link", href: entry.link, target: "_blank", rel: "noreferrer", text: entry.link }));
    }
    for (const source of entry.sources || []) {
      node.append(el("a", { class: "source", href: source.url, target: "_blank", rel: "noreferrer", text: source.label || source.url }));
    }
    if (media) node.append(drawRead(media));
    return node;
  }

  function createPostEditor(entry) {
    const input = el("textarea", { class: "reply-input edit-input", rows: "3", "aria-label": t("投稿を編集") });
    input.value = entry.text || "";
    input.addEventListener("compositionstart", () => { composing = true; });
    input.addEventListener("compositionend", () => { composing = false; if (drawPending) draw(); });
    const error = el("p", { class: "hint error", role: "alert", hidden: true });
    let saving = false;
    const save = async () => {
      if (saving || composing || offline()) return;
      saving = true;
      saveButton.disabled = cancelButton.disabled = input.disabled = true;
      error.hidden = true;
      try {
        const updated = await api.edit(entry.id, input.value);
        if (postEditor === editor) postEditor = null;
        if (onEntryChange) onEntryChange(updated);
        else upsert(updated);
        draw();
      } catch (caught) {
        error.textContent = String(caught.message || caught);
        error.hidden = false;
      } finally {
        saving = false;
        saveButton.disabled = cancelButton.disabled = input.disabled = false;
        editor.updateAvailability();
      }
    };
    input.addEventListener("keydown", (event) => {
      if ((event.ctrlKey || event.metaKey) && event.key === "Enter" && !event.isComposing) {
        event.preventDefault();
        void save();
      }
    });
    const saveButton = el("button", { class: "button save-edit", label: "保存", i18nTitle: "編集はオンライン時に利用できます", onclick: () => void save() });
    const cancelButton = el("button", { class: "chip ghost cancel-edit", label: "キャンセル", onclick: () => { postEditor = null; draw(); } });
    const editor = { id: entry.id, input, updateAvailability: () => { saveButton.disabled = saving || offline(); }, box: el("div", { class: "post-editor" }, input, error,
      el("div", { class: "compose-row" }, saveButton, cancelButton)) };
    return editor;
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
    const node = el("div", { class: `said-row ${mine ? "mine" : ""}`, "data-id": entry.id });
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

  function drawReplyOpen(post) {
    // Replying is one of the two things you do here - what you write goes back
    // into the timeline and into what the personas read next.
    return el("button", {
      class: "chip reply-open",
      label: "一言足す",
      onclick: () => {
        openReply = post.id;
        expanded.add(post.id);
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
    const send = async (speechAutoSend = false) => {
      const text = input.value.trim();
      if (!text || sending || disposed) return;
      sending = true;
      try {
        await voice.discard();
        if (disposed) return;
        await api.reply(replyTo, text, speechAutoSend);
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
      onSend: () => void send(true),
    });
    const mentions = createMentionPicker({ input, getPersonas, onChange: () => drafts.set(replyTo, input.value) });
    const box = el(
      "div",
      { class: "reply" },
      input,
      mentions.element,
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

  // Read just the selected post and its replies in conversation order.
  function spoken(root) {
    const lines = [];
    for (const thread of groups().slice().reverse()) {
      const post = thread.find((entry) => entry.kind === "post");
      if (post.id !== root) continue;
      const said = thread.filter((entry) => entry !== post);
      const heading = post.text || post.media?.describe || "投稿";
      lines.push({ id: post.id, text: heading });
      for (const entry of said) {
        const persona = getPersona(entry.speaker);
        lines.push({ id: entry.id, text: `${entry.speaker === "user" ? t("あなた") : persona?.name || entry.speaker}。${entry.text}` });
      }
    }
    return lines;
  }

  // Search links should reveal the matched reply even when the timeline folds
  // conversations by default. Subsequent redraws retain the user's choice.
  function reveal(id) {
    const entry = entries.find((entry) => entry.id === id);
    if (!entry) return;
    expanded.add(entry.kind === "post" ? entry.id : entry.root);
    draw();
  }

  function setSpeaking(root) {
    speakingThread = root;
    for (const button of view.querySelectorAll(".thread-speak")) {
      const on = button.dataset.thread === root;
      button.textContent = t(on ? "■ 停止" : "🔈 読み上げ");
      button.setAttribute("aria-pressed", String(on));
    }
  }

  return { element: view, set, upsert, draw, spoken, reveal, setSpeaking, entries: () => entries };
}
