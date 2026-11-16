export type Theme = "light" | "dark" | "system";

const storageKey = "tack-theme";

export function storedTheme(): Theme {
  const value = window.localStorage.getItem(storageKey);
  if (value === "light" || value === "dark" || value === "system") {
    return value;
  }
  return "system";
}

export function applyTheme(theme: Theme): void {
  const resolved =
    theme === "system"
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light"
      : theme;
  document.documentElement.dataset.theme = resolved;
  document.documentElement.dataset.themePreference = theme;
  window.localStorage.setItem(storageKey, theme);
}

export function applyStoredTheme(): void {
  applyTheme(storedTheme());
}
