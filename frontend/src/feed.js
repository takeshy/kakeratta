// The timeline.
//
// Posts, remarks and replies in the order they happened. A post nobody reacted
// to looks exactly like one that started a conversation - there is no unread
// count and no "3 AIs are thinking", because most posts are meant to pass
// without anyone saying anything.
import { avatarNode, userProfile } from "./avatar.js";
import { favoriteButton } from "./favorite-state.js";
import { el, clear, toast, when } from "./dom.js";
import { api, blobURL } from "./api.js";
import { t } from "./i18n.js";
import { gloss } from "./builtins.js";
import { renderMarkdown, spokenText } from "./markdown.js";
import { htmlDocument, renderHtmlDocument } from "./html-document.js";
import { isInfographic, renderInfographic } from "./infographic.js";
import { createMentionPicker } from "./mention-picker.js";
import { openSocial } from "./social.js";
import { showRouting } from "./routing-view.js";
import { createVoiceInput } from "./voice_input.js";

// Silence is the design, but an empty screen cannot say so for itself. The note
// is for the first couple of posts only: once the timeline has a few things in
// it, the user has seen what arriving looks like and the line is just noise.
export const SILENCE_NOTE_UNTIL = 3;
export const showsSilenceNote = (threads) => threads > 0 && threads < SILENCE_NOTE_UNTIL;

// Older external replies may have stored escaped newlines before submission
// normalized them. Read those as lines without changing the saved entry.
const entryText = (entry) => entry.model === "agent" ? (entry.text || "").replace(/\\n/g, "\n") : entry.text;

export function createFeed({ getPersona, getPersonas, getSettings = () => ({}), onReply, onEntryChange, onSpeak, onFixCondition, onFixReply, onOpenNote, onSelectPersona, onSpeaker, showIntro = true, standalone = false }) {
  const view = el("div", { class: "feed" });

  // A name on the timeline opens what that speaker said. It is text where
  // nothing is wired to open the list.
  function who(entry, name, linked = true) {
    if (!onSpeaker || !linked || !entry.speaker) return el("span", { class: "who", text: name });
    return el("button", { class: "who who-link", type: "button", text: name, "aria-label": t("{0} の発言一覧", name),
      onclick: (event) => { event.stopPropagation(); onSpeaker(entry.speaker, name); } });
  }
  let entries = [];
  let speakingThread = null;
  let openReply = null;
  let replyEditor = null;
  let postEditor = null;
  const expanded = new Set();
  const drafts = new Map();
  // The router's call, while its sheet is open. Kept so a decision that
  // arrives or changes after opening - the router was still working, the post
  // was edited - reaches the sheet rather than going stale in it.
  let routingView = null;
  let composing = false;
  let drawPending = false;
  const offline = () => globalThis.navigator?.onLine === false;

  // What answers an entry, directly or through another reply. Deleting a
  // remark or a reply takes these with it, as the server does.
  function answersTo(id) {
    const under = new Set([id]);
    const found = [];
    for (let grew = true; grew;) {
      grew = false;
      for (const other of entries) {
        if (other.deleted || under.has(other.id) || !other.replyTo || !under.has(other.replyTo)) continue;
        under.add(other.id);
        found.push(other);
        grew = true;
      }
    }
    return found;
  }

  async function removeEntry(entry, button) {
    // A persona's remark goes without the notes written with it: they stay,
    // to be corrected in its memory if they were wrong too.
    const remark = entry.speaker && entry.speaker !== "user" && entry.kind !== "post";
    const answers = entry.kind === "post" ? [] : answersTo(entry.id);
    let question = remark
      ? "この回答を消しますか。検索や以降の会話からは参照されなくなります。一緒に書いたメモは残ります。"
      : "この投稿を消しますか。この投稿だけを元にした担当のメモも消えます。";
    if (answers.length) {
      question = remark
        ? "この回答と、これに返事している {0} 件の発言を消しますか。検索や以降の会話からは参照されなくなります。一緒に書いたメモは残ります。"
        : "この投稿と、これに返事している {0} 件の発言を消しますか。これらだけを元にした担当のメモも消えます。";
    }
    if (!confirm(t(question, answers.length))) return;
    button.disabled = true;
    try {
      const removed = await api.remove(entry.id);
      const deleted = [{ ...entry, ...removed, deleted: true }, ...answers.map((other) => ({ ...other, deleted: true }))];
      for (const gone of deleted) {
        if (onEntryChange) onEntryChange(gone);
        else upsert(gone);
      }
    } catch (error) {
      toast(String(error.message || error), "error");
    } finally {
      button.disabled = false;
    }
  }
  const updateEditAvailability = () => {
    for (const button of view.querySelectorAll(".edit-post")) {
      button.disabled = offline() || !!postEditor || button.dataset.pending === "true";
    }
    postEditor?.updateAvailability();
  };
  globalThis.addEventListener?.("online", updateEditAvailability);
  globalThis.addEventListener?.("offline", updateEditAvailability);

  function set(next) {
    openArrivals(next);
    entries = next;
    draw();
    // Polls and reconnects also refresh an open decision, including one first
    // opened while the router was still working.
    void refreshRouting();
  }

  // refreshRouting fetches the router's call again for the sheet that is open,
  // if any. With an id, only when that is the post the sheet is about. A post
  // that has gone takes its sheet with it.
  async function refreshRouting(id) {
    if (routingView && !routingView.isOpen()) routingView = null;
    if (!routingView || (id && routingView.entryId !== id) || offline()) return;
    const current = entries.find((entry) => entry.id === routingView.entryId);
    if (!current || current.deleted) {
      routingView.close();
      routingView = null;
      return;
    }
    await routingView.refresh(current);
  }

  // One entry arriving, from the live channel. A late remark lands under the
  // post it belongs to rather than at the bottom.
  function upsert(entry) {
    openArrivals([entry]);
    const index = entries.findIndex((existing) => existing.id === entry.id);
    if (index >= 0) entries[index] = entry;
    else entries.push(entry);
    entries.sort((a, b) => new Date(a.at) - new Date(b.at));
    draw();
    // An edited post is routed again; what the sheet shows about it is stale.
    void refreshRouting(entry.id);
  }

  // A persona's remark lands under a folded post, where it is easy to miss:
  // nothing counts it and nothing moves. So a remark that arrives while the
  // timeline is already showing unfolds its conversation. Only arrivals do
  // this - the first load and older pages leave every fold as it was, and a
  // fold closed by hand stays closed until the next remark.
  function openArrivals(next) {
    if (!entries.length) return;
    const known = new Set(entries.map((entry) => entry.id));
    let latest = 0;
    for (const entry of entries) latest = Math.max(latest, new Date(entry.at).getTime() || 0);
    for (const entry of next) {
      if (known.has(entry.id) || entry.deleted || entry.kind === "post" || !entry.root) continue;
      if (!entry.speaker || entry.speaker === "user") continue;
      if ((new Date(entry.at).getTime() || 0) < latest) continue;
      expanded.add(entry.root);
    }
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
    if (postEditor && !(standalone ? entries.some(entry => entry.id === postEditor.id) : threads.some((thread) => thread.some((entry) => entry.id === postEditor.id)))) postEditor = null;
    // Keep the same field and recognizer across live timeline updates.
    if (replyEditor && (replyEditor.replyTo !== openReply ||
        !threads.some((thread) => thread.some((entry) => entry.id === openReply)))) {
      const previous = replyEditor;
      replyEditor = null;
      void previous.dispose();
    }
    const place = holdPlace();
    clear(view);
    if (standalone) {
      for (const entry of entries.filter(entry => !entry.deleted)) view.append(entry.kind === "post" ? drawPost(entry) : drawSaid(entry, entry));
      appendPlaceCredit();
      place();
      return;
    }
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
    appendPlaceCredit();
    place();
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

  // A redraw replaces every thread, so nothing on the page is left for the
  // browser to keep its place by, and one that lays the emptied timeline out
  // midway - removing the button that was just pressed can make it - drops the
  // reader at the top. The thread at the top of the screen is noted and put
  // back where it was.
  function holdPlace() {
    if (!view.isConnected || !globalThis.window?.scrollBy) return () => {};
    const anchor = [...view.querySelectorAll(".thread")]
      .find((node) => node.getBoundingClientRect().bottom > 0);
    if (!anchor) return () => {};
    const id = anchor.dataset.id;
    const top = anchor.getBoundingClientRect().top;
    return () => {
      const again = [...view.querySelectorAll(".thread")].find((node) => node.dataset.id === id);
      if (again) window.scrollBy(0, again.getBoundingClientRect().top - top);
    };
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
      el("button", { class: "chip ghost", label: "Blueskyに投稿", disabled: !!post.pending, onclick: () => void openSocial(post.id, getPersona, getSettings) }),
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

  // The body of anything said on the timeline. A persona's reply is model
  // written and never edited here, so the app renders Markdown or a complete
  // HTML document in an isolated frame; the infographic persona's Markdown is
  // laid out as an infographic. Your own
  // words are yours: they stay exactly as typed unless the setting asks for
  // Markdown to be honoured, and editing has always meant writing the source.
  function drawBody(entry) {
    if (!entry.text) return null;
    const mine = entry.speaker === "user";
    const html = !mine && htmlDocument(entryText(entry));
    if (html) return el("div", { class: "said said-html" }, renderHtmlDocument(html));
    if (mine && !getSettings()?.markdownInput) return el("p", { class: "said", text: entry.text });
    if (entry.speaker === "infographic" && isInfographic(entryText(entry))) {
      return el("div", { class: "said said-markdown said-infographic" }, renderInfographic(entryText(entry)));
    }
    return el("div", { class: "said said-markdown" }, renderMarkdown(entryText(entry)));
  }

  function appendPlaceCredit() {
    if (!entries.some(entry => !entry.deleted && entry.kind === "post" && entry.place?.approximate && entry.place?.label)) return;
    view.append(el("footer", { class: "place-credit" },
      el("span", { label: "地名データ：" }),
      el("a", { href: "https://www.geonames.org/", target: "_blank", rel: "noreferrer", text: "GeoNames" }),
      el("span", { text: " · " }),
      el("a", { href: "https://creativecommons.org/licenses/by/4.0/", target: "_blank", rel: "noreferrer", text: "CC BY 4.0" }),
      el("span", { label: "（最寄りの町・村を抽出）" }),
    ));
  }

  function drawPost(entry) {
    const media = entry.media;
    const mine = entry.speaker === "user";
    // The month's summary is the app's, but the user's to set right.
    const summary = entry.speaker === "kakeratta" && !!entry.summary;
    const persona = getPersona(entry.speaker);
    const node = el("div", { class: `post${summary ? " summary" : ""}` });
    node.append(
      el(
        "div",
        { class: "post-head" },
        avatarNode(mine ? userProfile(getSettings()) : summary ? { icon: "🗓" } : persona || {}),
        who(entry, mine ? userProfile(getSettings()).name : summary ? t("今月のまとめ") : persona ? gloss(persona).name : entry.speaker, !summary),
        el("span", { class: "at", text: when(entry.at) }),
        drawModel(entry),
        favoriteButton(entry),
        el("span", { class: "spacer" }),
        mine || summary ? el("button", {
          class: "chip ghost icon edit-post", text: "✎", "aria-label": t("編集"), disabled: offline() || !!entry.pending || !!postEditor,
          "data-pending": String(!!entry.pending), i18nTitle: "編集はオンライン時に利用できます",
          onclick: () => {
            if (offline() || entry.pending || postEditor) return;
            postEditor = createPostEditor(entry); draw(); postEditor.input.focus();
          },
        }) : null,
        drawRoutingChip(entry),
        // Append-only means a delete is recoverable in the log, but nobody
        // wants to go and get it. It stays out of the way until reached for.
        el("button", {
          class: "chip ghost remove",
          i18nTitle: "消す",
          disabled: !!entry.pending,
          label: "×",
          onclick: (event) => void removeEntry(entry, event.currentTarget),
        }),
      ),
    );
    const place = entry.place;
    if (place && Number.isFinite(place.lat) && Number.isFinite(place.lon) && Math.abs(place.lat) <= 90 && Math.abs(place.lon) <= 180) {
      const label = place.label ? (place.approximate ? t("{0}付近", place.label) : place.label) : `${place.lat.toFixed(4)}, ${place.lon.toFixed(4)}`;
      node.append(el("div", { class: "entry-place" },
        el("span", { text: `📍 ${label}` }),
      ));
    }
    if (entry.pending) node.append(el("p", { class: "hint", text: entry.syncError ? t("送信できませんでした: {0}", entry.syncError) : t("端末に保存済み・送信待ち") }));
    if (entry.localImage) node.append(el("img", { class: "local-shot", src: entry.localImage, alt: t("キャプチャ") }));
    if (media?.blobId) {
      node.append(
        el("a", { class: "shot", href: blobURL(media.blobId), target: "_blank" },
          el("img", { src: blobURL(media.blobId), alt: media.describe || t("キャプチャ"), loading: "lazy" })),
      );
    }
    if (postEditor?.id === entry.id) node.append(postEditor.box);
    else if (entry.text) node.append(drawBody(entry));
    if (entry.link) {
      node.append(el("a", { class: "link", href: entry.link, target: "_blank", rel: "noreferrer", text: entry.link }));
    }
    for (const source of entry.sources || []) {
      node.append(el("a", { class: "source", href: source.url, target: "_blank", rel: "noreferrer", text: source.label || source.url }));
    }
    if (media) node.append(drawRead(media));
    return node;
  }

  // The router's call about a post can be looked at: who it chose, whom it
  // passed over and why. It opens apart from the timeline, since a post and
  // the reasoning about it read badly in one column, and only on what the user
  // wrote - the router never judges a persona's remark.
  function drawRoutingChip(entry) {
    if (entry.speaker !== "user" || entry.pending) return null;
    return el("button", {
      class: "chip ghost icon routing-open", text: "⚖", "aria-label": t("担当の判断"), i18nTitle: "担当の判断",
      disabled: offline(),
      onclick: async () => {
        routingView?.close();
        routingView = await showRouting({ entry, getPersona, onFixCondition, onFixReply, onSelectPersona, onOpenNote });
      },
    });
  }

  function createPostEditor(entry) {
    const input = el("textarea", { class: "reply-input edit-input", rows: entry.summary ? "12" : "3", "aria-label": t("投稿を編集") });
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

  // Ultra answers a reply that asked for a better or deeper answer, and the
  // user should be able to see that it did. Super is the default and goes
  // unmarked.
  // A remark planned by an external agent is marked too.
  function drawModel(entry) {
    if (entry.model === "agent") return el("span", { class: "model-badge", label: "外部エージェント", i18nTitle: "外部エージェントが書いた回答です" });
    if (entry.model !== "ultra") return null;
    return el("span", { class: "model-badge", text: "Ultra", i18nTitle: "より深い回答を求められたので Ultra で答えました" });
  }

  function drawSaid(entry, post) {
    const persona = getPersona(entry.speaker);
    const mine = entry.speaker === "user";
    const node = el("div", { class: `said-row ${mine ? "mine" : ""}`, "data-id": entry.id });
    node.append(
      el(
        "div",
        { class: "said-head" },
        avatarNode(mine ? userProfile(getSettings()) : persona || {}),
        who(entry, mine ? userProfile(getSettings()).name : persona ? gloss(persona).name : entry.speaker),
        el("span", { class: "at", text: when(entry.at) }),
        drawModel(entry),
        favoriteButton(entry),
        drawRoutingChip(entry),
        // A persona's remark can be removed too: a wrong answer left in the
        // thread is found again by searches and read back as context.
        el("button", {
          class: "chip ghost remove", i18nTitle: "消す", disabled: !!entry.pending, label: "×",
          onclick: (event) => void removeEntry(entry, event.currentTarget),
        }),
      ),
      drawBody(entry) || el("p", { class: "said", text: entry.text }),
    );
    if (entry.link) {
      node.append(el("a", { class: "link", href: entry.link, target: "_blank", rel: "noreferrer", text: entry.link }));
    }
    for (const source of entry.sources || []) {
      node.append(
        el("a", { class: "source", href: source.url, target: "_blank", rel: "noreferrer", text: source.label || source.url }),
      );
    }
    // A remark on a post leaves a note behind, and the remark says which; the
    // note opens from here.
    if (!mine && persona && onOpenNote && entry.notes?.length) {
      node.append(el("div", { class: "said-notes" }, ...entry.notes.map((path) => el("button", {
        class: "chip ghost said-note", text: `📝 ${path.replace(/\.md$/, "")}`, i18nTitle: "この発言と一緒に書いたメモを開く",
        onclick: () => onOpenNote({ persona, path }),
      }))));
    }
    if (!mine && !standalone) {
      // A remark can be answered, and it can be disputed: what the persona
      // said is the one place its tone and instructions show, so that is
      // where they get corrected.
      node.append(
        el("div", { class: "said-actions" },
          el("button", {
            class: "chip ghost",
            label: "返す",
            onclick: () => {
              openReply = entry.id;
              draw();
            },
          }),
          persona && onFixReply ? el("button", {
            class: "chip ghost reply-fix", label: "返し方を直す",
            onclick: () => onFixReply({ persona, remark: entry, post }),
          }) : null),
      );
    }
    if (openReply === entry.id) node.append(drawReplyBox(entry.id));
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
      setSending(true);
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
        setSending(false);
      }
    };
    // The box is cleared once the reply lands, so it holds still until then.
    const setSending = (on) => {
      sending = on;
      input.readOnly = on;
      submit.disabled = on;
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
    const submit = el("button", { class: "button send", label: "送る", onclick: () => void send() });
    const box = el(
      "div",
      { class: "reply" },
      input,
      mentions.element,
      el(
        "div",
        { class: "compose-row" },
        voice.button,
        submit,
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
      // Read aloud says the words, not the Markdown punctuation that draws
      // them.
      const heading = post.text
        ? spokenText(post.text) || post.text
        : post.media?.describe || t("投稿");
      lines.push({ id: post.id, text: heading });
      for (const entry of said) {
        const persona = getPersona(entry.speaker);
        const body = spokenText(entryText(entry)) || entryText(entry);
        lines.push({ id: entry.id, text: `${entry.speaker === "user" ? userProfile(getSettings()).name : persona ? gloss(persona).name : entry.speaker}。${body}` });
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

  return { element: view, set, upsert, draw, spoken, reveal, setSpeaking, refreshRouting, entries: () => entries };
}
