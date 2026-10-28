import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { CancelledError, type QueryClient } from "@tanstack/react-query";
import { Suspense, type ComponentType, type PropsWithChildren } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type { AppShortcutEvent, KeybindingOverrides } from "../../shared/shortcuts";
import { TooltipProvider } from "../components/ui/tooltip";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSummary } from "../types/workspace";

const shellMocks = vi.hoisted(() => {
	const state = {
		newSessionListener: undefined as (() => void) | undefined,
		keyboardShortcutsListener: undefined as (() => void) | undefined,
		quitConfirmListener: undefined as ((request: { keepsRunning: boolean }) => void) | undefined,
		newShellTerminalListener: undefined as (() => void) | undefined,
		openSettingsListener: undefined as (() => void) | undefined,
		previousSessionListener: undefined as (() => void) | undefined,
		nextSessionListener: undefined as (() => void) | undefined,
		appShortcutListeners: new Set<(event: AppShortcutEvent) => void>(),
		openFolderPathListener: undefined as ((path: string) => void) | undefined,
		routeParams: {} as { hostId?: string; projectId?: string; sessionId?: string },
		routeSearch: {} as Record<string, unknown>,
		matchRouteTarget: null as string | null,
		workspaces: [] as WorkspaceSummary[],
		remoteWorkspaces: [] as WorkspaceSummary[],
		remoteFailedHostIds: [] as string[],
		removeRemoteProject: undefined as ((hostId: string, projectId: string) => Promise<void>) | undefined,
		configureRemoteProject: undefined as ((hostId: string, projectId: string) => void) | undefined,
		workspaceQuery: {
			data: [] as WorkspaceSummary[],
			dataUpdatedAt: 0,
			isError: false,
			isSuccess: true,
		},
		daemonStatus: { state: "stopped" } as {
			state: "ready" | "starting" | "stopped" | "error";
			port?: number;
			pid?: number;
			code?: "not_ready";
		},
		shellValue: undefined as
				| {
						workspaceStartupState?: string;
						createProject?: (input: {
							path: string;
							defaultBranch?: string;
							workerAgent: string;
							orchestratorAgent: string;
							asWorkspace?: boolean;
							clonePreparationId?: string;
						}) => Promise<void>;
				  }
			| undefined,
	};
	return {
		navigate: vi.fn(),
		onNewSessionShortcut: vi.fn((listener: () => void) => {
			state.newSessionListener = listener;
			return vi.fn();
		}),
		onKeyboardShortcutsHelp: vi.fn((listener: () => void) => {
			state.keyboardShortcutsListener = listener;
			return vi.fn();
		}),
		onQuitConfirmRequest: vi.fn((listener: (request: { keepsRunning: boolean }) => void) => {
			state.quitConfirmListener = listener;
			return vi.fn();
		}),
		resolveQuitConfirm: vi.fn(),
		onNewShellTerminalShortcut: vi.fn((listener: () => void) => {
			state.newShellTerminalListener = listener;
			return vi.fn();
		}),
		openShellTerminal: vi.fn((input: { projectId?: string; sessionId?: string }) => ({
			handleId: "pending-shell:test",
			projectId: input.projectId,
			sessionId: input.sessionId,
			workingDir: "",
			title: "Terminal 1",
			createdAt: new Date().toISOString(),
			optimistic: true as const,
		})),
		onOpenSettingsShortcut: vi.fn((listener: () => void) => {
			state.openSettingsListener = listener;
			return vi.fn();
		}),
		onPreviousSessionShortcut: vi.fn((listener: () => void) => {
			state.previousSessionListener = listener;
			return vi.fn();
		}),
		onNextSessionShortcut: vi.fn((listener: () => void) => {
			state.nextSessionListener = listener;
			return vi.fn();
		}),
		onShortcut: vi.fn((listener: (event: AppShortcutEvent) => void) => {
			state.appShortcutListeners.add(listener);
			return () => state.appShortcutListeners.delete(listener);
		}),
		router: {
			history: { canGoBack: vi.fn(() => true), back: vi.fn(), forward: vi.fn() },
			subscribe: vi.fn(() => () => {}),
		},
		getPathForFile: vi.fn((file: File) => `/dropped/${file.name}`),
		onOpenFolderPath: vi.fn((listener: (path: string) => void) => {
			state.openFolderPathListener = listener;
			return vi.fn();
		}),
		getKeybindings: vi.fn(async () => ({})),
		setKeybindings: vi.fn(async (overrides: KeybindingOverrides) => overrides),
		setKeybindingRecording: vi.fn(async () => undefined),
		queryClient: {
			ensureQueryData: vi.fn(),
			fetchQuery: vi.fn(),
			getQueryData: vi.fn(),
			getQueryState: vi.fn(),
			invalidateQueries: vi.fn(),
			cancelQueries: vi.fn(),
			prefetchQuery: vi.fn(async () => undefined),
			setQueryData: vi.fn(),
		},
		remoteDelete: vi.fn(),
		listRemoteHosts: vi.fn(async () => []),
		state,
	};
});

vi.mock("@tanstack/react-query", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-query")>()),
	useQueryClient: () => shellMocks.queryClient,
	// TerminalCacheProvider owns reviewer queries in production. This shell-only
	// harness has no routed terminal children and intentionally omits a provider.
	useQueries: () => [],
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	createFileRoute: () => (options: unknown) => ({ options }),
	Outlet: () => null,
	useMatch: () => undefined,
	useMatchRoute: () => (options: { to: string }) => shellMocks.state.matchRouteTarget === options.to,
	useNavigate: () => shellMocks.navigate,
	useRouter: () => shellMocks.router,
	useParams: () => shellMocks.state.routeParams,
	useSearch: () => shellMocks.state.routeSearch,
	useRouterState: ({ select }: { select: (state: { location: { search: unknown } }) => unknown }) =>
		select({ location: { search: shellMocks.state.routeSearch ?? {} } }),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: {
			onScrollPhase: () => () => {},
			onCloseShellTerminalShortcut: () => () => {},
			onPreviousTabShortcut: () => () => {},
			onNextTabShortcut: () => () => {},
			setCloseShellTerminalShortcutEnabled: () => {},
			onNewSessionShortcut: shellMocks.onNewSessionShortcut,
			onKeyboardShortcutsHelp: shellMocks.onKeyboardShortcutsHelp,
			onQuitConfirmRequest: shellMocks.onQuitConfirmRequest,
			resolveQuitConfirm: shellMocks.resolveQuitConfirm,
			onNewShellTerminalShortcut: shellMocks.onNewShellTerminalShortcut,
			onOpenSettingsShortcut: shellMocks.onOpenSettingsShortcut,
			onPreviousSessionShortcut: shellMocks.onPreviousSessionShortcut,
			onNextSessionShortcut: shellMocks.onNextSessionShortcut,
			onShortcut: shellMocks.onShortcut,
			getPathForFile: shellMocks.getPathForFile,
			onOpenFolderPath: shellMocks.onOpenFolderPath,
		},
		keybindings: {
			get: shellMocks.getKeybindings,
			set: shellMocks.setKeybindings,
			setRecording: shellMocks.setKeybindingRecording,
		},
		window: {},
		tray: {
			setAttentionState: () => undefined,
			onOpenSession: () => () => undefined,
		},
		remotes: { list: shellMocks.listRemoteHosts },
	},
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => shellMocks.state.workspaceQuery,
	useWorkspacePolling: () => undefined,
	useRemoteWorkspaces: () => ({ data: shellMocks.state.remoteWorkspaces, failedHostIds: shellMocks.state.remoteFailedHostIds, loadedProjectHostIds: [] }),
	useWorkspaceTraySessions: () => ({ data: [] }),
	workspaceQueryKey: ["workspaces"],
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	workspaceQueryOptions: {},
	toWorkspaceSession: (session: { id: string }, project: { id: string; name: string }) => ({ ...session, workspaceId: project.id, workspaceName: project.name }),
}));

vi.mock("../lib/host-clients", () => ({
	clientForHost: () => ({ DELETE: shellMocks.remoteDelete }),
	connectHost: vi.fn(),
	connectedHosts: () => [],
	disconnectHost: vi.fn(),
	subscribeConnectedHosts: () => () => undefined,
}));

vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ status: "authenticated", session: { user: { id: "test-user" } } }),
}));
vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "" } }) }));

vi.mock("../hooks/useDaemonStatus", () => ({
	useDaemonStatus: () => shellMocks.state.daemonStatus,
}));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/api-client")>()),
	apiClient: { POST: vi.fn(), DELETE: vi.fn(), PATCH: vi.fn() },
	apiErrorCode: (error: { code?: string } | undefined) => error?.code,
	apiErrorMessage: (error: { message?: string } | undefined) => error?.message ?? "request failed",
	hasTrustedApiBaseUrl: () => true,
}));

vi.mock("../lib/daemon-status", () => ({
	refreshDaemonStatus: vi.fn(async () => shellMocks.state.daemonStatus),
}));

// TerminalCacheProvider resolves the cloud terminal transport in production.
// These shell shortcut tests never mount a terminal, so keep that unrelated
// settings/query path out of the provider-free harness.
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: {}, ready: false, baseUrl: "" }),
}));

vi.mock("../hooks/useCloudOrg", () => ({
	useCloudOrg: () => ({ org: undefined, isLoading: false, error: undefined, ready: false }),
}));

// The shell layout opens standalone terminals; this suite only covers the
// shortcut subscriptions, so the mutation is stubbed rather than driven.
vi.mock("../hooks/useShellTerminals", () => ({
	useShellTerminals: () => ({ data: [], isSuccess: true }),
	useOpenShellTerminal: () => ({
		open: shellMocks.openShellTerminal,
		mutate: shellMocks.openShellTerminal,
	}),
}));

vi.mock("../hooks/useAgentReadinessQuery", () => ({
	agentReadinessQueryKey: ["agent-readiness"],
	// The shell reports the install's agent inventory once per launch, so the
	// mock has to answer this too. Undefined data means the hook reports nothing,
	// which keeps these shortcut tests free of telemetry side effects.
	useAgentReadinessQuery: () => ({ data: undefined }),
	useEnsureAgentReadiness: vi.fn(),
}));

vi.mock("../components/NotificationCenter", () => ({ NotificationRuntime: () => null }));
vi.mock("../components/HarnessUpdateNotice", () => ({ HarnessUpdateNotice: () => null }));
vi.mock("../components/DaemonStartupLoader", () => ({
	DaemonStartupLoader: () => <div data-testid="daemon-startup-loader" />,
}));
vi.mock("../components/CommandPalette", () => ({ CommandPalette: () => null }));
vi.mock("../components/OrchestratorReplacementDialog", () => ({ OrchestratorReplacementDialog: () => null }));
vi.mock("../components/OpenShareLinkDialog", () => ({ ShareLinkHost: () => null }));
vi.mock("../components/ShellTopbar", () => ({ ShellTopbar: () => null }));
vi.mock("../components/TitlebarNav", async () => {
	const { sidebarIsVisible, useUiStore: useStore } = await vi.importActual<
		typeof import("../stores/ui-store")
	>("../stores/ui-store");
	return {
		useCanGoForward: () => false,
		TitlebarNav: () => {
			const isSidebarOpen = useStore(sidebarIsVisible);
			const toggleSidebar = useStore((state) => state.toggleSidebar);
			return (
				<button
					aria-label={isSidebarOpen ? "Collapse sidebar" : "Expand sidebar"}
					onClick={toggleSidebar}
					type="button"
				/>
			);
		},
	};
});
vi.mock("../components/WindowTitlebar", () => ({ WindowTitlebar: () => null }));
vi.mock("../components/SettingsDialog", () => ({
	SettingsProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
	SettingsPane: () => null,
	useSettingsPage: () => null,
}));
vi.mock("../components/KeyboardShortcutsDialog", () => ({
	KeyboardShortcutsDialog: ({ open, onCustomize }: { open: boolean; onCustomize?: () => void }) => (open ? <div data-testid="keyboard-shortcuts"><button type="button" onClick={onCustomize}>Customize</button></div> : null),
}));
vi.mock("../lib/shell-context", () => ({
	ShellProvider: ({ children, value }: PropsWithChildren<{ value?: { workspaceStartupState?: string } }>) => {
		shellMocks.state.shellValue = value;
		return children;
	},
}));
vi.mock("../components/ui/sidebar", () => ({
	SidebarProvider: ({ children, open }: PropsWithChildren<{ open?: boolean }>) => (
		<div data-open={open ? "true" : "false"} data-testid="sidebar-provider">
			{children}
		</div>
	),
}));

vi.mock("../components/NewTaskNavigator", async () => {
	const { useUiStore: useStore } = await vi.importActual<typeof import("../stores/ui-store")>("../stores/ui-store");
	return {
		NewTaskNavigator: () => {
			const request = useStore((state) => state.newTaskRequest);
			return request ? <div data-testid="new-task-flow" data-project={request.projectId} data-host={request.hostId} /> : null;
		},
	};
});

vi.mock("../components/GlobalToast", async () => {
	const { useUiStore: useStore } = await vi.importActual<typeof import("../stores/ui-store")>("../stores/ui-store");
	return {
		GlobalToast: () => {
			const toast = useStore((state) => state.globalToast);
			return toast ? <div data-testid="global-toast">{toast.title}</div> : null;
		},
	};
});

vi.mock("../components/Sidebar", async () => {
	const { useUiStore: useStore } = await vi.importActual<typeof import("../stores/ui-store")>("../stores/ui-store");
	return {
		SIDEBAR_DEFAULT_WIDTH: 240,
		Sidebar: ({ topbarOffset, onRemoveRemoteProject, onConfigureRemoteProject }: {
			topbarOffset?: string;
			onRemoveRemoteProject: (hostId: string, projectId: string) => Promise<void>;
			onConfigureRemoteProject: (hostId: string, projectId: string) => void;
		}) => {
			shellMocks.state.removeRemoteProject = onRemoveRemoteProject;
			shellMocks.state.configureRemoteProject = onConfigureRemoteProject;
			const nonce = useStore((state) => state.createProjectNonce);
			const folderDropRequest = useStore((state) => state.folderDropRequest);
			return (
				<div data-testid="sidebar" data-topbar-offset={topbarOffset}>
					{nonce > 0 || folderDropRequest ? (
						<div data-path={folderDropRequest?.path} data-testid="create-project-flow" />
					) : null}
				</div>
			);
		},
	};
});

import { Route } from "../routes/_shell";
import { apiClient } from "../lib/api-client";
const ShellRoute = Route.options.component as ComponentType;

const workspaces = [
	{
		id: "proj-1",
		name: "Project One",
		path: "/one",
		sessions: [
			{ id: "sess-1", workspaceId: "proj-1", status: "working" },
			{ id: "sess-2", workspaceId: "proj-1", status: "terminated", isTerminated: true },
			{ id: "sess-merged-terminated", workspaceId: "proj-1", status: "merged", isTerminated: true },
			{ id: "sess-3", workspaceId: "proj-1", status: "idle" },
		],
	},
	{
		id: "proj-2",
		name: "Project Two",
		path: "/two",
		sessions: [{ id: "sess-cross", workspaceId: "proj-2", status: "working" }],
	},
] as unknown as WorkspaceSummary[];

async function renderShell() {
	const view = await act(async () => {
		return render(
			<Suspense fallback={null}>
				<ShellRoute />
			</Suspense>,
			{ wrapper: TooltipProvider },
		);
	});
	await waitFor(() => expect(shellMocks.onNewSessionShortcut).toHaveBeenCalledTimes(1), { timeout: 30_000 });
	await waitFor(() => expect(shellMocks.onKeyboardShortcutsHelp).toHaveBeenCalledTimes(1));
	await waitFor(() => expect(shellMocks.onNewShellTerminalShortcut).toHaveBeenCalledTimes(1));
	await waitFor(() => expect(shellMocks.onOpenSettingsShortcut).toHaveBeenCalledTimes(1));
	await waitFor(() => expect(shellMocks.onPreviousSessionShortcut).toHaveBeenCalledTimes(1));
	await waitFor(() => expect(shellMocks.onNextSessionShortcut).toHaveBeenCalledTimes(1));
	await waitFor(() => expect(shellMocks.onShortcut).toHaveBeenCalled());
	return view;
}

function setWorkspaces(list: WorkspaceSummary[]) {
	shellMocks.state.workspaces = list;
	shellMocks.state.workspaceQuery = { data: list, dataUpdatedAt: 0, isError: false, isSuccess: true };
}

/** Delivers an application shortcut the way the main process does. */
function emitAppShortcut(event: AppShortcutEvent) {
	act(() => {
		for (const listener of [...shellMocks.state.appShortcutListeners]) listener(event);
	});
}

function emitShortcut() {
	const listener = shellMocks.state.newSessionListener;
	if (!listener) throw new Error("shell shortcut listener was not registered");
	act(() => listener());
}

beforeEach(() => {
	// The shell remembers the last project for ⌘N outside one; start each test clean.
	window.localStorage.removeItem("ao.newTask.lastProject");
	shellMocks.navigate.mockReset();
	shellMocks.onNewSessionShortcut.mockClear();
	shellMocks.onKeyboardShortcutsHelp.mockClear();
	shellMocks.onNewShellTerminalShortcut.mockClear();
	shellMocks.openShellTerminal.mockClear();
	shellMocks.state.newShellTerminalListener = undefined;
	shellMocks.onOpenSettingsShortcut.mockClear();
	shellMocks.onPreviousSessionShortcut.mockClear();
	shellMocks.onNextSessionShortcut.mockClear();
	shellMocks.onShortcut.mockClear();
	shellMocks.state.appShortcutListeners.clear();
	shellMocks.router.history.canGoBack.mockReset().mockReturnValue(true);
	shellMocks.router.history.back.mockClear();
	shellMocks.router.history.forward.mockClear();
	useSidebarOrderStore.setState({ projectOrder: [], pendingCreatedProjectIds: [], sessionOrders: {} });
	shellMocks.getPathForFile.mockClear();
	shellMocks.onOpenFolderPath.mockClear();
	shellMocks.state.newSessionListener = undefined;
	shellMocks.state.keyboardShortcutsListener = undefined;
	shellMocks.state.quitConfirmListener = undefined;
	shellMocks.resolveQuitConfirm.mockClear();
	shellMocks.state.openSettingsListener = undefined;
	shellMocks.state.previousSessionListener = undefined;
	shellMocks.state.nextSessionListener = undefined;
	shellMocks.state.openFolderPathListener = undefined;
	shellMocks.state.routeParams = {};
	shellMocks.state.routeSearch = {};
	shellMocks.state.matchRouteTarget = null;
	shellMocks.state.workspaces = workspaces;
	shellMocks.state.remoteWorkspaces = [];
	shellMocks.state.remoteFailedHostIds = [];
	shellMocks.listRemoteHosts.mockClear();
	shellMocks.state.removeRemoteProject = undefined;
	shellMocks.state.configureRemoteProject = undefined;
	shellMocks.remoteDelete.mockReset().mockResolvedValue({});
	shellMocks.state.workspaceQuery = {
		data: workspaces,
		dataUpdatedAt: 0,
		isError: false,
		isSuccess: true,
	};
	shellMocks.state.daemonStatus = { state: "error", code: "not_ready" };
	shellMocks.state.shellValue = undefined;
	vi.mocked(apiClient.PATCH).mockReset().mockResolvedValue({ error: undefined });
	shellMocks.queryClient.fetchQuery.mockReset().mockResolvedValue(workspaces);
	shellMocks.queryClient.getQueryData.mockReset().mockReturnValue(workspaces);
	shellMocks.queryClient.getQueryState.mockReset().mockReturnValue({ dataUpdatedAt: 0 });
	useUiStore.setState({
		projectCreationPending: false,
		provisioningProjectIds: new Set(),
		createProjectNonce: 0,
		developerMode: false,
		folderDropRequest: null,
		globalToast: null,
		isSidebarOpen: true,
		newTaskRequest: null,
		remoteHosts: false,
		newShellTerminalNonce: 0,
		activeShellTerminalHandleId: null,
		settingsModal: null,
	});
});

describe("shell workspace startup", () => {
	// The shell lazy-loads the starting chat; warm the module so the first render
	// that needs it resolves within findBy's default timeout.
	beforeAll(async () => {
		await import("../components/chat/OrchestratorStartingChat");
	});
	it("rechecks a connected host when its session queries fail", async () => {
		useUiStore.setState({ developerMode: true, remoteHosts: true });
		const view = await renderShell();
		await waitFor(() => expect(shellMocks.listRemoteHosts).toHaveBeenCalledTimes(1));

		shellMocks.state.remoteFailedHostIds = ["box-a"];
		view.rerender(<Suspense fallback={null}><ShellRoute /></Suspense>);
		await waitFor(() => expect(shellMocks.listRemoteHosts).toHaveBeenCalledTimes(2));

		view.rerender(<Suspense fallback={null}><ShellRoute /></Suspense>);
		expect(shellMocks.listRemoteHosts).toHaveBeenCalledTimes(2);
	});

	it("opens the shared Project settings dialog for the selected remote host", async () => {
		shellMocks.state.remoteWorkspaces = [
			{ hostId: "box-a", id: "shared", name: "Shared", path: "/a", sessions: [] },
			{ hostId: "box-b", id: "shared", name: "Shared", path: "/b", sessions: [] },
		] as WorkspaceSummary[];
		await renderShell();

		act(() => shellMocks.state.configureRemoteProject?.("box-b", "shared"));
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "project", projectId: "shared", hostId: "box-b" });
	});

	it("leaves a remote session only when removing its project on the same host", async () => {
		useUiStore.setState({ developerMode: true, remoteHosts: true });
		shellMocks.state.routeParams = { hostId: "box-a", sessionId: "same-session" };
		shellMocks.state.remoteWorkspaces = [
			{ hostId: "box-a", id: "project-a", sessions: [{ id: "same-session" }] },
			{ hostId: "box-b", id: "project-b", sessions: [{ id: "same-session" }] },
		] as WorkspaceSummary[];
		await renderShell();

		await shellMocks.state.removeRemoteProject?.("box-b", "project-b");
		expect(shellMocks.navigate).not.toHaveBeenCalled();
		await shellMocks.state.removeRemoteProject?.("box-a", "project-a");
		expect(shellMocks.navigate).toHaveBeenCalledWith({ to: "/" });
	});

	it("resyncs Developer Mode when an attached daemon restarts on the same port", async () => {
		useUiStore.setState({ developerMode: true });
		shellMocks.state.daemonStatus = { state: "ready", port: 4777, pid: 101 };
		const view = await renderShell();
		await waitFor(() => expect(apiClient.PATCH).toHaveBeenCalledTimes(1));
		expect(apiClient.PATCH).toHaveBeenCalledWith("/api/v1/settings/chat-hibernation", {
			body: { enabled: true },
		});

		shellMocks.state.daemonStatus = { state: "ready", port: 4777, pid: 202 };
		view.rerender(
			<Suspense fallback={null}>
				<ShellRoute />
			</Suspense>,
		);

		await waitFor(() => expect(apiClient.PATCH).toHaveBeenCalledTimes(2));
	});

	it("keeps chat hibernation disabled when Developer Mode is off", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777, pid: 101 };
		await renderShell();

		await waitFor(() => expect(apiClient.PATCH).toHaveBeenCalledWith("/api/v1/settings/chat-hibernation", {
			body: { enabled: false },
		}));
	});

	it("routes duplicate-path project adds to the registered project and shows a toast", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		vi.mocked(apiClient.POST).mockResolvedValueOnce({
			data: undefined,
			error: {
				code: "PATH_ALREADY_REGISTERED",
				message: "A project at this path is already registered",
			},
		});

		await renderShell();

		await expect(
			shellMocks.state.shellValue?.createProject?.({
				path: "/one/",
				workerAgent: "codex",
				orchestratorAgent: "codex",
			}),
		).resolves.toBeUndefined();

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-1" },
		});
		expect(screen.getByTestId("global-toast")).toHaveTextContent("Project already added");
	});

	it("finishes clone provisioning after registration committed but its response was lost", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		shellMocks.state.routeParams = { projectId: "proj-1" };
		vi.mocked(apiClient.POST)
			.mockReset()
			.mockRejectedValueOnce(new TypeError("Failed to fetch"))
			.mockResolvedValueOnce({ error: {
				code: "PATH_ALREADY_REGISTERED", message: "Already registered", details: { existingProjectId: "proj-1" },
			} })
			.mockResolvedValueOnce({ data: { session: {
				id: "orch-existing", projectId: "proj-1", kind: "orchestrator", mode: "chat",
			} } });
		await renderShell();
		const input = { path: "/one", workerAgent: "codex", orchestratorAgent: "codex", clonePreparationId: "prepared" };
		await expect(shellMocks.state.shellValue?.createProject?.(input)).rejects.toThrow("Failed to fetch");
		await expect(shellMocks.state.shellValue?.createProject?.(input)).resolves.toBeUndefined();
		await waitFor(() => expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "orch-existing" },
		}));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/sessions", {
			body: { projectId: "proj-1", kind: "orchestrator", harness: "codex" },
		});
		expect(apiClient.POST).toHaveBeenCalledTimes(3);
		expect(useUiStore.getState().provisioningProjectIds.has("proj-1")).toBe(false);
	});

	it("uses the daemon project identity when an imported path is an alias", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		vi.mocked(apiClient.POST).mockResolvedValueOnce({ error: {
			code: "PATH_ALREADY_REGISTERED", message: "Already registered", details: { existingProjectId: "proj-1" },
		} });
		await renderShell();
		await expect(shellMocks.state.shellValue?.createProject?.({
			path: "/alias/one", workerAgent: "codex", orchestratorAgent: "codex",
		})).resolves.toBeUndefined();
		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId", params: { projectId: "proj-1" },
		});
	});

	it("refreshes a stale project list before opening the daemon's registered identity", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		vi.mocked(apiClient.POST).mockResolvedValueOnce({ error: {
			code: "PATH_ALREADY_REGISTERED", message: "Already registered", details: { existingProjectId: "new-registration" },
		} });
		await renderShell();
		shellMocks.queryClient.fetchQuery.mockResolvedValueOnce([{ ...workspaces[0], id: "new-registration", path: "/canonical" }]);
		await expect(shellMocks.state.shellValue?.createProject?.({ path: "/alias", workerAgent: "codex", orchestratorAgent: "codex" })).resolves.toBeUndefined();
		expect(shellMocks.queryClient.fetchQuery).toHaveBeenCalledWith(expect.objectContaining({ staleTime: 0, retry: false }));
		expect(shellMocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "new-registration" } });
	});

	it.each([undefined, null, 123, [], "", "missing", "../outside"])(
		"does not navigate to an unverified conflict identity: %j", async (existingProjectId) => {
			shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
			vi.mocked(apiClient.POST).mockResolvedValueOnce({ error: {
				code: "PATH_ALREADY_REGISTERED", message: "Already registered", requestId: "request-4403", details: { existingProjectId },
			} });
			await renderShell();
			await expect(shellMocks.state.shellValue?.createProject?.({ path: "/unknown", workerAgent: "codex", orchestratorAgent: "codex" })).rejects.toMatchObject({
				code: "PATH_ALREADY_REGISTERED", requestId: "request-4403", details: { existingProjectId },
			});
			expect(shellMocks.navigate).not.toHaveBeenCalled();
		},
	);

	it("preserves the original conflict when refreshing registered projects fails", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		vi.mocked(apiClient.POST).mockResolvedValueOnce({ error: {
			code: "PATH_ALREADY_REGISTERED", message: "Already registered", details: { existingProjectId: "not-cached" },
		} });
		await renderShell();
		shellMocks.queryClient.fetchQuery.mockRejectedValueOnce(new Error("refresh failed"));
		await expect(shellMocks.state.shellValue?.createProject?.({ path: "/unknown", workerAgent: "codex", orchestratorAgent: "codex" })).rejects.toMatchObject({ code: "PATH_ALREADY_REGISTERED", message: "Already registered" });
		expect(shellMocks.navigate).not.toHaveBeenCalled();
	});

	it("forwards an explicit default branch when creating a local project", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		vi.mocked(apiClient.POST).mockResolvedValueOnce({
			data: {
				project: {
					id: "proj-new",
					name: "proj-new",
					kind: "single_repo",
					path: "/repo/project",
					workspaceRepos: [],
				},
			},
		});

		await renderShell();

		await shellMocks.state.shellValue?.createProject?.({
			path: "/repo/project",
			defaultBranch: "main",
			workerAgent: "codex",
			orchestratorAgent: "codex",
		});

		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/projects", {
			body: {
				path: "/repo/project",
				asWorkspace: undefined,
				config: {
					defaultBranch: "main",
					worker: { agent: "codex" },
					orchestrator: { agent: "codex" },
				},
			},
		});
	});

	it("shows the Cloud loader while opening a cloud orchestrator", async () => {
		shellMocks.state.workspaces = [{ ...workspaces[0], kind: "cloud", sessions: [] }];
		shellMocks.state.workspaceQuery.data = shellMocks.state.workspaces;
		shellMocks.state.routeParams = { projectId: workspaces[0].id };
		useUiStore.getState().setProjectProvisioning(workspaces[0].id, true);
		await renderShell();
		expect(await screen.findByTestId("cloud-session-loader-screen")).toBeInTheDocument();
		expect(screen.queryByLabelText("Message the agent")).not.toBeInTheDocument();
	});

	it("opens optimistic chat during submission without a loading page", async () => {
		useUiStore.getState().setProjectCreationPending(true);
		const view = await renderShell();
		const composer = await screen.findByLabelText("Message the agent");
		expect(composer.closest("form")).toHaveAttribute("data-starting", "true");
		expect(screen.getByTestId("session-workspace-topbar")).toBeVisible();
		shellMocks.state.routeParams = { projectId: "proj-1" };
		act(() => {
			useUiStore.getState().setProjectProvisioning("proj-1", true);
			useUiStore.getState().setProjectCreationPending(false);
		});
		view.rerender(<Suspense fallback={null}><ShellRoute /></Suspense>);
		expect(screen.getByText("Getting your project ready")).toBeInTheDocument();
		shellMocks.state.routeParams = { projectId: "proj-1", sessionId: "sess-1" };
		view.rerender(<Suspense fallback={null}><ShellRoute /></Suspense>);
		expect(screen.getByLabelText("Message the agent")).toBe(composer);
		expect(screen.getByText("Getting your project ready")).toBeInTheDocument();
		act(() => useUiStore.getState().setProjectProvisioning("proj-1", false));
		expect(screen.queryByText("Getting your project ready")).not.toBeInTheDocument();
	});

	const spawnNewProject = () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 3001 };
		vi.mocked(apiClient.POST)
			.mockResolvedValueOnce({ data: { project: { id: "proj-new", name: "New", kind: "single_repo", path: "/repo/new" } } })
			.mockResolvedValueOnce({ data: { session: { id: "orch-new", projectId: "proj-new", kind: "orchestrator", mode: "chat", provisionState: "provisioning" } } });
	};

	it.each([
		{ savedOrder: ["proj-2", "proj-1"] },
		{ savedOrder: ["proj-2", "proj-1", "proj-new"] },
	])("saves a newly created project first ahead of saved order $savedOrder", async ({ savedOrder }) => {
		spawnNewProject();
		useSidebarOrderStore.getState().setProjectOrder(savedOrder);
		await renderShell();
		await shellMocks.state.shellValue?.createProject?.({ path: "/repo/new", workerAgent: "codex", orchestratorAgent: "codex" });

		expect(useSidebarOrderStore.getState().projectOrder).toEqual(["proj-new", "proj-2", "proj-1"]);
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.project-order") ?? "null")).toEqual(["proj-new", "proj-2", "proj-1"]);
	});

	it("opens the returned orchestrator without waiting for a workspace refetch", async () => {
		spawnNewProject();
		shellMocks.state.routeParams = { projectId: "proj-new" };
		await renderShell();
		await shellMocks.state.shellValue?.createProject?.({ path: "/repo/new", workerAgent: "codex", orchestratorAgent: "codex" });
		await waitFor(() => expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-new", sessionId: "orch-new" },
		}));
		const publish = shellMocks.queryClient.setQueryData.mock.calls.at(-1)?.[1] as (current: WorkspaceSummary[]) => WorkspaceSummary[];
		expect(publish([{ ...workspaces[0], id: "proj-new", name: "New", sessions: [] }])[0].sessions[0]).toMatchObject({ id: "orch-new", mode: "chat", provisionState: "provisioning" });
		expect(useUiStore.getState().provisioningProjectIds.has("proj-new")).toBe(false);
	});

	it("does not pull the user back to a new orchestrator after they left the project", async () => {
		spawnNewProject();
		shellMocks.state.routeParams = { projectId: "other" };
		await renderShell();
		await shellMocks.state.shellValue?.createProject?.({ path: "/repo/new", workerAgent: "codex", orchestratorAgent: "codex" });
		await waitFor(() => expect(useUiStore.getState().provisioningProjectIds.has("proj-new")).toBe(false));
		expect(shellMocks.navigate).not.toHaveBeenCalledWith(expect.objectContaining({ to: "/projects/$projectId/sessions/$sessionId" }));
	});

	it("leaves the session topbar row to the session split instead of reserving a full-width shell row", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		const sidebar = screen.getByTestId("sidebar");
		expect(screen.queryByTestId("session-topbar-host")).not.toBeInTheDocument();
		// The route shell owns only the frame. SessionView places its topbar host
		// inside the terminal panel so the inspector header can occupy this row too.
		expect(sidebar).not.toHaveAttribute("data-topbar-offset", "session");
		expect(document.querySelector(".center-panel-shell--session > .center-panel-surface")).toBeInTheDocument();
	});

	it("reveals the shell while session recovery remains pending", async () => {
		const checking: WorkspaceSummary[] = workspaces.map((workspace) => ({ ...workspace,
			sessions: workspace.sessions.map((session) => ({ ...session, statusReadiness: "checking" })),
		}));
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		shellMocks.state.workspaceQuery = { data: checking, dataUpdatedAt: 100, isError: false, isSuccess: true };
		shellMocks.queryClient.getQueryState.mockReturnValue({ dataUpdatedAt: 100 });
		shellMocks.queryClient.fetchQuery.mockResolvedValueOnce(checking);
		await renderShell();
		await waitFor(() => expect(shellMocks.state.shellValue?.workspaceStartupState).toBe("ready"));
		expect(screen.queryByTestId("daemon-startup-loader")).not.toBeInTheDocument();
		expect(screen.getByTestId("sidebar-provider")).toBeInTheDocument();
	});

	it("forces a confirmed fetch and preserves a collapsed sidebar preference", async () => {
		let resolveFetch: ((value: WorkspaceSummary[]) => void) | undefined;
		useUiStore.setState({ isSidebarOpen: false });
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		shellMocks.state.workspaceQuery = {
			data: [],
			dataUpdatedAt: 100,
			isError: false,
			isSuccess: true,
		};
		shellMocks.queryClient.getQueryState.mockReturnValue({ dataUpdatedAt: 100 });
		shellMocks.queryClient.fetchQuery.mockReturnValueOnce(
			new Promise<WorkspaceSummary[]>((resolve) => {
				resolveFetch = resolve;
			}),
		);

		const view = await renderShell();
		expect(screen.getByTestId("daemon-startup-loader")).toBeInTheDocument();
		expect(screen.queryByTestId("sidebar-provider")).not.toBeInTheDocument();
		expect(shellMocks.queryClient.fetchQuery).toHaveBeenCalledWith(expect.objectContaining({ staleTime: 0 }));

		await act(async () => resolveFetch?.(workspaces));

		await waitFor(() => expect(shellMocks.state.shellValue?.workspaceStartupState).toBe("ready"));
		expect(screen.getByTestId("sidebar-provider")).toHaveAttribute("data-open", "false");
		expect(useUiStore.getState().isSidebarOpen).toBe(false);
		view.unmount();
	});

	it("does not turn a cancelled confirmed fetch into a startup error", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		shellMocks.state.workspaceQuery = {
			data: [],
			dataUpdatedAt: 100,
			isError: false,
			isSuccess: true,
		};
		shellMocks.queryClient.getQueryState.mockReturnValue({ dataUpdatedAt: 100 });
		shellMocks.queryClient.fetchQuery.mockRejectedValueOnce(new CancelledError());

		await renderShell();

		await waitFor(() =>
			expect(shellMocks.queryClient.fetchQuery).toHaveBeenCalledWith(expect.objectContaining({ staleTime: 0 })),
		);
		expect(screen.getByTestId("daemon-startup-loader")).toBeInTheDocument();
	});

	it("forces a workspace fetch when a daemon returns ready on the same port", async () => {
		shellMocks.state.daemonStatus = { state: "starting", port: 4777 };
		shellMocks.queryClient.fetchQuery.mockResolvedValue(workspaces);

		const view = await renderShell();
		expect(screen.getByTestId("daemon-startup-loader")).toBeInTheDocument();
		expect(shellMocks.queryClient.fetchQuery).not.toHaveBeenCalled();

		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		view.rerender(
			<Suspense fallback={null}>
				<ShellRoute />
			</Suspense>,
		);

		await waitFor(() =>
			expect(shellMocks.queryClient.fetchQuery).toHaveBeenCalledWith(expect.objectContaining({ staleTime: 0 })),
		);
		await waitFor(() => expect(shellMocks.state.shellValue?.workspaceStartupState).toBe("ready"));
	});

	it("recovers after a newer workspace query succeeds", async () => {
		shellMocks.state.daemonStatus = { state: "ready", port: 4777 };
		shellMocks.state.workspaceQuery = {
			data: workspaces,
			dataUpdatedAt: 100,
			isError: false,
			isSuccess: true,
		};
		shellMocks.queryClient.getQueryState.mockReturnValue({ dataUpdatedAt: 100 });
		shellMocks.queryClient.fetchQuery.mockRejectedValueOnce(new Error("temporary failure"));

		const view = await renderShell();
		await waitFor(() => expect(shellMocks.state.shellValue?.workspaceStartupState).toBe("error"));

		shellMocks.state.workspaceQuery = {
			...shellMocks.state.workspaceQuery,
			dataUpdatedAt: 200,
		};
		view.rerender(
			<Suspense fallback={null}>
				<ShellRoute />
			</Suspense>,
		);

		await waitFor(() => expect(shellMocks.state.shellValue?.workspaceStartupState).toBe("ready"));
	});
});

describe("shell route loader", () => {
	const loader = Route.options.loader as (args: { context: { queryClient: Pick<QueryClient, "fetchQuery"> } }) => Promise<unknown>;

	it("treats a workspace fetch cancelled by a newer refetch as warmed, not a shell error", async () => {
		const fetchQuery = vi.fn(() => Promise.reject(new CancelledError()));
		await expect(loader({ context: { queryClient: { fetchQuery } as unknown as QueryClient } })).resolves.toBeUndefined();
		expect(fetchQuery).toHaveBeenCalledTimes(1);
	});

	it("still fails the shell on a real workspace fetch error", async () => {
		const failure = new Error("daemon down");
		const fetchQuery = vi.fn(() => Promise.reject(failure));
		await expect(loader({ context: { queryClient: { fetchQuery } as unknown as QueryClient } })).rejects.toBe(failure);
	});
});

describe("shell sidebar toggle", () => {
	it("does not open a collapsed sidebar on titlebar hover", async () => {
		useUiStore.setState({ isSidebarOpen: false });
		await renderShell();

		const provider = screen.getByTestId("sidebar-provider");
		const expandTrigger = screen.getByRole("button", { name: "Expand sidebar" });

		expect(provider).toHaveAttribute("data-open", "false");
		fireEvent.pointerEnter(expandTrigger);

		expect(provider).toHaveAttribute("data-open", "false");
		expect(useUiStore.getState().isSidebarOpen).toBe(false);
	});

	it("opens the sidebar when the titlebar toggle is clicked", async () => {
		useUiStore.setState({ isSidebarOpen: false });
		await renderShell();

		fireEvent.click(screen.getByRole("button", { name: "Expand sidebar" }));

		expect(useUiStore.getState().isSidebarOpen).toBe(true);
		expect(screen.getByTestId("sidebar-provider")).toHaveAttribute("data-open", "true");
		expect(screen.getByRole("button", { name: "Collapse sidebar" })).toBeInTheDocument();
	});
});

describe("shell new-shell-terminal shortcut subscription", () => {
	function pressNewShellTerminal() {
		const listener = shellMocks.state.newShellTerminalListener;
		if (!listener) throw new Error("new-shell-terminal listener was not registered");
		act(() => listener());
	}

	// Regression: the shell LAYOUT must own this, not the session view. When the
	// session view owned it, the shortcut did nothing outside a session route —
	// nothing was mounted to hear it.
	it("opens a terminal from the dedicated terminals route", async () => {
		shellMocks.state.matchRouteTarget = "/terminals";
		await renderShell();

		pressNewShellTerminal();

		expect(useUiStore.getState().newShellTerminalNonce).toBe(1);
		expect(shellMocks.openShellTerminal).toHaveBeenCalledTimes(1);
	});

	// Regression (#4772): ⌘T on the project board must not yank users into /terminals.
	it("ignores the shortcut on the project board", async () => {
		shellMocks.state.routeParams = { projectId: "proj-1" };
		await renderShell();

		pressNewShellTerminal();

		expect(useUiStore.getState().newShellTerminalNonce).toBe(0);
		expect(shellMocks.openShellTerminal).not.toHaveBeenCalled();
	});

	// Regression: a terminal opened from a session view must carry the session
	// id, not just its owning project's, so the daemon can resolve the
	// session's own worktree instead of the registered project root.
	it("scopes the terminal to the session in scope", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		pressNewShellTerminal();

		expect(shellMocks.openShellTerminal).toHaveBeenCalledWith(
			expect.objectContaining({ projectId: "proj-1", sessionId: "sess-1" }),
		);
	});

	it("preserves the cloud identity for a session-scoped terminal", async () => {
		const session = workspaces[0]!.sessions[0]!;
		session.cloud = { orgId: "cloud-org" };
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		pressNewShellTerminal();

		expect(shellMocks.openShellTerminal).toHaveBeenCalledWith(
			expect.objectContaining({
				projectId: "proj-1",
				sessionId: "sess-1",
				cloud: { orgId: "cloud-org" },
			}),
		);
		delete session.cloud;
	});

	// Session terminals always belong to the session on screen — there is no
	// longer an "owner" session whose worktree could be borrowed here (#3208).
	it("scopes the terminal to the session on screen, not the route's project alone", async () => {
		shellMocks.state.routeParams = { projectId: "proj-2", sessionId: "sess-cross" };
		await renderShell();

		pressNewShellTerminal();

		expect(shellMocks.openShellTerminal).toHaveBeenCalledWith(
			expect.objectContaining({ projectId: "proj-2", sessionId: "sess-cross" }),
		);
	});

	it("re-fires on a repeat press so a second terminal can be opened", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		pressNewShellTerminal();
		pressNewShellTerminal();

		expect(useUiStore.getState().newShellTerminalNonce).toBe(2);
		expect(shellMocks.openShellTerminal).toHaveBeenCalledTimes(2);
	});
});

describe("shell keyboard-shortcuts help subscription", () => {
	it("opens the Shortcuts settings page from Customize and closes help", async () => {
		await renderShell();
		act(() => shellMocks.state.keyboardShortcutsListener?.());
		fireEvent.click(screen.getByRole("button", { name: "Customize" }));

		expect(screen.queryByTestId("keyboard-shortcuts")).not.toBeInTheDocument();
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "shortcuts" });
	});

	it("opens the keyboard-shortcuts dialog", async () => {
		await renderShell();

		const listener = shellMocks.state.keyboardShortcutsListener;
		if (!listener) throw new Error("keyboard-shortcuts listener was not registered");
		act(() => listener());

		expect(screen.getByTestId("keyboard-shortcuts")).toBeInTheDocument();
	});
});

describe("shell quit confirm subscription", () => {
	it("shows the quit confirm over the startup loader and resolves it either way", async () => {
		shellMocks.state.daemonStatus = { state: "starting", port: 4777 };
		await renderShell();
		expect(screen.getByTestId("daemon-startup-loader")).toBeInTheDocument();

		const listener = shellMocks.state.quitConfirmListener;
		if (!listener) throw new Error("quit confirm listener was not registered");
		act(() => listener({ keepsRunning: true }));
		fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
		expect(shellMocks.resolveQuitConfirm).toHaveBeenLastCalledWith(false);
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Quit AO?" })).not.toBeInTheDocument());

		act(() => listener({ keepsRunning: true }));
		fireEvent.click(await screen.findByRole("button", { name: "Quit" }));
		expect(shellMocks.resolveQuitConfirm).toHaveBeenLastCalledWith(true);
		expect(shellMocks.resolveQuitConfirm).toHaveBeenCalledTimes(2);
		expect(screen.getByTestId("daemon-startup-loader")).toBeInTheDocument();
	});

	it("opens the Quit AO? dialog on request and reports the answer to the main process", async () => {
		await renderShell();

		const listener = shellMocks.state.quitConfirmListener;
		if (!listener) throw new Error("quit confirm listener was not registered");
		act(() => listener({ keepsRunning: true }));
		expect(
			await screen.findByText("AO will keep running in the background. You can reopen it from the menu bar."),
		).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Quit" }));
		expect(shellMocks.resolveQuitConfirm).toHaveBeenCalledExactlyOnceWith(true);
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Quit AO?" })).not.toBeInTheDocument());

		act(() => listener({ keepsRunning: false }));
		expect(await screen.findByText("Are you sure you want to quit AO?")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(shellMocks.resolveQuitConfirm).toHaveBeenLastCalledWith(false);
	});
});

describe("shell new-session shortcut subscription", () => {
	it("opens the new-task flow for the route project", async () => {
		shellMocks.state.routeParams = { projectId: "proj-1" };
		await renderShell();

		emitShortcut();

		expect(screen.getByTestId("new-task-flow")).toHaveAttribute("data-project", "proj-1");
		expect(screen.queryByTestId("create-project-flow")).not.toBeInTheDocument();
	});

	it("opens the new-task flow for the project owning the current session", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		emitShortcut();

		expect(screen.getByTestId("new-task-flow")).toHaveAttribute("data-project", "proj-1");
	});

	it("opens the standalone new-task flow when no project is in scope", async () => {
		await renderShell();

		emitShortcut();

		expect(screen.getByTestId("new-task-flow")).toHaveAttribute("data-project", "__standalone__");
		expect(screen.queryByTestId("create-project-flow")).not.toBeInTheDocument();
	});

	it("stays with the project the new chat page shows, not the last one opened", async () => {
		window.localStorage.setItem("ao.newTask.lastProject", "proj-1");
		shellMocks.state.matchRouteTarget = "/new";
		shellMocks.state.routeSearch = { project: "proj-2" };
		await renderShell();

		emitShortcut();

		expect(screen.getByTestId("new-task-flow")).toHaveAttribute("data-project", "proj-2");
	});

	it("stays with the project and host a remote new chat page shows", async () => {
		shellMocks.state.matchRouteTarget = "/new";
		shellMocks.state.routeSearch = { project: "remote-proj", host: "host-a" };
		await renderShell();

		emitShortcut();

		const flow = screen.getByTestId("new-task-flow");
		expect(flow).toHaveAttribute("data-project", "remote-proj");
		expect(flow).toHaveAttribute("data-host", "host-a");
	});
});

describe("shell application shortcut subscriptions", () => {
	it("opens settings", async () => {
		await renderShell();

		act(() => shellMocks.state.openSettingsListener?.());

		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global" });
		expect(shellMocks.navigate).not.toHaveBeenCalled();
	});

	// Sidebar order for the fixture: sess-3, sess-1 (newest first; terminated
	// sessions are not listed), then proj-2's sess-cross.
	it("moves to the next session in sidebar order, crossing into the next project", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		act(() => shellMocks.state.nextSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-2", sessionId: "sess-cross" },
		});
	});

	it("moves to the previous session and skips terminated ones", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		act(() => shellMocks.state.previousSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-3" },
		});
	});

	it("wraps to the last session when moving previous from the first", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-3" };
		await renderShell();

		act(() => shellMocks.state.previousSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-2", sessionId: "sess-cross" },
		});
	});

	it("wraps to the first session when moving next from the last", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-cross" };
		await renderShell();

		act(() => shellMocks.state.nextSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-3" },
		});
	});

	it("starts at the first session going forward and the last going backward from a project board", async () => {
		shellMocks.state.routeParams = { projectId: "proj-1" };
		await renderShell();

		act(() => shellMocks.state.nextSessionListener?.());
		expect(shellMocks.navigate).toHaveBeenLastCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-3" },
		});

		act(() => shellMocks.state.previousSessionListener?.());
		expect(shellMocks.navigate).toHaveBeenLastCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-2", sessionId: "sess-cross" },
		});
	});

	it("follows the project order the user dragged the sidebar into", async () => {
		useSidebarOrderStore.setState({ projectOrder: ["proj-2", "proj-1"] });
		shellMocks.state.routeParams = { sessionId: "sess-cross" };
		await renderShell();

		act(() => shellMocks.state.nextSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-3" },
		});
	});

	it("follows the session order the user dragged within a project", async () => {
		useSidebarOrderStore.setState({ sessionOrders: { "proj-1": ["sess-1", "sess-3"] } });
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		act(() => shellMocks.state.nextSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-3" },
		});
	});

	it("lists pinned sessions first, once", async () => {
		setWorkspaces([
			{
				...workspaces[0],
				sessions: [
					{ id: "sess-1", workspaceId: "proj-1", status: "working" },
					{ id: "sess-3", workspaceId: "proj-1", status: "idle", isPinned: true, pinnedAt: "2026-10-10T00:00:00Z" },
				],
			},
			workspaces[1],
		] as unknown as WorkspaceSummary[]);
		shellMocks.state.routeParams = { sessionId: "sess-3" };
		await renderShell();

		// Pinned sess-3 is first; its project row would list it again at the end.
		emitAppShortcut({ id: "jump-to-session", index: 0 });
		expect(shellMocks.navigate).toHaveBeenLastCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-3" },
		});
		emitAppShortcut({ id: "jump-to-session", index: 1 });
		expect(shellMocks.navigate).toHaveBeenLastCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-1" },
		});
		emitAppShortcut({ id: "jump-to-session", index: 2 });
		expect(shellMocks.navigate).toHaveBeenLastCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-2", sessionId: "sess-cross" },
		});
	});

	it("jumps to the Nth session in sidebar order and ignores a digit past the end", async () => {
		await renderShell();

		emitAppShortcut({ id: "jump-to-session", index: 1 });
		expect(shellMocks.navigate).toHaveBeenLastCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-1" },
		});

		shellMocks.navigate.mockClear();
		emitAppShortcut({ id: "jump-to-session", index: 8 });
		expect(shellMocks.navigate).not.toHaveBeenCalled();
	});

	it("leaves the digits to the command palette while it is open", async () => {
		await renderShell();
		useUiStore.setState({ isCommandPaletteOpen: true });

		emitAppShortcut({ id: "jump-to-session", index: 0 });

		expect(shellMocks.navigate).not.toHaveBeenCalled();
		useUiStore.setState({ isCommandPaletteOpen: false });
	});

	it("jumps between sessions that need attention, skipping the rest", async () => {
		const working = (id: string) => ({ id, workspaceId: "proj-1", status: "working" });
		const needsInput = (id: string) => ({ id, workspaceId: "proj-1", status: "needs_input" });
		setWorkspaces([
			{ ...workspaces[0], sessions: [working("a"), needsInput("b"), working("c"), needsInput("d")] },
			{ ...workspaces[1], sessions: [working("e")] },
		] as unknown as WorkspaceSummary[]);
		shellMocks.state.routeParams = { sessionId: "d" };
		await renderShell();
		const ids = () => shellMocks.navigate.mock.calls.map(([target]) => target.params.sessionId);

		emitAppShortcut({ id: "next-attention-session" });
		emitAppShortcut({ id: "previous-attention-session" });

		// Sidebar order is d, c, b, a (newest first) then e; the only other
		// attention session from d is b, both ways round.
		expect(ids().every((id) => id === "b")).toBe(true);
		expect(ids().length).toBeGreaterThan(0);
	});

	it("says so when nothing needs attention", async () => {
		shellMocks.state.routeParams = { sessionId: "sess-1" };
		await renderShell();

		emitAppShortcut({ id: "next-attention-session" });

		expect(shellMocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToast?.title).toBe("No other sessions need attention");
	});

	it("goes back and forward through route history", async () => {
		await renderShell();

		emitAppShortcut({ id: "history-back" });
		emitAppShortcut({ id: "history-forward" });

		expect(shellMocks.router.history.back).toHaveBeenCalledTimes(1);
		expect(shellMocks.router.history.forward).toHaveBeenCalledTimes(1);
	});

	it("does not go back past the first route", async () => {
		shellMocks.router.history.canGoBack.mockReturnValue(false);
		await renderShell();

		emitAppShortcut({ id: "history-back" });

		expect(shellMocks.router.history.back).not.toHaveBeenCalled();
	});

	it("toggles the sidebar from the main-process shortcut", async () => {
		useUiStore.setState({ isSidebarOpen: true });
		await renderShell();

		emitAppShortcut({ id: "toggle-sidebar" });
		expect(useUiStore.getState().isSidebarOpen).toBe(false);
		emitAppShortcut({ id: "toggle-sidebar" });
		expect(useUiStore.getState().isSidebarOpen).toBe(true);
	});

	it("moves between standalone sessions without constructing a project route", async () => {
		const standalone = {
			id: "__standalone__",
			name: "Standalone agents",
			kind: "standalone",
			path: "",
			sessions: [
				{ id: "standalone-1", workspaceId: "", status: "working" },
				{ id: "standalone-2", workspaceId: "", status: "idle" },
			],
		} as unknown as WorkspaceSummary;
		shellMocks.state.routeParams = { sessionId: "standalone-1" };
		shellMocks.state.workspaces = [...workspaces, standalone];
		shellMocks.state.workspaceQuery = {
			data: shellMocks.state.workspaces,
			dataUpdatedAt: 0,
			isError: false,
			isSuccess: true,
		};
		await renderShell();

		// The Scratchpad lists newest first (standalone-2, standalone-1).
		act(() => shellMocks.state.previousSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/sessions/$sessionId",
			params: { sessionId: "standalone-2" },
		});
	});

	it("cycles only sessions on the selected remote host when IDs collide", async () => {
		shellMocks.state.routeParams = { hostId: "box-b", sessionId: "sess-1" };
		shellMocks.state.remoteWorkspaces = [
			{ hostId: "box-a", id: "proj-1", sessions: [
				{ id: "sess-1", status: "working" }, { id: "box-a-next", status: "idle" },
			] },
			{ hostId: "box-b", id: "proj-1", sessions: [
				{ id: "sess-1", status: "working" }, { id: "box-b-next", status: "idle" },
			] },
		] as WorkspaceSummary[];
		await renderShell();

		act(() => shellMocks.state.nextSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: "box-b", projectId: "proj-1", sessionId: "box-b-next" },
		});
	});

	it("cycles remote standalone sessions on their host", async () => {
		shellMocks.state.routeParams = { hostId: "box-b", sessionId: "standalone-1" };
		shellMocks.state.remoteWorkspaces = [{
			hostId: "box-b", id: "__standalone__", sessions: [
				{ id: "standalone-1", status: "working" },
				{ id: "standalone-2", status: "idle" },
			],
		}] as WorkspaceSummary[];
		await renderShell();

		act(() => shellMocks.state.previousSessionListener?.());

		expect(shellMocks.navigate).toHaveBeenCalledWith({
			to: "/host/$hostId/session/$sessionId",
			params: { hostId: "box-b", sessionId: "standalone-2" },
		});
	});
});

describe("shell folder drag-and-drop", () => {
	function fileDragTransfer(options: { fileName?: string; isDirectory: boolean }) {
		const file = new File([], options.fileName ?? "dropped-folder");
		return {
			dropEffect: "none",
			items: [
				{
					getAsFile: () => file,
					kind: "file",
					webkitGetAsEntry: () => ({ isDirectory: options.isDirectory }),
				},
			],
			types: ["Files"],
		};
	}

	it("does not show the overlay for a plain-file drag", async () => {
		await renderShell();

		fireEvent.dragEnter(window, { dataTransfer: fileDragTransfer({ isDirectory: false }) });
		expect(screen.queryByTestId("folder-drop-overlay")).not.toBeInTheDocument();
	});

	it("shows the drop overlay while a folder is dragged over the window, and hides it on leave", async () => {
		await renderShell();

		fireEvent.dragEnter(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		expect(screen.getByTestId("folder-drop-overlay")).toBeInTheDocument();

		fireEvent.dragLeave(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		expect(screen.queryByTestId("folder-drop-overlay")).not.toBeInTheDocument();
	});

	// Regression: dragenter/dragleave fire again for every child-element boundary
	// the pointer crosses while hovering inside the window, not just at the
	// window's own edge. A relatedTarget-blind counter is what keeps the overlay
	// from flickering off mid-drag.
	it("does not flicker the overlay when the pointer crosses a child element", async () => {
		await renderShell();

		fireEvent.dragEnter(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		fireEvent.dragEnter(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		expect(screen.getByTestId("folder-drop-overlay")).toBeInTheDocument();

		fireEvent.dragLeave(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		expect(screen.getByTestId("folder-drop-overlay")).toBeInTheDocument();

		fireEvent.dragLeave(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		expect(screen.queryByTestId("folder-drop-overlay")).not.toBeInTheDocument();
	});

	it("resolves the dropped folder's real path and opens the create-project flow", async () => {
		await renderShell();

		fireEvent.dragEnter(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		fireEvent.drop(window, { dataTransfer: fileDragTransfer({ fileName: "my-project", isDirectory: true }) });

		expect(shellMocks.getPathForFile).toHaveBeenCalledTimes(1);
		expect(screen.queryByTestId("folder-drop-overlay")).not.toBeInTheDocument();
		expect(screen.getByTestId("create-project-flow")).toHaveAttribute("data-path", "/dropped/my-project");
		expect(useUiStore.getState().folderDropRequest).toEqual({ nonce: 1, path: "/dropped/my-project" });
	});

	it("ignores a drop that is not a folder", async () => {
		await renderShell();

		fireEvent.drop(window, { dataTransfer: fileDragTransfer({ isDirectory: false }) });

		expect(shellMocks.getPathForFile).not.toHaveBeenCalled();
		expect(useUiStore.getState().folderDropRequest).toBeNull();
		expect(screen.queryByTestId("create-project-flow")).not.toBeInTheDocument();
	});

	it("re-fires on a repeated drop so a second folder can be added", async () => {
		await renderShell();

		fireEvent.drop(window, { dataTransfer: fileDragTransfer({ fileName: "first", isDirectory: true }) });
		fireEvent.drop(window, { dataTransfer: fileDragTransfer({ fileName: "second", isDirectory: true }) });

		expect(useUiStore.getState().folderDropRequest).toEqual({ nonce: 2, path: "/dropped/second" });
	});

	// Regression: XtermTerminal used to stopPropagation() on a plain (non-folder)
	// file drop, so this drop event never reached the window and dragDepthRef
	// was never reset. The next folder drag then bumped from a stale nonzero
	// depth, missed the === 1 branch, and never showed the overlay. Xterm no
	// longer stops propagation for a non-directory drop, so this event now
	// always reaches the window; this test locks in that the window's own
	// reset logic correctly recovers once it does.
	it("does not suppress the next folder drag's overlay after a plain file drop resets the counter", async () => {
		await renderShell();

		fireEvent.dragEnter(window, { dataTransfer: fileDragTransfer({ isDirectory: false }) });
		fireEvent.drop(window, { dataTransfer: fileDragTransfer({ isDirectory: false }) });
		expect(screen.queryByTestId("folder-drop-overlay")).not.toBeInTheDocument();

		fireEvent.dragEnter(window, { dataTransfer: fileDragTransfer({ isDirectory: true }) });
		expect(screen.getByTestId("folder-drop-overlay")).toBeInTheDocument();
	});
});

describe("shell taskbar-icon folder drop subscription", () => {
	it("opens the create-project flow for a folder dropped on the app's icon/shortcut", async () => {
		await renderShell();
		expect(shellMocks.onOpenFolderPath).toHaveBeenCalledTimes(1);

		const listener = shellMocks.state.openFolderPathListener;
		if (!listener) throw new Error("open-folder-path listener was not registered");
		act(() => listener("/dropped-on-icon/my-project"));

		expect(useUiStore.getState().folderDropRequest).toEqual({ nonce: 1, path: "/dropped-on-icon/my-project" });
		expect(screen.getByTestId("create-project-flow")).toHaveAttribute("data-path", "/dropped-on-icon/my-project");
	});
});
