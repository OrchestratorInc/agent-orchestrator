import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, focusManager } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { appI18n } from "../i18n";
import type { WorkspaceSummary } from "../types/workspace";

const { captureRendererEventMock, cloudState, getMock, getSessionMock, hasTrustedApiBaseUrlMock, listProjectsMock, listSessionsMock, remoteGetMock } = vi.hoisted(
	() => ({
		captureRendererEventMock: vi.fn().mockResolvedValue(undefined),
		cloudState: { ready: false, org: undefined as { id: string } | undefined },
		getMock: vi.fn(),
		getSessionMock: vi.fn(),
		hasTrustedApiBaseUrlMock: vi.fn(() => true),
		listProjectsMock: vi.fn(),
		listSessionsMock: vi.fn(),
		remoteGetMock: vi.fn(),
	}),
);

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	hasTrustedApiBaseUrl: hasTrustedApiBaseUrlMock,
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: captureRendererEventMock }));
vi.mock("../lib/host-clients", () => ({ clientForHost: () => ({ GET: remoteGetMock }), connectedHosts: () => [], subscribeConnectedHosts: () => () => undefined }));

vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getSession: getSessionMock, listProjects: listProjectsMock, listSessions: listSessionsMock },
		ready: cloudState.ready,
		baseUrl: "https://cp.example.com",
	}),
}));

vi.mock("./useCloudOrg", () => ({
	useCloudOrg: () => ({ org: cloudState.org, isLoading: false, error: undefined, ready: cloudState.ready }),
}));

import { CloudCpError } from "../lib/cloud-cp";
import { UNKNOWN_CLOUD_PROJECT_REFETCH_DEBOUNCE_MS, cloudProjectsQueryKey, cloudSessionsQueryKey, refetchCloudProjectsForUnknownSessions, useCloudSessionQuery, useWorkspacePolling, useWorkspaceQuery, useWorkspaceScope, useWorkspaceSession, useWorkspaceTraySessions, workspaceQueryKey, workspaceQueryKeyForHost } from "./useWorkspaceQuery";

it("preserves the existing local and remote workspace cache keys", () => {
	expect(workspaceQueryKeyForHost()).toBe(workspaceQueryKey);
	expect(workspaceQueryKeyForHost("box-a")).toEqual(["remote-workspaces", "box-a"]);
});

function wrapper({ children }: { children: ReactNode }) {
	// The hook pins its own retry policy; retryDelay 0 keeps the error tests fast.
	const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

function respondWith(payload: {
	projects?: { data?: unknown; error?: unknown };
	sessions?: { data?: unknown; error?: unknown };
}) {
	getMock.mockImplementation(async (url: string) => {
		if (url === "/api/v1/projects") return payload.projects ?? { data: { projects: [] }, error: undefined };
		if (url === "/api/v1/sessions") return payload.sessions ?? { data: { sessions: [] }, error: undefined };
		throw new Error(`unexpected GET ${url}`);
	});
}

beforeEach(() => {
	captureRendererEventMock.mockClear();
	getMock.mockReset();
	hasTrustedApiBaseUrlMock.mockReset().mockReturnValue(true);
	cloudState.ready = false;
	cloudState.org = undefined;
	listProjectsMock.mockReset();
	listSessionsMock.mockReset().mockResolvedValue({ items: [] });
	remoteGetMock.mockReset();
});

describe("useWorkspaceQuery", () => {
	it.each(["pending", "failed", "removed"])("keeps local workspace cleanup state %s", async (workspaceCleanup) => {
		respondWith({
			projects: { data: { projects: [{ id: "p1", name: "Project", path: "/tmp/project" }] } },
			sessions: { data: { sessions: [{ id: "s1", projectId: "p1", harness: "codex", status: "working", statusReadiness: "ready",
				activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" }, isTerminated: true, workspaceCleanup, prs: [] }] } },
		});
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0].sessions[0].workspaceCleanup).toBe(workspaceCleanup);
	});

	it.each(["checking", "unavailable"] as const)("does not expose unverified activity while %s", async (statusReadiness) => {
		respondWith({
			projects: { data: { projects: [{ id: "p1", name: "Project", path: "/tmp/project" }] } },
			sessions: { data: { sessions: [{ id: "s1", projectId: "p1", harness: "codex", status: "working", statusReadiness,
				activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" }, updatedAt: "2026-01-01T00:00:00Z", prs: [] }] } },
		});
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0].sessions[0]).toMatchObject({ status: "unknown", statusReadiness });
		expect(result.current.data?.[0].sessions[0].activity).toBeUndefined();
		expect(captureRendererEventMock).not.toHaveBeenCalled();
	});

	it("keeps each artifact's inline URL, which the chat frames an HTML artifact from", async () => {
		const inlineUrl = "http://ao-inline-artifact.x.localhost:3001/q3/report.html";
		respondWith({
			projects: { data: { projects: [{ id: "p1", name: "Project", path: "/tmp/project" }] } },
			sessions: { data: { sessions: [{ id: "s1", projectId: "p1", harness: "codex", status: "working", statusReadiness: "ready",
				activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" }, updatedAt: "2026-01-01T00:00:00Z", prs: [],
				artifactFiles: [{ path: "q3/report.html", name: "report.html", kind: "html", size: 12, updatedAt: "2026-01-01T00:00:00Z", inlineUrl }] }] } },
		});
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0].sessions[0].artifactFiles?.[0]).toMatchObject({ path: "q3/report.html", inlineUrl });
	});

	it("rejects workspace reads while the daemon base URL is untrusted", async () => {
		hasTrustedApiBaseUrlMock.mockReturnValue(false);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(result.current.error).toEqual(new Error("AO daemon API is not ready"));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("fetches as soon as the daemon is ready instead of waiting out a retry backoff", async () => {
		hasTrustedApiBaseUrlMock.mockReturnValue(false);
		respondWith({ projects: { data: { projects: [{ id: "p1", name: "Project", path: "/p" }] } } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 60_000 } } });
		const slowRetryWrapper = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper: slowRetryWrapper });
		await waitFor(() => expect(result.current.failureCount).toBe(1));

		hasTrustedApiBaseUrlMock.mockReturnValue(true);
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }, { cancelRefetch: false });

		await waitFor(() => expect(result.current.isSuccess).toBe(true), { timeout: 1_000 });
		expect(result.current.data?.[0]).toMatchObject({ id: "p1" });
	});

	it("maps projects and their sessions, applying provider/status/title fallbacks", async () => {
		respondWith({
			projects: {
				data: {
					projects: [
						{
							id: "proj-1",
							name: "my-app",
							path: "/home/me/my-app",
							orchestratorAgent: "codex",
						},
					],
				},
				error: undefined,
			},
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							terminalHandleId: "term-1",
							terminalGeneration: "launch-2",
							displayName: "fix-bug",
							issueId: "github:acme/project-one#42",
							harness: "claude-code",
							reviewerHarness: "agy",
							branch: "qa/modal-worker",
							status: "mergeable",
							scmStatus: "review_pending",
							kanbanColumn: "ready",
							displayStatus: "Mergeable",
							isTerminated: false,
							autoInjectReview: false,
							autoInjectCI: false,
							activity: { state: "idle", lastActivityAt: "2026-06-10T15:30:00Z" },
							lastUserMessageAt: "2026-06-10T16:10:00Z",
							updatedAt: "2026-06-10T16:15:04Z",
						},
						{
							// Unknown harness/status and no displayName/issueId: falls back
							// to codex / unknown / the session id.
							id: "sess-2",
							projectId: "proj-1",
							harness: "mystery-agent",
							reviewerHarness: "mystery-reviewer",
							status: "bogus",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
						// Belongs to another project; must not leak into proj-1.
						{ id: "sess-3", projectId: "proj-2", isTerminated: false, updatedAt: "2026-06-10T16:15:04Z" },
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const [workspace] = result.current.data ?? [];
		expect(workspace).toMatchObject({
			id: "proj-1",
			name: "my-app",
			path: "/home/me/my-app",
			orchestratorAgent: "codex",
		});
		expect(workspace.sessions).toHaveLength(2);
		expect(workspace.sessions[0]).toMatchObject({
			id: "sess-1",
			terminalHandleId: "term-1",
			terminalGeneration: "launch-2",
			title: "fix-bug",
			issueId: "github:acme/project-one#42",
			provider: "claude-code",
			reviewerHarness: "agy",
			branch: "qa/modal-worker",
			status: "mergeable",
			scmStatus: "review_pending",
			kanbanColumn: "ready",
			displayStatus: "Mergeable",
			activity: { state: "idle", lastActivityAt: "2026-06-10T15:30:00Z" },
			lastUserMessageAt: "2026-06-10T16:10:00Z",
			autoInjectReview: false,
			autoInjectCI: false,
		});
		expect(workspace.sessions[1]).toMatchObject({
			id: "sess-2",
			title: "sess-2",
			provider: "codex",
			reviewerHarness: undefined,
			status: "unknown",
			branch: undefined,
			autoInjectReview: true,
			autoInjectCI: true,
		});
		expect(captureRendererEventMock).toHaveBeenCalledWith("ao.renderer.session_state_unknown", {
			field: "status",
			reason: "unrecognized",
		});
		expect(captureRendererEventMock).toHaveBeenCalledWith("ao.renderer.session_state_unknown", {
			field: "activity",
			reason: "missing",
		});
	});

	it("preserves scratch projects and leaves branchless scratch sessions branchless", async () => {
		respondWith({
			projects: {
				data: {
					projects: [
						{
							id: "scratch",
							name: "Scratch",
							kind: "scratch",
							path: "/home/me/.ao/scratch/default",
						},
					],
				},
				error: undefined,
			},
			sessions: {
				data: {
					sessions: [
						{
							id: "scratch-worker-1",
							projectId: "scratch",
							harness: "codex",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0]).toMatchObject({
			id: "scratch",
			kind: "scratch",
		});
		expect(result.current.data?.[0].sessions[0]).toMatchObject({
			id: "scratch-worker-1",
			branch: undefined,
		});
	});

	it("falls back to the direct session read while the workspace list has not caught up", async () => {
		getMock.mockImplementation(async (url: string, options?: { params?: { path?: { sessionId?: string } } }) => {
			if (url === "/api/v1/projects") {
				return {
					data: {
						projects: [{ id: "proj-1", name: "workspace3", path: "/tmp/workspace3", orchestratorAgent: "codex" }],
					},
					error: undefined,
				};
			}
			if (url === "/api/v1/sessions") {
				return { data: { sessions: [] }, error: undefined };
			}
			if (url === "/api/v1/sessions/{sessionId}") {
				expect(options?.params?.path?.sessionId).toBe("sess-orch");
				return {
					data: {
						session: {
							id: "sess-orch",
							projectId: "proj-1",
							displayName: "orchestrate",
							harness: "codex",
							kind: "orchestrator",
							mode: "tui",
							status: "working",
							kanbanColumn: "building",
							displayStatus: "Working",
							autoInjectReview: true,
							autoInjectCI: true,
							autoReviewEnabled: false,
							isPinned: false,
							isTerminated: false,
							terminateOnPrMerge: false,
							prs: [],
							activity: { state: "idle", lastActivityAt: "2026-09-04T10:00:00Z" },
							createdAt: "2026-09-04T10:00:00Z",
							updatedAt: "2026-09-04T10:00:01Z",
						},
					},
					error: undefined,
				};
			}
			throw new Error(`unexpected GET ${url}`);
		});

		const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
		const localWrapper = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		);

		const { result } = renderHook(() => useWorkspaceSession("sess-orch"), { wrapper: localWrapper });

		await waitFor(() => expect(result.current.data?.id).toBe("sess-orch"));
		expect(result.current.data).toMatchObject({
			id: "sess-orch",
			workspaceId: "proj-1",
			workspaceName: "workspace3",
			title: "orchestrate",
			provider: "codex",
			kind: "orchestrator",
		});
		await waitFor(() => {
			const cached = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey);
			expect(Array.isArray(cached)).toBe(true);
			expect(cached?.[0]?.sessions.some((session: { id: string }) => session.id === "sess-orch")).toBe(true);
		});
	});

	it("groups projectless sessions in Scratchpad after projects", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "standalone-1",
							displayName: "Research",
							provisionState: "failed",
							provisionError: "Agent failed to start",
							provisionSteps: [{ id: "agent", status: "running", startedAt: "2026-06-10T16:15:00Z" }],
							harness: "codex",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.map((workspace) => workspace.id)).toEqual(["proj-1", "__standalone__"]);
		expect(result.current.data?.[1]).toMatchObject({
			id: "__standalone__",
			name: "Scratchpad",
			kind: "standalone",
		});
		expect(result.current.data?.[1].sessions[0]).toMatchObject({
			id: "standalone-1",
			workspaceId: "__standalone__",
			workspaceName: "Scratchpad",
			title: "Research",
			branch: undefined,
			provisionState: "failed",
			provisionError: "Agent failed to start",
			provisionSteps: [{ id: "agent", status: "running", startedAt: "2026-06-10T16:15:00Z" }],
		});
	});

	it("localizes the standalone workspace name", async () => {
		await appI18n.changeLanguage("zh-CN");
		respondWith({
			sessions: {
				data: {
					sessions: [
						{
							id: "standalone-1",
							harness: "codex",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		try {
			const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
			await waitFor(() => expect(result.current.isSuccess).toBe(true));

			expect(result.current.data?.[0]).toMatchObject({ name: "草稿区" });
			expect(result.current.data?.[0].sessions[0]).toMatchObject({ workspaceName: "草稿区" });
		} finally {
			await appI18n.changeLanguage("en");
		}
	});

	it("maps each session's prs straight from the session list", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "pr_open",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
							prs: [
								{
									number: 278,
									state: "open",
									url: "u",
									ci: "passing",
									review: "approved",
									mergeability: "clean",
									reviewComments: false,
									updatedAt: "2026-06-10T16:15:04Z",
								},
							],
						},
						{
							id: "sess-2",
							projectId: "proj-1",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const sessions = result.current.data?.[0].sessions ?? [];
		expect(sessions[0].prs).toEqual([
			{
				number: 278,
				state: "open",
				url: "u",
				ci: "passing",
				review: "approved",
				mergeability: "clean",
				reviewComments: false,
				updatedAt: "2026-06-10T16:15:04Z",
			},
		]);
		// A session with no PRs maps to an empty stack, so the empty states render.
		expect(sessions[1].prs).toEqual([]);
	});

	it("preserves backend merged status for terminated merged sessions", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "merged",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].status).toBe("merged");
		expect(result.current.data?.[0].sessions[0].isTerminated).toBe(true);
	});

	it("falls back to terminated for terminated sessions without a known backend status", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "bogus",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].status).toBe("terminated");
		expect(result.current.data?.[0].sessions[0].isTerminated).toBe(true);
	});

	it("surfaces a projects fetch error", async () => {
		const failure = new TypeError("Failed to fetch");
		respondWith({ projects: { data: undefined, error: failure } });

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});

	it("surfaces a sessions fetch error even when projects load", async () => {
		const failure = new Error("sessions backend down");
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: { data: undefined, error: failure },
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});

	it("merges control-plane projects after local ones with kind cloud", async () => {
		cloudState.ready = true;
		cloudState.org = { id: "org-1" };
		listProjectsMock.mockResolvedValue({
			items: [
				{
					id: "cp-1",
					orgId: "org-1",
					displayName: "cloud-app",
					repositoryUrl: "https://github.com/acme/cloud-app",
					defaultBranch: "main",
					config: {},
					createdAt: "2026-08-01T00:00:00Z",
					updatedAt: "2026-08-01T00:00:00Z",
				},
			],
			page: { hasMore: false },
		});
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "standalone-1",
							displayName: "Research",
							harness: "codex",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.data).toHaveLength(3));

		expect(result.current.data?.[0]).toMatchObject({ id: "proj-1", name: "my-app", path: "/p" });
		expect(result.current.data?.[1]).toEqual({
			id: "cp-1",
			cloudOrgId: "org-1",
			name: "cloud-app",
			kind: "cloud",
			path: "",
			sessions: [],
		});
		expect(result.current.data?.[2]).toMatchObject({ id: "__standalone__", name: "Scratchpad" });
		expect(listProjectsMock).toHaveBeenCalledWith("org-1", { limit: 100 });
	});

	it("maps Cloud failing-check details into the shared PR facts", async () => {
		cloudState.ready = true;
		cloudState.org = { id: "org-1" };
		listProjectsMock.mockResolvedValue({
			items: [{ id: "cp-1", displayName: "cloud-app" }],
			page: { hasMore: false },
		});
		listSessionsMock.mockResolvedValue({
			items: [{
				id: "cloud-session-1", projectId: "cp-1", displayName: "Fix CI",
				harness: "codex", kind: "worker", status: "working", isTerminated: false,
				createdAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z",
				prs: [{
					url: "https://github.com/acme/cloud-app/pull/7", number: 7, state: "open",
					ci: "failing", review: "none", mergeability: "blocked", reviewComments: false,
					failingChecks: [{ name: "unit", status: "failed", conclusion: "failure", url: "https://ci/unit" }],
					updatedAt: "2026-08-01T00:00:00Z",
				}],
			}],
			page: { hasMore: false },
		});
		respondWith({ projects: { data: { projects: [] } }, sessions: { data: { sessions: [] } } });

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.data?.[0]?.sessions).toHaveLength(1));

		expect(result.current.data?.[0]?.sessions[0]?.prs[0]?.failingChecks).toEqual([
			{ name: "unit", status: "failed", conclusion: "failure", url: "https://ci/unit" },
		]);
	});

	it("keeps local projects when the cloud fetch fails", async () => {
		cloudState.ready = true;
		cloudState.org = { id: "org-1" };
		listProjectsMock.mockRejectedValue(new Error("control plane down"));
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		await waitFor(() => expect(listProjectsMock).toHaveBeenCalled());

		expect(result.current.data).toHaveLength(1);
		expect(result.current.data?.[0]).toMatchObject({ id: "proj-1" });
		expect(result.current.isError).toBe(false);
	});

	it("does not call the control plane while cloud is not ready", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(listProjectsMock).not.toHaveBeenCalled();
	});

	it("selects only attention-worthy worker sessions for the always-mounted tray", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{ id: "needs-input", projectId: "proj-1", displayName: "Needs input", harness: "codex", status: "needs_input", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "mergeable", projectId: "proj-1", displayName: "Mergeable", harness: "codex", status: "mergeable", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "working", projectId: "proj-1", displayName: "Working", harness: "codex", status: "working", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "merged", projectId: "proj-1", displayName: "Merged", harness: "codex", status: "merged", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "orchestrator", projectId: "proj-1", displayName: "Orchestrator", harness: "codex", kind: "orchestrator", status: "needs_input", updatedAt: "2026-08-01T00:00:00Z" },
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceTraySessions(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data).toEqual([
			{ projectId: "proj-1", projectName: "my-app", sessionId: "needs-input", title: "Needs input", zone: "action" },
			{ projectId: "proj-1", projectName: "my-app", sessionId: "mergeable", title: "Mergeable", zone: "merge" },
		]);
	});
});

describe("useWorkspaceScope board presentation", () => {
	it("reads a hosted session without contacting the laptop or AO Cloud", async () => {
		cloudState.ready = true;
		cloudState.org = { id: "org-1" };
		remoteGetMock.mockImplementation(async (path: string) => path.endsWith("/projects")
			? { data: { projects: [{ id: "p", name: "Hosted", path: "/host/repo" }] } }
			: { data: { sessions: [{ id: "s", projectId: "p", kind: "worker", status: "working" }] } });
		const { result } = renderHook(() => useWorkspaceSession("s", "box-a"), { wrapper });
		await waitFor(() => expect(result.current.data?.id).toBe("s"));
		expect(result.current.data?.hostId).toBe("box-a");
		expect(getMock).not.toHaveBeenCalled();
		expect(listProjectsMock).not.toHaveBeenCalled();
		expect(listSessionsMock).not.toHaveBeenCalled();
	});

	it("reads a hosted project from its selected daemon without querying local or cloud", async () => {
		cloudState.ready = true;
		cloudState.org = { id: "org-1" };
		remoteGetMock.mockImplementation(async (path: string) => path.endsWith("/projects")
			? { data: { projects: [{ id: "p", name: "Hosted", path: "/host/repo" }] } }
			: { data: { sessions: [{ id: "s", projectId: "p", kind: "worker", status: "working" }] } });
		const { result } = renderHook(() => useWorkspaceScope("p", "s", "box-a"), { wrapper });
		await waitFor(() => expect(result.current.data?.session?.id).toBe("s"));
		expect(result.current.data?.project?.name).toBe("Hosted");
		expect(remoteGetMock).toHaveBeenCalledTimes(2);
		expect(getMock).not.toHaveBeenCalled();
		expect(listProjectsMock).not.toHaveBeenCalled();
		expect(listSessionsMock).not.toHaveBeenCalled();
	});

	it.each([
		{ kind: "orchestrator", isTerminated: false, expected: false },
		{ kind: "orchestrator", isTerminated: true, expected: false },
		{ kind: "worker", isTerminated: false, expected: true },
		{ kind: "worker", isTerminated: true, expected: true },
		{ kind: undefined, isTerminated: false, expected: true },
	])("projects worker presence for $kind, terminated=$isTerminated", async ({ kind, isTerminated, expected }) => {
		respondWith({
			projects: { data: { projects: [{ id: "p", name: "project", path: "/repo" }] } },
			sessions: { data: { sessions: [{ id: "s", projectId: "p", kind, isTerminated, status: "working" }] } },
		});
		const { result } = renderHook(() => useWorkspaceScope("p"), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.hasWorkerSessions).toBe(expected);
		expect(result.current.data?.project).not.toHaveProperty("sessions");
	});

	it("resolves a cloud board after the local query succeeds without a matching project", async () => {
		cloudState.ready = true;
		cloudState.org = { id: "org-1" };
		respondWith({});
		listProjectsMock.mockResolvedValue({ items: [{ id: "cloud-1", displayName: "Cloud project" }] });
		listSessionsMock.mockResolvedValue({ items: [{ id: "cloud-worker", projectId: "cloud-1", kind: "worker", status: "working" }] });
		const { result } = renderHook(() => useWorkspaceScope("cloud-1"), { wrapper });
		await waitFor(() => expect(result.current.data?.hasWorkerSessions).toBe(true));
		expect(result.current.data?.project).toMatchObject({ id: "cloud-1", kind: "cloud", name: "Cloud project" });
	});
});

describe("useWorkspacePolling", () => {
	it("polls once per interval however many components subscribe", async () => {
		vi.useFakeTimers();
		try {
			respondWith({});
			const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
			const shared = ({ children }: { children: ReactNode }) => (
				<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
			);
			renderHook(
				() => {
					useWorkspacePolling();
					useWorkspaceQuery();
					useWorkspaceQuery();
					useWorkspaceTraySessions();
				},
				{ wrapper: shared },
			);
			await vi.advanceTimersByTimeAsync(0);
			const sessionFetches = () => getMock.mock.calls.filter(([url]) => url === "/api/v1/sessions").length;
			const afterMount = sessionFetches();
			await vi.advanceTimersByTimeAsync(15_000);
			expect(sessionFetches()).toBe(afterMount + 1);
			await vi.advanceTimersByTimeAsync(15_000);
			expect(sessionFetches()).toBe(afterMount + 2);
		} finally {
			vi.useRealTimers();
		}
	});
});

describe("cloud projects made elsewhere", () => {
	const cloudProject = (id: string, displayName: string) => ({
		id, orgId: "org-1", displayName, repositoryUrl: `https://github.com/acme/${displayName}`, defaultBranch: "main",
		config: {}, createdAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z",
	});
	const cloudSession = (id: string, projectId: string) => ({
		id, orgId: "org-1", projectId, kind: "worker", harness: "claude-code", displayName: id, status: "working",
		activityState: "active", isTerminated: false, prs: [], createdAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z",
	});

	function clientWrapper() {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
		const Wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		return { queryClient, Wrapper };
	}

	beforeEach(() => {
		cloudState.ready = true;
		cloudState.org = { id: "org-1" };
		respondWith({});
	});

	it("refetches the project list once when the session list has a session of an unknown project", async () => {
		// The project was made through the API after the list was fetched: the
		// list is refetched so the project and its session show up.
		listProjectsMock
			.mockResolvedValueOnce({ items: [cloudProject("cp-1", "known")], page: { hasMore: false } })
			.mockResolvedValue({ items: [cloudProject("cp-1", "known"), cloudProject("cp-new", "made-elsewhere")], page: { hasMore: false } });
		listSessionsMock.mockResolvedValue({ items: [cloudSession("s-new", "cp-new")], page: { hasMore: false } });
		const { queryClient, Wrapper } = clientWrapper();

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper: Wrapper });

		await waitFor(() => expect(result.current.data?.find((workspace) => workspace.id === "cp-new")?.sessions.map((session) => session.id)).toEqual(["s-new"]), { timeout: 3000 });
		expect(listProjectsMock).toHaveBeenCalledTimes(2);

		// Seen again (the next poll), it is not asked for again.
		await queryClient.refetchQueries({ queryKey: cloudSessionsQueryKey });
		await new Promise((resolve) => setTimeout(resolve, UNKNOWN_CLOUD_PROJECT_REFETCH_DEBOUNCE_MS + 100));
		expect(listProjectsMock).toHaveBeenCalledTimes(2);
	});

	it("refetches the project list when a session lookup finds a session of an unknown project", async () => {
		// A session opened straight from a link or right after the API made it.
		listProjectsMock.mockResolvedValue({ items: [cloudProject("cp-1", "known")], page: { hasMore: false } });
		getSessionMock.mockResolvedValue({ session: cloudSession("s-new", "cp-new") });
		const { Wrapper } = clientWrapper();

		const { result } = renderHook(() => ({ board: useWorkspaceQuery(), lookup: useCloudSessionQuery("org-1", "s-new") }), { wrapper: Wrapper });
		await waitFor(() => expect(result.current.board.data?.some((workspace) => workspace.id === "cp-1")).toBe(true));
		await waitFor(() => expect(result.current.lookup.data?.projectId).toBe("cp-new"));
		await waitFor(() => expect(listProjectsMock).toHaveBeenCalledTimes(2), { timeout: 3000 });
	});

	describe("polling a session lookup", () => {
		// The view polls the direct lookup while the list does not have the
		// session. It stops once the session is gone (404), the lookup fails, or
		// the session is terminated.
		async function pollFor(milliseconds: number) {
			await act(async () => {
				await vi.advanceTimersByTimeAsync(milliseconds);
			});
		}

		beforeEach(() => {
			vi.useFakeTimers();
			getSessionMock.mockReset();
			listProjectsMock.mockResolvedValue({ items: [], page: { hasMore: false } });
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it("keeps polling a live session", async () => {
			getSessionMock.mockResolvedValue({ session: cloudSession("s-1", "cp-1") });
			const { Wrapper } = clientWrapper();
			const { unmount } = renderHook(() => useCloudSessionQuery("org-1", "s-1", true, true), { wrapper: Wrapper });
			await pollFor(0);
			expect(getSessionMock).toHaveBeenCalledTimes(1);
			await pollFor(10_000);
			expect(getSessionMock).toHaveBeenCalledTimes(3);
			unmount();
		});

		it("stops for a session the control plane no longer has, without a retry", async () => {
			getSessionMock.mockRejectedValue(new CloudCpError("session not found", { status: 404, code: "not_found" }));
			const { Wrapper } = clientWrapper();
			const { result, unmount } = renderHook(() => useCloudSessionQuery("org-1", "s-gone", true, true), { wrapper: Wrapper });
			await pollFor(0);
			expect(result.current.isError).toBe(true);
			await pollFor(30_000);
			expect(getSessionMock).toHaveBeenCalledTimes(1);
			unmount();
		});

		it("stops once the lookup fails", async () => {
			getSessionMock
				.mockResolvedValueOnce({ session: cloudSession("s-1", "cp-1") })
				.mockRejectedValue(new CloudCpError("unavailable", { status: 503 }));
			const { Wrapper } = clientWrapper();
			const { result, unmount } = renderHook(() => useCloudSessionQuery("org-1", "s-1", true, true), { wrapper: Wrapper });
			await pollFor(0);
			expect(result.current.isError).toBe(false);
			// The next poll fails, and its one retry too.
			await pollFor(5_100);
			expect(result.current.isError).toBe(true);
			const calls = getSessionMock.mock.calls.length;
			expect(calls).toBe(3);
			await pollFor(30_000);
			expect(getSessionMock).toHaveBeenCalledTimes(calls);
			unmount();
		});

		it("stops for a terminated session", async () => {
			getSessionMock.mockResolvedValue({ session: { ...cloudSession("s-1", "cp-1"), isTerminated: true } });
			const { Wrapper } = clientWrapper();
			const { unmount } = renderHook(() => useCloudSessionQuery("org-1", "s-1", true, true), { wrapper: Wrapper });
			await pollFor(0);
			await pollFor(30_000);
			expect(getSessionMock).toHaveBeenCalledTimes(1);
			unmount();
		});
	});

	it("asks once for a project the list never returns, however often its session is seen", async () => {
		listProjectsMock.mockResolvedValue({ items: [cloudProject("cp-1", "known")], page: { hasMore: false } });
		listSessionsMock.mockResolvedValue({ items: [cloudSession("s-far", "cp-past-first-page")], page: { hasMore: false } });
		const { queryClient, Wrapper } = clientWrapper();

		renderHook(() => useWorkspaceQuery(), { wrapper: Wrapper });

		await waitFor(() => expect(listProjectsMock).toHaveBeenCalledTimes(2), { timeout: 3000 });
		for (let poll = 0; poll < 3; poll++) await queryClient.refetchQueries({ queryKey: cloudSessionsQueryKey });
		await new Promise((resolve) => setTimeout(resolve, UNKNOWN_CLOUD_PROJECT_REFETCH_DEBOUNCE_MS + 100));
		expect(listProjectsMock).toHaveBeenCalledTimes(2);
	});

	it("gathers sightings into one refetch, and leaves a list not fetched yet alone", () => {
		vi.useFakeTimers();
		try {
			const queryClient = new QueryClient();
			const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
			const key = [...cloudProjectsQueryKey, "https://cp.example.com", "org-1"];

			expect(refetchCloudProjectsForUnknownSessions(queryClient, "https://cp.example.com", "org-1", [{ projectId: "cp-a" }])).toBe(false);

			queryClient.setQueryData(key, [cloudProject("cp-1", "known")]);
			expect(refetchCloudProjectsForUnknownSessions(queryClient, "https://cp.example.com", "org-1", [{ projectId: "cp-1" }])).toBe(false);
			// A session lookup and an event see two new projects in the same moment.
			expect(refetchCloudProjectsForUnknownSessions(queryClient, "https://cp.example.com", "org-1", [{ projectId: "cp-a" }])).toBe(true);
			expect(refetchCloudProjectsForUnknownSessions(queryClient, "https://cp.example.com", "org-1", [{ projectId: "cp-b" }])).toBe(true);
			expect(refetchCloudProjectsForUnknownSessions(queryClient, "https://cp.example.com", "org-1", [{ projectId: "cp-a" }])).toBe(false);
			expect(invalidate).not.toHaveBeenCalled();
			vi.advanceTimersByTime(UNKNOWN_CLOUD_PROJECT_REFETCH_DEBOUNCE_MS);
			expect(invalidate).toHaveBeenCalledTimes(1);
			expect(invalidate).toHaveBeenCalledWith({ queryKey: key, exact: true });
		} finally {
			vi.useRealTimers();
		}
	});

	it("refetches the project list when the window regains focus", async () => {
		listProjectsMock.mockResolvedValue({ items: [cloudProject("cp-1", "known")], page: { hasMore: false } });
		const { Wrapper } = clientWrapper();

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper: Wrapper });
		await waitFor(() => expect(result.current.data?.some((workspace) => workspace.id === "cp-1")).toBe(true));
		expect(listProjectsMock).toHaveBeenCalledTimes(1);

		act(() => {
			focusManager.setFocused(false);
			focusManager.setFocused(true);
		});
		await waitFor(() => expect(listProjectsMock).toHaveBeenCalledTimes(2));
		focusManager.setFocused(undefined);
	});
});
