import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useSessionLinkNavigation } from "./use-session-link-navigation";

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), restore: vi.fn(), workspace: vi.fn(), cloudProjects: vi.fn(), cloudSessions: vi.fn() }));
vi.mock("./navigate-to-session", () => ({ useNavigateToSession: () => mocks.navigate }));
vi.mock("../hooks/useRestoreSession", () => ({ useRestoreSession: () => mocks.restore }));
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
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
}));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ ready: true, org: { id: "org-1" } }) }));

describe("useSessionLinkNavigation", () => {
	beforeEach(() => {
		mocks.navigate.mockReset();
		mocks.restore.mockReset();
		mocks.restore.mockResolvedValue({ status: "success" });
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
		const { result } = renderHook(() => useSessionLinkNavigation());
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
			const { result } = renderHook(() => useSessionLinkNavigation(undefined, "cloud"));
			act(() => expect(result.current("ao://sessions/project/session")).toBe(true));
			expect(mocks.navigate).toHaveBeenCalledWith("project", "session");
			expect(useUiStore.getState().globalToasts).toEqual([]);
			expect(mocks.workspace).not.toHaveBeenCalled();
	});

	it("resumes a terminated session once, then opens it", async () => {
		let finish!: (result: { status: "success" }) => void;
		mocks.restore.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("ao://sessions/project/terminated")).toBe(true));
		act(() => expect(result.current("ao://sessions/project/terminated")).toBe(true));
		expect(mocks.restore).toHaveBeenCalledTimes(1);
		expect(mocks.restore).toHaveBeenCalledWith("terminated", undefined);
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToasts).toEqual([
			expect.objectContaining({ title: "Resuming session terminated…", durationMs: 0, dedupeKey: "session-link:project:terminated" }),
		]);
		await act(async () => finish({ status: "success" }));
		expect(mocks.navigate).toHaveBeenCalledWith("project", "terminated");
		expect(useUiStore.getState().globalToasts).toEqual([]);
	});

	it("does not pull the user away when they moved on before the resume finished", async () => {
		let finish!: (result: { status: "success" }) => void;
		mocks.restore.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		window.location.hash = "#/projects/project/sessions/session";
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => { result.current("ao://sessions/project/terminated"); });
		window.location.hash = "#/projects/other-project/sessions/other-session";
		await act(async () => finish({ status: "success" }));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToasts).toEqual([]);
	});

	it("shows why a terminated session could not resume and stays put", async () => {
		mocks.restore.mockResolvedValue({ status: "not_resumable", message: "No saved session is available." });
		const { result } = renderHook(() => useSessionLinkNavigation());
		await act(async () => { result.current("ao://sessions/project/terminated"); });
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToasts).toEqual([
			expect.objectContaining({ title: "No saved session is available.", tone: "error", dedupeKey: "session-link:project:terminated" }),
		]);
	});

	it("navigates to the session and requests the artifact open, even after termination", () => {
		useUiStore.setState({ workspaceFileOpenRequest: null });
		const { result } = renderHook(() => useSessionLinkNavigation());
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
		const { result } = renderHook(() => useSessionLinkNavigation());
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
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("ao://sessions/project/session")).toBe(false));
		expect(useUiStore.getState().globalToasts.at(-1)?.title).toContain("daemon connection");
	});
});
