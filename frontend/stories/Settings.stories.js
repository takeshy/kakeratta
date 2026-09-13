import { createSettings } from "../src/settings.js";
import { serve, speaker } from "./api.js";
import { sheet } from "./frame.js";
import { about, dictatingSettings, settings } from "./fixtures.js";

export default { title: "Settings", parameters: { layout: "fullscreen" } };

function panelOf(current, info = about) {
  serve({ settings: current, about: info, models: ["nvidia/NVIDIA-Nemotron-Nano-9B-v2", "nvidia/Llama-3_3-Nemotron-Super-49B-v1_5"] });
  const panel = createSettings({ speaker, onSettings: () => {} });
  void panel.load();
  return sheet("設定", panel.element);
}

export const Default = { name: "既定（Nebius・ブラウザ音声認識）", render: () => panelOf(settings) };
export const Dictating = { name: "Web検索あり・録音して送る音声認識", render: () => panelOf(dictatingSettings) };
export const NoToken = {
  name: "トークン未設定の警告",
  render: () => panelOf(settings, { ...about, needsToken: false }),
};
