import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Download,
  KeyRound,
  Laptop,
  LogOut,
  RefreshCw,
  RotateCcw,
  Trash2,
  UploadCloud,
} from "lucide-react";

import {
  api,
  downloadExport,
  errorMessage,
  type Me,
  type SessionInfo,
  type Settings,
  type UpdateStatus,
} from "../lib/api";
import { formatDateTime } from "../lib/format";
import { applyTheme, storedTheme, type Theme } from "../lib/theme";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { SudoDialog } from "./sudo-dialog";
import { useToast } from "./toast";
import { OrganizeSettings } from "./organize-settings";
import { TrashSettings } from "./trash-settings";
import { PasskeySettings } from "./passkey-settings";
import { IntegrationsSettings } from "./integrations-settings";
import { AgentConnections } from "./agent-connections";
import { LogSettings } from "./log-settings";

interface SettingsPageProps {
  path: string;
  me: Me;
  navigate: (path: string) => void;
  onLogout: () => void;
}

type SettingsTab = "general" | "account" | "connections" | "update" | "data" | "organize" | "trash" | "integrations" | "logs";

const tabs: Array<{ id: SettingsTab; label: string; path: string }> = [
  { id: "general", label: "常规", path: "/settings" },
  { id: "account", label: "账户", path: "/settings/account" },
  { id: "connections", label: "Agent 连接", path: "/settings/connections" },
  { id: "organize", label: "项目与标签", path: "/settings/organize" },
  { id: "trash", label: "回收站", path: "/settings/trash" },
  { id: "integrations", label: "集成", path: "/settings/integrations" },
  { id: "update", label: "更新", path: "/settings/update" },
  { id: "data", label: "数据", path: "/settings/data" },
  { id: "logs", label: "日志", path: "/settings/logs" },
];

export function SettingsPage({ path, me, navigate, onLogout }: SettingsPageProps) {
  const segment = path.split("/")[2] || "general";
  const tab = (segment === "tokens" ? "connections" : segment) as SettingsTab;
  const { notify } = useToast();
  const queryClient = useQueryClient();
  const settingsQuery = useQuery<Settings>({
    queryKey: ["settings"],
    queryFn: () => api<Settings>("/settings"),
  });
  const updateQuery = useQuery<UpdateStatus>({
    queryKey: ["update-status"],
    queryFn: () => api<UpdateStatus>("/update/status"),
    refetchInterval: 20_000,
  });

  return (
    <section className="settings-page">
      <header className="settings-header">
        <div>
          <h1>设置</h1>
        </div>
        <div className="settings-identity">
          <strong>{me.user.username}</strong>
        </div>
      </header>

      <nav className="settings-tabs" aria-label="设置分类">
        {tabs.map((item) => (
          <button
            key={item.id}
            className={tab === item.id ? "settings-tab settings-tab-active" : "settings-tab"}
            type="button"
            aria-current={tab === item.id ? "page" : undefined}
            onClick={() => navigate(item.path)}
          >
            {item.label}
            {item.id === "update" && updateQuery.data?.update_available ? (
              <span className="update-dot" aria-label="有新版本" />
            ) : null}
          </button>
        ))}
      </nav>
      <label className="settings-mobile-tab">
        <span className="sr-only">设置页面</span>
        <select
          className="settings-select"
          aria-label="设置页面"
          value={tab}
          onChange={(event) => {
            const nextTab = tabs.find((item) => item.id === event.target.value);
            if (nextTab) navigate(nextTab.path);
          }}
        >
          {tabs.map((item) => (
            <option key={item.id} value={item.id}>
              {item.label}{item.id === "update" && updateQuery.data?.update_available ? " · 有新版本" : ""}
            </option>
          ))}
        </select>
      </label>

      <div className="settings-content">
        {tab === "general" ? (
          <GeneralSettings
            settings={settingsQuery.data}
            settingsLoading={settingsQuery.isPending}
            onSaved={() => {
              void queryClient.invalidateQueries({ queryKey: ["settings"] });
              void queryClient.invalidateQueries({ queryKey: ["update-status"] });
            }}
            notify={notify}
          />
        ) : null}
        {tab === "account" ? (
          <AccountSettings onLogout={onLogout} notify={notify} />
        ) : null}
        {tab === "connections" ? <AgentConnections /> : null}
        {tab === "organize" ? <OrganizeSettings /> : null}
        {tab === "trash" ? <TrashSettings /> : null}
        {tab === "integrations" ? <IntegrationsSettings /> : null}
        {tab === "update" ? (
          <UpdateSettings
            update={updateQuery.data}
            loading={updateQuery.isPending}
            onChanged={() => {
              void queryClient.invalidateQueries({ queryKey: ["update-status"] });
              void queryClient.invalidateQueries({ queryKey: ["settings"] });
            }}
            notify={notify}
          />
        ) : null}
        {tab === "data" ? <DataSettings notify={notify} /> : null}
        {tab === "logs" ? <LogSettings /> : null}
      </div>

      <div className="settings-footer-note">
        <button type="button" onClick={onLogout}>
          <LogOut size={14} aria-hidden />
          退出登录
        </button>
      </div>
    </section>
  );
}

function GeneralSettings({
  settings,
  settingsLoading,
  onSaved,
  notify,
}: {
  settings: Settings | undefined;
  settingsLoading: boolean;
  onSaved: () => void;
  notify: (message: string, tone?: "success" | "error" | "info") => void;
}) {
  const [theme, setThemeState] = useState<Theme>(storedTheme());
  const mutation = useMutation({
    mutationFn: (hours: number) =>
      api<Settings>("/settings", {
        method: "PATCH",
        body: JSON.stringify({ update_interval_hours: hours }),
      }),
    onSuccess: () => {
      notify("更新频率已保存", "success");
      onSaved();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  return (
    <div className="settings-grid">
      <section className="settings-card">
        <div className="settings-card-heading">
          <h2>主题</h2>
        </div>
        <p className="settings-copy">保存在这台设备，不跟随服务器。</p>
        <div className="segmented-control">
          {(["light", "dark", "system"] as Theme[]).map((value) => (
            <button
              key={value}
              type="button"
              className={theme === value ? "segment segment-active" : "segment"}
              onClick={() => {
                applyTheme(value);
                setThemeState(value);
              }}
            >
              {value === "light" ? "浅色" : value === "dark" ? "深色" : "跟随系统"}
            </button>
          ))}
        </div>
      </section>

      <section className="settings-card">
        <div className="settings-card-heading">
          <h2>更新检查</h2>
          <RefreshCw size={18} aria-hidden />
        </div>
        <p className="settings-copy">只有点「立即更新」才会重启服务。</p>
        <select
          className="settings-select"
          value={settings?.update_interval_hours ?? 24}
          onChange={(event) => mutation.mutate(Number(event.target.value))}
          disabled={settingsLoading || mutation.isPending}
          aria-label="更新检查频率"
        >
          <option value={0}>关闭自动检查</option>
          <option value={6}>每 6 小时</option>
          <option value={12}>每 12 小时</option>
          <option value={24}>每天一次</option>
          <option value={48}>每两天一次</option>
        </select>
        <p className="settings-meta">时区：{settings?.timezone ?? "Asia/Shanghai"}</p>
      </section>

    </div>
  );
}

function AccountSettings({
  onLogout,
  notify,
}: {
  onLogout: () => void;
  notify: (message: string, tone?: "success" | "error" | "info") => void;
}) {
  const queryClient = useQueryClient();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const sessionsQuery = useQuery<{ sessions: SessionInfo[] }>({
    queryKey: ["sessions"],
    queryFn: () => api<{ sessions: SessionInfo[] }>("/sessions"),
  });
  const passwordMutation = useMutation({
    mutationFn: () =>
      api<void>("/auth/password", {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      }),
    onSuccess: () => {
      notify("密码已更新，其他会话已失效", "success");
      onLogout();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });
  const deleteSessionMutation = useMutation({
    mutationFn: (id: string) => api<void>(`/sessions/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      notify("登录设备已移除", "success");
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  return (
    <div className="settings-grid">
      <section className="settings-card">
        <div className="settings-card-heading">
          <h2>改密码</h2>
          <KeyRound size={18} aria-hidden />
        </div>
        <p className="settings-copy">至少 12 位；修改后会退出所有设备。</p>
        <form
          className="form-stack"
          onSubmit={(event) => {
            event.preventDefault();
            passwordMutation.mutate();
          }}
        >
          <div className="field">
            <label htmlFor="current-password">当前密码</label>
            <Input
              id="current-password"
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor="new-password">新密码</label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </div>
          <Button type="submit" disabled={!currentPassword || newPassword.length < 12 || passwordMutation.isPending}>
            {passwordMutation.isPending ? "更新中…" : "更新密码"}
          </Button>
        </form>
      </section>

      <section className="settings-card settings-card-wide">
        <div className="settings-card-heading">
          <h2>登录设备</h2>
          <Laptop size={18} aria-hidden />
        </div>
        <div className="session-list">
          {sessionsQuery.data?.sessions.map((session) => (
            <div className="session-item" key={session.id}>
              <div className="session-icon">
                <Laptop size={16} aria-hidden />
              </div>
              <div className="session-main">
                <p>
                  {session.current ? "当前设备" : "其他设备"}
                  {session.ip ? ` · ${session.ip}` : ""}
                </p>
                <small>
                  最近 {formatDateTime(session.last_seen_at)}
                  {session.user_agent ? ` · ${session.user_agent}` : ""}
                </small>
              </div>
              {!session.current ? (
                <button
                  className="icon-button"
                  type="button"
                  aria-label="移除这台设备"
                  onClick={() => deleteSessionMutation.mutate(session.id)}
                >
                  <Trash2 size={15} />
                </button>
              ) : null}
            </div>
          ))}
          {sessionsQuery.isPending ? <p className="muted-copy">正在读取登录设备…</p> : null}
        </div>
      </section>
      <PasskeySettings />
    </div>
  );
}

function UpdateSettings({
  update,
  loading,
  onChanged,
  notify,
}: {
  update: UpdateStatus | undefined;
  loading: boolean;
  onChanged: () => void;
  notify: (message: string, tone?: "success" | "error" | "info") => void;
}) {
  const [sudoOpen, setSudoOpen] = useState(false);
  const [pendingApply, setPendingApply] = useState<string | null>(null);
  const [pendingRollback, setPendingRollback] = useState(false);
  const checkMutation = useMutation({
    mutationFn: () =>
      api<UpdateStatus>("/update/check", { method: "POST", body: JSON.stringify({}) }),
    onSuccess: () => {
      notify("已检查最新版本", "success");
      onChanged();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });
  const applyMutation = useMutation({
    mutationFn: (version: string) =>
      api("/update/apply", {
        method: "POST",
        body: JSON.stringify({ version }),
      }),
    onSuccess: () => {
      notify("更新已交给 updater，页面会在服务恢复后刷新", "success");
      onChanged();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });
  const rollbackMutation = useMutation({
    mutationFn: () =>
      api("/update/rollback", {
        method: "POST",
        body: JSON.stringify({}),
      }),
    onSuccess: () => {
      notify("回滚已交给 updater", "success");
      onChanged();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });
  const updateBadge = !update?.configured
    ? "未配置"
    : !update.latest_version
      ? "未检查"
      : update.update_available
        ? "可更新"
        : "已是最新";

  return (
    <div className="settings-grid">
      <section className="settings-card settings-card-wide">
        <div className="settings-card-heading">
          <h2>版本与更新</h2>
          <UploadCloud size={18} aria-hidden />
        </div>
        <div className="version-row">
          <div>
            <span className="version-label">当前版本</span>
            <strong className="version-number">{update?.current_version ?? "…"}</strong>
          </div>
          <div>
            <span className="version-label">最新版本</span>
            <strong className="version-number">{update?.latest_version ?? "未检查"}</strong>
          </div>
          <span className={update?.update_available ? "status-pill status-pill-hot" : "status-pill"}>
            {updateBadge}
          </span>
        </div>
        {update?.last_error ? <p className="form-error">{update.last_error}</p> : null}
        <p className="settings-copy">
          {!update?.configured
            ? "在服务器 .env 里配置 TACK_GITHUB_REPO 后，才能检查 GitHub Releases。"
            : update.last_checked_at
              ? `最近检查：${formatDateTime(update.last_checked_at)}`
              : "还没有检查过，点击下方按钮查看最新版本。"}
        </p>
        <div className="settings-button-row">
          <Button
            variant="quiet"
            type="button"
            onClick={() => checkMutation.mutate()}
            disabled={checkMutation.isPending || loading || !update?.configured}
          >
            <RefreshCw size={15} aria-hidden />
            {checkMutation.isPending ? "检查中…" : "立即检查"}
          </Button>
          {update?.latest_version ? (
            <Button
              type="button"
              onClick={() => {
                setPendingApply(update.latest_version);
                setSudoOpen(true);
              }}
              disabled={!update.update_available || applyMutation.isPending}
            >
              <UploadCloud size={15} aria-hidden />
              立即更新
            </Button>
          ) : null}
          {update?.updater?.previous_tag ? (
            <Button
              variant="quiet"
              type="button"
              onClick={() => {
                setPendingRollback(true);
                setSudoOpen(true);
              }}
              disabled={rollbackMutation.isPending}
            >
              <RotateCcw size={15} aria-hidden />
              回滚到 {update.updater.previous_tag}
            </Button>
          ) : null}
        </div>
        {update?.release_body ? (
          <div className="release-notes">
            <p className="settings-copy">更新说明</p>
            <pre>{update.release_body}</pre>
          </div>
        ) : null}
        {update?.updater ? (
          <div className="updater-state">
            <span>updater</span>
            <strong>{update.updater.status}</strong>
            <p>{update.updater.message || "等待操作"}</p>
          </div>
        ) : null}
      </section>
      <SudoDialog
        open={sudoOpen}
        title={pendingRollback ? "确认回滚" : "确认更新"}
        description="更新前会备份数据库；失败会自动回滚。"
        onClose={() => {
          setSudoOpen(false);
          setPendingApply(null);
          setPendingRollback(false);
        }}
        onSuccess={() => {
          if (pendingRollback) {
            rollbackMutation.mutate();
          } else if (pendingApply) {
            applyMutation.mutate(pendingApply);
          }
          setPendingApply(null);
          setPendingRollback(false);
        }}
      />
    </div>
  );
}

function DataSettings({
  notify,
}: {
  notify: (message: string, tone?: "success" | "error" | "info") => void;
}) {
  return (
    <div className="settings-grid">
      <section className="settings-card settings-card-wide">
        <div className="settings-card-heading">
          <h2>导出与备份</h2>
          <Download size={18} aria-hidden />
        </div>
        <p className="settings-copy">
          SQLite 是唯一真源。服务器每天 03:00 自动备份到 `data/backups/`，保留 14 天。
        </p>
        <div className="settings-button-row">
          <Button
            variant="quiet"
            type="button"
            onClick={() => {
              void downloadExport("json").then(
                () => notify("JSON 已下载", "success"),
                (error: unknown) => notify(errorMessage(error), "error"),
              );
            }}
          >
            <Download size={15} aria-hidden />
            导出 JSON
          </Button>
          <Button
            variant="quiet"
            type="button"
            onClick={() => {
              void downloadExport("csv").then(
                () => notify("CSV 已下载", "success"),
                (error: unknown) => notify(errorMessage(error), "error"),
              );
            }}
          >
            <Download size={15} aria-hidden />
            导出 CSV
          </Button>
        </div>
        <div className="data-note">
          <Laptop size={16} aria-hidden />
          <span>恢复：停 app → 替换 `data/tack.db` → 起 app。详见 `docs/runbook.md`。</span>
        </div>
      </section>
    </div>
  );
}
