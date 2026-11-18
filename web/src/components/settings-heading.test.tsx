import { render, screen } from "@testing-library/react";
import { RefreshCw } from "lucide-react";
import { expect, it } from "vitest";

import { SettingsHeading } from "./settings-heading";

it("keeps the section icon beside its title and the action on the right", () => {
  render(
    <SettingsHeading icon={RefreshCw} title="更新检查">
      <button type="button">刷新</button>
    </SettingsHeading>,
  );

  const heading = screen.getByRole("heading", { name: "更新检查" });
  const icon = heading.parentElement?.querySelector("svg");
  expect(icon).not.toBeNull();
  expect(heading.parentElement).toHaveClass("settings-card-title");
  expect(screen.getByRole("button", { name: "刷新" })).toBeInTheDocument();
});
