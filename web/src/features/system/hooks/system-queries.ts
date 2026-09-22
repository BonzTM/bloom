import { useQuery } from "@tanstack/react-query";
import { useSystemApi } from "../system-context.js";

export const systemKeys = {
  all: ["system"] as const,
  version: () => ["system", "version"] as const,
};

export function useVersion() {
  const api = useSystemApi();
  return useQuery({
    queryKey: systemKeys.version(),
    queryFn: ({ signal }) => api.version(signal),
    // The running binary's version never changes; refetch only on a new mount.
    staleTime: Number.POSITIVE_INFINITY,
  });
}
