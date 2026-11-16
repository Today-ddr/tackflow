import type { ProjectColor } from "../lib/api";

export function ProjectDot({
  color,
  size = "normal",
}: {
  color: ProjectColor;
  size?: "small" | "normal";
}) {
  return (
    <span
      className={`project-dot project-dot-${size} color-${color}`}
      aria-hidden="true"
    />
  );
}
