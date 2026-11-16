import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, errorMessage, type Project, type ProjectColor, type Tag } from "../lib/api";
import { invalidateTaskData } from "../app/task-cache";
import { projectColors } from "./project-dialog";
import { ProjectDot } from "./project-dot";
import { QueryFeedback } from "./query-feedback";
import { SudoDialog } from "./sudo-dialog";
import { Button } from "./ui/button";
import { Dialog } from "./ui/dialog";
import { Input } from "./ui/input";

type Editor = { kind: "projects" | "tags"; item?: Project | Tag };

export function OrganizeSettings() {
  const client = useQueryClient();
  const [editor, setEditor] = useState<Editor | null>(null);
  const [archive, setArchive] = useState<Project | null>(null);
  const [deleting, setDeleting] = useState<Tag | null>(null);
  const projects = useQuery({ queryKey: ["projects", "all"], queryFn: ({ signal }) => api<{ projects: Project[] }>("/projects?include_archived=1", { signal }) });
  const tags = useQuery({ queryKey: ["tags"], queryFn: ({ signal }) => api<{ tags: Tag[] }>("/tags", { signal }) });
  const archiveMutation = useMutation({
    mutationFn: (project: Project) => api<Project>(`/projects/${project.id}`, { method: "PATCH", body: JSON.stringify({ archived: !project.archived_at }) }),
    onSuccess: () => invalidateTaskData(client),
  });
  const deleteMutation = useMutation({
    mutationFn: (tag: Tag) => api<void>(`/tags/${tag.id}`, { method: "DELETE" }),
    onSuccess: async () => {
      setDeleting(null);
      await Promise.all([
        invalidateTaskData(client),
        client.invalidateQueries({ queryKey: ["tokens"] }),
      ]);
    },
  });

  return <div className="settings-grid">
    <section className="settings-card settings-card-wide">
      <div className="settings-card-heading"><h2>项目</h2><Button variant="quiet" type="button" onClick={() => setEditor({ kind: "projects" })}>新建项目</Button></div>
      <p className="settings-copy">用名称和颜色整理任务。归档后可随时恢复，任务会保留。</p>
      <QueryFeedback pending={projects.isPending} error={projects.error} retry={() => void projects.refetch()} />
      {archiveMutation.error ? <p className="form-error" role="alert">{errorMessage(archiveMutation.error)}</p> : null}
      {projects.data?.projects.length === 0 ? <p className="muted-copy">还没有项目。</p> : null}
      <div className="management-list">{projects.data?.projects.map((project) => <div className="management-item" key={project.id}>
        <div className="management-main"><strong><ProjectDot color={project.color} size="small" /> {project.name}</strong><small>{project.task_count} 个任务{project.archived_at ? " · 已归档" : ""}</small></div>
        <div className="settings-button-row"><button className="text-button" type="button" aria-label={`编辑项目 ${project.name}`} onClick={() => setEditor({ kind: "projects", item: project })}>编辑</button><button className="text-button" type="button" disabled={archiveMutation.isPending} aria-label={`${project.archived_at ? "恢复" : "归档"}项目 ${project.name}`} onClick={() => { archiveMutation.reset(); setArchive(project); }}>{project.archived_at ? "恢复" : "归档"}</button></div>
      </div>)}</div>
    </section>
    <section className="settings-card settings-card-wide">
      <div className="settings-card-heading"><h2>标签</h2><Button variant="quiet" type="button" onClick={() => setEditor({ kind: "tags" })}>新建标签</Button></div>
      <p className="settings-copy">删除标签会解除关联，任务会保留。</p>
      <QueryFeedback pending={tags.isPending} error={tags.error} retry={() => void tags.refetch()} />
      {tags.data?.tags.length === 0 ? <p className="muted-copy">还没有标签。</p> : null}
      <div className="management-list">{tags.data?.tags.map((tag) => <div className="management-item" key={tag.id}>
        <div className="management-main"><strong><ProjectDot color={tag.color} size="small" /> #{tag.name}</strong><small>{tag.task_count} 个任务</small></div>
        <div className="settings-button-row"><button className="text-button" type="button" aria-label={`编辑标签 ${tag.name}`} onClick={() => setEditor({ kind: "tags", item: tag })}>编辑</button><button className="text-button text-button-danger" type="button" aria-label={`删除标签 ${tag.name}`} onClick={() => { deleteMutation.reset(); setDeleting(tag); }}>删除</button></div>
      </div>)}</div>
    </section>
    {editor ? <MetadataEditor editor={editor} onClose={() => setEditor(null)} /> : null}
    <SudoDialog open={archive !== null} title={archive?.archived_at ? "恢复项目" : "归档项目"} description="确认身份后更改项目归档状态，任务会保留。" onClose={() => setArchive(null)} onSuccess={() => { if (archive) archiveMutation.mutate(archive); }} />
    {deleting ? (
      <Dialog
        open
        title="删除标签"
        subtitle={`删除「${deleting.name}」会解除标签关联，任务会保留。`}
        onClose={() => { if (!deleteMutation.isPending) setDeleting(null); }}
        footer={
          <div className="dialog-actions">
            <Button variant="quiet" type="button" disabled={deleteMutation.isPending} onClick={() => setDeleting(null)}>取消</Button>
            <Button type="button" disabled={deleteMutation.isPending} onClick={() => deleteMutation.mutate(deleting)}>{deleteMutation.isPending ? "删除中…" : "删除"}</Button>
          </div>
        }
      >
        <p>以此标签限制访问范围的 API 令牌会同时撤销，使用这些令牌的脚本将立即失效。</p>
        <p>此操作无法撤销。重新创建同名标签也不会恢复令牌，需要重新生成。</p>
        {deleteMutation.error ? <p className="form-error" role="alert">{errorMessage(deleteMutation.error)}</p> : null}
      </Dialog>
    ) : null}
  </div>;
}

function MetadataEditor({ editor, onClose }: { editor: Editor; onClose: () => void }) {
  const client = useQueryClient();
  const [name, setName] = useState(editor.item?.name ?? "");
  const [color, setColor] = useState<ProjectColor>(editor.item?.color ?? "sage");
  const label = editor.kind === "projects" ? "项目" : "标签";
  const mutation = useMutation({
    mutationFn: () => api(`/${editor.kind}${editor.item ? `/${editor.item.id}` : ""}`, { method: editor.item ? "PATCH" : "POST", body: JSON.stringify({ name: name.trim(), color }) }),
    onSuccess: async () => { await invalidateTaskData(client); onClose(); },
  });
  return <Dialog open title={`${editor.item ? "编辑" : "新建"}${label}`} onClose={() => { if (!mutation.isPending) onClose(); }} footer={<div className="dialog-actions"><Button variant="quiet" type="button" disabled={mutation.isPending} onClick={onClose}>取消</Button><Button type="submit" form="metadata-form" disabled={!name.trim() || mutation.isPending}>{mutation.isPending ? "保存中…" : "保存"}</Button></div>}>
    <form id="metadata-form" className="form-stack" onSubmit={(event) => { event.preventDefault(); if (name.trim() && !mutation.isPending) mutation.mutate(); }}>
      <div className="field"><label htmlFor="metadata-name">{label}名称</label><Input id="metadata-name" autoFocus maxLength={80} value={name} onChange={(event) => setName(event.target.value)} /></div>
      <div className="field"><label htmlFor="metadata-color">颜色</label><select id="metadata-color" value={color} onChange={(event) => setColor(event.target.value as ProjectColor)}>{projectColors.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></div>
      {mutation.error ? <p className="form-error" role="alert">{errorMessage(mutation.error)}</p> : null}
    </form>
  </Dialog>;
}
