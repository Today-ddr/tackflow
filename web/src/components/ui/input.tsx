import type * as React from "react";

import { cn } from "../../lib/utils";

export function Input({ className, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      className={cn(
        "h-11 w-full rounded-[10px] border border-[var(--line)] bg-[var(--surface)] px-3.5 text-[15px] text-[var(--ink)] outline-none transition-colors duration-150 ease-out placeholder:text-[var(--muted)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[var(--accent-soft)]",
        className,
      )}
      {...props}
    />
  );
}
