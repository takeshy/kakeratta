// The dictation modules were written for speech-popup, where strings are
// authored in Japanese and translated on the way out. This app is Japanese
// only, so the source string is the string - this keeps the ported modules
// unchanged rather than editing their messages one by one.
export function t(source, ...args) {
  return String(source).replace(/\{(\d+)\}/g, (token, index) =>
    index < args.length ? String(args[index]) : token
  );
}

export const getLanguage = () => "ja";

// The ported modules and their tests switch language; this app is Japanese
// only, so the call is accepted and changes nothing.
export function setLanguage() {}

export function resolveLanguage() {
  return "ja";
}
