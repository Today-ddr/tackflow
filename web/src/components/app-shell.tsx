import { LogOut, Search } from "lucide-react";
import { useMutation } from "@tanstack/react-query";

import { api, type Me } from "../lib/api";
import { Button } from "./ui/button";

interface AppShellProps {
  me: Me;
  onLogout: () => void;
}

export function AppShell({ me, onLogout }: AppShellProps) {
  const logout = useMutation({
    mutationFn: () => api<void>("/auth/logout", { method: "POST" }),
    onSuccess: onLogout,
  });

  return (
    <div className="app-frame">
      <aside className="sidebar">
        <div>
          <div className="wordmark">Tack</div>
          <p className="sidebar-kicker">把念头钉住</p>
        </div>

        <nav className="sidebar-nav" aria-label="主导航">
          <button className="nav-item nav-item-active" type="button">
            <span>收件箱</span>
            <span className="count">0</span>
          </button>
          <button className="nav-item" type="button" disabled>
            <span>全部</span>
          </button>
        </nav>

        <div className="sidebar-section">
          <p className="sidebar-label">项目</p>
          <p className="sidebar-empty">还没有项目</p>
        </div>

        <div className="sidebar-footer">
          <span className="user-name">{me.user.username}</span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            <LogOut size={15} aria-hidden />
            退出
          </Button>
        </div>
      </aside>

      <main className="workspace">
        <header className="workspace-header">
          <div>
            <p className="eyebrow">收件箱</p>
            <h1>把今天钉住。</h1>
          </div>
          <Button variant="quiet" size="sm" disabled>
            <Search size={15} aria-hidden />
            搜索
            <span className="shortcut">⌘K</span>
          </Button>
        </header>

        <section className="board" aria-label="任务看板">
          <div className="column">
            <div className="column-heading">
              <h2>收件箱</h2>
              <span className="count">0</span>
            </div>
            <div className="empty-state">
              <p>先记下一件事。</p>
              <span>任务看板将在 M1 接入这张纸。</span>
            </div>
          </div>
        </section>
      </main>
    </div>
  );
}
