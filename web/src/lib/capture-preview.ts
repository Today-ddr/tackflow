export interface CapturePreview {
  title: string;
  tags: string[];
  project: string;
  priority: number;
  due: string;
}

const tagPattern = /#([\p{L}\p{N}_-]+)/gu;
const projectPattern = /@([\p{L}\p{N}_-]+)/u;
const priorityPattern = /!p([0-3])/iu;
const duePattern = /\^([^\s]+)/u;

export function previewCapture(text: string): CapturePreview {
  const firstLine = text.split(/\r?\n/, 1)[0] ?? "";
  const tags = Array.from(firstLine.matchAll(tagPattern), (match) => match[1]);
  return {
    title: firstLine
      .replace(tagPattern, " ")
      .replace(projectPattern, " ")
      .replace(priorityPattern, " ")
      .replace(duePattern, " ")
      .replace(/\s+/g, " ")
      .trim(),
    tags,
    project: firstLine.match(projectPattern)?.[1] ?? "",
    priority: Number(firstLine.match(priorityPattern)?.[1] ?? 0),
    due: firstLine.match(duePattern)?.[1] ?? "",
  };
}
