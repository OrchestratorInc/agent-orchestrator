import { useQuery } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import type { ApprovalMode } from "../types/conversation";

export function useChatPermissionModesQuery(hostId: string | undefined, pendingHarness: string | undefined) {
	return useQuery({
		queryKey: ["chat-permission-modes", hostId ?? "", pendingHarness ?? ""],
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/chat/permission-modes", {
				params: { query: { agents: pendingHarness ?? "" } },
			});
			if (error) throw error;
			return new Map(data.agents.map((entry) => [entry.agent, entry.modes as ApprovalMode[]]));
		},
		enabled: Boolean(pendingHarness),
		staleTime: 60_000,
		refetchInterval: (query) => pendingHarness && query.state.data && !query.state.data.has(pendingHarness) && query.state.dataUpdateCount < 10 ? 3_000 : false,
	});
}

export function useChatPermissionModes(hostId: string | undefined, pendingHarness: string | undefined) {
	return useChatPermissionModesQuery(hostId, pendingHarness).data;
}
