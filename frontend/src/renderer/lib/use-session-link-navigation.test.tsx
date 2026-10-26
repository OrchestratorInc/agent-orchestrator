import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useSessionLinkNavigation, useSessionLinkSource } from "./use-session-link-navigation";

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), workspace: vi.fn(), cloudProjects: vi.fn(), cloudSessions: vi.fn(), get: vi.fn() }));
vi.mock("./api-client", () => ({ apiClient: { GET: mocks.get } }));
vi.mock("./navigate-to-session", () => ({ useNavigateToSession: () => mocks.navigate }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => mocks.workspace(),
	useCloudProjectsQuery: () => mocks.cloudProjects(),
	useCloudSessionsQuery: () => mocks.cloudSessions(),
	toCloudWorkspace: (project: { id: string; displayName?: string }, sessions: Array<{ id: string; projectId: string }>) => ({
		id: project.id,
		name: project.displayName ?? project.id,
		path: "",
		sessions: sessions.filter((session) => session.projectId === project.id),
	}),
	toWorkspaceSession: (row: object, project: { id: string; name: string }) => ({ ...row, workspaceId: project.id, workspaceName: project.name }),
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
}));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ ready: true, org: { id: "org-1" } }) }));

let client: QueryClient;
function wrapper({ children }: { children: ReactNode }) { return <QueryClientProvider client={client}>{children}</QueryClientProvider>; }
afterEach(() => client.clear());

describe("useSessionLinkNavigation", () => {
	beforeEach(() => {
		client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		mocks.get.mockReset();
		mocks.navigate.mockReset();
		mocks.workspace.mockClear();
		mocks.cloudProjects.mockClear();
		mocks.cloudSessions.mockClear();
		mocks.cloudProjects.mockReturnValue({ isSuccess: true, data: [] });
		mocks.cloudSessions.mockReturnValue({ isSuccess: true, data: [] });
		useUiStore.setState({ globalToast: null, globalToasts: [], globalToastSequence: 0 });
		mocks.workspace.mockReturnValue({
			isSuccess: true,
			data: [
				{ id: "other-project", sessions: [{ id: "other-session", isTerminated: false }] },
				{ id: "project", sessions: [{ id: "session", title: "renamed", isTerminated: false }, { id: "terminated", isTerminated: true }] },
			],
		});
	});

	it("selects the exact cross-project session by stable ID", () => {
		const { result } = renderHook(() => useSessionLinkNavigation(), { wrapper });
		act(() => expect(result.current("ao://sessions/other-project/other-session")).toBe(true));
		expect(mocks.navigate).toHaveBeenCalledWith("other-project", "other-session");
		expect(useUiStore.getState().globalToasts).toEqual([]);
	});

	it("resolves a remote chat link on its source host without querying local or Cloud", () => {
		mocks.workspace.mockReturnValue({ isSuccess: true, data: [{ id: "project", sessions: [{ id: "session", isTerminated: true }] }] });
		const queryClient = new QueryClient();
		queryClient.setQueryData(["remote-workspaces", "box-a"], [{ id: "project", sessions: [{ id: "session", isTerminated: false }] }]);
		queryClient.setQueryData(["remote-workspaces", "box-b"], [{ id: "project", sessions: [{ id: "session", isTerminated: true }] }]);
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		const { result } = renderHook(() => useSessionLinkNavigation("box-a"), { wrapper });
		act(() => expect(result.current("ao://sessions/project/session")).toBe(true));
		expect(mocks.navigate).toHaveBeenCalledWith("project", "session", "box-a");
		expect(mocks.workspace).not.toHaveBeenCalled();
		expect(mocks.cloudProjects).not.toHaveBeenCalled();
		expect(mocks.cloudSessions).not.toHaveBeenCalled();
	});

	it("resolves a Cloud chat link without querying local or remote workspaces", () => {
			mocks.workspace.mockReturnValue({ isSuccess: false, data: undefined });
			mocks.cloudProjects.mockReturnValue({ isSuccess: true, data: [{ id: "project" }] });
			mocks.cloudSessions.mockReturnValue({ isSuccess: true, data: [{ id: "session", projectId: "project", isTerminated: false }] });
			const { result } = renderHook(() => useSessionLinkNavigation(undefined, "cloud"), { wrapper });
			act(() => expect(result.current("ao://sessions/project/session")).toBe(true));
			expect(mocks.navigate).toHaveBeenCalledWith("project", "session");
			expect(useUiStore.getState().globalToasts).toEqual([]);
			expect(mocks.workspace).not.toHaveBeenCalled();
	});

	it("shows feedback instead of navigating to a terminated session", () => {
		const { result } = renderHook(() => useSessionLinkNavigation(), { wrapper });
		act(() => expect(result.current("ao://sessions/project/terminated")).toBe(false));
		act(() => expect(result.current("ao://sessions/project/terminated")).toBe(false));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToasts).toEqual([
			expect.objectContaining({
				title: "Session terminated is terminated",
				nonce: 2,
				placement: "top-center",
				dismissible: true,
				durationMs: 5_000,
				dedupeKey: "session-link:project:terminated",
			}),
		]);
	});

	it("navigates to the session and requests the artifact open, even after termination", () => {
		useUiStore.setState({ workspaceFileOpenRequest: null });
		const { result } = renderHook(() => useSessionLinkNavigation(), { wrapper });
		act(() => expect(result.current("ao://sessions/project/terminated/artifacts/plans/my%20plan.md")).toBe(true));
		expect(mocks.navigate).toHaveBeenCalledWith("project", "terminated");
		expect(useUiStore.getState().workspaceFileOpenRequest).toEqual(expect.objectContaining({
			sessionId: "terminated",
			path: "plans/my plan.md",
			source: "artifact",
		}));
		expect(useUiStore.getState().globalToasts).toEqual([]);
	});

	it.each([
		["ao://sessions/project/missing", "missing or is not accessible"],
		["ao://sessions/project/session/kill", "malformed or unsupported"],
	])("rejects %s with actionable feedback", (url, message) => {
		const { result } = renderHook(() => useSessionLinkNavigation(), { wrapper });
		act(() => expect(result.current(url)).toBe(false));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToasts.at(-1)).toEqual(expect.objectContaining({
			title: expect.stringContaining(message),
			tone: "error",
			placement: "top-center",
			dismissible: true,
			dedupeKey: "session-link:error",
		}));
	});

	it("does not navigate when the workspace cannot be verified", () => {
		mocks.workspace.mockReturnValue({ isSuccess: false, data: undefined });
		const { result } = renderHook(() => useSessionLinkNavigation(), { wrapper });
		act(() => expect(result.current("ao://sessions/project/session")).toBe(false));
		expect(useUiStore.getState().globalToasts.at(-1)?.title).toContain("daemon connection");
	});
	it("loads an ended artifact target absent from the live cache without loading archive pages", async () => {
		mocks.workspace.mockReturnValue({ isSuccess: true, data: [{ id: "project", archivedCount: 1, sessions: [] }] });
		mocks.get.mockResolvedValue({ data: { session: { id: "ended", projectId: "project", isTerminated: true } } });
		const { result } = renderHook(() => useSessionLinkNavigation(), { wrapper });
		act(() => expect(result.current("ao://sessions/project/ended/artifacts/plan.md")).toBe(true));
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith("project", "ended"));
		expect(mocks.get).toHaveBeenCalledExactlyOnceWith("/api/v1/sessions/{sessionId}", { params: { path: { sessionId: "ended" } } });
		expect(useUiStore.getState().workspaceFileOpenRequest).toEqual(expect.objectContaining({ sessionId: "ended", path: "plan.md", source: "artifact" }));
	});

	it("rejects a detail target belonging to another project", async () => {
		mocks.workspace.mockReturnValue({ isSuccess: true, data: [{ id: "project", archivedCount: 1, sessions: [] }] });
		mocks.get.mockResolvedValue({ data: { session: { id: "ended", projectId: "other-project", isTerminated: true } } });
		const { result } = renderHook(() => useSessionLinkNavigation(), { wrapper });
		act(() => { result.current("ao://sessions/project/ended/artifacts/plan.md"); });
		await waitFor(() => expect(useUiStore.getState().globalToasts.at(-1)?.title).toContain("missing or is not accessible"));
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("loads ended hover metadata on demand and preserves its restore state", async () => {
		const live = [{ id: "project", archivedCount: 1, sessions: [] }];
		mocks.workspace.mockReturnValue({ isSuccess: true, data: live });
		mocks.get.mockResolvedValue({ data: { session: { id: "ended", projectId: "project", isTerminated: true, artifactFiles: [{ path: "plan.md" }] } } });
		const { result } = renderHook(() => useSessionLinkSource(undefined, undefined, "ao://sessions/project/ended"), { wrapper });
		await waitFor(() => expect(result.current.workspaces[0].sessions[0]?.isTerminated).toBe(true));
		expect(result.current.workspaces[0].sessions[0].artifactFiles).toEqual([{ path: "plan.md" }]);
		expect(live[0].sessions).toEqual([]);
		expect(mocks.get).toHaveBeenCalledTimes(1);
	});

});
