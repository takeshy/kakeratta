// The stories load the app's own modules and the app's own stylesheet, so what
// is reviewed here is the thing that ships, not a copy of it.
export default {
  stories: ["../stories/**/*.stories.js"],
  framework: { name: "@storybook/html-vite", options: {} },
};
