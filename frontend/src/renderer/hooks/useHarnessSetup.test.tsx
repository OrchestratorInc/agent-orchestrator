import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentsQueryKey, useAgentsQuery } from "./useAgentsQuery";
import { agentAuthPlansQueryKey } from "./useAgentAuth";
import { agentInstallJobsQueryKey, useHarnessSetup } from "./useHarnessSetup";

const apiMocks = vi.hoisted(() => ({
	GET: vi.fn(),
	POST: vi.fn(),
	DELETE: vi.fn(),
}));

vi.mock("../lib/api-client", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/api-client")>();
	return {
		...actual,
		apiClient: { ...actual.apiClient, GET: apiMocks.GET, POST: apiMocks.POST, DELETE: apiMocks.DELETE },
	};
});

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

let queryClient: QueryClient;

beforeEach(() => {
	apiMocks.GET.mockReset();
	apiMocks.POST.mockReset();
	apiMocks.DELETE.mockReset().mockResolvedValue({});
	queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	apiMocks.GET.mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents/installers") return { data: { agents: [] } };
		if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } };
		if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } };
		return { data: undefined };
	});
});

describe("useHarnessSetup", () => {
	it.each([undefined, { code: "SHELL_TERMINAL_NOT_FOUND" }])("refreshes readiness after closing an attached login terminal (cleanup error: %j)", async (cleanupError) => {
		const agent = { id: "qwen", label: "Qwen" };
		let authorized = false;
		const defaultGet = apiMocks.GET.getMockImplementation()!;
		apiMocks.GET.mockImplementation(async (path: string) => path === "/api/v1/agents"
			? { data: { supported: [agent], installed: [agent], authorized: authorized ? [agent] : [] } }
			: defaultGet(path));
		apiMocks.DELETE.mockResolvedValue({ error: cleanupError });
		apiMocks.POST.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				authorized = true;
				return { data: { agent: { ...agent, authStatus: "authorized" }, installed: true, supported: true } };
			}
			return { data: { agentId: "qwen", action: "login", terminal: { handleId: "auth-qwen", title: "Qwen login", createdAt: "2026-10-22T00:00:00Z" } } };
		});
		const { result } = renderHook(() => ({ setup: useHarnessSetup(), agents: useAgentsQuery() }), { wrapper });
		await waitFor(() => expect(result.current.agents.data?.authorized).toEqual([]));
		await act(async () => { await result.current.setup.startAuth("qwen"); });
		act(() => result.current.setup.handleTerminalState("attached"));
		await act(async () => { await result.current.setup.closeAuth(); });
		await waitFor(() => expect(result.current.agents.data?.authorized).toEqual([agent]));
		expect(result.current.setup.authWorkflow).toBeNull();
		expect(result.current.setup.actionErrors.qwen).toBeUndefined();
	});

	it("starts the installed agent's terminal login flow", async () => {
		apiMocks.GET.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/installers") return { data: { agents: [] } };
			if (path === "/api/v1/agents/auth-plans") {
				return {
					data: {
						plans: [
							{
								agentId: "codex",
								action: "login",
								launchMode: "terminal",
								available: true,
								displayCommand: "codex login",
								documentationUrl: "https://github.com/openai/codex",
							},
						],
					},
				};
			}
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } };
			return { data: undefined };
		});
		apiMocks.POST.mockResolvedValue({
			data: {
				agentId: "codex",
				action: "login",
				terminal: {
					handleId: "auth-codex",
					projectId: null,
					sessionId: null,
					workingDir: "/tmp",
					title: "Codex login",
					createdAt: "2026-09-29T00:00:00Z",
				},
			},
		});

		const { result } = renderHook(() => useHarnessSetup(), { wrapper });
		await waitFor(() => expect(result.current.authPlanFor("codex")?.available).toBe(true));

		await act(async () => {
			await result.current.startAuth("codex");
		});

		expect(apiMocks.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth", {
			params: { path: { agent: "codex" } },
		});
		expect(result.current.authWorkflow?.agentId).toBe("codex");
		expect(result.current.authWorkflow?.terminal.title).toBe("Codex login");
	});

	it("refreshes agent readiness and authentication once an install job succeeds", async () => {
		const invalidated: unknown[] = [];
		const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async (filters) => {
			invalidated.push(filters?.queryKey);
			return undefined;
		});
		renderHook(() => useHarnessSetup(), { wrapper });
		await waitFor(() => {
			expect(apiMocks.GET).toHaveBeenCalledWith("/api/v1/agents/install-jobs");
		});

		// The install runner writes the finished job straight into the cache, which
		// is the same path a polled success takes.
		queryClient.setQueryData(agentInstallJobsQueryKey, [
			{ status: "succeeded", target: "cursor", updatedAt: "2026-09-17T00:00:00Z" },
		]);

		await waitFor(() => {
			expect(invalidated).toContainEqual(agentsQueryKey);
			expect(invalidated).toContainEqual(agentAuthPlansQueryKey);
		});
		expect(invalidateSpy).toHaveBeenCalled();
	});

	it("keeps checking readiness briefly after a successful auth terminal exits", async () => {
		apiMocks.GET.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/installers") return { data: { agents: [] } };
			if (path === "/api/v1/agents/auth-plans") {
				return {
					data: {
						plans: [{ agentId: "codex", action: "login", launchMode: "terminal", available: true }],
					},
				};
			}
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } };
			return { data: undefined };
		});
		let readinessChecks = 0;
		apiMocks.POST.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/{agent}/auth") {
				return {
					data: {
						agentId: "codex",
						action: "login",
						terminal: {
							handleId: "auth-codex",
							projectId: null,
							sessionId: null,
							workingDir: "/tmp",
							title: "Codex login",
							createdAt: "2026-09-29T00:00:00Z",
						},
					},
				};
			}
			if (path === "/api/v1/agents/{agent}/probe") {
				readinessChecks += 1;
				return {
					data: {
						agent: { id: "codex", authStatus: readinessChecks === 1 ? "unauthorized" : "authorized" },
						supported: true,
						installed: true,
					},
				};
			}
			return { data: undefined };
		});

		const { result } = renderHook(() => useHarnessSetup(), { wrapper });
		await waitFor(() => expect(result.current.authPlanFor("codex")?.available).toBe(true));
		await act(async () => {
			await result.current.startAuth("codex");
		});
		act(() => result.current.handleTerminalState("exited"));

		await waitFor(() => expect(readinessChecks).toBeGreaterThanOrEqual(2));
		await waitFor(() => expect(result.current.authWorkflow).toBeNull());
	});
});
