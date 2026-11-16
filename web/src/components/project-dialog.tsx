import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";

import { api, errorMessage, type Project, type ProjectColor } from "../lib/api";
import { useToast } from "./toast";
import { Button } from "./ui/button";
import { Dialog } from "./ui/dialog";
import { Input } from "./ui/input";
import { ProjectDot } from "./project-dot";
import { invalidateTaskData } from "../app/task-cache";

export const projectColors: Array<{ value: ProjectColor; label: string }> = [
  { value: "sage", label: "鼠尾草" },
  { value: "clay", label: "陶土" },
  { value: "ink", label: "墨色" },
  { value: "sky", label: "晴空" },
  { value: "plum", label: "梅子" },
];

interface ProjectDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated?: (project: Project) => void;
}

export function ProjectDialog({ open, onClose, onCreated }: ProjectDialogProps) {
  const [name, setName] = useState("");
  const [color, setColor] = useState<ProjectColor>("sage");
  const queryClient = useQueryClient();
  const { notify } = useToast();

  const mutation = useMutation({
    mutationFn: () =>
      api<Project>("/projects", {
        method: "POST",
        body: JSON.stringify({ name, color }),
      }),
    onSuccess: (project) => {
      setName("");
      setColor("sage");
      void invalidateTaskData(queryClient);
      notify(`项目「${project.name}」已创建`, "success");
      onCreated?.(project);
      onClose();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="新建项目"
      subtitle="项目是看板上的彩色分类，不是另一块板。"
      footer={
        <div className="dialog-actions">
          <Button variant="quiet" type="button" onClick={onClose}>
            取消
          </Button>
          <Button
            type="submit"
            form="project-form"
            disabled={!name.trim() || mutation.isPending}
          >
            <Plus size={15} aria-hidden />
            {mutation.isPending ? "创建中…" : "创建项目"}
          </Button>
        </div>
      }
    >
      <form
        id="project-form"
        className="form-stack"
        onSubmit={(event) => {
          event.preventDefault();
          if (name.trim()) {
            mutation.mutate();
          }
        }}
      >
        <div className="field">
          <label htmlFor="project-name">名称</label>
          <Input
            id="project-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="例如 codeg、写书、生活"
            maxLength={80}
            autoFocus
          />
        </div>
        <div className="field">
          <span className="field-label">颜色</span>
          <div className="color-picker" role="radiogroup" aria-label="项目颜色">
            {projectColors.map((item) => (
              <button
                key={item.value}
                className={`color-choice color-${item.value} ${color === item.value ? "color-choice-active" : ""}`}
                type="button"
                role="radio"
                aria-checked={color === item.value}
                aria-label={item.label}
                onClick={() => setColor(item.value)}
              >
                <ProjectDot color={item.value} />
                <span>{item.label}</span>
              </button>
            ))}
          </div>
        </div>
      </form>
    </Dialog>
  );
}
