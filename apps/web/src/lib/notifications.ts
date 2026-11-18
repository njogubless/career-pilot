import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

/** In-app notifications (Phase 9): derived from records, deduplicated server-side. */

export type Notification = { id: string; kind: string; title: string; body: string; link: string; read_at: string | null; created_at: string };

export type NotificationPrefs = {
  high_match: boolean;
  min_match_score: number;
  approvals: boolean;
  workflow_events: boolean;
  replies: boolean;
  follow_ups: boolean;
  weekly_report: boolean;
};

export const kindIcon: Record<string, string> = {
  HIGH_MATCH: "star",
  APPROVAL_REQUESTED: "front_hand",
  WORKFLOW_NEEDS_INPUT: "edit_note",
  WORKFLOW_FINISHED: "task_alt",
  REPLY_RECEIVED: "reply",
  MESSAGE_BOUNCED: "report",
  FOLLOW_UP_READY: "schedule_send",
  REPORT_READY: "analytics",
  SCHEDULE_SKIPPED: "event_busy",
};

export function useNotifications(limit = 30) {
  return useQuery({
    queryKey: ["notifications", limit],
    queryFn: () => api.get<{ data: Notification[]; unread_count: number }>(`/api/v1/notifications?limit=${limit}`),
    refetchInterval: 60_000,
  });
}

export function useNotificationActions() {
  const qc = useQueryClient();
  const done = () => void qc.invalidateQueries({ queryKey: ["notifications"] });
  return {
    read: useMutation({ mutationFn: (id: string) => api.post(`/api/v1/notifications/${id}/read`), onSuccess: done }),
    readAll: useMutation({ mutationFn: () => api.post("/api/v1/notifications/read-all"), onSuccess: done }),
  };
}

export function useNotificationPrefs() {
  return useQuery({ queryKey: ["notification-prefs"], queryFn: () => api.get<NotificationPrefs>("/api/v1/notifications/preferences") });
}

export function useSaveNotificationPrefs() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: NotificationPrefs) => api.put<NotificationPrefs>("/api/v1/notifications/preferences", v),
    onSuccess: (p) => qc.setQueryData(["notification-prefs"], p),
  });
}
