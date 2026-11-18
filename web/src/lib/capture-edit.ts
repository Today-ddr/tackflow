const projectToken = /@[\p{L}\p{N}_-]+/gu;
const tagToken = (name: string) =>
  new RegExp(`#${escapeRegExp(name)}(?![\\p{L}\\p{N}_-])`, "u");
const priorityToken = /!p[0-3]/giu;
const dueToken = /\^[^\s]+/gu;

export function isCaptureTokenName(value: string): boolean {
  return /^[\p{L}\p{N}_-]+$/u.test(value);
}

export function setCaptureProject(text: string, name: string | null): string {
  return replaceFirstLineToken(text, projectToken, name ? `@${name}` : "");
}

export function toggleCaptureTag(text: string, name: string): string {
  if (tagToken(name).test(firstLine(text))) {
    return replaceFirstLineToken(text, tagToken(name), "");
  }
  return appendFirstLineToken(text, `#${name}`);
}

export function setCapturePriority(text: string, priority: number): string {
  return replaceFirstLineToken(text, priorityToken, priority > 0 ? `!p${priority}` : "");
}

export function setCaptureDue(text: string, value: string | null): string {
  return replaceFirstLineToken(text, dueToken, value ? `^${value}` : "");
}

function firstLine(text: string): string {
  const end = text.indexOf("\n");
  return end === -1 ? text : text.slice(0, end);
}

function restLines(text: string): string {
  const end = text.indexOf("\n");
  return end === -1 ? "" : text.slice(end);
}

function appendFirstLineToken(text: string, token: string): string {
  const headline = firstLine(text).trimEnd();
  return [headline, token].filter(Boolean).join(" ") + restLines(text);
}

function replaceFirstLineToken(text: string, pattern: RegExp, token: string): string {
  const headline = firstLine(text);
  pattern.lastIndex = 0;
  if (!pattern.test(headline)) {
    return token ? appendFirstLineToken(text, token) : text;
  }
  pattern.lastIndex = 0;
  const next = headline.replace(pattern, " ").replace(/\s+/g, " ").trim();
  const withToken = [next, token].filter(Boolean).join(" ");
  return withToken + restLines(text);
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
