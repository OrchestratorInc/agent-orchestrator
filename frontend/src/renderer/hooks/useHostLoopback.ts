import { useQuery, useQueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";

type Remotes = typeof aoBridge.remotes;
export type LoopbackReach = Awaited<ReturnType<Remotes["loopbackReach"]>>;
export type PortForward = Awaited<ReturnType<Remotes["forwardPort"]>>;

const FORWARDS_KEY = ["port-forwards"] as const;

export function useLoopbackReach(hostId?: string): LoopbackReach | undefined {
	return useQuery({
		queryKey: ["loopback-reach", hostId],
		queryFn: () => aoBridge.remotes.loopbackReach(hostId!),
		enabled: Boolean(hostId),
		staleTime: 60_000,
	}).data;
}

export function usePortForwards() {
	const queryClient = useQueryClient();
	const forwards = useQuery({ queryKey: FORWARDS_KEY, queryFn: () => aoBridge.remotes.portForwards() }).data ?? [];
	const settle = () => void queryClient.invalidateQueries({ queryKey: FORWARDS_KEY });
	return {
		forwards,
		forward: (hostId: string, port: number) => aoBridge.remotes.forwardPort(hostId, port).finally(settle),
		stop: (hostId: string, port: number) => aoBridge.remotes.stopPortForward(hostId, port).finally(settle),
	};
}
