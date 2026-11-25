import { useQuery } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { queryClient } from "../lib/query-client";

const reachQuery = (hostId?: string) => ({
	queryKey: ["loopback-reach", hostId],
	queryFn: () => aoBridge.remotes.loopbackReach(hostId!),
	staleTime: 60_000,
});

export function useLoopbackReach(hostId?: string): boolean | undefined {
	return useQuery({
		...reachQuery(hostId),
		enabled: Boolean(hostId),
		refetchInterval: (query) => (query.state.data === false ? 60_000 : false),
	}).data;
}

export const fetchLoopbackReach = (hostId: string): Promise<boolean> => queryClient.fetchQuery(reachQuery(hostId));
