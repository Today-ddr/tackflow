import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Inbox,
  LayoutGrid,
  LogOut,
  Plus,
  Search,
  Settings,
} from "lucide-react";

import {
  api,
  errorMessage,
  type Me,
  type Project,
  type Tag,
  type TasksResponse,
  type UpdateStatus,
} from "../lib/api";
import type { BoardFilter } from "../lib/format";
import { fetchAllTasks } from "../lib/tasks";
import { useTaskEvents } from "../app/task-events";
import { Board } from "./board";
import { CapturePage } from "./capture-page";
import { CommandPalette } from "./command-palette";
import { ProjectDialog } from "./project-dialog";
import { ProjectDot } from "./project-dot";
import { SettingsPage } from "./settings-page";
import { TaskSheet } from "./task-sheet";

interface AppShellProps {
  me: Me;
  path: string;
  navigate: (path: string) => void;
  onLogout: () => void;
}

export function AppShell({ me, path, navigate, onLogout }: AppShellProps) {
  useTaskEvents(me.auth_kind === "session");
  const [filter, setFilter] = useState<BoardFilter>({ kind: "all" });
  const [query, setQuery] = useState("");
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [taskID, setTaskID] = useState<number | null>(null);
  const [projectOpen, setProjectOpen] = useState(false);

  const tasksQuery = useQuery<TasksResponse>({
    queryKey: ["tasks"],
    queryFn: ({ signal }) => fetchAllTasks(signal),
  });
  const projectsQuery = useQuery<{ projects: Project[] }>({
    queryKey: ["projects"],
    queryFn: ({ signal }) => api<{ projects: Project[] }>("/projects", { signal }),
  });
  const tagsQuery = useQuery<{ tags: Tag[] }>({
    queryKey: ["tags"],
    queryFn: ({ signal }) => api<{ tags: Tag[] }>("/tags", { signal }),
  });
  const updateQuery = useQuery<UpdateStatus>({
    queryKey: ["update-status"],
    queryFn: () => api<UpdateStatus>("/update/status"),
    refetchInterval: 60_000,
  });

  const tasks = tasksQuery.data?.tasks ?? [];
  const projects = projectsQuery.data?.projects ?? [];
  const tags = tagsQuery.data?.tags ?? [];
  const inboxCount = tasks.filter((task) => task.status === "inbox").length;
  const isSettings = path.startsWith("/settings");
  const isCapture = path === "/capture";

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || document.querySelector("dialog[open]")) {
        return;
      }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((value) => !value);
        return;
      }
      const target = event.target as HTMLElement | null;
      const typing =
        target?.tagName === "INPUT" ||
        target?.tagName === "TEXTAREA" ||
        target?.tagName === "SELECT" ||
        target?.isContentEditable;
      if (!typing && !event.metaKey && !event.ctrlKey && event.key.toLowerCase() === "n") {
        event.preventDefault();
        navigate("/capture");
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [navigate]);

  useEffect(() => {
    if (path !== "/") {
      setPaletteOpen(false);
      setTaskID(null);
    }
  }, [path]);

  const error =
    tasksQuery.isError || projectsQuery.isError || tagsQuery.isError
      ? errorMessage(tasksQuery.error ?? projectsQuery.error ?? tagsQuery.error)
      : "";

  const filterLabel = useMemo(() => {
    if (filter.kind === "project") {
      return filter.name;
    }
    if (filter.kind === "tag") {
      return `#${filter.name}`;
    }
    return "";
  }, [filter]);

  const mobileTitle = isSettings ? "设置" : filterLabel || "看板";

  if (isCapture) {
    return <CapturePage onBack={() => navigate("/")} />;
  }

  return (
    <div className="app-frame">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <button className="wordmark" type="button" onClick={() => navigate("/")}>
            Tack
          </button>
          <p className="sidebar-kicker">任务看板</p>
        </div>

        <button className="quick-capture" type="button" onClick={() => navigate("/capture")}>
          <Plus size={16} aria-hidden />
          记一笔
          <kbd>N</kbd>
        </button>

        <nav className="sidebar-nav" aria-label="主导航">
          <button
            className={`nav-item ${filter.kind === "inbox" && !isSettings ? "nav-item-active" : ""}`}
            type="button"
            onClick={() => {
              setFilter({ kind: "inbox" });
              setQuery("");
              navigate("/");
            }}
          >
            <span className="nav-label">
              <Inbox size={15} aria-hidden />
              收件箱
            </span>
            <span className="count">{inboxCount}</span>
          </button>
          <button
            className={`nav-item ${filter.kind === "all" && !isSettings ? "nav-item-active" : ""}`}
            type="button"
            onClick={() => {
              setFilter({ kind: "all" });
              setQuery("");
              navigate("/");
            }}
          >
            <span className="nav-label">
              <LayoutGrid size={15} aria-hidden />
              全部任务
            </span>
            <span className="count">{tasks.length}</span>
          </button>
        </nav>

        <div className="sidebar-section">
          <div className="sidebar-section-heading">
            <p className="sidebar-label">项目</p>
            <button
              className="icon-button icon-button-small"
              type="button"
              aria-label="新建项目"
              onClick={() => setProjectOpen(true)}
            >
              <Plus size={14} />
            </button>
          </div>
          <div className="sidebar-list">
            {projects.length === 0 ? (
              <p className="sidebar-empty">还没有项目。</p>
            ) : (
              projects.map((project) => (
                <button
                  className={`sidebar-item ${filter.kind === "project" && filter.id === project.id ? "sidebar-item-active" : ""}`}
                  type="button"
                  key={project.id}
                  onClick={() => {
                    setFilter({
                      kind: "project",
                      id: project.id,
                      name: project.name,
                      color: project.color,
                    });
                    setQuery("");
                    navigate("/");
                  }}
                >
                  <span className="sidebar-item-main">
                    <ProjectDot color={project.color} size="small" />
                    {project.name}
                  </span>
                  <span className="count">{project.task_count}</span>
                </button>
              ))
            )}
          </div>
        </div>

        <div className="sidebar-section sidebar-tags">
          <div className="sidebar-section-heading">
            <p className="sidebar-label">标签</p>
          </div>
          <div className="tag-cloud">
            {tags.length === 0 ? (
              <p className="sidebar-empty">#tag 会在记录时自动出现。</p>
            ) : (
              tags.slice(0, 10).map((tag) => (
                <button
                  className={`tag-cloud-item ${filter.kind === "tag" && filter.id === tag.id ? "tag-cloud-item-active" : ""}`}
                  type="button"
                  key={tag.id}
                  onClick={() => {
                    setFilter({
                      kind: "tag",
                      id: tag.id,
                      name: tag.name,
                      color: tag.color,
                    });
                    setQuery("");
                    navigate("/");
                  }}
                >
                  #{tag.name}
                  <span>{tag.task_count}</span>
                </button>
              ))
            )}
          </div>
        </div>

        <div className="sidebar-footer">
          <button
            className={`settings-link ${isSettings ? "settings-link-active" : ""}`}
            type="button"
            onClick={() => navigate("/settings")}
          >
            <Settings size={15} aria-hidden />
            设置
            {updateQuery.data?.update_available ? (
              <span className="update-dot" aria-label="有新版本" />
            ) : null}
          </button>
          <div className="user-line">
            <span className="user-name">{me.user.username}</span>
            <button className="icon-button icon-button-small" type="button" onClick={onLogout} aria-label="退出">
              <LogOut size={14} />
            </button>
          </div>
        </div>
      </aside>

      <main className="workspace">
        <header className="mobile-topbar">
          <div className="mobile-topbar-title">
            <button className="wordmark" type="button" onClick={() => navigate("/")}>
              Tack
            </button>
            <span>{mobileTitle}</span>
          </div>
          <div className="mobile-topbar-actions">
            <button className="icon-button" type="button" onClick={() => setPaletteOpen(true)} aria-label="搜索">
              <Search size={18} />
            </button>
            <button className="icon-button" type="button" onClick={() => navigate("/settings")} aria-label="设置">
              <Settings size={18} />
              {updateQuery.data?.update_available ? <span className="update-dot" /> : null}
            </button>
          </div>
        </header>

        {isSettings ? (
          <SettingsPage me={me} path={path} navigate={navigate} onLogout={onLogout} />
        ) : error ? (
          <div className="page-state" role="alert">
            <p>看板暂时打不开。</p>
            <span>{error}</span>
            <button
              className="text-button"
              type="button"
              onClick={() => {
                void tasksQuery.refetch();
                void projectsQuery.refetch();
                void tagsQuery.refetch();
              }}
            >
              重试
            </button>
          </div>
        ) : tasksQuery.isPending || projectsQuery.isPending || tagsQuery.isPending ? (
          <div className="page-state">
            <p>正在载入任务…</p>
          </div>
        ) : (
          <Board
            tasks={tasks}
            filter={filter}
            query={query}
            onOpenTask={setTaskID}
            onOpenPalette={() => setPaletteOpen(true)}
            onOpenCapture={() => navigate("/capture")}
            onClearFilter={() => setFilter({ kind: "all" })}
          />
        )}
      </main>

      <nav className="mobile-nav" aria-label="手机导航">
        <button type="button" onClick={() => navigate("/")} aria-label="看板">
          <LayoutGrid size={19} aria-hidden />
          <span>看板</span>
        </button>
        <button
          type="button"
          onClick={() => {
            setFilter({ kind: "inbox" });
            navigate("/");
          }}
          aria-label="收件箱"
        >
          <Inbox size={19} aria-hidden />
          <span>收件箱</span>
        </button>
        <button
          className="mobile-capture"
          type="button"
          onClick={() => navigate("/capture")}
          aria-label="记一笔"
        >
          <Plus size={22} aria-hidden />
          <span>记一笔</span>
        </button>
        <button type="button" onClick={() => setPaletteOpen(true)} aria-label="搜索">
          <Search size={19} aria-hidden />
          <span>搜索</span>
        </button>
        <button type="button" onClick={() => navigate("/settings")} aria-label="设置">
          <Settings size={19} aria-hidden />
          <span>设置</span>
          {updateQuery.data?.update_available ? <span className="update-dot" /> : null}
        </button>
      </nav>

      {paletteOpen ? (
        <CommandPalette
          tasks={tasks}
          onClose={() => setPaletteOpen(false)}
          onOpenTask={setTaskID}
          onOpenCapture={() => navigate("/capture")}
          onOpenSettings={() => navigate("/settings")}
        />
      ) : null}
      {taskID !== null ? (
        <TaskSheet taskID={taskID} projects={projects} onClose={() => setTaskID(null)} />
      ) : null}
      <ProjectDialog
        open={projectOpen}
        onClose={() => setProjectOpen(false)}
        onCreated={(project) => {
          setFilter({
            kind: "project",
            id: project.id,
            name: project.name,
            color: project.color,
          });
          navigate("/");
        }}
      />
      {filterLabel ? <span className="sr-only">当前筛选：{filterLabel}</span> : null}
    </div>
  );
}
