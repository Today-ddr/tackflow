import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { LockKeyhole } from "lucide-react";

import { api, errorMessage } from "../lib/api";
import { Button } from "./ui/button";
import { Dialog } from "./ui/dialog";
import { Input } from "./ui/input";
import { useToast } from "./toast";

interface SudoDialogProps {
  open: boolean;
  title?: string;
  description?: string;
  onClose: () => void;
  onSuccess: () => void;
}

export function SudoDialog({
  open,
  title = "确认是你",
  description = "这个操作需要重新输入当前密码，5 分钟内有效。",
  onClose,
  onSuccess,
}: SudoDialogProps) {
  const [password, setPassword] = useState("");
  const { notify } = useToast();
  const mutation = useMutation({
    mutationFn: () =>
      api("/auth/sudo", {
        method: "POST",
        body: JSON.stringify({ password }),
      }),
    onSuccess: () => {
      setPassword("");
      notify("验证通过", "success");
      onClose();
      onSuccess();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  return (
    <Dialog
      open={open}
      onClose={() => {
        setPassword("");
        onClose();
      }}
      title={title}
      subtitle={description}
      footer={
        <div className="dialog-actions">
          <Button variant="quiet" type="button" onClick={onClose}>
            取消
          </Button>
          <Button
            type="submit"
            form="sudo-form"
            disabled={!password || mutation.isPending}
          >
            <LockKeyhole size={15} aria-hidden />
            {mutation.isPending ? "验证中…" : "验证"}
          </Button>
        </div>
      }
    >
      <form
        id="sudo-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (password) {
            mutation.mutate();
          }
        }}
      >
        <div className="field">
          <label htmlFor="sudo-password">当前密码</label>
          <Input
            id="sudo-password"
            type="password"
            autoFocus
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </div>
      </form>
    </Dialog>
  );
}
