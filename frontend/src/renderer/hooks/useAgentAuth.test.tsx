import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../lib/api-client";
import { shellTerminalsQueryKey } from "./useShellTerminals";
import { cancelAgentUILogin, getAgentUILogin, probeAgentAuth, startAgentUILogin, submitAgentUILoginCode, useAgentAuthPlans, useStartAgentAuth } from "./useAgentAuth";

function wrapper(queryClient: QueryClient) {
	return ({ children }: PropsWithChildren) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("agent authentication hooks", () => {
	afterEach(() => vi.restoreAllMocks());

	it("resumes an existing background login without another native command", async () => {
		vi.spyOn(apiClient, "POST").mockResolvedValue({ error: { code: "AGENT_AUTH_IN_PROGRESS", details: { operationId: "existing" } } } as never);
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { id: "existing", status: "running", method: "browser" } } as never);
		const result = await startAgentUILogin("codex", "browser", "");
		expect(result.id).toBe("existing");
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: "codex", operationId: "existing" } } });
	});

	it("does not put credential submissions into the query mutation cache", async () => {
		vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { id: "login", status: "running" } } as never);
		await startAgentUILogin("codex", "api_key", "test-secret");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth/operations", { params: { path: { agent: "codex" } }, body: { method: "api_key", secret: "test-secret" } });
	});

	it("submits one-time codes directly without a mutation-cache entry", async () => {
		vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { id: "login-1", agentId: "claude-code", status: "running", acceptsCode: false } } as never);
		await submitAgentUILoginCode("claude-code", "login-1", "one-time-code#state");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth/operations/{operationId}/code", { params: { path: { agent: "claude-code", operationId: "login-1" } }, body: { code: "one-time-code#state" } });
	});

	it("treats an already-removed login as cancelled but preserves other errors", async () => {
		const remove = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ error: { code: "AGENT_AUTH_OPERATION_NOT_FOUND" } } as never);
		await expect(cancelAgentUILogin("codex", "old")).resolves.toBeUndefined();
		remove.mockResolvedValue({ error: { code: "AGENT_AUTH_CANCEL_PENDING", message: "Login is still stopping" } } as never);
		await expect(cancelAgentUILogin("codex", "old")).rejects.toThrow("Login is still stopping");
	});

	it("loads display-safe authentication plans", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({
			data: { plans: [{ agentId: "codex", action: "login", launchMode: "terminal", available: true, documentationUrl: "https://example.test" }] },
		} as never);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

		const { result } = renderHook(() => useAgentAuthPlans(), { wrapper: wrapper(client) });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0]).toMatchObject({ agentId: "codex", action: "login" });
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/auth-plans");
	});

	it("preserves the code and HTTP status when a login operation disappeared", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({ error: { code: "AGENT_AUTH_OPERATION_NOT_FOUND" }, response: new Response(null, { status: 404 }) } as never);
		await expect(getAgentUILogin("codex", "missing")).rejects.toMatchObject({ code: "AGENT_AUTH_OPERATION_NOT_FOUND", status: 404 });
	});

	it("starts a fixed agent flow and adds its terminal to an existing cache before navigation", async () => {
		const terminal = { handleId: "shellterm-auth", workingDir: "/tmp/ao", title: "Log in to Codex", createdAt: new Date().toISOString() };
		vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { agentId: "codex", action: "login", terminal } } as never);
		const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
		client.setQueryData(shellTerminalsQueryKey, [{
			handleId: "shellterm-existing", workingDir: "/tmp/ao", title: "Existing", createdAt: new Date(0).toISOString(),
		}]);
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const { result } = renderHook(() => useStartAgentAuth(), { wrapper: wrapper(client) });

		await act(async () => {
			await result.current.mutateAsync("codex");
		});

		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth", {
			params: { path: { agent: "codex" } },
		});
		expect(invalidate).toHaveBeenCalledWith({ queryKey: shellTerminalsQueryKey });
		expect(client.getQueryData<Array<{ handleId: string }>>(shellTerminalsQueryKey)?.map((item) => item.handleId)).toEqual([
			"shellterm-existing",
			"shellterm-auth",
		]);
	});

	it("runs the existing fresh probe for Check login", async () => {
		vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { id: "codex", installed: true, authStatus: "authorized" } } as never);

		await probeAgentAuth("codex");

		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", {
			params: { path: { agent: "codex" } },
		});
	});
});
