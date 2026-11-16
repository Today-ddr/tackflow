import { useCallback, useEffect, useState } from "react";

function currentPath(): string {
  return window.location.pathname || "/";
}

export function usePathname(): [string, (path: string) => void] {
  const [path, setPath] = useState(currentPath);

  useEffect(() => {
    const onPopState = () => setPath(currentPath());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const navigate = useCallback((nextPath: string) => {
    if (nextPath === currentPath()) {
      return;
    }
    window.history.pushState({}, "", nextPath);
    window.dispatchEvent(new PopStateEvent("popstate"));
    window.scrollTo(0, 0);
  }, []);

  return [path, navigate];
}
