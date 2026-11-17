import { useEffect, useId, useRef, useState, type RefObject } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowUp, HelpCircle, LoaderCircle } from "lucide-react";
import { api, errorMessage, type Task } from "../lib/api";
import { previewCapture } from "../lib/capture-preview";
import { invalidateTaskData } from "../app/task-cache";
import { useToast } from "./toast";

interface CaptureComposerProps {
  text: string;
  onTextChange: (text: string) => void;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  autoFocus?: boolean;
  onFocusChange?: (focused: boolean) => void;
}

export function CaptureComposer({ text, onTextChange, inputRef, autoFocus = false, onFocusChange }: CaptureComposerProps) {
  const client = useQueryClient();
  const { notify } = useToast();
  const [helpOpen, setHelpOpen] = useState(false);
  const [focused, setFocused] = useState(false);
  const helpID = useId();
  const inFlight = useRef(false);
  const preview = previewCapture(text);
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

  const submit = () => {
    if (!text.trim() || inFlight.current) return;
    inFlight.current = true;
    mutation.mutate(text.trim());
  };

  return <form className={`capture-composer ${focused ? "capture-composer-focused" : ""}`} aria-label="快速收集" onSubmit={(event) => { event.preventDefault(); submit(); }} onFocus={() => { setFocused(true); onFocusChange?.(true); }} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) { setFocused(false); onFocusChange?.(false); } }}>
    <div className="composer-input-row">
      <textarea ref={inputRef} aria-label="记一笔" placeholder="记一笔，先放进收件箱…" rows={1} value={text} readOnly={mutation.isPending} enterKeyHint="enter" onChange={(event) => { onTextChange(event.target.value); mutation.reset(); }} onKeyDown={(event) => {
        if (event.nativeEvent.isComposing || event.keyCode === 229) return;
        if ((event.metaKey || event.ctrlKey) && event.key === "Enter") { event.preventDefault(); submit(); }
      }} />
      <button className="composer-send" type="submit" aria-label="存进收件箱" title="存进收件箱 · ⌘/Ctrl Enter" disabled={!text.trim() || mutation.isPending}>{mutation.isPending ? <LoaderCircle size={18} aria-hidden className="loading-icon" /> : <ArrowUp size={19} aria-hidden />}</button>
    </div>
    {(preview.project || preview.tags.length || preview.priority || preview.due) ? <div className="composer-preview" aria-label="识别到的属性">{preview.project ? <span>@{preview.project}</span> : null}{preview.tags.map((tag, index) => <span key={`${tag}-${index}`}>#{tag}</span>)}{preview.priority ? <span>P{preview.priority}</span> : null}{preview.due ? <span>{preview.due}</span> : null}</div> : null}
    <div className="composer-footer"><button className="composer-help" type="button" aria-expanded={helpOpen} aria-controls={helpID} onClick={() => setHelpOpen((value) => !value)}><HelpCircle size={14} aria-hidden />输入语法</button><span>⌘ / Ctrl ↵ 保存</span></div>
    {helpOpen ? <p className="composer-syntax" id={helpID}>#标签 · @已有项目 · !p0–p3 优先级 · ^今天 / ^明天 / ^日期。第一行是标题，后续内容作为备注。</p> : null}
    {mutation.error ? <p className="form-error" role="alert">{errorMessage(mutation.error)}</p> : null}
  </form>;
}
