import { sampleAvatar } from "./avatar.js";
import { createSettings } from "../src/settings.js";
import { serve, speaker } from "./api.js";
import { sheet } from "./frame.js";
import { about, dictatingSettings, settings } from "./fixtures.js";

export default { title: "Settings", parameters: { layout: "fullscreen" } };

function panelOf(current, info = about, extra = {}, after) {
  serve({ settings: current, about: info, models: [settings.lowModel, settings.middleModel, settings.highModel, settings.visionModel], ...extra });
  // As on the hosted app, where an account can be closed; closing it here
  // only says so.
  const panel = createSettings({ speaker, onSettings: () => {}, closeAccount: async () => { console.info("closeAccount (story)"); } });
  void panel.load().then(() => after?.(panel.element));
  return sheet("設定", panel.element);
}

export const Default = { name: "既定（Nebius・ブラウザ音声認識）", render: () => panelOf(settings) };
export const WithUsage = {
  name: "API利用・金額あり（サンプル）",
  render: () => panelOf(settings, about, { usage: {
    month: "2026-10", first: "2026-10-01T09:00:00Z",
    rows: [
      { component: "routing", category: "other", calls: 60, estimatedUsd: 0.0324, searches: 0 },
      { component: "persona", persona: "book", category: "reply", calls: 42, estimatedUsd: 0.3248, reportedUsd: 0.12, searches: 18 },
      { component: "persona", persona: "book", category: "scheduled", calls: 16, estimatedUsd: 0.0862, searches: 3 },
      { component: "persona", persona: "book", category: "other", calls: 4, estimatedUsd: 0.0126, searches: 0 },
      { component: "persona", persona: "agent", category: "reply", calls: 6, estimatedUsd: 0.0418, searches: 4 },
      { component: "other", category: "other", calls: 8, estimatedUsd: 0.024, searches: 0 },
      { component: "embedding", category: "other", calls: 60, estimatedUsd: 0.0012, searches: 0 },
    ],
  } }, element => {
    const section = element.querySelector(".settings-usage");
    section.querySelector('input[type="month"]').value = "2026-10";
    section.open = true;
    section.scrollIntoView({ block: "start" });
  }),
};
export const ExtraLarge = {
  name: "特大文字（スマホ幅の確認）",
  globals: { textSize: "xlarge" },
  render: () => panelOf(settings),
};
export const DailyTime = { name: "毎日の振り返りの時刻を指定", render: () => panelOf({ ...settings, dailyTime: "07:30" }) };
export const Dictating = { name: "Web検索あり・音声のAI整形ON", render: () => panelOf(dictatingSettings) };
export const InitialSetup = { name: "初回設定", render: () => panelOf({ ...settings, apiKey: "" }) };
// Once an external agent is connected, the waits before falling back show.
export const ExternalAgent = {
  name: "外部エージェント接続済み",
  render: () => panelOf({ ...settings, externalAgent: { claimMinutes: 3, planMinutes: 15 } }, about, { mcp: { configured: true, path: "/api/mcp/users/sample", agent: { away: false, lastSeen: new Date(Date.now() - 40000).toISOString() } } }),
};

export const MultipleExternalAgents = {
  name: "外部エージェントの切り替え",
  render: () => panelOf(settings, about, { mcp: {
    configured: true, path: "/api/mcp/users/sample",
    connections: [{ id: "default", name: "Hermes", active: true, claimMinutes: 3, planMinutes: 15 }, { id: "other", name: "My research agent", active: false, claimMinutes: 10, planMinutes: 60 }],
    agent: { away: false, lastSeen: new Date().toISOString() },
  } }),
};

export const ProfilePhoto = { name: "名前・写真のプロフィール", render: () => panelOf({ ...settings, profile: { name: "佐藤", icon: "🌸", avatar: sampleAvatar("S") } }) };
