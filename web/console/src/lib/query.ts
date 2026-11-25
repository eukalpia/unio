import { QueryClient } from "@tanstack/react-query";
import { ApiError, ReauthRequiredError } from "./api/errors";

/** Query keys in one place, so invalidation stays consistent across pages. */
export const queryKeys = {
  cluster: ["cluster"] as const,
  metrics: ["metrics", "summary"] as const,
  buckets: ["buckets"] as const,
  objects: (bucket: string, prefix: string) => ["objects", bucket, prefix] as const,
  users: ["users"] as const,
  policies: ["policies"] as const,
};

function shouldRetry(failureCount: number, error: unknown): boolean {
  if (error instanceof ReauthRequiredError) {
    return false;
  }
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
    return false;
  }
  return failureCount < 2;
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 5_000,
        retry: shouldRetry,
      },
      mutations: {
        retry: false,
      },
    },
  });
}

/** The one query client for the app. */
export const queryClient = createQueryClient();
