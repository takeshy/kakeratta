import { createSettings } from "../src/settings.js";
import { serve, speaker } from "./api.js";
import { sheet } from "./frame.js";
import { about, dictatingSettings, settings } from "./fixtures.js";

export default { title: "Settings", parameters: { layout: "fullscreen" } };

function panelOf(current, info = about, extra = {}) {
  serve({ settings: current, about: info, models: [settings.lowModel, settings.middleModel, settings.highModel, settings.visionModel], ...extra });
  const panel = createSettings({ speaker, onSettings: () => {} });
  void panel.load();
  return sheet("設定", panel.element);
}

export const Default = { name: "既定（Nebius・ブラウザ音声認識）", render: () => panelOf(settings) };
export const Dictating = { name: "Web検索あり・音声のAI整形ON", render: () => panelOf(dictatingSettings) };
export const InitialSetup = { name: "初回設定", render: () => panelOf({ ...settings, apiKey: "" }) };
// Once an external agent is connected, the waits before falling back show.
export const ExternalAgent = {
  name: "外部エージェント接続済み",
  render: () => panelOf({ ...settings, externalAgent: { claimMinutes: 3, planMinutes: 15 } }, about, { mcp: { configured: true, path: "/api/mcp/users/sample", agent: { away: false, lastSeen: new Date(Date.now() - 40000).toISOString() } } }),
};
