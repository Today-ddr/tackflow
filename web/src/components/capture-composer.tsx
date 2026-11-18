import { useEffect, useId, useRef, useState, type RefObject } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowUp,
  CalendarDays,
  Check,
  ChevronDown,
  Flag,
  Folder,
  Hash,
  HelpCircle,
  LoaderCircle,
} from "lucide-react";
import {
  api,
  errorMessage,
  type Project,
  type Tag,
  type Task,
  type TaskPriority,
} from "../lib/api";
import { previewCapture } from "../lib/capture-preview";
import {
  isCaptureTokenName,
  setCaptureDue,
  setCapturePriority,
  setCaptureProject,
  toggleCaptureTag,
} from "../lib/capture-edit";
import { priorityLabel } from "../lib/format";
import { invalidateTaskData } from "../app/task-cache";
import { ProjectDot } from "./project-dot";
import { useToast } from "./toast";

interface CaptureComposerProps {
  text: string;
  onTextChange: (text: string) => void;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  projects?: Project[];
  tags?: Tag[];
  autoFocus?: boolean;
  onFocusChange?: (focused: boolean) => void;
}

type ComposerMenu = "project" | "tag" | "priority" | "due" | null;

export function CaptureComposer({
  text,
  onTextChange,
  inputRef,
  projects = [],
  tags = [],
  autoFocus = false,
  onFocusChange,
}: CaptureComposerProps) {
  const client = useQueryClient();
  const { notify } = useToast();
  const [helpOpen, setHelpOpen] = useState(false);
  const [focused, setFocused] = useState(false);
  const [openMenu, setOpenMenu] = useState<ComposerMenu>(null);
  const helpID = useId();
  const toolsRef = useRef<HTMLDivElement>(null);
  const inFlight = useRef(false);
  const preview = previewCapture(text);
  const activeTagNames = new Set(preview.tags.map((tag) => tag.toLowerCase()));
  const mutation = useMutation({
    mutationFn: (value: string) => api<Task>("/capture", { method: "POST", body: JSON.stringify({ text: value }) }),
    onSuccess: () => {
      onTextChange("");
      void invalidateTaskData(client);
      notify("已存进收件箱", "success");
      inputRef.current?.focus();
    },
    onError: (error) => notify(errorMessage(error), "error"),
    onSettled: () => { inFlight.current = false; },
  });

  useEffect(() => { if (autoFocus) inputRef.current?.focus(); }, [autoFocus, inputRef]);
  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.style.height = "auto";
    input.style.height = `${Math.max(32, Math.min(input.scrollHeight, 160))}px`;
  }, [text, inputRef]);

  useEffect(() => {
    if (!openMenu) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!toolsRef.current?.contains(event.target as Node)) {
        setOpenMenu(null);
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [openMenu]);

  const submit = () => {
    if (!text.trim() || !preview.title.trim() || inFlight.current) return;
    inFlight.current = true;
    mutation.mutate(text.trim());
  };

  const applyText = (next: string) => {
    inputRef.current?.focus();
    onTextChange(next);
    setOpenMenu(null);
    window.requestAnimationFrame(() => {
      const input = inputRef.current;
      if (!input) return;
      input.setSelectionRange(input.value.length, input.value.length);
    });
  };

  const toggleMenu = (menu: Exclude<ComposerMenu, null>) => {
    setOpenMenu((current) => current === menu ? null : menu);
  };

  const chooseProject = (project: Project | null) => {
    if (project && !isCaptureTokenName(project.name)) return;
    applyText(setCaptureProject(text, project?.name ?? null));
  };

  const toggleTag = (tag: Tag) => {
    if (!isCaptureTokenName(tag.name)) return;
    applyText(toggleCaptureTag(text, tag.name));
  };

  const choosePriority = (priority: TaskPriority) => {
    applyText(setCapturePriority(text, priority));
  };

  const chooseDue = (value: string) => {
    applyText(setCaptureDue(text, value || null));
  };

  return <form className={`capture-composer ${focused ? "capture-composer-focused" : ""}`} aria-label="快速收集" onSubmit={(event) => { event.preventDefault(); submit(); }} onFocus={() => { setFocused(true); onFocusChange?.(true); }} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) { setFocused(false); setOpenMenu(null); onFocusChange?.(false); } }} onKeyDown={(event) => {
    if (event.key === "Escape" && openMenu) {
      event.stopPropagation();
      setOpenMenu(null);
      inputRef.current?.focus();
    }
  }}>
    <div className="composer-input-row">
      <textarea ref={inputRef} aria-label="记一笔" placeholder="记一笔，先放进收件箱…" rows={1} value={text} readOnly={mutation.isPending} enterKeyHint="enter" onChange={(event) => { onTextChange(event.target.value); mutation.reset(); }} onKeyDown={(event) => {
        if (event.nativeEvent.isComposing || event.keyCode === 229) return;
        if ((event.metaKey || event.ctrlKey) && event.key === "Enter") { event.preventDefault(); submit(); }
      }} />
      <button className="composer-send" type="submit" aria-label="存进收件箱" title="存进收件箱 · ⌘/Ctrl Enter" disabled={!text.trim() || !preview.title.trim() || mutation.isPending}>{mutation.isPending ? <LoaderCircle size={18} aria-hidden className="loading-icon" /> : <ArrowUp size={19} aria-hidden />}</button>
    </div>
    <div className="composer-tools" ref={toolsRef} aria-label="快速添加属性">
      <div className="composer-tool">
        <button className={`composer-chip ${preview.project ? "composer-chip-active" : ""}`} type="button" aria-haspopup="menu" aria-expanded={openMenu === "project"} aria-label={`选择项目，当前${preview.project ? ` @${preview.project}` : "未分类"}`} onClick={() => toggleMenu("project")} title="选择项目">
          <Folder size={13} aria-hidden />
          <span className="composer-chip-label">{preview.project ? `@${preview.project}` : "项目"}</span>
          <ChevronDown size={11} aria-hidden />
        </button>
        {openMenu === "project" ? <div className="composer-menu" role="menu" aria-label="选择项目">
          <button className="composer-menu-item" type="button" role="menuitemradio" aria-checked={!preview.project} onClick={() => chooseProject(null)}><span className="composer-menu-marker">{!preview.project ? <Check size={13} aria-hidden /> : null}</span>未分类</button>
          {projects.map((project) => {
            const selected = preview.project.toLowerCase() === project.name.toLowerCase();
            const disabled = !isCaptureTokenName(project.name);
            return <button key={project.id} className="composer-menu-item" type="button" role="menuitemradio" aria-checked={selected} disabled={disabled} title={disabled ? "名称含空格或特殊字符，请手动输入" : undefined} onClick={() => chooseProject(project)}><span className="composer-menu-marker">{selected ? <Check size={13} aria-hidden /> : null}</span><ProjectDot color={project.color} size="small" /><span className="composer-menu-label">{project.name}</span>{disabled ? <span className="composer-menu-hint">手动输入</span> : null}</button>;
          })}
          {projects.length === 0 ? <p className="composer-menu-empty">还没有项目</p> : null}
        </div> : null}
      </div>
      <div className="composer-tool">
        <button className={`composer-chip ${preview.tags.length ? "composer-chip-active" : ""}`} type="button" aria-haspopup="menu" aria-expanded={openMenu === "tag"} aria-label={`选择标签，已选 ${preview.tags.length} 个`} onClick={() => toggleMenu("tag")} title="选择标签">
          <Hash size={13} aria-hidden />
          <span className="composer-chip-label">{preview.tags.length === 0 ? "标签" : preview.tags.length === 1 ? `#${preview.tags[0]}` : `#${preview.tags[0]} +${preview.tags.length - 1}`}</span>
          <ChevronDown size={11} aria-hidden />
        </button>
        {openMenu === "tag" ? <div className="composer-menu" role="menu" aria-label="选择标签">
          {tags.map((tag) => {
            const selected = activeTagNames.has(tag.name.toLowerCase());
            const disabled = !isCaptureTokenName(tag.name);
            return <button key={tag.id} className="composer-menu-item" type="button" role="menuitemcheckbox" aria-checked={selected} disabled={disabled} title={disabled ? "名称含空格或特殊字符，请手动输入" : undefined} onClick={() => toggleTag(tag)}><span className="composer-menu-marker">{selected ? <Check size={13} aria-hidden /> : null}</span><ProjectDot color={tag.color} size="small" /><span className="composer-menu-label">#{tag.name}</span>{disabled ? <span className="composer-menu-hint">手动输入</span> : null}</button>;
          })}
          {tags.length === 0 ? <p className="composer-menu-empty">还没有标签，可直接输入 #标签</p> : null}
        </div> : null}
      </div>
      <div className="composer-tool">
        <button className={`composer-chip ${preview.priority ? "composer-chip-active" : ""}`} type="button" aria-haspopup="menu" aria-expanded={openMenu === "priority"} aria-label={`选择优先级，当前${preview.priority ? ` P${preview.priority}` : "无"}`} onClick={() => toggleMenu("priority")} title="选择优先级">
          <Flag size={13} aria-hidden />
          <span className="composer-chip-label">{preview.priority ? `P${preview.priority} ${priorityLabel(preview.priority as TaskPriority)}` : "优先级"}</span>
          <ChevronDown size={11} aria-hidden />
        </button>
        {openMenu === "priority" ? <div className="composer-menu" role="menu" aria-label="选择优先级">
          {([0, 1, 2, 3] as TaskPriority[]).map((value) => <button key={value} className="composer-menu-item" type="button" role="menuitemradio" aria-checked={preview.priority === value} onClick={() => choosePriority(value)}><span className="composer-menu-marker">{preview.priority === value ? <Check size={13} aria-hidden /> : null}</span><span className="composer-menu-label">{value === 0 ? "无优先级" : `P${value} ${priorityLabel(value)}`}</span></button>)}
        </div> : null}
      </div>
      <div className="composer-tool">
        <button className={`composer-chip ${preview.due ? "composer-chip-active" : ""}`} type="button" aria-haspopup="menu" aria-expanded={openMenu === "due"} aria-label={`选择日期，当前${preview.due ? ` ${preview.due}` : "无"}`} onClick={() => toggleMenu("due")} title="选择截止日期">
          <CalendarDays size={13} aria-hidden />
          <span className="composer-chip-label">{preview.due || "日期"}</span>
          <ChevronDown size={11} aria-hidden />
        </button>
        {openMenu === "due" ? <div className="composer-menu composer-menu-right" role="menu" aria-label="选择日期">
          {["今天", "明天", "后天"].map((value) => <button key={value} className="composer-menu-item" type="button" role="menuitemradio" aria-checked={preview.due === value} onClick={() => chooseDue(value)}><span className="composer-menu-marker">{preview.due === value ? <Check size={13} aria-hidden /> : null}</span><span className="composer-menu-label">{value}</span></button>)}
          <label className="composer-date-field">其他日期<input type="date" onChange={(event) => { if (event.target.value) chooseDue(event.target.value); }} /></label>
          {preview.due ? <button className="composer-menu-item composer-menu-clear" type="button" role="menuitem" onClick={() => chooseDue("")}>清除日期</button> : null}
        </div> : null}
      </div>
    </div>
    <div className="composer-footer"><button className="composer-help" type="button" aria-expanded={helpOpen} aria-controls={helpID} onClick={() => setHelpOpen((value) => !value)}><HelpCircle size={14} aria-hidden />输入语法</button><span>⌘ / Ctrl ↵ 保存</span></div>
    {helpOpen ? <p className="composer-syntax" id={helpID}>可直接输入 #标签 · @已有项目 · !p0–p3 优先级 · ^今天 / ^明天 / ^日期，也可用上方按钮选择。第一行是标题，后续内容作为备注。</p> : null}
    {mutation.error ? <p className="form-error" role="alert">{errorMessage(mutation.error)}</p> : null}
  </form>;
}
