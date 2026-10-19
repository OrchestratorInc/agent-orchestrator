import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { post, remove } = vi.hoisted(() => ({ post: vi.fn(), remove: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { POST: post, DELETE: remove },
	apiErrorMessage: () => "request failed",
}));
vi.mock("./useSessionInterfaceTransition", () => ({
	sessionInterfaceTransitionQueryKey: (id: string) => ["session-interface-transition", id],
}));

import { agentAccountsQueryKey, useAgentAccountActions } from "./useAgentAccounts";

const codexAccount = { id: "codex-a", provider: "codex", isDefault: true };
const codexCatalog = ["agent-models", "codex", "project-a"] as const;
const claudeCatalog = ["agent-models", "claude-code", "project-a"] as const;
const remoteCatalog = ["agent-models", "remote-host", "codex", "project-a"] as const;
const revalidation = ["agent-model-revalidation", "codex", "project-a"] as const;

function setup() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(agentAccountsQueryKey, [codexAccount]);
	for (const key of [codexCatalog, claudeCatalog, remoteCatalog, revalidation]) queryClient.setQueryData(key, { models: [] });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	return { queryClient, ...renderHook(() => useAgentAccountActions(), { wrapper }) };
}

beforeEach(() => {
	post.mockReset();
	remove.mockReset();
});

describe("account changes refresh model capabilities", () => {
	it.each(["default", "remove", "login"] as const)("invalidates only affected local catalogs after successful %s", async (action) => {
		post.mockResolvedValue({ data: action === "login" ? { status: "complete", account: codexAccount } : { accounts: [codexAccount] } });
		remove.mockResolvedValue({ data: { accounts: [] } });
		const { result, queryClient } = setup();
		await act(async () => {
			if (action === "default") await result.current.makeDefault("codex-a");
			if (action === "remove") await result.current.remove("codex-a");
			if (action === "login") await result.current.verifyLogin("login-a");
		});
		expect(queryClient.getQueryState(codexCatalog)?.isInvalidated).toBe(true);
		expect(queryClient.getQueryState(revalidation)?.isInvalidated).toBe(true);
		expect(queryClient.getQueryState(claudeCatalog)?.isInvalidated).toBe(false);
		expect(queryClient.getQueryState(remoteCatalog)?.isInvalidated).toBe(false);
	});

	it("preserves cached capabilities while login is pending", async () => {
		post.mockResolvedValue({ data: { status: "waiting" } });
		const { result, queryClient } = setup();
		await act(async () => { await result.current.verifyLogin("login-a"); });
		expect(queryClient.getQueryState(codexCatalog)?.isInvalidated).toBe(false);
	});

	it("preserves cached capabilities after a refused default change", async () => {
		post.mockResolvedValue({ error: { message: "account unavailable" } });
		const { result, queryClient } = setup();
		await act(async () => { await expect(result.current.makeDefault("codex-a")).rejects.toThrow("request failed"); });
		expect(queryClient.getQueryState(codexCatalog)?.isInvalidated).toBe(false);
	});
});
