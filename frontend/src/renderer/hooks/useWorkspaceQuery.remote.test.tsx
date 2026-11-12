import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

const { localGet, remoteConnect, remotePreviewUrl } = vi.hoisted(() => ({ localGet: vi.fn(), remoteConnect: vi.fn(), remotePreviewUrl: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: localGet }, hasTrustedApiBaseUrl: () => true, reportingFetch: (input: Request) => fetch(input) }));
vi.mock("../lib/bridge", () => ({ aoBridge: { remotes: { connect: remoteConnect, previewUrl: remotePreviewUrl, disconnect: vi.fn() } } }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ ready: false, baseUrl: "", client: {} }) }));
vi.mock("./useCloudOrg", () => ({ useCloudOrg: () => ({ org: undefined, ready: false }) }));

import { connectHost, connectedHosts, disconnectHost } from "../lib/host-clients";
import { useUiStore } from "../stores/ui-store";
import { appI18n } from "../i18n";
import { sessionUiKey } from "../lib/hosts";
import { useRemoteUnpinStore } from "../stores/remote-unpin-store";
import { clearOptimisticSessionKill, trackOptimisticSessionKill } from "./optimistic-session-kills";
import { remoteWorkspaceQueryKey, useRemoteWorkspaces, useWorkspaceQuery, useWorkspaceSession } from "./useWorkspaceQuery";

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

beforeEach(() => useUiStore.setState({ developerMode: true, remoteHosts: true }));

afterEach(async () => {
	useUiStore.setState({ developerMode: false, remoteHosts: false });
	for (const hostId of connectedHosts()) await disconnectHost(hostId);
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

async function prepareTwoHosts() {
	localGet.mockImplementation(async (path: string) => path === "/api/v1/projects"
		? { data: { projects: [{ id: "project-1", name: "Local", path: "/local" }] } }
		: { data: { sessions: [{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [] }] } });
	remoteConnect.mockResolvedValue({ hostId: "box-a", label: "Box A", url: "http://box-a:3001", base: "http://127.0.0.1:4000" });
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
		const url = input instanceof Request ? input.url : String(input);
		return new Response(JSON.stringify(url.endsWith("/projects")
			? { projects: [{ id: "project-1", name: "Remote", path: "/remote", autoSpawnPauses: [{ harness: "claude-code", model: "sonnet", effort: "high", kind: "worker", errorCode: "UNSUPPORTED_MODEL", failures: 3, message: "Unavailable", pausedAt: "now" }] }]  }
			: { sessions: [{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [] }] }),
		{ status: 200, headers: { "content-type": "application/json" } });
	}));
	await connectHost("http://box-a:3001");
}

it("lists remote projects separately so local actions cannot target a same-named remote session", async () => {
	await prepareTwoHosts();
	const { result } = renderHook(() => ({ local: useWorkspaceQuery(), remote: useRemoteWorkspaces() }), { wrapper });
	await waitFor(() => expect(result.current.remote.data).toHaveLength(1));
	expect(result.current.local.data?.map((project) => project.name)).toEqual(["Local"]);
	expect(result.current.remote.data?.map((project) => [project.name, project.sessions[0]?.hostId])).toEqual([["Remote", "box-a"]]);
	expect(result.current.remote.data?.[0].autoSpawnPauses?.[0]).toMatchObject({ model: "sonnet", effort: "high", kind: "worker" });
});

it("reports a failed remote query instead of treating the host as empty and healthy", async () => {
	remoteConnect.mockResolvedValue({ hostId: "box-a", label: "Box A", url: "http://box-a:3001", base: "http://127.0.0.1:4000" });
	vi.stubGlobal("fetch", vi.fn(async () => new Response('{"error":"unavailable"}', {
		status: 500,
		headers: { "content-type": "application/json" },
	})));
	await connectHost("http://box-a:3001");
	const { result } = renderHook(() => useRemoteWorkspaces(), { wrapper });
	await waitFor(() => expect(result.current.failedHostIds).toEqual(["box-a"]), { timeout: 3000 });
	expect(result.current.data).toEqual([]);
	expect(result.current.loadedProjectHostIds).toEqual([]);
});

it.each([
	[401, true],
	[426, true],
	[502, true],
	[530, true],
	[500, false],
])("rechecks a connected host after a remote HTTP %i response only when connection health is in doubt", async (status, shouldRecheck) => {
	const hostId = `box-${status}`;
	remoteConnect.mockResolvedValue({ hostId, label: "Box", url: "http://box:3001", base: "http://127.0.0.1:4000" });
	vi.stubGlobal("fetch", vi.fn(async () => Response.json({ error: "failed" }, { status })));
	await connectHost("http://box:3001");
	const dispatched = vi.spyOn(window, "dispatchEvent");
	const { result } = renderHook(() => useWorkspaceSession("session-1", hostId), { wrapper });
	await waitFor(() => expect(result.current.isError).toBe(true));
	expect(dispatched.mock.calls.some(([event]) => event.type === "ao:remote-hosts-changed")).toBe(shouldRecheck);
});

it("coalesces simultaneous and repeated remote request failures into one host recheck", async () => {
	remoteConnect.mockResolvedValue({ hostId: "box-gate", label: "Box", url: "http://box:3001", base: "http://127.0.0.1:4000" });
	vi.stubGlobal("fetch", vi.fn(async () => Response.json({ error: "unauthorized", code: "BAD_PASSWORD" }, { status: 401 })));
	await connectHost("http://box:3001");
	const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
	const dispatched = vi.spyOn(window, "dispatchEvent");
	const { result } = renderHook(() => useRemoteWorkspaces(), { wrapper });
	await waitFor(() => expect(result.current.failedHostIds).toEqual(["box-gate"]), { timeout: 3000 });
	const refreshCount = () => dispatched.mock.calls.filter(([event]) => event.type === "ao:remote-hosts-changed").length;
	expect(refreshCount()).toBe(1);
	await act(async () => { await result.current.refetch(); });
	expect(refreshCount()).toBe(1);
	now.mockReturnValue(1_015_000);
	await act(async () => { await result.current.refetch(); });
	expect(refreshCount()).toBe(2);
});

it("keeps a host's registered projects visible when its sessions request fails", async () => {
	remoteConnect.mockResolvedValue({ hostId: "box-a", label: "Box A", url: "http://box-a:3001", base: "http://127.0.0.1:4000" });
	let sessionsFail = true;
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
		const url = input instanceof Request ? input.url : String(input);
		const isProjects = url.endsWith("/projects");
		return new Response(JSON.stringify(isProjects
			? { projects: [{ id: "project-1", name: "Remote", path: "/remote" }] }
			: sessionsFail ? { error: "unavailable" } : { sessions: [{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [] }] }), {
			status: !isProjects && sessionsFail ? 500 : 200,
			headers: { "content-type": "application/json" },
		});
	}));
	await connectHost("http://box-a:3001");
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const { result } = renderHook(() => useRemoteWorkspaces(), {
		wrapper: ({ children }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
	});

	await waitFor(() => expect(result.current.failedHostIds).toEqual(["box-a"]), { timeout: 3000 });
	expect(result.current.loadedProjectHostIds).toEqual(["box-a"]);
	expect(result.current.data).toEqual([expect.objectContaining({
		hostId: "box-a", id: "project-1", name: "Remote", sessions: [],
	})]);

	sessionsFail = false;
	await act(async () => { await queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey("box-a") }); });
	await waitFor(() => expect(result.current.failedHostIds).toEqual([]));
	expect(result.current.data[0]?.sessions).toEqual([expect.objectContaining({ id: "session-1", hostId: "box-a" })]);

	sessionsFail = true;
	await act(async () => { await queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey("box-a") }); });
	await waitFor(() => expect(result.current.failedHostIds).toEqual(["box-a"]), { timeout: 3000 });
	expect(result.current.data[0]).toEqual(expect.objectContaining({ hostId: "box-a", id: "project-1" }));
});

it("maps remote artifact raw reads through the host proxy", async () => {
	await prepareTwoHosts();
	const rawUrl = "http://ao-preview-artifact.onuxg43jn5xc2mi.localhost:3001/docs/report.md?raw=true";
	const mapped = "http://ao-preview-token.localhost:4000/docs/report.md?raw=true";
	remotePreviewUrl.mockResolvedValue(mapped);
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
		const url = input instanceof Request ? input.url : String(input);
		return Response.json(url.endsWith("/projects")
			? { projects: [{ id: "project-1", name: "Remote", path: "/remote" }] }
			: { sessions: [{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [], artifactFiles: [{ path: "docs/report.md", name: "report.md", kind: "markdown", size: 12, rawUrl }] }] });
	}));
	const { result } = renderHook(() => useRemoteWorkspaces(), { wrapper });
	await waitFor(() => expect(result.current.data[0]?.sessions[0]?.artifactFiles?.[0]?.rawUrl).toBe(mapped));
	expect(remotePreviewUrl).toHaveBeenCalledWith("box-a", "session-1", rawUrl);
});

it("never keeps a remote daemon's inline artifact origin, which here would name this computer", async () => {
	await prepareTwoHosts();
	remotePreviewUrl.mockResolvedValue("http://ao-preview-token.localhost:4000/q3/report.html?raw=true");
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
		const url = input instanceof Request ? input.url : String(input);
		return Response.json(url.endsWith("/projects")
			? { projects: [{ id: "project-1", name: "Remote", path: "/remote" }] }
			: { sessions: [{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [], artifactFiles: [{ path: "q3/report.html", name: "report.html", kind: "html", size: 12, rawUrl: "http://ao-preview-artifact.x.localhost:3001/q3/report.html?raw=true", inlineUrl: "http://ao-inline-artifact.x.localhost:3001/q3/report.html" }] }] });
	}));
	const { result } = renderHook(() => useRemoteWorkspaces(), { wrapper });
	await waitFor(() => expect(result.current.data[0]?.sessions[0]?.artifactFiles?.[0]?.path).toBe("q3/report.html"));
	expect(result.current.data[0]?.sessions[0]?.artifactFiles?.[0]?.inlineUrl).toBeUndefined();
});

it("shows a saved host's last lists without reading it, and its session while it is offline", async () => {
	const fetchMock = vi.fn(async () => new Response("{}", { status: 200 }));
	vi.stubGlobal("fetch", fetchMock);
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(["remote-workspaces", "box-a", "projects"], [{ id: "project-1", name: "Remote", path: "/remote" }]);
	queryClient.setQueryData(["remote-workspaces", "box-a", "sessions"], [{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [] }]);
	const cachedHostIds = ["box-a"];
	const { result } = renderHook(() => ({
		saved: useRemoteWorkspaces({ cachedHostIds }),
		connectedOnly: useRemoteWorkspaces(),
		session: useWorkspaceSession("session-1", "box-a", false),
	}), { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider> });
	expect(result.current.saved.data.map((project) => [project.name, project.hostId])).toEqual([["Remote", "box-a"]]);
	expect(result.current.saved.failedHostIds).toEqual([]);
	expect(result.current.saved.loadedProjectHostIds).toEqual([]);
	// The new-task pickers list only hosts that can take work.
	expect(result.current.connectedOnly.data).toEqual([]);
	expect(result.current.session.data?.id).toBe("session-1");
	expect(fetchMock).not.toHaveBeenCalled();
});

it("keeps the same lists across re-renders until a host's data changes", async () => {
	await prepareTwoHosts();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const { result, rerender } = renderHook(() => useRemoteWorkspaces(), {
		wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
	});
	await waitFor(() => {
		expect(result.current.data).toHaveLength(1);
		expect(result.current.loadedSessionHostIds).toEqual(["box-a"]);
	});
	const { data, failedHostIds, loadedProjectHostIds, loadedSessionHostIds } = result.current;
	rerender();
	// The shell passes these to the memoized sidebar; new lists on every render would re-render it.
	expect(result.current.data).toBe(data);
	expect(result.current.failedHostIds).toBe(failedHostIds);
	expect(result.current.loadedProjectHostIds).toBe(loadedProjectHostIds);
	expect(result.current.loadedSessionHostIds).toBe(loadedSessionHostIds);
	act(() => client.setQueryData(["remote-workspaces", "box-a", "projects"], [{ id: "project-1", name: "Renamed", path: "/remote" }]));
	await waitFor(() => expect(result.current.data[0]?.name).toBe("Renamed"));
});

it("re-maps remote session titles when Developer Mode changes with unchanged host data", async () => {
	remoteConnect.mockResolvedValue({ hostId: "box-a", label: "Box A", url: "http://box-a:3001", base: "http://127.0.0.1:4000" });
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
		const url = input instanceof Request ? input.url : String(input);
		return Response.json(url.endsWith("/projects")
			? { projects: [{ id: "project-1", name: "Remote", path: "/remote" }] }
			: { sessions: [{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [], displayName: "Fix login", terminalTitle: "vim main.go" }] });
	}));
	await connectHost("http://box-a:3001");
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const { result } = renderHook(() => useRemoteWorkspaces(), {
		wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
	});
	// Developer Mode is on (beforeEach): the terminal title shows.
	await waitFor(() => expect(result.current.data[0]?.sessions[0]?.title).toBe("vim main.go"));
	const sessionsData = client.getQueryData(["remote-workspaces", "box-a", "sessions"]);

	act(() => useUiStore.setState({ developerMode: false }));

	expect(client.getQueryData(["remote-workspaces", "box-a", "sessions"])).toBe(sessionsData);
	expect(result.current.data[0]?.sessions[0]?.title).toBe("Fix login");
});

// A saved host whose lists are only in the cache: nothing below refetches, so
// each change has to reach the memoized lists without new host data.
function renderCachedHost(sessions: unknown[], projects: unknown[] = [{ id: "project-1", name: "Remote", path: "/remote" }]) {
	vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200 })));
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(["remote-workspaces", "box-a", "projects"], projects);
	client.setQueryData(["remote-workspaces", "box-a", "sessions"], sessions);
	const cachedHostIds = ["box-a"];
	const view = renderHook(() => useRemoteWorkspaces({ cachedHostIds }), {
		wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
	});
	return { client, ...view };
}

it("applies and lifts an unpin made here while the host's lists stay the same", () => {
	const pinnedAt = "2026-11-01T00:00:00Z";
	const { client, result } = renderCachedHost([{ id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [], isPinned: true, pinnedAt }]);
	const sessionsData = client.getQueryData(["remote-workspaces", "box-a", "sessions"]);
	expect(result.current.data[0]?.sessions[0]?.isPinned).toBe(true);
	try {
		act(() => useRemoteUnpinStore.getState().add("box-a", "session-1", pinnedAt));
		expect(result.current.data[0]?.sessions[0]?.isPinned).toBe(false);

		act(() => useRemoteUnpinStore.getState().remove([sessionUiKey("session-1", "box-a")]));
		expect(result.current.data[0]?.sessions[0]?.isPinned).toBe(true);
		expect(client.getQueryData(["remote-workspaces", "box-a", "sessions"])).toBe(sessionsData);
	} finally {
		useRemoteUnpinStore.setState({ unpins: {} });
	}
});

it("marks a remote session terminated while its kill is in flight, and rolls it back when the kill fails", () => {
	const live = { id: "session-1", projectId: "project-1", harness: "codex", status: "working", prs: [] };
	const { client, result } = renderCachedHost([live]);
	const sessionsKey = ["remote-workspaces", "box-a", "sessions"];
	expect(result.current.data[0]?.sessions[0]?.isTerminated).not.toBe(true);
	try {
		act(() => trackOptimisticSessionKill("session-1", "box-a"));
		expect(result.current.data[0]?.sessions[0]?.isTerminated).toBe(true);

		// A poll lands mid-kill: the mapped lists now hold the terminated row.
		act(() => client.setQueryData(sessionsKey, [{ ...live }]));
		expect(result.current.data[0]?.sessions[0]?.isTerminated).toBe(true);
	} finally {
		// The kill fails. Nothing touches this host's raw lists on rollback.
		const rawSessions = client.getQueryData(sessionsKey);
		act(() => clearOptimisticSessionKill("session-1", "box-a"));
		expect(client.getQueryData(sessionsKey)).toBe(rawSessions);
	}
	expect(result.current.data[0]?.sessions[0]?.isTerminated).not.toBe(true);
});

it("renames a remote Scratchpad when the language changes", async () => {
	const { result } = renderCachedHost([{ id: "session-1", harness: "codex", status: "working", prs: [] }], []);
	expect(result.current.data.map((project) => project.name)).toEqual(["Scratchpad"]);
	try {
		await act(() => appI18n.changeLanguage("de"));
		expect(result.current.data.map((project) => project.name)).toEqual(["Notizblock"]);
		expect(result.current.data[0]?.sessions[0]?.workspaceName).toBe("Notizblock");
	} finally {
		await act(() => appI18n.changeLanguage("en"));
	}
	expect(result.current.data.map((project) => project.name)).toEqual(["Scratchpad"]);
});
