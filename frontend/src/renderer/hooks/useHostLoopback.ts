import { useQuery } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";

export function useLoopbackReach(hostId?: string): boolean | undefined {
	return useQuery({
		queryKey: ["loopback-reach", hostId],
		queryFn: () => aoBridge.remotes.loopbackReach(hostId!),
		enabled: Boolean(hostId),
		staleTime: 60_000,
		refetchInterval: (query) => (query.state.data === false ? 60_000 : false),
	}).data;
}
