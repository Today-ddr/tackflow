import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, type LoginResponse, type Me } from "../lib/api";
import { AppShell } from "../components/app-shell";
import { LoginForm } from "../components/login-form";

export default function App() {
  const queryClient = useQueryClient();
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
            sudo_expires_at: null,
          }));
        }}
      />
    );
  }

  return (
    <AppShell
      me={meQuery.data}
      onLogout={() => queryClient.setQueryData<Me | null>(["me"], null)}
    />
  );
}
