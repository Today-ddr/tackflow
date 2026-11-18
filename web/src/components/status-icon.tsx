import {
  Circle,
  CircleCheck,
  CircleDot,
  CircleEllipsis,
  Inbox,
} from "lucide-react";

import type { TaskStatus } from "../lib/api";

const statusIcons = {
  inbox: Inbox,
  todo: Circle,
  doing: CircleDot,
  review: CircleEllipsis,
  done: CircleCheck,
} satisfies Record<TaskStatus, typeof Circle>;

export function StatusIcon({
  status,
  size = 16,
}: {
  status: TaskStatus;
  size?: number;
}) {
  const Icon = statusIcons[status];
  return <Icon size={size} aria-hidden />;
}
