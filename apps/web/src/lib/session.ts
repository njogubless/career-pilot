import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, setCsrfToken } from "./api";

export type User = { id: string; email: string; display_name: string; created_at: string };
type SessionResponse = { user: User; csrf_token: string };

export const sessionKey = ["session"] as const;

/** Current session, or null when signed out. */
export function useSession() {
  return useQuery({
    queryKey: sessionKey,
    queryFn: async (): Promise<User | null> => {
      try {
        const s = await api.get<SessionResponse>("/api/v1/auth/session");
        setCsrfToken(s.csrf_token);
        return s.user;
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          setCsrfToken(null);
          return null;
        }
        throw e;
      }
    },
    staleTime: 5 * 60_000,
    retry: false,
  });
}

function useSessionMutation<I>(path: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: I) => api.post<SessionResponse>(path, input),
    onSuccess: (s) => {
      setCsrfToken(s.csrf_token);
      qc.clear();
      qc.setQueryData(sessionKey, s.user);
    },
  });
}

export const useLogin = () => useSessionMutation<{ email: string; password: string }>("/api/v1/auth/login");
export const useRegister = () =>
  useSessionMutation<{ email: string; password: string; display_name: string }>("/api/v1/auth/register");

export function useLogout(all = false) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<void>(all ? "/api/v1/auth/logout-all" : "/api/v1/auth/logout"),
    onSettled: () => {
      setCsrfToken(null);
      qc.clear();
      qc.setQueryData(sessionKey, null);
    },
  });
}
