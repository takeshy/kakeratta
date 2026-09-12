// The chat beside the book.
//
// One thread carries everything: what you asked, what the app answered, and
// what a companion said without being asked. A persona's remark arrives late
// and lands in the same place as an answer, which is what makes it feel like
// someone reading alongside rather than a notification.
//
// Nothing is ever written into the body text. The page stays the page.
import { clear, el, toast } from "./dom.js";
import { createMicButton } from "./micbutton.js";
import { dictationSupported } from "./dictation.js";

const modeLabels = {
  person: "人物",
  scene: "場面",
  recap: "あらすじ",
  current: "いまの文脈",
  review: "振り返り",
  technical: "技術書",
  persona: "",
  say: "",
};

export function createChatPanel({ api, reader, getSettings }) {
  const body = el("div", { class: "chat" });
  let turns = [];
  let busy = false;
  let opened = null;
  let draft = "";

  const personaOf = (id) => (opened?.personas || []).find((plugin) => plugin.id === id);

  async function load(next) {
    opened = next || opened;
    if (!opened) return;
    try {
      turns = (await api.conversation(opened.book.id)) || [];
    } catch (error) {
      toast(String(error), "error");
    }
    draw();
  }

  // Called when a persona speaks up on its own: the thread has changed without
  // anyone asking for it.
  async function refresh() {
    if (!opened) return;
    try {
      turns = (await api.conversation(opened.book.id)) || [];
      draw();
    } catch {
      /* the thread will be redrawn on the next event */
    }
  }

  async function send(text, { ask = true } = {}) {
    if (!opened || busy) return;
    if (!String(text || "").trim() && !ask) return;
    busy = true;
    draft = "";
    draw();
    try {
      turns = (ask ? await api.ask(opened.book.id, text) : await api.say(opened.book.id, text)) || turns;
    } catch (error) {
      toast(String(error), "error");
    } finally {
      busy = false;
      draw();
    }
  }

  // An answer given earlier was given at an earlier boundary. Saying so is the
  // point of the app: the same question has a different answer further in.
  function boundaryNote(turn) {
    if (!opened || turn.role === "user") return null;
    if (opened.book.category === "technical") return null;
    const end = opened.book.end.absolute || 1;
    const boundary = farthest();
    if (turn.boundary.absolute >= boundary - 1) return null;
    return `${Math.round((turn.boundary.absolute / end) * 100)}% 時点の答え`;
  }

  function farthest() {
    const entries = opened?.state?.entries || [];
    return entries.reduce(
      (far, entry) => Math.max(far, entry.range.end.absolute),
      opened?.state?.furthest?.absolute || 0,
    );
  }

  function speakerName(turn) {
    if (turn.role === "user") return "あなた";
    const plugin = personaOf(turn.speaker);
    return plugin ? plugin.name : "この本について";
  }

  function draw() {
    clear(body);
    const thread = el("div", { class: "thread" });
    for (const turn of turns) {
      const note = boundaryNote(turn);
      const plugin = personaOf(turn.speaker);
      const label = modeLabels[turn.mode] ?? turn.mode;
      thread.append(
        el(
          "div",
          { class: `turn turn-${turn.role} ${plugin ? "turn-persona" : ""}` },
          el(
            "div",
            { class: "turn-head" },
            plugin ? el("span", { class: "avatar", text: plugin.icon || "◎" }) : null,
            el("span", { class: "who", text: speakerName(turn) }),
            turn.role === "user" || !label ? null : el("span", { class: "turn-mode", text: label }),
          ),
          el("div", { class: "answer", text: turn.text }),
          // Every message can take the reader back to the text it is about.
          turn.anchor && turn.anchor.absolute
            ? el("button", {
              class: "anchor",
              text: "原文へ",
              onclick: () => reader.goTo(turn.anchor),
            })
            : null,
          note ? el("span", { class: "at", text: note }) : null,
          (turn.sources || []).map((source) =>
            el(
              "button",
              { class: "source", onclick: () => reader.goTo(source.position) },
              el("span", { class: "label", text: source.label }),
              source.snippet ? el("span", { class: "snippet", text: source.snippet }) : null,
            )
          ),
        ),
      );
    }
    if (!turns.length) {
      thread.append(
        el("p", {
          class: "hint",
          text: opened?.book?.category === "technical"
            ? "本の全体を読んだうえで答えます。書かれた後に変わったことは、出典つきで伝えます。"
            : "読んだところまでの話しかしません。進めば答えも変わります。空のまま送ると、ここまでのあらすじを思い出します。",
        }),
      );
    }
    if (busy) thread.append(el("p", { class: "status", text: "考えています…" }));
    body.append(thread);
    thread.scrollTop = thread.scrollHeight;

    const input = el("textarea", {
      class: "ask-input",
      placeholder: (opened?.personas || []).length
        ? "この人だれだっけ／鍵を拾った場面はどこ／この人あやしい"
        : "この人だれだっけ／鍵を拾った場面はどこ／ここまでのあらすじ",
      rows: "2",
    });
    input.value = draft;
    input.addEventListener("input", () => {
      draft = input.value;
    });
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        void send(input.value);
      }
    });
    // Asking out loud matters here: the reader may be listening with the book
    // in one hand. The send phrase ("over") asks the question.
    const mic = dictationSupported(getSettings?.())
      ? createMicButton({
        api,
        getSettings,
        getText: () => input.value,
        setText: (value) => {
          input.value = value;
          draft = value;
        },
        onSend: (value) => void send(value),
        label: "話して聞く",
      })
      : null;
    body.append(
      input,
      el(
        "div",
        { class: "row" },
        el("button", {
          class: "button primary",
          text: busy ? "…" : "聞く",
          onclick: () => void send(input.value),
        }),
        // Thinking out loud is not a question. A remark goes to the companions
        // and waits for whichever of them has something to say.
        (opened?.personas || []).length
          ? el("button", {
            class: "button",
            text: "つぶやく",
            title: "答えを求めずに書き残す。ペルソナが反応することがあります",
            onclick: () => void send(input.value, { ask: false }),
          })
          : null,
        mic?.element,
        turns.length
          ? el("button", {
            class: "button ghost",
            text: "会話を消す",
            onclick: async () => {
              if (!confirm("この本についての会話を消しますか（メモと記録は残ります）")) return;
              await api.clearConversation(opened.book.id);
              turns = [];
              draw();
            },
          })
          : null,
      ),
    );
  }

  return {
    element: body,
    draw: (next) => (next && next !== opened ? load(next) : draw()),
    load,
    refresh,
  };
}
