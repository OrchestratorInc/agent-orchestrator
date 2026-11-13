import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { ArchiveModalHost } from "./ArchiveModalHost";
import { SessionsBoard } from "./SessionsBoard";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	navigate: vi.fn(),
	localGet: vi.fn(),
	localPost: vi.fn(),
	remoteGet: vi.fn(),
	remotePost: vi.fn(),
	requestNewTask: vi.fn(),
	openRemoteProjectSettings: vi.fn(),
	retryRemoteHosts: vi.fn(),
	remoteHosts: [] as Array<{ hostId: string; label: string; url: string; status: string; failureReason?: string }>,
	connected: ["box-a", "box-b"],
	workspaces: {} as Record<string, {
		hostId: string; id: string; name: string; path: string; orchestratorAgent: string;
		sessions: Array<Record<string, unknown>>;
	}>,
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...await importOriginal<typeof import("@tanstack/react-router")>(),
	useNavigate: () => mocks.navigate,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	remoteSessionsQueryKey: (hostId: string) => ["remote-workspaces", hostId, "sessions"],
	workspaceQueryKeyForHost: (hostId?: string) => hostId ? ["remote-workspaces", hostId] : ["workspaces"],
	useWorkspaceQuery: () => ({
		data: [{ id: "shared", name: "Local", sessions: [worker("local", "Local worker")] }],
		isError: false, isSuccess: false,
	}),
	useRemoteProjectQuery: (hostId: string) => ({
		data: mocks.workspaces[hostId], isError: false, isSuccess: Boolean(mocks.workspaces[hostId]),
		dataUpdatedAt: mocks.workspaces[hostId] ? Date.now() - 12 * 60_000 : 0,
	}),
	useRemoteHostWorkspacesQuery: (hostId: string) => ({
		data: mocks.workspaces[hostId] ? [mocks.workspaces[hostId]] : undefined, isError: false,
	}),
	useCloudProjectsQuery: () => ({ data: undefined }),
}));
vi.mock("../hooks/useBoardPresentation", () => ({
	useBoardPresentation: () => ({ showStartup: true, showWelcome: false, showProjectEmpty: false, workspaceStartupState: "error" }),
}));
vi.mock("../hooks/useProjectOrchestratorAction", () => ({
	useProjectOrchestratorAction: () => ({
		isProjectRestarting: false, isProvisioning: false, openNewTask: vi.fn(), openOrchestrator: vi.fn(),
	}),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: mocks.localGet, POST: mocks.localPost },
	apiErrorCode: (error: { code?: string }) => error?.code,
	apiErrorMessage: (error: { message?: string }, fallback: string) => error?.message ?? fallback,
}));
vi.mock("../lib/host-clients", () => ({
	clientForHost: (hostId: string) => ({
		GET: (...args: unknown[]) => mocks.remoteGet(hostId, ...args),
		POST: (...args: unknown[]) => mocks.remotePost(hostId, ...args),
	}),
	connectedHosts: () => mocks.connected,
	labelForHost: (hostId: string) => mocks.connected.includes(hostId) ? `Host ${hostId}` : undefined,
	subscribeConnectedHosts: () => () => undefined,
}));
vi.mock("../lib/shell-context", () => ({
	useShellMaybe: () => ({
		openRemoteProjectSettings: mocks.openRemoteProjectSettings,
		remoteHosts: mocks.remoteHosts,
		retryRemoteHosts: mocks.retryRemoteHosts,
	}),
}));
// A real store, so the shell's archive host follows openArchive/closeArchive.
vi.mock("../stores/ui-store", async () => {
	const { create } = await import("zustand");
	let nonce = 0;
	const state = {
		developerMode: true,
		diagnostics: true,
		remoteHosts: true,
		requestNewTask: mocks.requestNewTask,
		showGlobalToast: vi.fn(),
		restartingProjectIds: new Set<string>(),
		provisioningProjectIds: new Set<string>(),
		orchestratorStartupErrors: {} as Record<string, string>,
		setProjectRestarting: vi.fn(),
		setOrchestratorStartupError: vi.fn(),
		archiveModal: null as Record<string, unknown> | null,
	};
	const useUiStore = create<typeof state & {
		openArchive: (request: Record<string, unknown>) => void;
		closeArchive: () => void;
	}>()((set) => ({
		...state,
		openArchive: (request) => set({ archiveModal: { ...request, nonce: ++nonce } }),
		closeArchive: () => set({ archiveModal: null }),
	}));
	return { sidebarOccupiesLayout: () => true, useUiStore };
});
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => null }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

function worker(hostId: string, title = "Fix login", status = "working") {
	return {
		hostId, id: "worker-1", workspaceId: "shared", workspaceName: "Todo App",
		title, provider: "opencode", kind: "worker", status,
		updatedAt: "2026-09-28T00:00:00Z", prs: [],
	};
}

/** Every client a test renders with, so afterEach can empty it. */
const clients: QueryClient[] = [];

function renderBoard(hostId: string, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	clients.push(client);
	const view = (host: string) => <QueryClientProvider client={client}>
		<TooltipProvider><SessionsBoard key={host} hostId={host} projectId="shared" /><ArchiveModalHost /></TooltipProvider>
	</QueryClientProvider>;
	const rendered = render(view(hostId));
	return { ...rendered, client, showHost: (host: string) => rendered.rerender(view(host)) };
}

beforeEach(() => {
	for (const mock of [mocks.navigate, mocks.localGet, mocks.localPost, mocks.remoteGet, mocks.remotePost,
		mocks.requestNewTask, mocks.openRemoteProjectSettings, mocks.retryRemoteHosts]) mock.mockReset();
	mocks.connected = ["box-a", "box-b"];
	mocks.remoteHosts = [];
	useUiStore.setState({ archiveModal: null });
	mocks.workspaces = {
		"box-a": { hostId: "box-a", id: "shared", name: "Todo App", path: "/todo", orchestratorAgent: "opencode", sessions: [worker("box-a")] },
		"box-b": { hostId: "box-b", id: "shared", name: "Todo App", path: "/todo", orchestratorAgent: "opencode", sessions: [worker("box-b", "Fix billing")] },
	};
	mocks.remoteGet.mockResolvedValue({ data: { prs: [] } });
	mocks.remotePost.mockResolvedValue({ data: {}, error: undefined });
});

// React Query hands updates to React on a 0ms timer. One queued as a test's
// last fetch settles could fire after this file's DOM was torn down, and React
// then threw "window is not defined" as an unhandled error that failed the
// whole run. Unmount, empty the clients, then wait one tick so anything
// already queued lands while the DOM still exists.
afterEach(async () => {
	cleanup();
	for (const client of clients.splice(0)) client.clear();
	await new Promise((resolve) => setTimeout(resolve, 0));
});

it("renders the shared board with host-scoped data, PRs, actions, and navigation", async () => {
	const { showHost } = renderBoard("box-a");
	expect(screen.getByTestId("board")).toHaveAttribute("data-host-id", "box-a");
	expect(screen.getByText("Fix login")).toBeVisible();
	expect(screen.queryByText("Local worker")).toBeNull();
	expect(screen.queryByTestId("daemon-startup-loader")).toBeNull();
	await waitFor(() => expect(mocks.remoteGet).toHaveBeenCalledWith("box-a", "/api/v1/sessions/{sessionId}/pr", expect.anything()));
	fireEvent.click(screen.getByRole("button", { name: "Fix login" }));
	expect(mocks.navigate).toHaveBeenCalledWith({
		to: "/host/$hostId/project/$projectId/session/$sessionId",
		params: { hostId: "box-a", projectId: "shared", sessionId: "worker-1" },
	});
	showHost("box-b");
	expect(screen.getByText("Fix billing")).toBeVisible();
	expect(screen.queryByText("Fix login")).toBeNull();
	await waitFor(() => expect(mocks.remoteGet).toHaveBeenCalledWith("box-b", "/api/v1/sessions/{sessionId}/pr", expect.anything()));
	expect(mocks.localGet).not.toHaveBeenCalled();
});

it("archives and restores through the selected host with host-scoped navigation", async () => {
	const { showHost } = renderBoard("box-b");
	fireEvent.click(screen.getByRole("button", { name: "Archive Fix billing" }));
	fireEvent.click(screen.getByRole("button", { name: "Confirm, archive session" }));
	await waitFor(() => expect(mocks.remotePost).toHaveBeenCalledWith("box-b", "/api/v1/sessions/{sessionId}/kill", {
		params: { path: { sessionId: "worker-1" } },
	}));
	mocks.workspaces["box-b"].sessions = [worker("box-b", "Fix billing", "terminated")];
	showHost("box-a");
	showHost("box-b");
	fireEvent.click(screen.getByRole("button", { name: /Archive, 1 session/ }));
	fireEvent.click(await screen.findByRole("button", { name: "Restore Fix billing" }));
	await waitFor(() => expect(mocks.remotePost).toHaveBeenCalledWith("box-b", "/api/v1/sessions/{sessionId}/restore", {
		params: { path: { sessionId: "worker-1" } },
	}));
	await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({
		to: "/host/$hostId/project/$projectId/session/$sessionId",
		params: { hostId: "box-b", projectId: "shared", sessionId: "worker-1" },
	}));
	expect(mocks.localPost).not.toHaveBeenCalled();
});

it("keeps actions unavailable while the host is offline", () => {
	mocks.connected = [];
	renderBoard("box-a");
	expect(screen.getByTestId("board-host-offline")).toHaveTextContent("Offline");
	expect(screen.queryByRole("button", { name: "Archive Fix login" })).toBeNull();
	expect(screen.queryByRole("button", { name: "New task" })).toBeNull();
	expect(mocks.remotePost).not.toHaveBeenCalled();
});

it("names the offline host and retries it from the board", () => {
	mocks.connected = [];
	mocks.remoteHosts = [{ hostId: "box-a", label: "Azure box", url: "http://box-a", status: "offline" }];
	renderBoard("box-a");
	expect(screen.getByTestId("board-topbar-label")).toHaveTextContent("Azure box");
	expect(screen.getByTestId("board-host-offline")).toHaveTextContent("Updated 12m ago");
	expect(screen.queryByRole("alert")).toBeNull();
	expect(screen.getByText("Fix login")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Retry Azure box" }));
	expect(mocks.retryRemoteHosts).toHaveBeenCalledOnce();
});

it("shows the offline host in place of an empty board when nothing was saved", () => {
	mocks.connected = [];
	mocks.remoteHosts = [{ hostId: "box-a", label: "Azure box", url: "http://box-a", status: "connecting" }];
	delete mocks.workspaces["box-a"];
	renderBoard("box-a");
	expect(screen.getByRole("heading", { name: "Azure box" })).toBeVisible();
	expect(screen.getByRole("button", { name: "Connecting" })).toBeDisabled();
});

it("sends a refused device to the host settings instead of retrying", () => {
	mocks.connected = [];
	mocks.remoteHosts = [{ hostId: "box-a", label: "Azure box", url: "http://box-a", status: "offline", failureReason: "unauthorized" }];
	delete mocks.workspaces["box-a"];
	const openGlobalSettings = vi.fn();
	useUiStore.setState({ openGlobalSettings } as never);
	renderBoard("box-a");
	expect(screen.getByTestId("board-host-offline-empty")).toHaveTextContent("Pair again");
	expect(screen.queryByText(/Updated/)).toBeNull();
	fireEvent.click(screen.getByRole("button", { name: "Edit Azure box" }));
	expect(openGlobalSettings).toHaveBeenCalledWith("remoteHosts", { preserveProject: true });
	expect(mocks.retryRemoteHosts).not.toHaveBeenCalled();
});
