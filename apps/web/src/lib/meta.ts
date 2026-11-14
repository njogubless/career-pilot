import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "./api";

export type IntegrationStatus = "available" | "unavailable" | "not_configured" | "not_implemented";

export type Meta = {
  version: string;
  environment: string;
  integrations: Record<string, { status: IntegrationStatus; detail?: string }>;
};

export function useMeta() {
  return useQuery({
    queryKey: ["meta"],
    queryFn: () => apiFetch<Meta>("/api/v1/meta"),
    staleTime: 30_000,
  });
}
