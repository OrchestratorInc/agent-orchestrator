import { useQuery } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import type { ApprovalMode } from "../types/conversation";

/** The permission modes the picked chat agent runs in, strictest first, as the daemon reports them without starting anything. */
export function useChatPermissionModes(hostId: string | undefined, pendingHarness: string | undefined) {
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
		// An agent whose check had not finished (just after daemon start) is left out; ask
		// again for about half a minute. A send waits for the answer either way.
		refetchInterval: (query) => pendingHarness && query.state.data && !query.state.data.has(pendingHarness) && query.state.dataUpdateCount < 10 ? 3_000 : false,
	}).data;
}
