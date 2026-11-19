import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "../lib/cloud-cp";
import type { WorkspaceSummary } from "../types/workspace";

const { deleteMock, remoteDeleteMock, navigateMock } = vi.hoisted(() => ({
	deleteMock: vi.fn(),
	remoteDeleteMock: vi.fn(),
	navigateMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock },
	apiErrorCode: (error: { code?: string } | undefined) => error?.code,
	apiErrorMessage: (error: { message?: string } | undefined, fallback?: string) => error?.message ?? fallback ?? "",
}));
vi.mock("../lib/host-clients", () => ({
	clientForHost: (hostId: string) => ({ DELETE: (...args: unknown[]) => remoteDeleteMock(hostId, ...args) }),
}));
vi.mock("../lib/telemetry", () => ({
	addRendererExceptionStep: vi.fn(),
	captureRendererEvent: vi.fn(),
	captureRendererException: vi.fn(),
}));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigateMock,
}));

import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useUiStore } from "../stores/ui-store";
import { applyOptimisticProjectRemovals, clearOptimisticProjectRemoval, isOptimisticallyRemovedProject } from "./optimistic-project-removals";
import { isRemoveTarget, useRemoveProject } from "./useRemoveProject";
import { cloudProjectsQueryKey, remoteProjectsQueryKey, remoteSessionsQueryKey, remoteWorkspaceQueryKey, sharedCloudWorkspacesQueryKey, workspaceQueryKey } from "./useWorkspaceQuery";

const project = (id: string, name: string, extra: Partial<WorkspaceSummary> = {}): WorkspaceSummary => ({
	id,
	name,
	kind: "single_repo",
	path: `/repos/${id}`,
	sessions: [],
	...extra,
});

const first = project("proj-1", "first");
const doomed = project("proj-2", "doomed", {
	sessions: [{
		id: "sess-2",
		workspaceId: "proj-2",
		workspaceName: "doomed",
		title: "worker",
		provider: "codex",
		status: "working",
		updatedAt: "2026-11-01T00:00:00Z",
		prs: [],
	}],
});
const last = project("proj-3", "last");

/** What the daemon lists. The fetch filters pending removals, as fetchWorkspaces does. */
let serverLocal: WorkspaceSummary[];
let serverReadFails: boolean;

function setup({ viewing = false, cloud = { client: {} as CloudCpClient, orgId: "org-1" as string | undefined } } = {}) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
	queryClient.setQueryDefaults(workspaceQueryKey, {
		queryFn: async () => {
			if (serverReadFails) throw new Error("daemon unreachable");
			return applyOptimisticProjectRemovals(serverLocal);
		},
	});
	queryClient.setQueryData(workspaceQueryKey, serverLocal);
	const isViewing = vi.fn(() => viewing);
	// Matches the way the shell finds the row, so the kind tells local from cloud.
	const findWorkspace = (projectId: string, hostId?: string, cloud?: boolean) =>
		[...(queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey) ?? []),
			...(queryClient.getQueryData<WorkspaceSummary[]>(remoteWorkspaceQueryKey("box-a")) ?? [])]
			.find((item) => isRemoveTarget(item, projectId, hostId, cloud));
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	const view = renderHook(() => useRemoveProject({ findWorkspace, isViewing, cloud }), { wrapper });
	return { queryClient, remove: view.result.current, isViewing };
}

const localIds = (queryClient: QueryClient) =>
	queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey)?.map((item) => item.id);

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}

const toasts = () => useUiStore.getState().globalToasts;

beforeEach(() => {
	serverLocal = [first, doomed, last];
	serverReadFails = false;
	deleteMock.mockReset();
	remoteDeleteMock.mockReset();
	navigateMock.mockReset().mockResolvedValue(undefined);
	useUiStore.getState().clearGlobalToast();
	useSidebarOrderStore.setState({ pinnedProjectIds: [] });
});

afterEach(() => {
	for (const id of ["proj-1", "proj-2", "proj-3", "cloud-1"]) {
		clearOptimisticProjectRemoval(id);
		clearOptimisticProjectRemoval(id, "box-a");
		clearOptimisticProjectRemoval(id, undefined, true);
	}
});

describe("useRemoveProject", () => {
	it("drops the project and says so before the daemon answers, with no pending state", async () => {
		const response = deferred<{ data: { projectId: string; removedStorageDir: boolean } }>();
		deleteMock.mockReturnValue(response.promise);
		useSidebarOrderStore.setState({ pinnedProjectIds: ["proj-2"] });
		const { queryClient, remove } = setup();

		act(() => remove("proj-2"));

		await waitFor(() => expect(localIds(queryClient)).toEqual(["proj-1", "proj-3"]));
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/projects/{id}", { params: { path: { id: "proj-2" } } });
		expect(toasts()).toEqual([expect.objectContaining({ title: "Removed doomed", tone: "success" })]);
		// Not viewing the project: the route stays where it is.
		expect(navigateMock).not.toHaveBeenCalled();

		// A poll lands while the daemon is still tearing the project down.
		await act(() => queryClient.refetchQueries({ queryKey: workspaceQueryKey, exact: true }));
		expect(localIds(queryClient)).toEqual(["proj-1", "proj-3"]);

		serverLocal = [first, last];
		await act(async () => response.resolve({ data: { projectId: "proj-2", removedStorageDir: false } }));
		await waitFor(() => expect(isOptimisticallyRemovedProject("proj-2")).toBe(false));
		expect(localIds(queryClient)).toEqual(["proj-1", "proj-3"]);
		expect(useSidebarOrderStore.getState().pinnedProjectIds).toEqual([]);
		expect(toasts()).toHaveLength(1);
	});

	it("leaves the project's page before the project leaves the list", async () => {
		deleteMock.mockReturnValue(new Promise(() => undefined));
		const { queryClient, remove, isViewing } = setup({ viewing: true });
		let listedWhileNavigating: string[] | undefined;
		navigateMock.mockImplementation(async () => { listedWhileNavigating = localIds(queryClient); });

		act(() => remove("proj-2"));

		await waitFor(() => expect(localIds(queryClient)).toEqual(["proj-1", "proj-3"]));
		expect(isViewing).toHaveBeenCalledWith(doomed);
		expect(navigateMock).toHaveBeenCalledWith({ to: "/", replace: true });
		expect(listedWhileNavigating).toEqual(["proj-1", "proj-2", "proj-3"]);
	});

	it("puts the project back in place with the reason and a Retry that removes it again", async () => {
		deleteMock.mockResolvedValueOnce({ error: { message: "Session kill failed: agent busy", code: "PROJECT_REMOVE_FAILED" } });
		useSidebarOrderStore.setState({ pinnedProjectIds: ["proj-2"] });
		const { queryClient, remove } = setup();

		act(() => remove("proj-2"));

		await waitFor(() => expect(toasts()[0]).toMatchObject({ title: "Could not remove doomed", tone: "error" }));
		expect(toasts()).toHaveLength(1);
		expect(toasts()[0]?.body).toBe("Session kill failed: agent busy");
		expect(localIds(queryClient)).toEqual(["proj-1", "proj-2", "proj-3"]);
		expect(isOptimisticallyRemovedProject("proj-2")).toBe(false);
		// The pin stays, so the project returns to its pinned place.
		expect(useSidebarOrderStore.getState().pinnedProjectIds).toEqual(["proj-2"]);

		deleteMock.mockReturnValueOnce(new Promise(() => undefined));
		act(() => toasts()[0]?.action?.run());
		await waitFor(() => expect(localIds(queryClient)).toEqual(["proj-1", "proj-3"]));
		expect(deleteMock).toHaveBeenCalledTimes(2);
	});

	it("restores the snapshot when the daemon cannot be read after a failed removal", async () => {
		deleteMock.mockResolvedValueOnce({ error: { message: "socket hang up" } });
		const { queryClient, remove } = setup();
		serverReadFails = true;

		act(() => remove("proj-2"));

		await waitFor(() => expect(toasts()[0]).toMatchObject({ title: "Could not remove doomed", tone: "error" }));
		expect(localIds(queryClient)).toEqual(["proj-1", "proj-2", "proj-3"]);
	});

	it("stays removed when the request failed but the teardown finished anyway", async () => {
		deleteMock.mockImplementationOnce(async () => {
			serverLocal = [first, last];
			return { error: { message: "Failed to remove project" } };
		});
		const { queryClient, remove } = setup();

		act(() => remove("proj-2"));

		await waitFor(() => expect(isOptimisticallyRemovedProject("proj-2")).toBe(false));
		await waitFor(() => expect(localIds(queryClient)).toEqual(["proj-1", "proj-3"]));
		expect(toasts()).toEqual([expect.objectContaining({ title: "Removed doomed", tone: "success" })]);
	});

	it("keeps the tombstone when the read after a successful removal fails", async () => {
		deleteMock.mockImplementationOnce(async () => {
			serverReadFails = true;
			return { data: { projectId: "proj-2", removedStorageDir: false } };
		});
		const { queryClient, remove } = setup();

		act(() => remove("proj-2"));

		await waitFor(() => expect(queryClient.getQueryState(workspaceQueryKey)?.status).toBe("error"));
		await new Promise((resolve) => setTimeout(resolve, 0));
		// Nothing confirmed the removal, so a list requested before it cannot bring the row back.
		expect(isOptimisticallyRemovedProject("proj-2")).toBe(true);
		expect(localIds(queryClient)).toEqual(["proj-1", "proj-3"]);
	});

	it("lets a remote host's project list, not its session list, decide a failed removal", async () => {
		const remote = project("proj-2", "far", { hostId: "box-a" });
		remoteDeleteMock.mockImplementationOnce(async () => {
			// The host finished the teardown after the request gave up.
			hostProjects = [];
			return { error: { message: "context deadline exceeded" } };
		});
		const { queryClient, remove } = setup();
		let hostProjects = [{ id: "proj-2", name: "far", path: "/far" }];
		queryClient.setQueryDefaults(remoteWorkspaceQueryKey("box-a"), { queryFn: async () => [] });
		queryClient.setQueryDefaults(remoteProjectsQueryKey("box-a"), { queryFn: async () => hostProjects });
		queryClient.setQueryDefaults(remoteSessionsQueryKey("box-a"), { queryFn: async () => { throw new Error("sessions unavailable"); } });
		queryClient.setQueryData(remoteProjectsQueryKey("box-a"), hostProjects);
		queryClient.setQueryData(remoteSessionsQueryKey("box-a"), []);
		queryClient.setQueryData(remoteWorkspaceQueryKey("box-a"), [remote]);

		act(() => remove("proj-2", "box-a"));

		await waitFor(() => expect(queryClient.getQueryData(remoteProjectsQueryKey("box-a"))).toEqual([]));
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(toasts()).toEqual([expect.objectContaining({ title: "Removed far", tone: "success" })]);
		expect(queryClient.getQueryData(remoteWorkspaceQueryKey("box-a"))).toEqual([]);
	});

	it("removes a remote project from its host's lists the same way", async () => {
		const remote = project("proj-2", "far", { hostId: "box-a" });
		const response = deferred<{ error?: { message: string } }>();
		remoteDeleteMock.mockReturnValue(response.promise);
		const { queryClient, remove } = setup();
		let hostProjects = [{ id: "proj-2", name: "far", path: "/far" }];
		// Defaults merge by key prefix in registration order: the host's list first, its raw projects list after.
		queryClient.setQueryDefaults(remoteWorkspaceQueryKey("box-a"), {
			queryFn: async () => applyOptimisticProjectRemovals(hostProjects.map((item) => ({ ...remote, id: item.id }))),
		});
		queryClient.setQueryDefaults(remoteProjectsQueryKey("box-a"), { queryFn: async () => hostProjects });
		queryClient.setQueryData(remoteProjectsQueryKey("box-a"), hostProjects);
		queryClient.setQueryData(remoteWorkspaceQueryKey("box-a"), [remote]);

		act(() => remove("proj-2", "box-a"));

		await waitFor(() => expect(queryClient.getQueryData(remoteWorkspaceQueryKey("box-a"))).toEqual([]));
		expect(isOptimisticallyRemovedProject("proj-2", "box-a")).toBe(true);
		// The local project with the same id is untouched.
		expect(localIds(queryClient)).toEqual(["proj-1", "proj-2", "proj-3"]);
		expect(remoteDeleteMock).toHaveBeenCalledWith("box-a", "/api/v1/projects/{id}", { params: { path: { id: "proj-2" } } });

		hostProjects = [];
		await act(async () => response.resolve({}));
		await waitFor(() => expect(isOptimisticallyRemovedProject("proj-2", "box-a")).toBe(false));
		expect(queryClient.getQueryData(remoteWorkspaceQueryKey("box-a"))).toEqual([]);
	});

	it("drops a cloud project from the raw cloud list too, and keeps it hidden after the control plane accepts", async () => {
		const cloudy = project("cloud-1", "cloudy", { kind: "cloud" });
		serverLocal = [first, cloudy];
		const deleteProject = vi.fn().mockResolvedValue({});
		const { queryClient, remove } = setup({ cloud: { client: { deleteProject } as unknown as CloudCpClient, orgId: "org-1" } });
		const rawKey = [...cloudProjectsQueryKey, "https://cp.example", "org-1"];
		// The control plane archives after answering, so it still lists the
		// project; the fetch filters pending removals, as useCloudProjectsQuery does.
		queryClient.setQueryDefaults(cloudProjectsQueryKey, {
			queryFn: async () => [{ id: "cloud-1", name: "cloudy" }].filter((item) => !isOptimisticallyRemovedProject(item.id, undefined, true)),
		});
		queryClient.setQueryData(rawKey, [{ id: "cloud-1", name: "cloudy" }]);

		act(() => remove("cloud-1"));

		expect(queryClient.getQueryData(rawKey)).toEqual([]);
		await waitFor(() => expect(deleteProject).toHaveBeenCalledWith("org-1", "cloud-1"));
		await waitFor(() => expect(toasts()).toEqual([expect.objectContaining({ title: "Deleted cloudy", tone: "success" })]));
		// Cloud ids are never reused, so the tombstone outlives the async archive.
		expect(isOptimisticallyRemovedProject("cloud-1", undefined, true)).toBe(true);
		expect(queryClient.getQueryData(rawKey)).toEqual([]);
	});

	it("puts a cloud project back in the raw cloud list when its removal fails and the list cannot be read", async () => {
		const cloudy = project("cloud-1", "cloudy", { kind: "cloud" });
		serverLocal = [first, cloudy];
		const deleteProject = vi.fn().mockRejectedValue(new Error("control plane said no"));
		const { queryClient, remove } = setup({ cloud: { client: { deleteProject } as unknown as CloudCpClient, orgId: "org-1" } });
		const rawKey = [...cloudProjectsQueryKey, "https://cp.example", "org-1"];
		queryClient.setQueryDefaults(cloudProjectsQueryKey, { queryFn: async () => { throw new Error("offline"); } });
		queryClient.setQueryData(rawKey, [{ id: "other", name: "other" }, { id: "cloud-1", name: "cloudy" }]);

		act(() => remove("cloud-1"));

		await waitFor(() => expect(toasts()[0]).toMatchObject({ title: "Could not remove cloudy", body: "control plane said no", tone: "error" }));
		expect(queryClient.getQueryData(rawKey)).toEqual([{ id: "other", name: "other" }, { id: "cloud-1", name: "cloudy" }]);
		expect(isOptimisticallyRemovedProject("cloud-1", undefined, true)).toBe(false);
	});

	it("puts a shared cloud project back when the control plane refuses to remove it", async () => {
		// Shared with the user, so it is not in their own cloud project list.
		const shared = project("cloud-1", "theirs", { kind: "cloud", shared: { sources: [] } as unknown as WorkspaceSummary["shared"] });
		serverLocal = [first, shared];
		const deleteProject = vi.fn().mockRejectedValue(new Error("Not the project owner"));
		const { queryClient, remove } = setup({ cloud: { client: { deleteProject } as unknown as CloudCpClient, orgId: "org-1" } });
		const ownKey = [...cloudProjectsQueryKey, "https://cp.example", "org-1"];
		const sharedKey = [...sharedCloudWorkspacesQueryKey, "https://cp.example"];
		queryClient.setQueryDefaults(cloudProjectsQueryKey, { queryFn: async () => [] });
		queryClient.setQueryDefaults(sharedCloudWorkspacesQueryKey, { queryFn: async () => [shared] });
		queryClient.setQueryData(ownKey, []);
		queryClient.setQueryData(sharedKey, [shared]);

		act(() => remove("cloud-1"));

		await waitFor(() => expect(toasts()[0]).toMatchObject({ title: "Could not remove theirs", body: "Not the project owner", tone: "error" }));
		expect(isOptimisticallyRemovedProject("cloud-1", undefined, true)).toBe(false);
		expect(toasts()[0]?.action?.label).toBe("Retry");
	});

	it("removes the cloud row the user picked, not a local project with the same id", async () => {
		const cloudTwin = project("proj-2", "cloud twin", { kind: "cloud" });
		serverLocal = [first, doomed, last, cloudTwin];
		const deleteProject = vi.fn().mockReturnValue(new Promise(() => undefined));
		const { queryClient, remove } = setup({ cloud: { client: { deleteProject } as unknown as CloudCpClient, orgId: "org-1" } });

		act(() => remove("proj-2", undefined, true));

		await waitFor(() => expect(deleteProject).toHaveBeenCalledWith("org-1", "proj-2"));
		expect(deleteMock).not.toHaveBeenCalled();
		expect(isOptimisticallyRemovedProject("proj-2", undefined, true)).toBe(true);
		// The local project keeps its row and its tombstone stays clear.
		expect(isOptimisticallyRemovedProject("proj-2")).toBe(false);
		expect(applyOptimisticProjectRemovals([doomed, cloudTwin])).toEqual([doomed]);
		expect(localIds(queryClient)).toContain("proj-2");
		// A Cloud project is deleted, not removed from a list.
		expect(toasts()).toEqual([expect.objectContaining({ title: "Deleted cloud twin" })]);
	});

	it("removes the local row the user picked, not a cloud project with the same id", async () => {
		const cloudTwin = project("proj-2", "cloud twin", { kind: "cloud" });
		serverLocal = [cloudTwin, first, doomed, last];
		deleteMock.mockReturnValue(new Promise(() => undefined));
		const deleteProject = vi.fn();
		const { remove } = setup({ cloud: { client: { deleteProject } as unknown as CloudCpClient, orgId: "org-1" } });

		act(() => remove("proj-2", undefined, false));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledWith("/api/v1/projects/{id}", { params: { path: { id: "proj-2" } } }));
		expect(deleteProject).not.toHaveBeenCalled();
		expect(applyOptimisticProjectRemovals([doomed, cloudTwin])).toEqual([cloudTwin]);
	});

	it("removes a local row with no kind (older daemon), not a cloud project listed before it", async () => {
		const cloudTwin = project("proj-2", "cloud twin", { kind: "cloud" });
		const legacyLocal = project("proj-2", "legacy", { kind: undefined });
		serverLocal = [cloudTwin, first, legacyLocal, last];
		deleteMock.mockReturnValue(new Promise(() => undefined));
		const deleteProject = vi.fn();
		const { remove } = setup({ cloud: { client: { deleteProject } as unknown as CloudCpClient, orgId: "org-1" } });

		act(() => remove("proj-2", undefined, false));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledWith("/api/v1/projects/{id}", { params: { path: { id: "proj-2" } } }));
		expect(deleteProject).not.toHaveBeenCalled();
		expect(toasts()).toEqual([expect.objectContaining({ title: "Removed legacy" })]);
	});

	it("does not remove a cloud project while the control plane is not ready", async () => {
		serverLocal = [first, project("cloud-1", "cloudy", { kind: "cloud" })];
		const { queryClient, remove } = setup({ cloud: { client: {} as CloudCpClient, orgId: undefined } });

		act(() => remove("cloud-1"));

		await waitFor(() => expect(toasts()[0]).toMatchObject({ title: "Could not remove cloudy", tone: "error" }));
		expect(isOptimisticallyRemovedProject("cloud-1", undefined, true)).toBe(false);
		expect(localIds(queryClient)).toEqual(["proj-1", "cloud-1"]);
	});
});
