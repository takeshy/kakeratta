import "../src/styles.css";
import { setLanguage } from "../src/i18n.js";

// Both languages come out of the same source strings, so a story that reads
// well in one and badly in the other is worth seeing without a rebuild.
export default {
  globalTypes: {
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
  initialGlobals: { lang: "ja" },
  decorators: [
    (story, context) => {
      setLanguage(context.globals.lang || "ja");
      document.documentElement.lang = context.globals.lang || "ja";
      return story();
    },
  ],
  parameters: {
    layout: "fullscreen",
    options: { storySort: { order: ["Screen", "Room", "Compose", "Timeline", "Search"] } },
  },
};
