import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { SessionsBoard } from "./SessionsBoard";
import { TooltipProvider } from "./ui/tooltip";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const mocks = vi.hoisted(() => ({
	navigate: vi.fn(),
	localGet: vi.fn(),
	localPost: vi.fn(),
	remoteGet: vi.fn(),
	remotePost: vi.fn(),
	requestNewTask: vi.fn(),
	openRemoteProjectSettings: vi.fn(),
	connected: ["box-a", "box-b"],
	workspaces: {} as Record<string, WorkspaceSummary>,
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...await importOriginal<typeof import("@tanstack/react-router")>(),
	useNavigate: () => mocks.navigate,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	workspaceQueryKeyForHost: (hostId?: string) => hostId ? ["remote-workspaces", hostId] : ["workspaces"],
	useWorkspaceQuery: () => ({
		data: [{ id: "shared", name: "Local", sessions: [{ ...worker("local", "Local worker"), hostId: undefined }] }],
		isError: false, isSuccess: false,
	}),
	useRemoteProjectQuery: (hostId: string) => ({
		data: mocks.workspaces[hostId], isError: false, isSuccess: Boolean(mocks.workspaces[hostId]),
	}),
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
	labelForHost: (hostId: string) => `Host ${hostId}`,
	subscribeConnectedHosts: () => () => undefined,
}));
vi.mock("../lib/shell-context", () => ({
	useShellMaybe: () => ({ openRemoteProjectSettings: mocks.openRemoteProjectSettings }),
}));
vi.mock("../stores/ui-store", () => {
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
	};
	return { useUiStore: Object.assign((selector: (value: typeof state) => unknown) => selector(state), { getState: () => state }) };
});
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => null }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

function worker(hostId: string, title = "Fix login", status: WorkspaceSession["status"] = "working"): WorkspaceSession {
	return {
		hostId, id: "worker-1", workspaceId: "shared", workspaceName: "Todo App",
		title, provider: "opencode", kind: "worker", status,
		updatedAt: "2026-09-28T00:00:00Z", prs: [],
	};
}

function renderBoard(hostId: string, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	const view = (host: string) => <QueryClientProvider client={client}>
		<TooltipProvider><SessionsBoard key={host} hostId={host} projectId="shared" /></TooltipProvider>
	</QueryClientProvider>;
	const rendered = render(view(hostId));
	return { ...rendered, client, showHost: (host: string) => rendered.rerender(view(host)) };
}

beforeEach(() => {
	for (const mock of [mocks.navigate, mocks.localGet, mocks.localPost, mocks.remoteGet, mocks.remotePost,
		mocks.requestNewTask, mocks.openRemoteProjectSettings]) mock.mockReset();
	mocks.connected = ["box-a", "box-b"];
	mocks.workspaces = {
		"box-a": { hostId: "box-a", id: "shared", name: "Todo App", path: "/todo", orchestratorAgent: "opencode", sessions: [worker("box-a")] },
		"box-b": { hostId: "box-b", id: "shared", name: "Todo App", path: "/todo", orchestratorAgent: "opencode", sessions: [worker("box-b", "Fix billing")] },
	};
	mocks.remoteGet.mockResolvedValue({ data: { prs: [] } });
	mocks.localGet.mockResolvedValue({ data: { prs: [] } });
	mocks.remotePost.mockResolvedValue({ data: {}, error: undefined });
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
	expect(screen.getByRole("alert", { name: "" })).toHaveTextContent("Host is offline");
	expect(screen.queryByRole("button", { name: "Archive Fix login" })).toBeNull();
	expect(screen.queryByRole("button", { name: "New task" })).toBeNull();
	expect(mocks.remotePost).not.toHaveBeenCalled();
});

it("opens and archives same-ID workers on their own host from All sessions", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={client}><TooltipProvider>
		<SessionsBoard remoteWorkspaces={Object.values(mocks.workspaces)} />
	</TooltipProvider></QueryClientProvider>);
	expect(screen.getAllByTestId("board-session-card")).toHaveLength(3);
	expect(screen.getAllByTestId("board-session-project").map((label) => label.textContent)).toEqual(expect.arrayContaining(["Todo App · Host box-a", "Todo App · Host box-b"]));
	fireEvent.click(screen.getByRole("button", { name: "Fix billing" }));
	expect(mocks.navigate).toHaveBeenCalledWith({
		to: "/host/$hostId/project/$projectId/session/$sessionId",
		params: { hostId: "box-b", projectId: "shared", sessionId: "worker-1" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Archive Fix billing" }));
	fireEvent.click(screen.getByRole("button", { name: "Confirm, archive session" }));
	await waitFor(() => expect(mocks.remotePost).toHaveBeenCalledWith("box-b", "/api/v1/sessions/{sessionId}/kill", {
		params: { path: { sessionId: "worker-1" } },
	}));
	expect(mocks.localPost).not.toHaveBeenCalled();
});

it("isolates restore failures for same-ID archived workers on different hosts", async () => {
	for (const workspace of Object.values(mocks.workspaces)) {
		workspace.sessions = workspace.sessions.map((session) => ({ ...session, status: "terminated", isTerminated: true }));
	}
	mocks.remotePost.mockImplementation(async (hostId: string) => hostId === "box-a"
		? { error: { message: "Host A restore failed" } }
		: { data: {} });
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={client}><TooltipProvider>
		<SessionsBoard remoteWorkspaces={Object.values(mocks.workspaces)} />
	</TooltipProvider></QueryClientProvider>);
	fireEvent.click(screen.getByRole("button", { name: "Archive, 2 sessions" }));
	fireEvent.click(await screen.findByRole("button", { name: "Restore Fix login" }));
	await screen.findByText("Host A restore failed");
	const archive = screen.getByRole("list", { name: "Archived sessions" });
	const billingCard = within(archive).getByText("Fix billing").closest("[data-testid='board-session-card']");
	expect(billingCard).not.toHaveTextContent("Host A restore failed");
	fireEvent.click(screen.getByRole("button", { name: "Restore Fix billing" }));
	await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({
		to: "/host/$hostId/project/$projectId/session/$sessionId",
		params: { hostId: "box-b", projectId: "shared", sessionId: "worker-1" },
	}));
});

it("shows a failed host without hiding available sessions and retries its list", () => {
	const retry = vi.fn();
	render(<QueryClientProvider client={new QueryClient()}><TooltipProvider>
		<SessionsBoard remoteWorkspaces={[mocks.workspaces["box-a"]]} failedHostIds={["box-b"]} onRetryRemoteHosts={retry} />
	</TooltipProvider></QueryClientProvider>);
	expect(screen.getByRole("alert")).toHaveTextContent("Host box-b: Could not load sessions.");
	expect(screen.getByText("Fix login")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Retry" }));
	expect(retry).toHaveBeenCalledOnce();
});
