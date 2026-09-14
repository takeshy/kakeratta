// Fast local routing, not an AI request. Ambiguous queries can be switched
// from the results screen, so a short name keeps the inexpensive default.
export function initialSearchMode(value) {
  const query = value.normalize("NFKC").trim();
  if (/[?？!！。\n\r]/u.test(query)) return "hybrid";
  if (/(教えて|おしえて|探して|さがして|調べて|しらべて|知りたい|思い出したい|どこ(?:で|の|に)?|どんな|いつ(?:の|食べ|飲ん|行っ)?|なぜ|どうして|について|だった|でした|ました|かった|食べた|飲んだ|買った|行った|読んだ|見た|使った|くて|ような)/u.test(query)) return "hybrid";
  if (/\b(what|where|when|why|how|which|who|find|tell|show|remember|recommend)\b/i.test(query)) return "hybrid";
  const words = query.split(/\s+/u);
  if (words.length >= 5) return "hybrid";
  if (/[\p{Script=Hiragana}\p{Script=Han}]/u.test(query) && [...query].length > 20) return "hybrid";
  return "keyword";
}
