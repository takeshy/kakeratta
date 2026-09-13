import { createSettings } from "../src/settings.js";
import { serve, speaker } from "./api.js";
import { sheet } from "./frame.js";
import { about, dictatingSettings, settings } from "./fixtures.js";

export default { title: "Settings", parameters: { layout: "fullscreen" } };

function panelOf(current, info = about) {
  serve({ settings: current, about: info, models: [settings.lowModel, settings.middleModel, settings.highModel, settings.visionModel] });
  const panel = createSettings({ speaker, onSettings: () => {} });
  void panel.load();
  return sheet("設定", panel.element);
}

export const Default = { name: "既定（Nebius・ブラウザ音声認識）", render: () => panelOf(settings) };
export const Dictating = { name: "Web検索あり・音声のAI整形ON", render: () => panelOf(dictatingSettings) };
export const NoToken = {
  name: "トークン未設定の警告",
  render: () => panelOf(settings, { ...about, needsToken: false }),
};
