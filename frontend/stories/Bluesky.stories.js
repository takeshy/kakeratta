import { createBlueskyConnection } from "../src/bluesky.js";
import { openSocial } from "../src/social.js";
import { getLanguage, t } from "../src/i18n.js";
import { serve } from "./api.js";
import { screen, sheet } from "./frame.js";

export default { title: "Bluesky", parameters: { layout: "fullscreen" } };

function sample() {
  const ja = getLanguage() === "ja";
  const rows = [{ id: "tabe", name: "tabe", icon: "🍚", active: true }, { id: "gout", name: "gout", icon: "🩺", active: true }];
  const lines = ja ? [
    "今日の麻婆豆腐、かなり辛かったけどうまかった。",
    "辛さの中に花椒の香りもありそうですね。ご飯が進みそうです。",
    "食事で気をつけたいことも、必要なら一緒に確認しましょう。",
    "花椒の香りがよかった。また食べたい。",
  ] : [
    "Today's mapo tofu was really spicy, but delicious.",
    "It looks like the Sichuan pepper adds fragrance as well as heat. A good match for rice.",
    "We can also go over any food concerns if you'd like.",
    "I loved the Sichuan pepper aroma. I'd have it again.",
  ];
  const entries = [
    { id: "sample-post", root: "sample-post", kind: "post", speaker: "user", text: lines[0] },
    { id: "sample-tabe", root: "sample-post", replyTo: "sample-post", kind: "remark", speaker: "tabe", text: lines[1] },
    { id: "sample-gout", root: "sample-post", replyTo: "sample-post", kind: "remark", speaker: "gout", text: lines[2] },
    { id: "sample-user", root: "sample-post", replyTo: "sample-tabe", kind: "reply", speaker: "user", text: lines[3] },
  ].map((entry, i) => ({ ...entry, at: new Date(Date.now() - (10 - i) * 60000).toISOString() }));
  const posts = entries.map((entry) => ({ entryId: entry.id, parent: entry.replyTo || "", text: entry.speaker === "user"
    ? entry.text : `${rows.find((row) => row.id === entry.speaker).icon} ${entry.speaker}からの返信:\n${entry.text}` }));
  return { rows, entries, posts, root: "sample-post" };
}

export const Connect = {
  name: "自分のBlueskyアカウントを接続",
  render: () => {
    document.querySelectorAll(".social-dialog").forEach((dialog) => dialog.close());
    serve();
    return sheet("Bluesky連携", createBlueskyConnection().element);
  },
};

function conversation(preview, posted = false) {
  document.querySelectorAll(".social-dialog").forEach((dialog) => dialog.close());
  const data = sample();
  if (posted) data.posts = data.posts.map(post => ({ ...post, publication: { state: "posted", url: "https://bsky.app/profile/reader.example.com/post/sample" } }));
  serve({ social: data, bluesky: { available: true, connected: true, did: "did:plc:sample", handle: "reader.example.com" } });
  const host = screen({ rows: data.rows, entries: data.entries });
  // The production dialog is opened after Storybook mounts the background.
  requestAnimationFrame(async () => {
    if (!host.isConnected) return;
    await openSocial(data.root, (id) => data.rows.find((row) => row.id === id));
    const dialog = document.querySelector(".social-dialog");
    if (!dialog || !preview) return;
    const checks = dialog.querySelectorAll('.social-selection input[type="checkbox"]');
    for (const index of [1, 3]) {
      checks[index].checked = true;
      checks[index].dispatchEvent(new Event("change", { bubbles: true }));
    }
    // Let the fixture connection load before using the real preview action.
    const button = [...dialog.querySelectorAll("button")].find((node) => node.textContent === t("プレビュー"));
    const ready = () => {
      if (!button.disabled) { observer.disconnect(); button.click(); }
    };
    const observer = new MutationObserver(ready);
    observer.observe(button, { attributes: true, attributeFilter: ["disabled"] });
    ready();
  });
  return host;
}

export const Selection = { name: "元投稿だけを初期選択", render: () => conversation(false) };
export const Preview = { name: "公開する会話と返信先を確認", render: () => conversation(true) };

export const Posted = { name: "投稿済み・親投稿へのリンク", render: () => conversation(true, true) };
