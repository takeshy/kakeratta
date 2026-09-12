// The timeline.
//
// Posts, remarks and replies in the order they happened. A post nobody reacted
// to looks exactly like one that started a conversation - there is no unread
// count and no "3 AIs are thinking", because most posts are meant to pass
// without anyone saying anything.
import { el, clear, toast, when } from "./dom.js";
import { api, blobURL } from "./api.js";

export function createFeed({ getPersona, onReply }) {
  const view = el("div", { class: "feed" });
  let entries = [];
  let openReply = null;

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
    clear(view);
    const threads = groups();
    if (!threads.length) {
      view.append(
        el("p", {
          class: "empty",
          text: "まだ何もありません。気になった画面を撮って貼ってください。反応は遅れて届きます。",
        }),
      );
      return;
    }
    for (const thread of threads) view.append(drawThread(thread));
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
        el("span", { class: "at", text: when(entry.at) }),
        entry.place ? el("span", { class: "place", text: placeLabel(entry.place) }) : null,
        el("span", { class: "spacer" }),
        el("button", {
          class: "chip ghost",
          title: "消す",
          text: "×",
          onclick: async () => {
            if (!confirm("この投稿を消しますか")) return;
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
          el("img", { src: blobURL(media.blobId), alt: media.describe || "キャプチャ", loading: "lazy" })),
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
    if (media.error) return el("p", { class: "read error", text: `読み取れませんでした: ${media.error}` });
    if (!media.readAt) return el("p", { class: "read pending", text: "読み取り中…" });
    const summary = media.describe || (media.text ? `${[...media.text].length}文字` : "読み取り結果");
    const body = el("div", { class: "read-body" });
    if (media.origin) body.append(el("p", { class: "origin", text: `出典の手がかり: ${media.origin}` }));
    if (media.text) body.append(el("pre", { class: "ocr", text: media.text }));
    if (!body.children.length) body.append(el("p", { class: "hint", text: "文字は読み取れませんでした。" }));
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
        el("span", { class: "who", text: mine ? "あなた" : persona?.name || entry.speaker }),
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
          text: "返す",
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
    return el("button", {
      class: "chip ghost reply-open",
      text: "一言足す",
      onclick: () => {
        openReply = post.id;
        draw();
      },
    });
  }

  // A reply is not a command: it goes back into the timeline and the personas
  // see it next time, the same as anything else.
  function drawReplyBox(replyTo) {
    const input = el("textarea", { class: "reply-input", rows: "2", placeholder: "返事や、@名前 で指名" });
    const send = async () => {
      const text = input.value.trim();
      if (!text) return;
      try {
        await api.reply(replyTo, text);
        openReply = null;
        onReply?.();
      } catch (error) {
        toast(String(error.message || error), "error");
      }
    };
    input.addEventListener("keydown", (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
        event.preventDefault();
        void send();
      }
    });
    const box = el(
      "div",
      { class: "reply" },
      input,
      el(
        "div",
        { class: "compose-row" },
        el("button", { class: "button primary", text: "送る", onclick: () => void send() }),
        el("button", {
          class: "chip ghost",
          text: "やめる",
          onclick: () => {
            openReply = null;
            draw();
          },
        }),
      ),
    );
    setTimeout(() => input.focus(), 0);
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
