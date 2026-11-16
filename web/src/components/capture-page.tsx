import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, CornerDownLeft, Hash, Rocket } from "lucide-react";

import { api, errorMessage, type Task } from "../lib/api";
import { previewCapture } from "../lib/capture-preview";
import { useToast } from "./toast";

interface CapturePageProps {
  onBack: () => void;
}

export function CapturePage({ onBack }: CapturePageProps) {
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const queryClient = useQueryClient();
  const { notify } = useToast();
  const preview = previewCapture(text);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const mutation = useMutation({
    mutationFn: () =>
      api<Task>("/capture", {
        method: "POST",
        body: JSON.stringify({ text }),
      }),
    onSuccess: () => {
      setText("");
      void queryClient.invalidateQueries({ queryKey: ["tasks"] });
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      void queryClient.invalidateQueries({ queryKey: ["tags"] });
      notify("已钉进收件箱", "success");
      inputRef.current?.focus();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  const submit = () => {
    if (text.trim()) {
      mutation.mutate();
    }
  };

  return (
    <main className="capture-page">
      <header className="capture-header">
        <button className="back-button" type="button" onClick={onBack}>
          <ArrowLeft size={18} aria-hidden />
          看板
        </button>
        <span className="capture-wordmark">Tack</span>
        <span className="capture-mode">收件箱</span>
      </header>

      <section className="capture-card" aria-labelledby="capture-title">
        <h1 id="capture-title">记一笔</h1>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <textarea
            ref={inputRef}
            value={text}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
                event.preventDefault();
                submit();
              }
            }}
            rows={7}
            placeholder="修一下登录后的跳转 #bug @codeg !p2 ^明天"
            aria-label="记一笔"
          />
          <div className="capture-preview" aria-live="polite">
            {preview.title ? (
              <span className="preview-title">{preview.title}</span>
            ) : (
              <span className="preview-empty">写一句要记住的话…</span>
            )}
            {preview.project ? (
              <span className="preview-chip preview-project">@{preview.project}</span>
            ) : null}
            {preview.tags.map((tag) => (
              <span className="preview-chip preview-tag" key={tag}>
                <Hash size={12} aria-hidden />
                {tag}
              </span>
            ))}
            {preview.priority ? (
              <span className="preview-chip preview-priority">P{preview.priority}</span>
            ) : null}
            {preview.due ? (
              <span className="preview-chip preview-due">^ {preview.due}</span>
            ) : null}
          </div>
          <div className="capture-actions">
            <span className="capture-hint">
              <CornerDownLeft size={14} aria-hidden />
              <kbd>⌘ ↵</kbd> 提交后留在这一页
            </span>
            <button className="primary-button capture-submit" type="submit" disabled={!text.trim() || mutation.isPending}>
              <Rocket size={16} aria-hidden />
              {mutation.isPending ? "正在钉住…" : "存进收件箱"}
            </button>
          </div>
        </form>
      </section>

      <footer className="capture-footer">
        <span># 标签</span>
        <span>@ 项目必须已存在</span>
        <span>!p0–p3 优先级</span>
        <span>^今天 / ^明天 / ^日期</span>
      </footer>
    </main>
  );
}
