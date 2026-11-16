import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, errorMessage, type LoginResponse, type Me } from "../lib/api";
import { AppShell } from "../components/app-shell";
import { LoginForm } from "../components/login-form";
import { usePathname } from "../lib/navigation";
import { useToast } from "../components/toast";

export default function App() {
  const queryClient = useQueryClient();
  const [path, navigate] = usePathname();
  const { notify } = useToast();
  const logout = useMutation({
    mutationFn: async () => {
      try {
        await api<void>("/auth/logout", { method: "POST" });
      } catch (error) {
        if (!(error instanceof ApiError && error.status === 401)) throw error;
      }
    },
    onSuccess: async () => {
      await queryClient.cancelQueries();
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== "me" });
      queryClient.getMutationCache().clear();
      queryClient.setQueryData<Me | null>(["me"], null);
      navigate("/");
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });
  const meQuery = useQuery<Me | null>({
    queryKey: ["me"],
    queryFn: async () => {
      try {
        return await api<Me>("/auth/me");
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) {
          return null;
        }
        throw error;
      }
    },
  });

  if (meQuery.isPending) {
    return (
      <main className="loading-page" aria-live="polite">
        正在打开 Tack…
      </main>
    );
  }

  if (meQuery.isError) {
    return (
      <main className="loading-page" role="alert">
        无法连接 Tack。
        <button type="button" onClick={() => void meQuery.refetch()}>
          重试
        </button>
      </main>
    );
  }

  if (!meQuery.data) {
    return (
      <LoginForm
        onSuccess={(result: LoginResponse) => {
          queryClient.setQueryData<Me | null>(["me"], () => ({
            user: result.user,
            auth_kind: "session",
            sudo_expires_at: null,
          }));
        }}
      />
    );
  }

  return (
    <AppShell
      me={meQuery.data}
      path={path}
      navigate={navigate}
      onLogout={() => {
        if (!logout.isPending) logout.mutate();
      }}
    />
  );
}
