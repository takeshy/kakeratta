import "../src/styles.css";
import { setLanguage } from "../src/i18n.js";
import { setTextSize } from "../src/text-size.js";

// Both languages come out of the same source strings, so a story that reads
// well in one and badly in the other is worth seeing without a rebuild.
export default {
  globalTypes: {
    textSize: {
      description: "文字サイズ",
      toolbar: {
        icon: "zoom",
        items: [
          { value: "standard", title: "標準" },
          { value: "large", title: "大" },
          { value: "xlarge", title: "特大" },
        ],
        dynamicTitle: true,
      },
    },
    lang: {
      description: "表示言語",
      toolbar: {
        icon: "globe",
        items: [
          { value: "ja", title: "日本語" },
          { value: "en", title: "English" },
        ],
        dynamicTitle: true,
      },
    },
  },
  initialGlobals: { lang: "ja", textSize: "standard" },
  decorators: [
    (story, context) => {
      setLanguage(context.globals.lang || "ja");
      document.documentElement.lang = context.globals.lang || "ja";
      setTextSize(context.globals.textSize || "standard");
      return story();
    },
  ],
  parameters: {
    layout: "fullscreen",
    options: { storySort: { order: ["Screen", "Room", "Compose", "Timeline", "Search"] } },
  },
};
