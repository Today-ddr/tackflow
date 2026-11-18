import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

export function SettingsHeading({
  icon: Icon,
  title,
  titleID,
  children,
}: {
  icon?: LucideIcon;
  title: string;
  titleID?: string;
  children?: ReactNode;
}) {
  return (
    <div className="settings-card-heading">
      <div className="settings-card-title">
        {Icon ? <Icon size={15} aria-hidden /> : null}
        <h2 id={titleID}>{title}</h2>
      </div>
      {children}
    </div>
  );
}
