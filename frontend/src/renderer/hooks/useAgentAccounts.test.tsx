import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

const { local, remote } = vi.hoisted(() => ({
	local: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() },
	remote: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() },
}));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/api-client")>()),
	apiClient: local,
}));
vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: (hostId?: string) => (hostId === "host-a" ? remote : local),
}));

import { useAgentAccountActions, useAgentAccounts, useSessionAgentAccounts } from "./useAgentAccounts";

describe("agent accounts of a remote session", () => {
	// This computer's daemon has never heard of a remote session: it answers the
	// account read with its own default and the move with "Unknown session".
	it("reads and moves the session on its host, never on this computer's daemon", async () => {
		const airAccounts = [{ id: "air-team", provider: "claude" }, { id: "air-personal", provider: "claude" }];
		remote.GET.mockImplementation(async (path: string) =>
			path === "/api/v1/agents/accounts" ? { data: { accounts: airAccounts } } : { data: { claude: "air-team" } });
		remote.PUT.mockResolvedValue({ data: { transition: { id: "t1" } } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;

		const { result } = renderHook(() => ({
			session: useSessionAgentAccounts("s1", "host-a"),
			accounts: useAgentAccounts(true, "host-a"),
			actions: useAgentAccountActions("host-a"),
		}), { wrapper });

		await waitFor(() => expect(result.current.session.data).toEqual({ claude: "air-team" }));
		await waitFor(() => expect(result.current.accounts.data).toEqual(airAccounts));
		await result.current.actions.switchSession("s1", "air-personal");

		expect(remote.PUT).toHaveBeenCalledWith("/api/v1/agents/accounts/sessions/{sessionId}", {
			params: { path: { sessionId: "s1" } }, body: { accountId: "air-personal" },
		});
		expect(local.GET).not.toHaveBeenCalled();
		expect(local.PUT).not.toHaveBeenCalled();
	});
});
