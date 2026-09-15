import { applyReplacementRules } from "./replacements.js";
import { trailingCommand } from "./speech_commands.js";
// Turns browser speech recognition results into draft text: send phrases,
// spoken symbols, and replacement rules. Pure, so it runs under plain node
// (see ../../tests/speech.test.js).

// speechDraft appends the transcript to whatever is already in the box and
// reports whether the user spoke a send phrase ("over"), which copies & closes.
export function speechDraft(base, transcript, final, sendPhrase = "終わります", _afterSilence = false, normalized = false, symbolCommands, replacements) {
  const phrases = sendPhrase.split(/[,、\n]/).map((phrase) => phrase.trim())
    .filter(Boolean).sort((a, b) => b.length - a.length);
  const pattern = phrases.map((phrase) => {
    const escaped = phrase.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    // Word-based scripts need a Unicode boundary (e.g. terminé must not
    // match the end of indéterminé). Scripts commonly written without spaces
    // still allow a command immediately after the dictated text.
    const unspaced = /^[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Thai}\p{Script=Lao}\p{Script=Khmer}\p{Script=Myanmar}\p{Script=Tibetan}]/u.test(phrase);
    return (!unspaced && /^[\p{L}\p{N}_]/u.test(phrase) ? "(?<![\\p{L}\\p{N}\\p{M}_])" : "") + escaped;
  }).join("|");
  const command = pattern ? new RegExp(`(?:${pattern})[\\s。．.!！?？、,،؛؟।॥]*$`, "iu") : null;
  const send = final && !!command && command.test(transcript);
  let spoken = send && command ? transcript.replace(command, "").trimEnd() : transcript;
  if (final && !normalized) spoken = convertSpokenSymbol(spoken, !send, symbolCommands);
  // After the symbols, so a rule may produce text the symbol commands would
  // otherwise have eaten, and only once the text is final: a partial transcript
  // would replace half a phrase and never see the rest of it.
  if (final && !normalized) spoken = applyReplacementRules(spoken, replacements);
  if (final) {
    spoken = spoken.replace(/[。．.۔।॥։]+[ \t　]*(?=[!?！？؟])/g, "");
    // A separately dictated mark replaces the full stop immediately before
    // the caret too, without changing earlier sentences or crossing a newline.
    if (/^[ \t　]*[!?！？؟]/.test(spoken) && /[。．.۔।॥։]+[ \t　]*$/.test(base)) {
      base = base.replace(/[。．.۔।॥։]+[ \t　]*$/, "");
      spoken = spoken.replace(/^[ \t　]+/, "");
    }
  }
  return {
    text: base + (base && spoken && !/\s$/.test(base) && !/^[\s,.?!、。？！؟]/.test(spoken) ? " " : "") + spoken,
    send
  };
}

// Configured symbol commands convert after recognition is final.
// Preserve punctuation produced by the recognizer, including trailing 。 and 、.
export function convertSpokenSymbol(text, commands = true, phrases = {
  question: "クエスチョン, クエスチョンマーク, question, question mark",
  newline: "エンター, enter"
}) {
  if (!commands) return text;
  const outputs = { question: "?", newline: "\n", exclamation: "!" };
  const matches = Object.entries(outputs)
    .map(([key, symbol]) => ({ key, symbol, match: trailingCommand(text, phrases[key] ?? "") }))
    .filter(entry => entry.match)
    .sort((a, b) => a.match.index - b.match.index);
  if (!matches.length) return text;
  const { key, symbol, match } = matches[0];
  const prefix = text.slice(0, match.index);
  return prefix.replace(key === "question" || key === "exclamation" ? /[。．.۔।॥։ \t　]+$/ : /[ \t　]+$/, "") + symbol;
}
