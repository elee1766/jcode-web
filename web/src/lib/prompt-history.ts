// Backend transcript history is authoritative. Local history also retains web
// prompts before an upstream snapshot catches up.
export function promptHistory(id: string, transcript: string[]): string[] {
  let saved: string[] = [];
  try {
    const value = JSON.parse(localStorage.getItem(`jw.prompts.${id}`) ?? "[]");
    if (Array.isArray(value)) saved = value.filter(v => typeof v === "string");
  } catch { /* Ignore unavailable or corrupt local storage. */ }
  return [...new Set([...transcript, ...saved.filter(p => !transcript.includes(p))].filter(Boolean).reverse())].slice(0, 100);
}

export function rememberPrompt(id: string, text: string) {
  try {
    const history = promptHistory(id, []).filter(p => p !== text);
    localStorage.setItem(`jw.prompts.${id}`, JSON.stringify([...history.reverse(), text].slice(-100)));
  } catch { /* Sending a message must not depend on local storage. */ }
}
