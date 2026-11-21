import { useQuery, useQueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { queryClient } from "../lib/query-client";

type Remotes = typeof aoBridge.remotes;
export type LoopbackReach = Awaited<ReturnType<Remotes["loopbackReach"]>>;
export type PortForward = Awaited<ReturnType<Remotes["forwardPort"]>>;

const FORWARDS_KEY = ["port-forwards"] as const;

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

export function usePortForwards() {
	const queryClient = useQueryClient();
	const forwards = useQuery({ queryKey: FORWARDS_KEY, queryFn: () => aoBridge.remotes.portForwards(), refetchInterval: 5_000 }).data ?? [];
	const settle = () => void queryClient.invalidateQueries({ queryKey: FORWARDS_KEY });
	return {
		forwards,
		forward: (hostId: string, port: number) => aoBridge.remotes.forwardPort(hostId, port).finally(settle),
		stop: (hostId: string, port: number) => aoBridge.remotes.stopPortForward(hostId, port).finally(settle),
	};
}
