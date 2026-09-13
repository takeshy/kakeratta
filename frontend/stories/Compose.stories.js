import { compose, panel } from "./frame.js";

export default { title: "Compose" };

export const Empty = { name: "空（1行）", render: () => panel(compose().element) };

export const Written = {
  name: "書きかけ（伸びる）",
  render: () => panel(compose({
    text: "喫茶店の音がうるさいのに集中できるのはなぜなのか。\n静かすぎると逆に落ち着かない、というのは前にも思った気がする。\nあとで しおり に聞く。",
  }).element),
};

export const Mentioned = {
  name: "担当を指名した直後",
  render: () => panel(compose({ text: "@みち " }).element),
};
