import { useQuery } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { queryClient } from "../lib/query-client";

type Remotes = typeof aoBridge.remotes;
export type LoopbackReach = Awaited<ReturnType<Remotes["loopbackReach"]>>;

const reachQuery = (hostId?: string) => ({
	queryKey: ["loopback-reach", hostId],
	queryFn: () => aoBridge.remotes.loopbackReach(hostId!),
	staleTime: 60_000,
});

export function useLoopbackReach(hostId?: string): LoopbackReach | undefined {
	return useQuery({
		...reachQuery(hostId),
		enabled: Boolean(hostId),
		refetchInterval: (query) => (query.state.data === "off" ? 10_000 : false),
	}).data;
}

export const fetchLoopbackReach = (hostId: string): Promise<LoopbackReach> => queryClient.fetchQuery(reachQuery(hostId));
