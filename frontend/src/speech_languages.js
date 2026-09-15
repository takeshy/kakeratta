import { t, getLanguage } from "./i18n.js";

// Browser speech recognition cannot advertise its installed languages, so offer
// broad BCP-47 suggestions and let users enter any other code.
const BROWSER = `af-ZA am-ET ar-EG hy-AM as-IN az-AZ be-BY bn-BD bn-IN bs-BA bg-BG
  rup-BG my-MM yue-Hant-HK ca-ES ceb km-KH hr-HR cs-CZ da-DK nl-NL en-GB en-IN
  en-US et-EE fa-IR fil-PH fi-FI fr-FR gl-ES ka-GE de-DE el-GR gu-IN ha-NG he-IL
  hi-IN hu-HU is-IS id-ID it-IT ja-JP jv-ID kea-CV kn-IN kk-KZ ko-KR ky-KG lv-LV
  ln-CD lt-LT mk-MK ms-MY ml-IN mt-MT cmn-Hans-CN mr-IN mn-MN ne-NP nb-NO or-IN
  pl-PL pt-BR pt-PT pa-IN pa-Guru-IN ro-RO ru-RU sr-RS sd-Arab-IN sk-SK sl-SI
  es-419 es-US sw-KE sv-SE tg-TJ te-IN th-TH tr-TR uk-UA uz-UZ vi-VN
  es-ES zh-CN zh-TW ta-IN`.split(/\s+/);

export function speechLanguageCodes() {
  return [...new Set(BROWSER)];
}

export function speechLanguageLabel(code, locale = getLanguage()) {
  try {
    const displayCode = code === "jw" ? "jv" : code;
    const localized = new Intl.DisplayNames([locale], { type: "language" }).of(displayCode);
    const native = new Intl.DisplayNames([displayCode], { type: "language" }).of(displayCode);
    return `${localized === native ? localized : `${localized} / ${native}`} (${code})`;
  } catch {
    return code;
  }
}

export function speechLanguageOptions(current = "auto") {
  const options = speechLanguageCodes()
    .map(value => ({ value, label: speechLanguageLabel(value) }))
    .sort((a, b) => a.label.localeCompare(b.label, getLanguage()));
  // Preserve legacy tags and custom codes exactly.
  if (current && current !== "auto" && !options.some(option => option.value === current)) {
    options.unshift({ value: current, label: speechLanguageLabel(current) });
  }
  options.unshift({ value: "auto", label: t("WebView の言語を使用") });
  options.push({ value: "custom", label: t("その他（言語コード指定）") });
  return options;
}
