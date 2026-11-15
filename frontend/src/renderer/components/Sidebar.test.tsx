import { SidebarProvider } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// Disable motion animations so AnimatePresence unmounts children immediately
// (no exit-animation timer keeps them alive after conditional removal).
vi.mock("motion/react", async (importOriginal) => {
	const actual = await importOriginal<typeof import("motion/react")>();
	return {
		...actual,
		AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
	};
});
import { act, cleanup, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { components } from "../../api/schema";
import {
	Sidebar,
	SIDEBAR_DEFAULT_WIDTH,
	SIDEBAR_MIN_WIDTH,
	resolveNextNavigationAfterSessionKill,
} from "./Sidebar";
import {
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { sessionInterfaceTransitionStatus } from "../test/interface-transition-fixtures";
import { useUiStore } from "../stores/ui-store";
import { aoBridge } from "../lib/bridge";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useSessionImportStore } from "../stores/session-import-store";
import { sessionInterfaceTransitionQueryKey } from "../hooks/useSessionInterfaceTransition";
import { resetSessionPreviewWarmth } from "../hooks/useSessionPreviewHover";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { useRemoteWorkspaces } from "../hooks/useWorkspaceQuery";
import { clearOptimisticSessionKill, trackOptimisticSessionKill } from "../hooks/optimistic-session-kills";
import { useRemoteUnpinStore } from "../stores/remote-unpin-store";
import { sessionUiKey } from "../lib/hosts";

type DragOverTestEvent = {
	active: {
		id: string;
		rect: { current: { initial: null; translated: null } };
	};
	activatorEvent: null;
	delta: { x: number; y: number };
	over: { id: string; rect: { height: number; top: number } } | null;
};

const {
	checkUpdateMock,
	cloudGateState,
	cloudSessionState,
	dragEnds,
	dragOvers,
	dragStarts,
	downloadUpdateMock,
	getMock,
	navigateMock,
	mockLocation,
	mockParams,
	routeStore,
	postMock,
	renameSessionMock,
	spawnMock,
	resumeOrchestratorMock,
	updateStatusMock,
	commandPaletteEnabled,
	trustedApi,
} = vi.hoisted(
	() => ({
		cloudGateState: { cloudEnabled: true, localEnabled: true, client: "" },
		cloudSessionState: {
			configured: false,
			session: null as null | { user: { email: string; displayName?: string } },
			status: "unauthenticated" as "authenticated" | "loading" | "unauthenticated",
			signIn: vi.fn(),
			signOut: vi.fn().mockResolvedValue(undefined),
		},
		dragEnds: new Map<string, (event: { active: { id: string }; over: { id: string } | null }) => void>(),
		dragOvers: new Map<string, (event: DragOverTestEvent) => void>(),
		dragStarts: new Map<string, (event: { active: { id: string } }) => void>(),
		getMock: vi.fn(),
		postMock: vi.fn(),
		navigateMock: vi.fn(),
		mockLocation: { pathname: "/" },
		mockParams: { hostId: undefined as string | undefined, projectId: undefined as string | undefined, sessionId: undefined as string | undefined },
		// The router hooks subscribe to the route, as the real ones do, so a
		// memoized Sidebar still sees a route change without new props.
		routeStore: { version: 0, listeners: new Set<() => void>() },
		renameSessionMock: vi.fn().mockResolvedValue(undefined),
		spawnMock: vi.fn(),
		resumeOrchestratorMock: vi.fn(),
		updateStatusMock: vi.fn(),
		downloadUpdateMock: vi.fn(),
		checkUpdateMock: vi.fn(),
		commandPaletteEnabled: { current: true },
		trustedApi: { current: false },
	}),
);

vi.mock("@dnd-kit/core", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@dnd-kit/core")>();
	return {
		...actual,
		DndContext: ({ children, id, onDragEnd, onDragOver, onDragStart }: {
			children: React.ReactNode;
			id?: string;
			onDragEnd?: (event: { active: { id: string }; over: { id: string } | null }) => void;
			onDragOver?: (event: DragOverTestEvent) => void;
			onDragStart?: (event: { active: { id: string } }) => void;
		}) => {
			if (id && onDragEnd) dragEnds.set(id, onDragEnd);
			if (id && onDragOver) dragOvers.set(id, onDragOver);
			if (id && onDragStart) dragStarts.set(id, onDragStart);
			return <div data-dnd-context={id}>{children}</div>;
		},
		DragOverlay: ({ children }: { children: React.ReactNode }) => children,
	};
});

vi.mock("../lib/rename-session", () => ({ renameSession: renameSessionMock }));
vi.mock("../lib/spawn-orchestrator", () => ({
	spawnOrchestrator: spawnMock,
	resumeOrchestrator: resumeOrchestratorMock,
}));
// Counts session-row renders by id; every SessionRow calls this hook once.
const sessionRowRenders = vi.hoisted(() => new Map<string, number>());
vi.mock("../hooks/useSessionRename", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useSessionRename")>();
	return {
		...actual,
		useSessionRename: (...args: Parameters<typeof actual.useSessionRename>) => {
			const id = args[0]?.id;
			if (id) sessionRowRenders.set(id, (sessionRowRenders.get(id) ?? 0) + 1);
			return actual.useSessionRename(...args);
		},
	};
});
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => cloudSessionState }));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => cloudGateState }));
// Local (dev-only) cloud sign-in is off in these tests; mock it like its cloud
// siblings so the sign-in row never fires a real settings fetch.
vi.mock("../hooks/useCloudLocalAuth", () => ({
	useCloudLocalAuth: () => ({
		available: false,
		cpUrl: "",
		register: async () => {
			throw new Error("useCloudLocalAuth is mocked");
		},
		login: async () => {
			throw new Error("useCloudLocalAuth is mocked");
		},
	}),
}));
vi.mock("../hooks/useCommandPaletteEnabled", () => ({
	useCommandPaletteEnabled: () => commandPaletteEnabled.current,
}));

vi.mock("../lib/platform", () => ({
	isLinuxPlatform: () => false,
	isMacPlatform: () => true,
	isWindowsPlatform: () => false,
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
		useParams: () => {
			useSyncExternalStore(subscribeRoute, routeVersion);
			return { ...mockParams };
		},
		useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) => {
			useSyncExternalStore(subscribeRoute, routeVersion);
			return select({ location: { pathname: mockLocation.pathname } });
		},
	};
});

function subscribeRoute(listener: () => void) {
	routeStore.listeners.add(listener);
	return () => routeStore.listeners.delete(listener);
}

function routeVersion() {
	return routeStore.version;
}

/** Tells the mocked router hooks that `mockParams` / `mockLocation` changed. */
function notifyRouteChange() {
	routeStore.version += 1;
	for (const listener of routeStore.listeners) listener();
}

vi.mock("../lib/bridge", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/bridge")>();
	return {
		aoBridge: {
			...actual.aoBridge,
			updates: {
				...actual.aoBridge.updates,
				getStatus: updateStatusMock,
				download: downloadUpdateMock,
				check: checkUpdateMock,
			},
		},
	};
});

vi.mock("../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { trackerIntakeEnabled: true }, isLoading: false, error: undefined }),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	hasTrustedApiBaseUrl: () => trustedApi.current,
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string") {
			return error.message;
		}
		return "Request failed";
	},
}));

const workspace: WorkspaceSummary = {
	id: "proj-1",
	name: "Project One",
	path: "/repo/project-one",
	orchestratorAgent: "claude-code",
	sessions: [],
};

const session: WorkspaceSession = {
	id: "proj-1-1",
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: "fix login",
	provider: "claude-code",
	kind: "worker",
	branch: "session/proj-1-1",
	status: "working",
	updatedAt: "2026-06-30T00:00:00Z",
	prs: [],
};

// The row archive is confirmed, not instant: open the shared modal and accept.
async function confirmArchiveFromRow(row: HTMLElement) {
	fireEvent.click(within(row).getByLabelText("Archive session"));
	const dialog = await screen.findByRole("dialog");
	fireEvent.click(within(dialog).getByRole("button", { name: "Confirm, archive session" }));
}

const exitedOrchestrator: WorkspaceSession = {
	...session,
	id: "proj-1-orch",
	kind: "orchestrator",
	status: "exited",
	activity: { state: "exited", lastActivityAt: "2026-06-30T00:00:00Z" },
	isTerminated: false,
};

function sidebarPR(overrides: Partial<WorkspaceSession["prs"][number]> = {}): WorkspaceSession["prs"][number] {
	return {
		url: "https://github.com/acme/project-one/pull/7",
		number: 7,
		state: "open",
		ci: "unknown",
		review: "none",
		mergeability: "unknown",
		reviewComments: false,
		updatedAt: "2026-06-30T00:00:00Z",
		...overrides,
	};
}

type CreateProjectInput = {
	path: string;
	workerAgent: string;
	orchestratorAgent: string;
	trackerIntake?: unknown;
	asWorkspace?: boolean;
};
type CreateProjectHandler = (input: CreateProjectInput) => Promise<void>;
type CloneProjectHandler = (input: {
	remoteUrl: string;
	destinationParent: string;
	workerAgent: string;
	orchestratorAgent: string;
	trackerIntake?: unknown;
	signal?: AbortSignal;
}) => Promise<void>;
type InitializeProjectHandler = (path: string) => Promise<void>;
type RemoveProjectHandler = (projectId: string, cloud: boolean) => void;
type ImportValidationResult = components["schemas"]["ImportValidationResult"];
type RepoGitStatus = components["schemas"]["RepoGitStatus"];

function repoStatus(repoPath: string, overrides: Partial<RepoGitStatus> = {}): RepoGitStatus {
	return {
		repoPath,
		isRepo: false,
		hasCommit: false,
		hasOrigin: false,
		isEmptyFolder: false,
		needsGitInit: false,
		requiredActions: [],
		blockingErrors: [],
		...overrides,
	};
}

function importValidation(path: string, overrides: Partial<ImportValidationResult> = {}): ImportValidationResult {
	const importKind = overrides.importKind ?? "workspace";
	return {
		importKind,
		isValid: true,
		blockingErrors: [],
		root: repoStatus(path, importKind === "project" ? { isRepo: true, hasCommit: true, hasOrigin: true } : {}),
		childRepos: [],
		nextStep: "continue",
		...overrides,
	};
}

function renderSidebar({
	onCloneProject = vi.fn().mockResolvedValue(undefined) as CloneProjectHandler,
	onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler,
	onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler,
	onRemoveProject = vi.fn().mockResolvedValue(undefined) as RemoveProjectHandler,
	seedAgents = true,
	workspaces = [workspace],
	remoteHosts = [],
	remoteWorkspaces = [],
	onCreateRemoteProject = vi.fn().mockResolvedValue(undefined),
	onInitializeRemoteProject = vi.fn().mockResolvedValue(undefined),
	onRemoveRemoteProject = vi.fn().mockResolvedValue(undefined),
	initialOpen = true,
	topbarOffset = "toolbar",
	expandedProjectIds,
	seed,
}: {
	onCloneProject?: CloneProjectHandler;
	onCreateProject?: CreateProjectHandler;
	onInitializeProject?: InitializeProjectHandler;
	onRemoveProject?: RemoveProjectHandler;
	seedAgents?: boolean;
	workspaces?: WorkspaceSummary[];
	remoteHosts?: RemoteHost[];
	remoteWorkspaces?: WorkspaceSummary[];
	onCreateRemoteProject?: (hostId: string, input: CreateProjectInput) => Promise<void>;
	onInitializeRemoteProject?: (hostId: string, path: string) => Promise<void>;
	onRemoveRemoteProject?: (hostId: string, projectId: string) => Promise<void>;
	initialOpen?: boolean;
	topbarOffset?: "toolbar" | "titlebar" | "trafficLights" | "session";
	expandedProjectIds?: string[];
	seed?: (client: QueryClient) => void;
} = {}) {
	// Most legacy sidebar tests exercise session rows and assume their fixture
	// project was previously open. Tests for the empty-store behavior opt out.
	window.localStorage.setItem(
		"ao.sidebar.expanded-projects",
		JSON.stringify(expandedProjectIds ?? workspaces.map(({ id }) => id)),
	);
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	if (seedAgents) {
		queryClient.setQueryData(agentReadinessQueryKey, {
			agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
		});
	}
	seed?.(queryClient);
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<SidebarProvider defaultOpen={initialOpen}>
					<Sidebar
						topbarOffset={topbarOffset}
						onCloneProject={onCloneProject}
						onCreateProject={onCreateProject}
						onInitializeProject={onInitializeProject}
						onRemoveProject={onRemoveProject}
						workspaces={workspaces}
						remoteHosts={remoteHosts}
						remoteWorkspaces={remoteWorkspaces}
						onCreateRemoteProject={onCreateRemoteProject}
						onInitializeRemoteProject={onInitializeRemoteProject}
						onRemoveRemoteProject={onRemoveRemoteProject}
					/>
				</SidebarProvider>
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return onRemoveProject;
}

it("keeps new projects at the top across refreshes and restores the order after restart", () => {
	const alpha = { ...workspace, id: "alpha", name: "Alpha", sessions: [] };
	const beta = { ...workspace, id: "beta", name: "Beta", sessions: [] };
	const newest = { ...workspace, id: "zulu", name: "Zulu", sessions: [] };
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const noop = vi.fn().mockResolvedValue(undefined);
	const tree = (workspaces: WorkspaceSummary[]) => (
		<QueryClientProvider client={client}><TooltipProvider><SidebarProvider>
			<Sidebar workspaces={workspaces} onCloneProject={noop} onCreateProject={noop} onInitializeProject={noop} onRemoveProject={noop} onCreateRemoteProject={noop} onInitializeRemoteProject={noop} />
		</SidebarProvider></TooltipProvider></QueryClientProvider>
	);
	const view = render(tree([alpha, beta]));
	const rows = () => Array.from(document.querySelectorAll("[data-project-drop-target][data-project-id]"));
	view.rerender(tree([newest, alpha, beta]));
	const newRow = rows()[0];
	expect(rows().map((row) => row.getAttribute("data-project-id"))).toEqual(["zulu", "alpha", "beta"]);
	view.rerender(tree([alpha, beta, newest]));
	expect(rows().map((row) => row.getAttribute("data-project-id"))).toEqual(["zulu", "alpha", "beta"]);
	expect(rows()[0]).toBe(newRow);
	expect(JSON.parse(window.localStorage.getItem("ao.sidebar.project-order") ?? "null")).toEqual(["zulu", "alpha", "beta"]);
	view.unmount();
	const restarted = render(tree([]));
	restarted.rerender(tree([alpha, beta, newest]));
	expect(rows().map((row) => row.getAttribute("data-project-id"))).toEqual(["zulu", "alpha", "beta"]);
});

it("shows the creation order before the new project snapshot arrives and preserves it through refreshes", () => {
	const alpha = { ...workspace, id: "alpha", name: "Alpha", sessions: [] };
	const beta = { ...workspace, id: "beta", name: "Beta", sessions: [] };
	const newest = { ...workspace, id: "zulu", name: "Zulu", sessions: [] };
	const olderCloud = { ...workspace, id: "older-cloud", name: "Older cloud", kind: "cloud" as const, sessions: [] };
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const noop = vi.fn().mockResolvedValue(undefined);
	const tree = (workspaces: WorkspaceSummary[]) => (
		<QueryClientProvider client={client}><TooltipProvider><SidebarProvider>
			<Sidebar workspaces={workspaces} onCloneProject={noop} onCreateProject={noop} onInitializeProject={noop} onRemoveProject={noop} onCreateRemoteProject={noop} onInitializeRemoteProject={noop} />
		</SidebarProvider></TooltipProvider></QueryClientProvider>
	);
	window.localStorage.setItem("ao.sidebar.project-order", JSON.stringify(["beta", "alpha", "zulu"]));
	useSidebarOrderStore.getState().setProjectOrder(["beta", "alpha", "zulu"]);
	const view = render(tree([alpha, beta]));
	act(() => useSidebarOrderStore.getState().moveProjectToTop("zulu"));
	view.rerender(tree([alpha, beta, olderCloud, newest]));
	const ids = () => Array.from(document.querySelectorAll("[data-project-drop-target][data-project-id]"), (row) => row.getAttribute("data-project-id"));
	expect(ids()).toEqual(["zulu", "older-cloud", "beta", "alpha"]);
	view.rerender(tree([]));
	view.rerender(tree([alpha, beta, olderCloud, newest]));
	expect(ids()).toEqual(["zulu", "older-cloud", "beta", "alpha"]);
	expect(JSON.parse(window.localStorage.getItem("ao.sidebar.project-order") ?? "null")).toEqual(["zulu", "older-cloud", "beta", "alpha"]);
});

it.each(["invalid JSON", '{"unexpected":true}', '[4,null]'])("ignores invalid saved project order: %s", (saved) => {
	window.localStorage.setItem("ao.sidebar.project-order", saved);
	renderSidebar();
	expect(document.querySelector(`[data-project-id="${workspace.id}"]`)).toBeInTheDocument();
});

function mockAgentReadinessResponse(response: {
	data: { agents: ReturnType<typeof agentReadiness>[] };
	error: undefined;
} | Promise<{
	data: { agents: ReturnType<typeof agentReadiness>[] };
	error: undefined;
}>) {
	const fallback = getMock.getMockImplementation();
	getMock.mockImplementation((path: string) =>
		path === "/api/v1/agents/readiness" ? Promise.resolve(response) : fallback?.(path),
	);
}

/** Projects restore their persisted disclosure state. */

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	await userEvent.click(await screen.findByRole("option", { name: optionName }));
}

function codedError(message: string, code: "NOT_A_GIT_REPO" | "PROJECT_UNBORN") {
	const error = new Error(message) as Error & { code: string };
	error.code = code;
	return error;
}

async function openCreateProjectDialog(
	path = "/repo/new-project",
	scan: {
		path: string;
		repos: Array<{
			name: string;
			path: string;
			relativePath: string;
			branch: string;
			remote: string;
			hasRemote: boolean;
			status?: "ok" | "error";
			reason?: string;
			needsGitInit?: boolean;
		}>;
	} = {
		path,
		repos: [
			{ name: "project", path, relativePath: ".", branch: "main", remote: "origin", hasRemote: true, status: "ok" },
		],
	},
) {
	const user = userEvent.setup();
	window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue(path);
	window.ao!.app.scanImportFolder = vi.fn().mockResolvedValue(scan);
	await openLocalProjectSources(user);
	await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
	await screen.findByRole("dialog", { name: "Set up project" });
	await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
	await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
	return user;
}

// Cloud is on by default here, so the add-project flow first asks Local or
// Cloud; these tests exercise the local sources behind "Local".
async function openLocalProjectSources(user: ReturnType<typeof userEvent.setup>) {
	await user.click(screen.getByLabelText("New project"));
	await user.click(await screen.findByRole("button", { name: "On this computer" }));
}

function fireDrag(
	type: "dragStart" | "dragOver" | "drop",
	element: Element,
	props: { clientY?: number },
) {
	const dataTransfer = { setData: () => {}, getData: () => "", setDragImage: () => {}, dropEffect: "", effectAllowed: "" };
	const event = createEvent[type](element);
	Object.defineProperty(event, "dataTransfer", { value: dataTransfer, configurable: true });
	if (props.clientY !== undefined) {
		Object.defineProperty(event, "clientY", { value: props.clientY, configurable: true });
	}
	fireEvent(element, event);
}

beforeEach(() => {
	window.localStorage.clear();
	useSidebarOrderStore.setState({ projectOrder: [], pendingCreatedProjectIds: [], pinnedProjectIds: [], sessionOrders: {} });
	useSessionImportStore.setState({ importedCounts: {} });
	dragEnds.clear();
	dragOvers.clear();
	dragStarts.clear();
	document.documentElement.style.removeProperty("--ao-sidebar-w");
	commandPaletteEnabled.current = true;
	trustedApi.current = false;
	cloudGateState.cloudEnabled = true;
	cloudGateState.localEnabled = true;
	cloudGateState.client = "";
	cloudSessionState.configured = false;
	cloudSessionState.session = null;
	cloudSessionState.status = "unauthenticated";
	cloudSessionState.signIn.mockReset();
	cloudSessionState.signOut.mockReset().mockResolvedValue(undefined);
	useUiStore.setState({
		developerMode: false,
		isCommandPaletteOpen: false,
		newTaskRequest: null,
		settingsModal: null,
		provisioningProjectIds: new Set(),
		restartingProjectIds: new Set(),
		updateInstallPromptOpen: false,
	});
	getMock.mockReset();
	getMock.mockResolvedValue({
		data: {
			agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
		},
		error: undefined,
	});
	postMock.mockReset();
	postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string; remoteUrl?: string } }) => {
		if (path === "/api/v1/projects/clone/check") {
			return { data: { remoteUrl: options?.body?.remoteUrl ?? "", path: "/repo/web-app", exists: false, canUseExisting: false } };
		}
		if (path === "/api/v1/projects/clone/prepare") {
			return {
				data: { path: "/repo/web-app", remoteUrl: "git@github.com:acme/web-app.git", preparationId: "prep-web-app" },
				error: undefined,
			};
		}
		if (path === "/api/v1/imports/validate") {
			const selectedPath = options?.body?.path ?? "/repo/workspace";
			const importKind = options?.body?.importKind ?? "workspace";
			return {
				data: importValidation(selectedPath, { importKind }),
				error: undefined,
			};
		}
		if (path === "/api/v1/imports/prepare-git") {
			const selectedPath = options?.body?.path ?? "/repo/workspace";
			return {
				data: {
					events: [],
					validation: importValidation(selectedPath),
				},
				error: undefined,
			};
		}
		return { data: undefined, error: undefined };
	});
	window.ao!.app.scanImportFolder = vi.fn().mockImplementation(async ({ path }: { path: string }) => ({
		path,
		repos: [],
	}));
	window.ao!.app.getGitHubLogin = vi.fn().mockResolvedValue("test-user");
	window.ao!.app.getRepositoryBranch = vi.fn().mockResolvedValue(undefined);
	navigateMock.mockReset();
	renameSessionMock.mockReset().mockResolvedValue(undefined);
	spawnMock.mockReset();
	resumeOrchestratorMock.mockReset().mockResolvedValue(undefined);
	updateStatusMock.mockReset().mockResolvedValue({ state: "idle" });
	downloadUpdateMock.mockReset().mockResolvedValue(undefined);
	checkUpdateMock.mockReset().mockResolvedValue(undefined);
	mockParams.projectId = undefined;
	mockParams.sessionId = undefined;
	mockParams.hostId = undefined;
	mockLocation.pathname = "/";
});

afterEach(() => {
	vi.restoreAllMocks();
});

describe("Sidebar", () => {
	it.each([true, false])("only warms an adopted session on hover (needsResume=%s)", async (needsResume) => {
		vi.useFakeTimers();
		try {
			trustedApi.current = true;
			renderSidebar({
				workspaces: [{ ...workspace, sessions: [{ ...session, mode: "chat", needsResume, activity: { state: "exited", lastActivityAt: session.updatedAt } }] }],
				seed: (client) => client.setQueryData(["conversation", session.id], { pages: [], pageParams: [] }),
			});
			fireEvent.mouseEnter(screen.getByLabelText("Open fix login"));
			await act(async () => { await vi.advanceTimersByTimeAsync(500); });
			if (needsResume) expect(postMock).not.toHaveBeenCalled();
			else expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/resume-agent", { params: { path: { sessionId: session.id } } });
		} finally {
			vi.useRealTimers();
		}
	});

	it("creates local or remote projects from the header button", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [],
			remoteHosts: [
				{ hostId: "box-a", label: "Host A", url: "http://box-a:3011", status: "connected" },
				{ hostId: "box-b", label: "Host B", url: "http://box-b:3011", status: "connected" },
			],
		});

		await user.click(screen.getByRole("button", { name: "New project" }));
		const chooser = await screen.findByRole("dialog");
		// Remote machines are connected, so the local tile covers them too.
		await user.click(within(chooser).getByRole("button", { name: "On your machines" }));
		expect(screen.queryByRole("button", { name: "New project on Host B" })).not.toBeInTheDocument();
		await user.click(within(chooser).getByRole("combobox", { name: "Machine" }));
		await user.click(screen.getByRole("option", { name: "Host B" }));
		expect(screen.getByRole("dialog")).toBe(chooser);
		expect(within(chooser).getByRole("combobox", { name: "Machine" })).toHaveTextContent("Host B");
		await user.click(within(chooser).getByRole("combobox", { name: "Machine" }));
		await user.click(screen.getByRole("option", { name: "This computer" }));
		expect(screen.getByRole("dialog")).toBe(chooser);
	});

	it("lists remote projects beside local and Cloud projects with host-qualified sessions", () => {
		mockParams.hostId = "box-b";
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		const remoteSession = (hostId: string, title: string): WorkspaceSession => ({
			...session, hostId, title,
		});
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [session] }],
			remoteHosts: [
				{ hostId: "box-a", label: "Host A", url: "http://box-a:3001", status: "connected" },
				{ hostId: "box-b", label: "Host B", url: "http://box-b:3001", status: "connected" },
			],
			remoteWorkspaces: [
				{ ...workspace, hostId: "box-a", sessions: [remoteSession("box-a", "Task on A")] },
				{ ...workspace, hostId: "box-b", sessions: [remoteSession("box-b", "Task on B")] },
			],
		});

		const projects = screen.getByTestId("sidebar-projects-scroller");
		expect(within(projects).getByText("Host A")).toBeInTheDocument();
		expect(within(projects).getByText("Host B")).toBeInTheDocument();
		expect(screen.queryByText("Remote hosts")).not.toBeInTheDocument();
		const remoteSessions = within(projects).getAllByTestId("remote-session-row");
		expect(remoteSessions).toHaveLength(2);
		expect(remoteSessions[0]).not.toHaveAttribute("aria-current");
		expect(remoteSessions[1]).toHaveAttribute("aria-current", "page");
		fireEvent.click(remoteSessions[0]);
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: "box-a", projectId: "proj-1", sessionId: "proj-1-1" },
		});
	});

	it("shows the cloud sign-in entry point while signed out", () => {
		cloudSessionState.configured = true;
		renderSidebar();

		const signInControls = screen.getAllByLabelText("Sign in");
		expect(signInControls).toHaveLength(2);
		const activeControl = signInControls.find((control) => control.tabIndex === 0);
		expect(activeControl).toBeDefined();
		fireEvent.click(activeControl!);
		expect(cloudSessionState.signIn).toHaveBeenCalledOnce();
	});

	it("keeps cloud account controls visible while signed in", () => {
		cloudSessionState.configured = true;
		cloudSessionState.status = "authenticated";
		cloudSessionState.session = { user: { email: "user@example.com" } };
		renderSidebar();

		expect(screen.getAllByLabelText("Signed in as user@example.com")).toHaveLength(2);
	});

	it("shows only the name in the footer row and the email in the account menu", async () => {
		const user = userEvent.setup();
		cloudSessionState.configured = true;
		cloudSessionState.status = "authenticated";
		cloudSessionState.session = { user: { email: "ada@example.com", displayName: "Ada Park" } };
		renderSidebar();

		const account = screen.getAllByLabelText("Signed in as ada@example.com").find((el) => el.tabIndex === 0)!;
		expect(account).toHaveTextContent("Ada Park");
		expect(account).not.toHaveTextContent("ada@example.com");

		await user.click(account);
		const menu = await screen.findByRole("menu");
		expect(within(menu).getByText("Ada Park")).toBeVisible();
		expect(within(menu).getByText("ada@example.com")).toBeVisible();
		await user.click(within(menu).getByRole("menuitem", { name: "Sign out" }));
		expect(cloudSessionState.signOut).toHaveBeenCalledOnce();
	});

	it("opens the account menu from the collapsed rail avatar instead of signing out", async () => {
		const user = userEvent.setup();
		cloudSessionState.configured = true;
		cloudSessionState.status = "authenticated";
		cloudSessionState.session = { user: { email: "user@example.com" } };
		renderSidebar({ initialOpen: false });

		const railAvatar = screen.getAllByLabelText("Signed in as user@example.com").find((el) => el.tabIndex === 0)!;
		await user.click(railAvatar);

		expect(cloudSessionState.signOut).not.toHaveBeenCalled();
		expect(await screen.findByRole("menuitem", { name: "Sign out" })).toBeVisible();
	});

	it("keeps AO account controls visible for remote hosts when Cloud execution is off", () => {
		cloudGateState.cloudEnabled = false;
		cloudSessionState.configured = true;
		cloudSessionState.status = "authenticated";
		cloudSessionState.session = { user: { email: "user@example.com" } };
		renderSidebar();

		expect(screen.getAllByLabelText("Signed in as user@example.com")).toHaveLength(2);
	});

	it("shows AO sign-in for remote hosts when Cloud execution is off", () => {
		cloudGateState.cloudEnabled = false;
		cloudSessionState.configured = true;
		renderSidebar();

		expect(screen.getAllByLabelText("Sign in")).toHaveLength(2);
	});

	it("shows the Artifacts row under Automations with a count of live artifacts", async () => {
		const user = userEvent.setup();
		const withArtifacts: WorkspaceSummary = {
			...workspace,
			sessions: [
				{
					...session,
					outputType: "artifact",
					artifactFiles: [
						{ kind: "html", name: "flow.html", path: "flow.html", size: 1, updatedAt: "2026-10-11T10:00:00Z" },
						{ kind: "markdown", name: "plan.md", path: "plan.md", size: 1, updatedAt: "2026-10-10T10:00:00Z" },
					],
				},
				{
					...session,
					id: "proj-1-2",
					isTerminated: true,
					outputType: "artifact",
					artifactFiles: [{ kind: "markdown", name: "old.md", path: "old.md", size: 1, updatedAt: "2026-10-01T10:00:00Z" }],
				},
			],
		};
		renderSidebar({ workspaces: [withArtifacts] });

		const automations = screen.getByRole("button", { name: "Automations" });
		const artifacts = screen.getByRole("button", { name: "Artifacts" });
		expect(automations.compareDocumentPosition(artifacts) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(artifacts).toHaveTextContent("2");
		expect(artifacts).not.toHaveAttribute("aria-current");

		await user.click(artifacts);
		expect(navigateMock).toHaveBeenCalledWith({ to: "/artifacts" });
	});

	it("omits the Artifacts count when there are none and marks the row active on its route", () => {
		mockLocation.pathname = "/artifacts";
		renderSidebar();

		const artifacts = screen.getByRole("button", { name: "Artifacts" });
		expect(artifacts).toHaveAttribute("aria-current", "page");
		expect(artifacts.textContent).toBe("Artifacts");
	});

	it("navigates home from the brand row", async () => {
		const user = userEvent.setup();
		mockParams.projectId = "proj-1";
		renderSidebar();

		await user.click(screen.getByRole("button", { name: "Go to home" }));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/" });
	});

	it("suppresses focus chrome without removing keyboard focusability", () => {
		renderSidebar();

		expect(document.querySelector('[data-slot="sidebar-container"]')).toHaveClass("sidebar-focusless");
		expect(screen.getAllByRole("button", { name: "Settings" })[0]).toHaveAttribute("tabindex", "0");
	});

	it("keeps the Settings footer flush with the bottom edge", () => {
		renderSidebar();

		const footer = document.querySelector('[data-sidebar="footer"]');
		expect(footer).toHaveClass("!pt-0", "!pb-2");
		expect(footer).not.toHaveClass("border-t");
		expect(footer?.querySelector(".border-t")).toBeNull();
		expect(screen.getAllByRole("button", { name: "Settings" })[0]).toHaveClass("h-9");
		expect(screen.getAllByRole("button", { name: "Mobile" })[0]).toHaveClass("size-9");
		expect(footer?.className).not.toContain("--size-center-panel-bottom-inset");
		expect(footer?.className).not.toContain("--size-center-panel-inset-mac");
	});

	it("keeps only the expanded Settings control keyboard-accessible while expanded", () => {
		renderSidebar();

		const settingsButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-label="Settings"]'));
		const expandedButton = settingsButtons.find((button) => button.textContent?.includes("Settings"));
		const collapsedButton = settingsButtons.find((button) => !button.textContent?.includes("Settings"));

		expect(screen.queryByTestId("awake-button")).not.toBeInTheDocument();
		expect(screen.getByTestId("footer-mobile-menu")).toHaveAttribute("tabindex", "0");
		expect(settingsButtons).toHaveLength(2);
		expect(expandedButton).toHaveAttribute("tabindex", "0");
		expect(expandedButton?.parentElement).not.toHaveAttribute("aria-hidden");
		expect(collapsedButton).toHaveAttribute("tabindex", "-1");
		expect(collapsedButton?.closest('[aria-hidden="true"]')).toBeInTheDocument();
	});

	it("keeps only the collapsed Settings control keyboard-accessible while collapsed", () => {
		renderSidebar({ initialOpen: false });

		const settingsButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-label="Settings"]'));
		const expandedButton = settingsButtons.find((button) => button.textContent?.includes("Settings"));
		const collapsedButton = settingsButtons.find((button) => !button.textContent?.includes("Settings"));

		expect(screen.getByTestId("footer-mobile-menu")).toHaveAttribute("tabindex", "-1");
		expect(settingsButtons).toHaveLength(2);
		expect(expandedButton).toHaveAttribute("tabindex", "-1");
		expect(expandedButton?.closest('[aria-hidden="true"]')).toBeInTheDocument();
		// Hidden from assistive tech, not removed from layout: display: none would drop
		// the footer content while the sidebar slides away and leave its border behind.
		expect(expandedButton?.closest("[hidden]")).toBeNull();
		expect(collapsedButton).toHaveAttribute("tabindex", "0");
		// The sidebar content is off-screen while collapsed: inert + aria-hidden, but the
		// edge rail that reopens it stays live.
		const inertRegion = collapsedButton?.closest("[inert]");
		expect(inertRegion).toHaveAttribute("aria-hidden", "true");
		expect(inertRegion?.closest('[data-slot="sidebar-container"]')).not.toBeNull();
		expect(document.querySelector('[data-sidebar="rail"]')?.closest("[inert]")).toBeNull();
	});

	it("keeps sidebar scrolling functional without a layout gutter", () => {
		renderSidebar();

		const content = document.querySelector('[data-sidebar="content"]');
		expect(content).toHaveClass("overflow-y-auto", "scrollbar-none");
		expect(content).not.toHaveClass("project-sidebar-scrollbar");
		// Section headers stay put: only each section's own list scrolls.
		expect(screen.getByTestId("sidebar-projects-scroller")).not.toContainElement(screen.getByText("Projects"));
	});

	it("opens project settings instead of spawning when no orchestrator agent is configured", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, orchestratorAgent: undefined }] });

		await user.click(screen.getByRole("button", { name: "Spawn Project One orchestrator" }));

		expect(useUiStore.getState().settingsModal).toEqual({ scope: "project", projectId: "proj-1" });
		expect(navigateMock).not.toHaveBeenCalled();
		expect(spawnMock).not.toHaveBeenCalled();
	});

	// The sidebar owns its own openOrchestrator, separate from
	// useProjectOrchestratorAction. Clicking Orchestrator on an orchestrator whose
	// agent exited must resume it, not navigate to the dead terminal.
	it("resumes an exited orchestrator instead of opening its dead terminal", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [exitedOrchestrator] }] });

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		await waitFor(() => expect(resumeOrchestratorMock).toHaveBeenCalledWith("proj-1-orch"));
		expect(spawnMock).not.toHaveBeenCalled();
	});

	it("opens an exited orchestrator at once and reports a failed resume", async () => {
		const user = userEvent.setup();
		const error = new Error("resume request failed");
		useUiStore.getState().clearGlobalToast();
		vi.spyOn(console, "error").mockImplementation(() => {});
		resumeOrchestratorMock.mockRejectedValueOnce(error);
		renderSidebar({ workspaces: [{ ...workspace, sessions: [exitedOrchestrator] }] });

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-orch" },
		});
		await waitFor(() => expect(console.error).toHaveBeenCalledWith("Failed to resume orchestrator:", error));
		expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Resume agent",
			body: "resume request failed",
			tone: "error",
		});
	});

	it("opens an exited orchestrator without resuming during an interface transition", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [exitedOrchestrator] }],
			seed: (client) => {
				client.setQueryData(
					sessionInterfaceTransitionQueryKey(exitedOrchestrator.id),
					sessionInterfaceTransitionStatus(exitedOrchestrator.id),
				);
			},
		});

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		expect(resumeOrchestratorMock).not.toHaveBeenCalled();
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-orch" },
		});
	});

	it("does not spawn from the sidebar while the orchestrator is provisioning", async () => {
		const user = userEvent.setup();
		useUiStore.getState().setProjectProvisioning("proj-1", true);
		try {
			renderSidebar();

			const spawnButton = screen.getByRole("button", { name: "Spawn Project One orchestrator" });
			expect(spawnButton).toBeDisabled();
			await user.click(spawnButton);

			expect(spawnMock).not.toHaveBeenCalled();
			expect(navigateMock).not.toHaveBeenCalled();
		} finally {
			useUiStore.getState().setProjectProvisioning("proj-1", false);
		}
	});

	it("shows a ConfirmDialog and hands the removal to the shell when confirmed", async () => {
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		// The ConfirmDialog renders via Radix Portal — find it by role
		const dialog = await screen.findByRole("dialog", { name: "Remove project" });
		expect(dialog).toBeInTheDocument();
		expect(dialog).toHaveTextContent("Project One");

		await user.click(screen.getByRole("button", { name: "Remove" }));
		// The fixture has no kind, like a row from an older daemon: still a local row.
		await waitFor(() => expect(onRemoveProject).toHaveBeenCalledWith("proj-1", false));
		// The shell leaves the project's page only when it is open (useRemoveProject).
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("shows no Removing… state on the row while the removal runs", async () => {
		// Removal is optimistic: the shell drops the row from the list, so the
		// row never shows progress or an error of its own, even while a slow
		// teardown is still running.
		const onRemoveProject = vi.fn() as RemoveProjectHandler;
		const user = userEvent.setup();
		renderSidebar({ onRemoveProject });

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));
		await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));

		expect(screen.queryByRole("dialog", { name: "Remove project" })).not.toBeInTheDocument();
		expect(onRemoveProject).toHaveBeenCalledTimes(1);
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.queryByText(/Removing/)).not.toBeInTheDocument();
	});

	it("does not remove the project when cancellation is clicked in the ConfirmDialog", async () => {
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		await screen.findByRole("dialog", { name: "Remove project" });
		await user.click(screen.getByRole("button", { name: "Cancel" }));

		// Dialog should close and the handler must not have fired
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Remove project" })).not.toBeInTheDocument());
		expect(onRemoveProject).not.toHaveBeenCalled();
	});

	it("requests a new task for the project from the kebab menu", async () => {
		const user = userEvent.setup();
		renderSidebar();
		const before = useUiStore.getState().newTaskRequest?.nonce ?? 0;

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: /New task/ }));

		const request = useUiStore.getState().newTaskRequest;
		expect(request?.projectId).toBe("proj-1");
		expect(request?.nonce ?? 0).toBeGreaterThan(before);
	});

	it("opens the standalone board from the Scratchpad archived sessions action", () => {
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{
							...session,
							id: "adhoc-archived",
							title: "archived",
							workspaceId: STANDALONE_WORKSPACE_ID,
							workspaceName: "Scratchpad",
							isTerminated: true,
							status: "terminated",
						},
					],
				},
			],
		});

		const archiveAction = screen.getByRole("button", { name: "Archived sessions" }).closest("[data-scratchpad-archive-action]");
		expect(archiveAction).toHaveClass("opacity-0", "scale-[0.8]");
		fireEvent.click(screen.getByRole("button", { name: "Archived sessions" }));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/sessions" });
	});

	it("opens a new ad hoc agent directly from the ad hoc row action", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [],
				},
			],
		});
		const before = useUiStore.getState().newTaskRequest?.nonce ?? 0;

		expect(screen.queryByLabelText("Project actions for Scratchpad")).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Open a new agent" }));

		const request = useUiStore.getState().newTaskRequest;
		expect(request?.projectId).toBe(STANDALONE_WORKSPACE_ID);
		expect(request?.nonce ?? 0).toBeGreaterThan(before);
	});

	it("opens a new ad hoc agent on a remote host from its Scratchpad row", async () => {
		const user = userEvent.setup();
		renderSidebar({
			remoteHosts: [{ hostId: "box-a", label: "Host A", url: "http://box-a:3011", status: "connected" }],
			remoteWorkspaces: [
				{ id: STANDALONE_WORKSPACE_ID, name: "Scratchpad", kind: STANDALONE_PROJECT_KIND, path: "", hostId: "box-a", sessions: [] },
			],
		});
		const row = document.querySelector<HTMLElement>(`li[data-project-id="${STANDALONE_WORKSPACE_ID}"][data-host-id="box-a"]`)!;

		expect(within(row).queryByLabelText("Project actions for Scratchpad · Host A")).not.toBeInTheDocument();
		await user.click(within(row).getByRole("button", { name: "Open a new agent" }));

		expect(useUiStore.getState().newTaskRequest).toMatchObject({ projectId: STANDALONE_WORKSPACE_ID, hostId: "box-a" });
	});

	it("lists ad hoc agents in their own Scratchpad section, not under a project row", () => {
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" },
					],
				},
			],
		});

		// Section header, not a project row.
		expect(screen.getByRole("button", { name: "Scratchpad" })).toBeInTheDocument();
		expect(document.querySelector(`li[data-project-id="${STANDALONE_WORKSPACE_ID}"]`)).toBeNull();
		const section = document.querySelector("[data-scratchpad-section]")!;
		expect(section).toContainElement(screen.getByText("baby"));
		expect(section).toHaveClass("mb-2", "min-h-0");
		expect(screen.queryByRole("button", { name: /show (more|fewer) agents/i })).not.toBeInTheDocument();
		expect(screen.getByTestId("sidebar-projects-scroller")).not.toContainElement(screen.getByText("baby"));
		// Sessions sits above Projects, so standalone agents are found first.
		expect(section.compareDocumentPosition(screen.getByRole("button", { name: "Projects" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	it("optically centers the Project and Scratchpad header add icons", () => {
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [],
				},
			],
		});

		for (const label of ["New project", "Open a new agent"]) {
			// Shared 24px row-action button (Button icon-xs); the icon is centered, no optical nudge.
			expect(screen.getByRole("button", { name: label })).toHaveClass("size-control-sm", "justify-center");
		}
	});

	it("opens an ad hoc agent on its own session route", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" },
					],
				},
			],
		});

		await user.click(screen.getByText("baby"));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "adhoc-1" } });
	});

	it("lists the person's Cloud standalone agents in the Scratchpad, badged, and opens each on its project's route", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" }],
				},
				{
					id: "agent-1",
					name: "Fix the flake",
					kind: "cloud",
					cloudOrgId: "org-1",
					standaloneAgent: true,
					path: "",
					sessions: [{ ...session, id: "cloud-agent", title: "Fix the flake", workspaceId: "agent-1", workspaceName: "Fix the flake", cloud: { orgId: "org-1" } }],
				},
				{
					id: "agent-shared",
					name: "Shared agent",
					kind: "cloud",
					cloudOrgId: "org-2",
					standaloneAgent: true,
					shared: { source: "link" },
					path: "",
					sessions: [{ ...session, id: "shared-agent", title: "Their work", workspaceId: "agent-shared", workspaceName: "Shared agent", cloud: { orgId: "org-2", shared: true } }],
				},
			],
		});

		const section = document.querySelector("[data-scratchpad-section]")!;
		expect(section).toContainElement(screen.getByText("baby"));
		const agentRow = screen.getByText("Fix the flake").closest("[data-session-row]")!;
		expect(section).toContainElement(agentRow as HTMLElement);
		// Only the Cloud agent carries the badge that tells it from a local one.
		expect(within(agentRow as HTMLElement).getByText("Cloud")).toBeInTheDocument();
		expect(within(screen.getByText("baby").closest("[data-session-row]") as HTMLElement).queryByText("Cloud")).toBeNull();
		// It is no project of its own; a standalone agent shared with the person stays with the shared projects.
		expect(document.querySelector('li[data-project-id="agent-1"]')).toBeNull();
		expect(document.querySelector('li[data-project-id="agent-shared"]')).not.toBeNull();
		expect(section).not.toContainElement(screen.queryByText("Their work"));

		await user.click(screen.getByText("Fix the flake"));
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "agent-1", sessionId: "cloud-agent" },
		});
	});

	it("opens a Cloud standalone agent shared with the person on its session, not on a board", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: "agent-shared",
					name: "Shared agent",
					kind: "cloud",
					cloudOrgId: "org-2",
					standaloneAgent: true,
					shared: { source: "link" },
					path: "",
					sessions: [{ ...session, id: "shared-agent", title: "Their work", workspaceId: "agent-shared", workspaceName: "Shared agent", cloud: { orgId: "org-2", shared: true } }],
				},
			],
		});

		const row = document.querySelector('li[data-project-id="agent-shared"]') as HTMLElement;
		await user.click(within(row).getAllByText("Shared agent")[0]!);
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "agent-shared", sessionId: "shared-agent" },
		});
		expect(navigateMock).not.toHaveBeenCalledWith(expect.objectContaining({ to: "/projects/$projectId" }));
	});

	it("offers no new task, board, orchestrator or project settings for a Cloud standalone agent, owned or shared", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: "agent-1",
					name: "Fix the flake",
					kind: "cloud",
					cloudOrgId: "org-1",
					standaloneAgent: true,
					path: "",
					sessions: [{ ...session, id: "cloud-agent", title: "Fix the flake", workspaceId: "agent-1", workspaceName: "Fix the flake", cloud: { orgId: "org-1" } }],
				},
				{
					id: "agent-shared",
					name: "Shared agent",
					kind: "cloud",
					cloudOrgId: "org-2",
					standaloneAgent: true,
					shared: { source: "link" },
					path: "",
					sessions: [{ ...session, id: "shared-agent", title: "Their work", workspaceId: "agent-shared", workspaceName: "Shared agent", cloud: { orgId: "org-2", shared: true } }],
				},
			],
		});

		// The person's own agent is a Scratchpad row, with no project row or project menu.
		expect(screen.queryByLabelText("Project actions for Fix the flake")).toBeNull();

		// A shared one keeps its row, but its right-click menu opens no board and
		// starts no task in it: someone else's agent takes no session but its own.
		const row = document.querySelector('li[data-project-id="agent-shared"]') as HTMLElement;
		fireEvent.contextMenu(within(row).getAllByText("Shared agent")[0]!);
		expect(await screen.findByRole("menuitem", { name: /Pin to top/ })).toBeInTheDocument();
		for (const name of [/New task/, /Open dashboard/, /Project settings/, /Remove project/, /orchestrator/i]) {
			expect(screen.queryByRole("menuitem", { name })).toBeNull();
		}
		await user.keyboard("{Escape}");

		// A regular project still offers a new task and its board.
		await user.click(screen.getByLabelText("Project actions for Project One"));
		expect(await screen.findByRole("menuitem", { name: /New task/ })).toBeInTheDocument();
		await user.click(await screen.findByRole("menuitem", { name: /Open dashboard/ }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
	});

	it("never leaves the Scratchpad an empty header: an archived-only Cloud agent shows the way to its archive", async () => {
		const user = userEvent.setup();
		const agent = (sessions: WorkspaceSession[]): WorkspaceSummary => ({
			id: "agent-1", name: "Fix the flake", kind: "cloud", cloudOrgId: "org-1", standaloneAgent: true, path: "", sessions,
		});
		const agentSession = { ...session, id: "cloud-agent", title: "Fix the flake", workspaceId: "agent-1", workspaceName: "Fix the flake", cloud: { orgId: "org-1" } };

		// Archived, its only session is not a row, but the section keeps one row:
		// the archive, where it can be opened and restored.
		renderSidebar({ workspaces: [workspace, agent([{ ...agentSession, isTerminated: true }])] });
		const section = document.querySelector("[data-scratchpad-section]") as HTMLElement;
		expect(section).not.toBeNull();
		expect(within(section).queryByText("Fix the flake")).toBeNull();
		await user.click(within(section).getByRole("button", { name: "Archived sessions, 1" }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/sessions" });
		cleanup();

		// Active, the session is the row, and there is no archive row.
		renderSidebar({ workspaces: [workspace, agent([agentSession])] });
		const active = document.querySelector("[data-scratchpad-section]") as HTMLElement;
		expect(within(active).getByText("Fix the flake")).toBeInTheDocument();
		expect(within(active).queryByRole("button", { name: /^Archived sessions, / })).toBeNull();
		cleanup();

		// A local Scratchpad still shows its sessions beside an archived agent.
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" }],
				},
				agent([{ ...agentSession, isTerminated: true }]),
			],
		});
		const local = document.querySelector("[data-scratchpad-section]") as HTMLElement;
		expect(local).toContainElement(screen.getByText("baby"));
		expect(within(local).queryByRole("button", { name: /^Archived sessions, / })).toBeNull();
	});

	it("shows the Sessions section with its + before any standalone session exists", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				workspace,
				{ id: "agent-refused", name: "Refused start", kind: "cloud", cloudOrgId: "org-1", standaloneAgent: true, path: "", sessions: [] },
			],
		});

		const section = document.querySelector("[data-scratchpad-section]") as HTMLElement;
		expect(within(section).getByRole("button", { name: "Sessions" })).toBeInTheDocument();
		expect(within(section).queryByText("Refused start")).toBeNull();
		expect(document.querySelector('li[data-project-id="agent-refused"]')).toBeNull();
		await user.click(within(section).getByRole("button", { name: "Open a new agent" }));
		expect(useUiStore.getState().newTaskRequest?.projectId).toBe(STANDALONE_WORKSPACE_ID);
	});

	it("collapses the Scratchpad section from its header", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" },
					],
				},
			],
		});

		await user.click(screen.getByRole("button", { name: "Scratchpad" }));
		expect(screen.queryByText("baby")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Scratchpad" }));
		expect(screen.getByText("baby")).toBeInTheDocument();
	});

	it("saves the Scratchpad section state and starts closed when it was left closed", async () => {
		const user = userEvent.setup();
		const workspaces = [
			{
				id: STANDALONE_WORKSPACE_ID,
				name: "Scratchpad",
				kind: STANDALONE_PROJECT_KIND,
				path: "",
				sessions: [
					{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" },
				],
			},
		];
		renderSidebar({ workspaces });
		await user.click(screen.getByRole("button", { name: "Scratchpad" }));
		const saved = window.localStorage.getItem("ao.sidebar.sections");
		expect(JSON.parse(saved ?? "null")).toMatchObject({ scratchpad: false });
		cleanup();

		window.localStorage.setItem("ao.sidebar.sections", saved ?? "");
		renderSidebar({ workspaces });
		expect(screen.queryByText("baby")).not.toBeInTheDocument();
	});

	it("caps the ad hoc agent list at 5 and lets the section grow to half the available height", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: Array.from({ length: 13 }, (_, index) => ({
						...session,
						id: `adhoc-${index + 1}`,
						title: `Agent ${index + 1}`,
						workspaceId: STANDALONE_WORKSPACE_ID,
						workspaceName: "Scratchpad",
						// Descending so sortedWorkerSessions keeps the fixture order.
						updatedAt: `2026-06-${30 - index}T00:00:00Z`,
					})),
				},
			],
		});

		expect(screen.getByText("Agent 5")).toBeInTheDocument();
		expect(screen.queryByText("Agent 6")).not.toBeInTheDocument();

		const scroller = screen.getByTestId("sidebar-scratchpad-scroller");
		expect(scroller).toHaveClass("overflow-y-auto");
		expect(scroller.style.height).toBe("");
		expect(scroller.style.maxHeight).toBe("");
		expect(document.querySelector<HTMLElement>("[data-scratchpad-section]")!.style.maxHeight).toBe("");

		await user.click(screen.getByRole("button", { name: "Show 8 more agents" }));

		expect(screen.getByText("Agent 6")).toBeInTheDocument();
		expect(screen.getByText("Agent 13")).toBeInTheDocument();
		expect(scroller.style.maxHeight).toBe("");
		expect(screen.getByRole("button", { name: "Show fewer agents" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show fewer agents" }));

		expect(screen.queryByText("Agent 6")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 8 more agents" })).toBeInTheDocument();
	});

	it("fades the scroll edges of project and Scratchpad sections as they scroll", () => {
		renderSidebar({
			workspaces: [
				...Array.from({ length: 2 }, (_, index) => ({
					...workspace,
					id: `proj-${index + 1}`,
					name: `Project ${index + 1}`,
				})),
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" }],
				},
			],
		});

		for (const testId of ["sidebar-projects-scroller", "sidebar-scratchpad-scroller"]) {
			const scroller = screen.getByTestId(testId);
			Object.defineProperties(scroller, {
				scrollHeight: { configurable: true, value: 200 },
				clientHeight: { configurable: true, value: 100 },
				scrollTop: { configurable: true, writable: true, value: 0 },
			});

			fireEvent.scroll(scroller);
			expect(scroller).toHaveAttribute("data-fade-end");
			expect(scroller).not.toHaveAttribute("data-fade-start");

			scroller.scrollTop = 50;
			fireEvent.scroll(scroller);
			expect(scroller).toHaveAttribute("data-fade-start");
			expect(scroller).toHaveAttribute("data-fade-end");

			scroller.scrollTop = 100;
			fireEvent.scroll(scroller);
			expect(scroller).toHaveAttribute("data-fade-start");
			expect(scroller).not.toHaveAttribute("data-fade-end");
		}
	});

	it("offers ad hoc agent creation from the project add flow before the ad hoc row exists", async () => {
		const user = userEvent.setup();
		renderSidebar();
		const before = useUiStore.getState().newTaskRequest?.nonce ?? 0;

		await openLocalProjectSources(user);
		await user.click(await screen.findByRole("button", { name: "Start a standalone agent" }));

		const request = useUiStore.getState().newTaskRequest;
		expect(request?.projectId).toBe(STANDALONE_WORKSPACE_ID);
		expect(request?.nonce ?? 0).toBeGreaterThan(before);
	});

	it("opens the create-project flow when the no-project shortcut signal arrives", async () => {
		renderSidebar();

		act(() => {
			useUiStore.getState().requestCreateProject();
		});

		expect(await screen.findByRole("dialog", { name: "New project" })).toBeInTheDocument();
	});

	it("keeps the create-project shortcut available when there are no projects", async () => {
		renderSidebar({ workspaces: [] });

		act(() => {
			useUiStore.getState().requestCreateProject();
		});

		expect(await screen.findByRole("dialog", { name: "New project" })).toBeInTheDocument();
	});

	it("reveals orchestrator and kebab buttons on the project row (no dashboard button)", () => {
		renderSidebar();

		expect(screen.queryByLabelText("Open Project One dashboard")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Spawn Project One orchestrator")).toBeInTheDocument();
		expect(screen.getByLabelText("Project actions for Project One")).toBeInTheDocument();
	});

	it("keeps project row actions in the keyboard tab order without a duplicate disclosure stop", () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const disclosure = screen.getByLabelText("Toggle Project One sessions");
		expect(disclosure.tagName).toBe("BUTTON");
		// The overlay folder button duplicates the project row's tab stop.
		expect(disclosure).toHaveProperty("tabIndex", -1);
		expect(screen.getByLabelText("Spawn Project One orchestrator")).toHaveProperty("tabIndex", 0);
		expect(screen.getByLabelText("Project actions for Project One")).toHaveProperty("tabIndex", 0);
		expect(screen.getByLabelText("Pin session")).toHaveProperty("tabIndex", 0);
		expect(screen.queryByRole("button", { name: "Rename fix login" })).not.toBeInTheDocument();
		expect(screen.getByLabelText("Archive session")).toHaveProperty("tabIndex", 0);
	});

	it("fades the message age out in favor of the overlaid hover actions", () => {
		const lastUserMessageAt = "2026-06-29T23:55:00Z";
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [{ ...session, lastUserMessageAt }] }],
		});

		const openSession = screen.getByLabelText("Open fix login");
		const label = within(openSession).getByText("fix login");
		const actions = screen.getByLabelText("Pin session").closest("[data-session-actions]");
		const actionButtons = screen.getByLabelText("Pin session").parentElement;
		const time = actions?.querySelector("time");

		expect(openSession).toHaveClass("pr-[36px]");
		expect(openSession).toHaveClass(
			"group-hover/session-row:pr-sidebar-project-actions",
			"group-has-[:focus-visible]/session-row:pr-sidebar-project-actions",
		);
		expect(label).toHaveClass("min-w-0", "flex-1", "truncate");
		expect(actions).toHaveAttribute("data-session-actions");
		expect(actionButtons).toHaveClass(
			"absolute",
			"right-1",
			"gap-0.5",
			"opacity-0",
			"scale-[0.8]",
			"duration-normal",
			"group-has-[:focus-visible]/session-row:pointer-events-auto",
			"group-has-[:focus-visible]/session-row:scale-100",
			"group-has-[:focus-visible]/session-row:opacity-100",
		);
		expect(actionButtons).toHaveAttribute("data-session-action-buttons", "");
		expect(time).toHaveAttribute("datetime", lastUserMessageAt);
		expect(time).toHaveClass(
			"absolute",
			"right-2",
			"opacity-100",
			"group-has-[:focus-visible]/session-row:opacity-0",
		);
		expect(openSession).toHaveClass("pl-1.5");
		expect(openSession.closest("li")).toHaveClass("pl-0.5");
	});

	it("keeps session status and actions stable when an action receives keyboard focus", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [{ ...session, status: "needs_input" }] }] });

		const openSession = screen.getByLabelText("Open fix login");
		const row = openSession.closest<HTMLElement>("[data-session-row]");
		const status = openSession.querySelector("[data-session-status]");

		if (!row) throw new Error("Session row not found");
		expect(status).toBeInTheDocument();
		openSession.focus();
		await user.tab();

		expect(screen.getByLabelText("Pin session")).toHaveFocus();
		expect(row).toContainElement(openSession);
		expect(row).toContainElement(status as HTMLElement);
		expect(row).toContainElement(screen.getByLabelText("Pin session"));
	});

	it("toggles project sessions from the folder icon without selecting the project first", async () => {
		const user = userEvent.setup();
		const other: WorkspaceSummary = {
			id: "proj-2",
			name: "Project Two",
			path: "/repo/project-two",
			orchestratorAgent: "claude-code",
			sessions: [{ ...session, id: "proj-2-1", workspaceId: "proj-2", workspaceName: "Project Two", title: "other task" }],
		};
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [session] }, other],
		});

		expect(screen.getByText("fix login")).toBeInTheDocument();
		expect(screen.getByText("other task")).toBeInTheDocument();

		const folder = screen.getByLabelText("Toggle Project Two sessions");
		expect(folder).toBeTruthy();
		await user.click(folder);

		expect(screen.queryByText("other task")).not.toBeInTheDocument();
		expect(screen.getByText("fix login")).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});


	it("uses deliberate interaction recency for both worker order and age", () => {
		const at = "2026-07-05T00:00:00Z";
		const directed = {
			...session, id: "directed", title: "orchestrator directed",
			lastUserMessageAt: "2026-07-01T00:00:00Z", lastInteractionAt: at,
		};
		const human = {
			...session, id: "human", title: "human directed",
			lastUserMessageAt: "2026-07-03T00:00:00Z",
		};
		renderSidebar({ workspaces: [{ ...workspace, sessions: [human, directed] }] });
		const directedButton = screen.getByLabelText("Open orchestrator directed");
		const humanButton = screen.getByLabelText("Open human directed");
		expect(directedButton.compareDocumentPosition(humanButton) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(document.querySelector(`time[datetime="${at}"]`)).toHaveAttribute("datetime", at);
	});

	it("lists worker sessions by the user's last message, matching the row's message age", () => {
		// Agent activity bumps updatedAt, but must not reorder rows past the shown age.
		const busyAgent: WorkspaceSession = {
			...session,
			id: "proj-1-busy",
			title: "busy agent",
			createdAt: "2026-06-29T00:00:00Z",
			lastUserMessageAt: "2026-07-01T00:00:00Z",
			updatedAt: "2026-07-05T00:00:00Z",
		};
		const recentlyMessaged: WorkspaceSession = {
			...session,
			id: "proj-1-messaged",
			title: "recently messaged",
			createdAt: "2026-06-29T00:00:00Z",
			lastUserMessageAt: "2026-07-03T00:00:00Z",
			updatedAt: "2026-07-03T00:00:00Z",
		};
		const neverMessaged: WorkspaceSession = {
			...session,
			id: "proj-1-never-messaged",
			title: "never messaged",
			createdAt: "2026-07-02T00:00:00Z",
			updatedAt: "2026-07-06T00:00:00Z",
		};
		const invalidMessage: WorkspaceSession = {
			...session,
			id: "proj-1-invalid-message",
			title: "invalid message",
			createdAt: "2026-06-28T00:00:00Z",
			lastUserMessageAt: "not-a-timestamp",
			updatedAt: "2026-07-07T00:00:00Z",
		};
		const tieOlderUpdate: WorkspaceSession = {
			...session,
			id: "proj-1-tie-older",
			title: "tie older update",
			createdAt: "2026-06-27T00:00:00Z",
			updatedAt: "2026-06-27T00:00:00Z",
		};
		const tieNewerUpdate: WorkspaceSession = {
			...session,
			id: "proj-1-tie-newer",
			title: "tie newer update",
			createdAt: "2026-06-27T00:00:00Z",
			updatedAt: "2026-06-28T00:00:00Z",
		};
		renderSidebar({
			workspaces: [{
				...workspace,
				sessions: [busyAgent, tieOlderUpdate, recentlyMessaged, invalidMessage, neverMessaged, tieNewerUpdate],
			}],
		});

		const sessionButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('[data-session-row] button[aria-label^="Open "]'));
		expect(sessionButtons.map((button) => button.getAttribute("aria-label"))).toEqual([
			"Open recently messaged",
			"Open never messaged",
			"Open busy agent",
			"Open invalid message",
			"Open tie newer update",
			"Open tie older update",
		]);
	});

	it("navigates to the project board when a project with worker sessions is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		// Click the project name text — it's inside SidebarMenuButton and bubbles up to onProjectClick.
		await user.click(screen.getByText("Project One"));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
	});

	it("opens the board, not the orchestrator, when an empty project row is clicked", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [] }] });
		await userEvent.click(screen.getByText("Project One"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
		expect(spawnMock).not.toHaveBeenCalled();
	});

	it.each([false, true])("opens an empty project's orchestrator from its button (existing: %s)", async (existing) => {
		const orchestrator: WorkspaceSession = { ...session, id: "proj-1-orc", kind: "orchestrator", title: "Orchestrator" };
		spawnMock.mockResolvedValue(orchestrator.id);
		renderSidebar({ workspaces: [{ ...workspace, sessions: existing ? [orchestrator] : [] }] });
		await userEvent.click(screen.getByRole("button", { name: existing ? "Open Project One orchestrator" : "Spawn Project One orchestrator" }));
		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: orchestrator.id },
		}));
		expect(spawnMock).toHaveBeenCalledTimes(existing ? 0 : 1);
	});

	it("lists a shared project's orchestrator for its guest and offers none of the owner's actions", async () => {
		const orchestrator: WorkspaceSession = { ...session, id: "shared-orc", workspaceId: "proj-1", kind: "orchestrator", title: "Shared orchestrator" };
		const shared: WorkspaceSummary = { ...workspace, kind: "cloud", cloudOrgId: "org-elsewhere", shared: { byName: "Alice" }, sessions: [orchestrator] };
		renderSidebar({ workspaces: [shared], expandedProjectIds: ["proj-1"] });
		await userEvent.click(await screen.findByText("Shared orchestrator"));
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "shared-orc" },
		});
		expect(screen.queryByRole("button", { name: "Project actions for Project One" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));
		expect(spawnMock).not.toHaveBeenCalled();
	});

	it("never offers a guest to start an orchestrator in a shared project", () => {
		const shared: WorkspaceSummary = { ...workspace, kind: "cloud", cloudOrgId: "org-elsewhere", shared: {}, sessions: [session] };
		renderSidebar({ workspaces: [shared], expandedProjectIds: ["proj-1"] });
		expect(screen.queryByRole("button", { name: "Spawn Project One orchestrator" })).toBeNull();
		expect(screen.getByText("fix login")).toBeTruthy();
	});

	it("offers a guest no Archive on a session shared with them, and keeps it on the owner's own", async () => {
		const sharedSession: WorkspaceSession = { ...session, cloud: { orgId: "org-elsewhere", shared: true } };
		const shared: WorkspaceSummary = { ...workspace, kind: "cloud", cloudOrgId: "org-elsewhere", shared: {}, sessions: [sharedSession] };
		renderSidebar({ workspaces: [shared], expandedProjectIds: ["proj-1"] });
		expect(screen.getByText("fix login")).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Archive session" })).toBeNull();
		cleanup();
		renderSidebar({ workspaces: [{ ...workspace, kind: "cloud", cloudOrgId: "org-mine", sessions: [{ ...session, cloud: { orgId: "org-mine" } }] }], expandedProjectIds: ["proj-1"] });
		expect(screen.getByRole("button", { name: "Archive session" })).toBeTruthy();
	});

	it("keeps the owner's project actions on a project of their own", () => {
		renderSidebar({ workspaces: [{ ...workspace, kind: "cloud", cloudOrgId: "org-mine", sessions: [session] }], expandedProjectIds: ["proj-1"] });
		expect(screen.getByRole("button", { name: "Project actions for Project One" })).toBeTruthy();
		expect(screen.getByRole("button", { name: "Spawn Project One orchestrator" })).toBeTruthy();
	});

	it("returns to the project board from an orchestrator session without collapsing", async () => {
		const user = userEvent.setup();
		const orchestrator: WorkspaceSession = {
			...session,
			id: "proj-1-orc",
			title: "Orchestrator",
			kind: "orchestrator",
		};
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-orc";
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [orchestrator, session] }],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();

		await user.click(screen.getByText("Project One"));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "true");
	});

	it("collapses an expanded project when its board is already active", async () => {
		const user = userEvent.setup();
		mockParams.projectId = "proj-1";
		mockParams.sessionId = undefined;
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [session] }],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();

		await user.click(screen.getByText("Project One"));

		expect(navigateMock).not.toHaveBeenCalled();
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "false");
	});

	it("expands a collapsed project when opening its orchestrator", async () => {
		const user = userEvent.setup();
		const orchestrator: WorkspaceSession = {
			...session,
			id: "proj-1-orc",
			title: "Orchestrator",
			kind: "orchestrator",
		};
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [orchestrator, session] }],
		});

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "false");

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-orc" },
		});
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "true");
	});

	it("defaults worker and orchestrator agents when creating a project", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		renderSidebar({ onCreateProject });

		await openLocalProjectSources(user);
		expect(screen.getByRole("dialog", { name: "New project" })).toBeInTheDocument();
		expect(window.ao!.app.chooseDirectory).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(window.ao!.app.chooseDirectory).toHaveBeenCalledWith("Choose a project repository");
		const dialog = screen.getByRole("dialog", { name: "Set up project" });
		expect(dialog).toHaveClass("left-1/2", "top-1/2", "-translate-x-1/2", "-translate-y-1/2");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(
				expect.objectContaining({
					path: "/repo/new-project",
					workerAgent: "claude-code",
					orchestratorAgent: "claude-code",
				}),
			),
		);
	});

	it("in Developer Mode, starts a new project on the most preferred agent for every role", async () => {
		useUiStore.setState({ developerMode: true });
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		renderSidebar({ onCreateProject });

		await openLocalProjectSources(user);
		expect(screen.getByRole("dialog", { name: "New project" })).toBeInTheDocument();
		expect(window.ao!.app.chooseDirectory).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(window.ao!.app.chooseDirectory).toHaveBeenCalledWith("Choose a project repository");
		const dialog = screen.getByRole("dialog", { name: "Set up project" });
		expect(dialog).toHaveClass("left-1/2", "top-1/2", "-translate-x-1/2", "-translate-y-1/2");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(
				expect.objectContaining({
					path: "/repo/new-project",
					workerAgent: "claude-code",
					orchestratorAgent: "claude-code",
					reviewerAgent: "claude-code",
				}),
			),
		);
	});

	it("opens an already registered project before agent setup", async () => {
		const user = userEvent.setup();
		useUiStore.getState().clearGlobalToast();
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/project-one/");
		renderSidebar();

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));

		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-1" },
		}));
		expect(screen.queryByRole("dialog", { name: "Set up project" })).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalledWith("/api/v1/imports/validate", expect.anything());
		expect(useUiStore.getState().globalToasts).toHaveLength(1);
		expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Project already added",
			body: "Opened the registered project for this folder.",
		});
	});

	it("clones a Git URL into the selected folder before starting agents", async () => {
		const user = userEvent.setup();
		const onCloneProject = vi.fn().mockResolvedValue(undefined) as CloneProjectHandler;
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo");
		renderSidebar({ onCloneProject, onCreateProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: "Clone a repo" }));
		expect(await screen.findByRole("dialog", { name: "Clone a Git repository" })).toBeInTheDocument();

		await user.type(
			await screen.findByRole("textbox", { name: "Repository URL" }),
			"git@github.com:acme/web-app.git",
		);
		await user.click(screen.getByRole("button", { name: "Choose where to clone the repository" }));
		expect(window.ao!.app.chooseDirectory).toHaveBeenCalledWith({ title: "Choose where to clone the repository", defaultPath: "~/ao/projects" });
		await waitFor(() => expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled());
		await user.click(screen.getByRole("button", { name: "Continue" }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledWith(expect.objectContaining({
			path: "/repo/web-app",
			clonePreparationId: "prep-web-app",
			workerAgent: "claude-code",
			orchestratorAgent: "claude-code",
		})));
		expect(onCloneProject).not.toHaveBeenCalled();
	});

	it("creates the selected local repository after backing out of a clone", async () => {
		const user = userEvent.setup();
		const onCloneProject = vi.fn().mockResolvedValue(undefined) as CloneProjectHandler;
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi
			.fn()
			.mockResolvedValueOnce("/repo")
			.mockResolvedValueOnce("/repo/local-project");
		window.ao!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/local-project",
			repos: [
				{
					name: "local-project",
					path: "/repo/local-project",
					relativePath: ".",
					branch: "main",
					remote: "origin",
					hasRemote: true,
					status: "ok",
				},
			],
		});
		renderSidebar({ onCloneProject, onCreateProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: "Clone a repo" }));
		await user.type(
			await screen.findByRole("textbox", { name: "Repository URL" }),
			"git@github.com:acme/web-app.git",
		);
		await user.click(screen.getByRole("button", { name: "Choose where to clone the repository" }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled());
		await user.click(await screen.findByRole("button", { name: "Continue" }));

		await user.click(await screen.findByRole("button", { name: "Back to clone details" }));
		await user.click(await screen.findByRole("button", { name: "Back to code source" }));
		await user.click(await screen.findByRole("button", { name: /^Open a local folder$/i }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(
				expect.objectContaining({
					path: "/repo/local-project",
					workerAgent: "claude-code",
					orchestratorAgent: "claude-code",
				}),
			),
		);
		expect(onCloneProject).not.toHaveBeenCalled();
	});

	it("prioritizes authorized project agents by preferred agent order", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		mockAgentReadinessResponse({
			data: {
				agents: [
					agentReadiness("goose", "Goose"),
					agentReadiness("devin", "Devin"),
					agentReadiness("aider", "Aider"),
					agentReadiness("opencode", "OpenCode"),
					agentReadiness("cursor", "Cursor"),
				],
			},
			error: undefined,
		});
		renderSidebar({ onCreateProject, seedAgents: false });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Worker agent" })).toHaveTextContent(/cursor/i);
		expect(screen.getByRole("combobox", { name: "Orchestrator agent" })).toHaveTextContent(/cursor/i);

		await user.click(screen.getByRole("combobox", { name: "Worker agent" }));
		expect((await screen.findAllByRole("option")).map((option) => option.textContent)).toEqual([
			"Cursor",
			"OpenCode",
			"Aider",
			"Devin",
			"Goose",
			"Manage agents…",
		]);
		await user.keyboard("{Escape}");

		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(
				expect.objectContaining({
					workerAgent: "cursor",
					orchestratorAgent: "cursor",
				}),
			),
		);
	});

	it("in Developer Mode, starts on the preferred agent and the best unattended-safe reviewer", async () => {
		useUiStore.setState({ developerMode: true });
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		mockAgentReadinessResponse({
			data: {
				agents: [
					agentReadiness("goose", "Goose"),
					agentReadiness("devin", "Devin"),
					agentReadiness("aider", "Aider"),
					agentReadiness("opencode", "OpenCode"),
					agentReadiness("cursor", "Cursor"),
				],
			},
			error: undefined,
		});
		renderSidebar({ onCreateProject, seedAgents: false });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(screen.queryByRole("combobox", { name: "Worker agent" })).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Create and start" }));
		// Cursor leads AO's priority among these but is not an unattended-safe
		// reviewer; OpenCode is the best one installed, so it reviews.
		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(
				expect.objectContaining({
					workerAgent: "cursor",
					orchestratorAgent: "cursor",
					reviewerAgent: "opencode",
				}),
			),
		);
	});

	it("opens the agent sheet after project validation", async () => {
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/new-project", { path: "/repo/new-project", repos: [] });

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(onInitializeProject).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
	});

	it("opens agent setup for a validated project folder nested inside a parent repo", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/parent/universe");
		window.ao!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/parent/universe",
			repos: [],
			setupWarning:
				"Selected folder is inside an existing Git repository at /repo/parent. Orchestrator will initialize this folder as a separate repository.",
		});
		renderSidebar({ onCreateProject, onInitializeProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(onInitializeProject).not.toHaveBeenCalled();
		expect(onCreateProject).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
	});

	it("shows repository initialization recovery for git repos with no commits", async () => {
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/unborn", {
			path: "/repo/unborn",
			repos: [
				{
					name: "unborn",
					path: "/repo/unborn",
					relativePath: ".",
					branch: "HEAD",
					remote: "",
					hasRemote: false,
					status: "error",
					reason: "Repository must have at least one commit.",
				},
			],
		});
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
	});

	it("does not initialize Git when the project creation is cancelled", async () => {
		const onCreateProject = vi
			.fn()
			.mockRejectedValueOnce(
				codedError("This folder is not a Git repository.", "NOT_A_GIT_REPO"),
			) as unknown as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/new-project", { path: "/repo/new-project", repos: [] });
	await user.click(screen.getByRole("button", { name: "Close project agents dialog" }));
		expect(onInitializeProject).not.toHaveBeenCalled();
		expect(screen.queryByRole("dialog", { name: "Set up project" })).not.toBeInTheDocument();
	});

	it("does not initialize Git a second time after validation", async () => {
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockRejectedValue(new Error("git init failed")) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/new-project", { path: "/repo/new-project", repos: [] });
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
		expect(onInitializeProject).not.toHaveBeenCalled();
	});

	it("can create a workspace project from the project add flow", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		renderSidebar({ onCreateProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));

		expect(window.ao!.app.chooseDirectory).toHaveBeenCalledWith("Choose a workspace folder");
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/workspace",
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
				asWorkspace: true,
			}),
		);
	});

	it("does not run single-repo Git setup recovery for workspace imports", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi
			.fn()
			.mockRejectedValueOnce(
				codedError("This folder is not a Git repository.", "NOT_A_GIT_REPO"),
			) as unknown as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.ao!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		renderSidebar({ onCreateProject, onInitializeProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
		expect(onInitializeProject).not.toHaveBeenCalled();
		await waitFor(() => expect(useUiStore.getState().globalToast?.body).toBe("This folder is not a Git repository."));
		expect(screen.queryByText(/Import failed · workspace not registered/i)).not.toBeInTheDocument();
		expect(window.ao!.app.checkAncestorRepo).toHaveBeenCalledWith("/repo/workspace");
		expect(window.ao!.app.scanImportFolder).toHaveBeenCalledTimes(2);
	});

	it("shows detected repository validation when workspace import fails", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("workspace not registered")) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/Users/test/dev/acme");
		window.ao!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/Users/test/dev/acme", {
						childRepos: [repoStatus("/Users/test/dev/acme/api", { isRepo: true, hasCommit: true, hasOrigin: true })],
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		window.ao!.app.scanImportFolder = vi.fn().mockResolvedValueOnce({
			path: "/Users/test/dev/acme",
			repos: [],
		}).mockResolvedValueOnce({
			path: "/Users/test/dev/acme",
			repos: [
				{
					name: "web",
					path: "/Users/test/dev/acme/web",
					relativePath: "web",
					branch: "HEAD",
					remote: "",
					hasRemote: false,
					status: "error",
					reason: "Repository name is reserved by Orchestrator.",
				},
				{
					name: "api",
					path: "/Users/test/dev/acme/api",
					relativePath: "api",
					branch: "main",
					remote: "git@github.com:acme/api.git",
					hasRemote: true,
					status: "ok",
				},
			],
		});
		renderSidebar({ onCreateProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(useUiStore.getState().globalToast?.body).toBe("workspace not registered"));
		expect(screen.queryByText(/Import failed · workspace not registered/i)).not.toBeInTheDocument();
		expect(screen.queryByText("workspace not registered")).not.toBeInTheDocument();
		expect(window.ao!.app.checkAncestorRepo).toHaveBeenCalledWith("/Users/test/dev/acme");
		expect(window.ao!.app.scanImportFolder).toHaveBeenCalledTimes(2);
	});

	it("blocks workspace import when no child repository is initialized", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("workspace not registered")) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.ao!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/repo/workspace", {
						isValid: false,
						blockingErrors: ["WORKSPACE_CHILD_REPO_REQUIRED"],
						nextStep: "error",
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		renderSidebar({ onCreateProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		expect(screen.getByText("Importing a workspace requires at least one direct child Git repository that already has a commit and an origin remote. You can import this folder as a project instead.")).toBeInTheDocument();
		expect(screen.queryByText("No repositories detected in this folder.")).not.toBeInTheDocument();
		expect(screen.queryByText("/repo/workspace")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Continue" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Import as project" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Go Back" }));
		expect(screen.getByRole("dialog", { name: "New project" })).toBeInTheDocument();
		expect(onCreateProject).not.toHaveBeenCalled();
	});

	it("imports a workspace without initialized repositories that need fixes", async () => {
		const user = userEvent.setup();
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.ao!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/repo/workspace", {
						childRepos: [
							repoStatus("/repo/workspace/api", { isRepo: true, hasCommit: true, hasOrigin: true }),
							repoStatus("/repo/workspace/unborn", { isRepo: true, requiredActions: ["git_commit", "set_remote"] }),
							repoStatus("/repo/workspace/no-remote", { isRepo: true, hasCommit: true, requiredActions: ["set_remote"] }),
						],
						nextStep: "prepare_git",
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		window.ao!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/workspace",
			repos: [
				{ name: "api", path: "/repo/workspace/api", relativePath: "api", branch: "main", remote: "origin", hasRemote: true, status: "ok" },
				{ name: "unborn", path: "/repo/workspace/unborn", relativePath: "unborn", branch: "", remote: "", hasRemote: false, status: "ok", needsGitInit: true },
				{ name: "no-remote", path: "/repo/workspace/no-remote", relativePath: "no-remote", branch: "main", remote: "", hasRemote: false, status: "ok", needsGitInit: true },
			],
		});
		renderSidebar({ onCreateProject: vi.fn().mockResolvedValue(undefined) as CreateProjectHandler });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });

		expect(screen.getByText("unborn")).toBeInTheDocument();
		expect(screen.queryByText("Set an origin remote for the child repositories marked below before importing this workspace.")).not.toBeInTheDocument();
		expect(screen.getByText("The 2 repositories marked below have no commit or origin remote yet. Orchestrator will import this workspace without them, and agents will not work in them.")).toBeInTheDocument();
		expect(screen.getByText("Git setup needed · Not included")).toBeInTheDocument();
		expect(screen.getByText("No remote · Not included")).toBeInTheDocument();
		expect(screen.queryByRole("dialog", { name: "Prepare project" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: /Set up|Hide setup/i })).not.toBeInTheDocument();
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
	});

	it("blocks workspace repositories until their remotes are configured", async () => {
		const user = userEvent.setup();
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.ao!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/repo/workspace", {
						childRepos: [repoStatus("/repo/workspace/temp", { isRepo: true, hasCommit: true, requiredActions: ["set_remote"] })],
						nextStep: "prepare_git",
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		window.ao!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/workspace",
			repos: [{ name: "temp", path: "/repo/workspace/temp", relativePath: "temp", branch: "main", remote: "", hasRemote: false, isRepo: true, hasCommit: true, status: "ok", needsGitInit: false }],
		});
		renderSidebar({ onCreateProject: vi.fn().mockResolvedValue(undefined) as CreateProjectHandler });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		expect(screen.getByRole("dialog", { name: "Import workspace" })).toBeInTheDocument();
		expect(screen.getByText("temp")).toBeInTheDocument();
		expect(screen.getByText("Set an origin remote for the child repositories marked below before importing this workspace.")).toBeInTheDocument();
		expect(screen.queryByRole("textbox", { name: "Origin remote URL" })).not.toBeInTheDocument();
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Set up|Hide setup/i })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
	});

	it("offers project import when all workspace children are plain folders", async () => {
		const user = userEvent.setup();
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.ao!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockResolvedValue({
				data: importValidation("/repo/workspace", {
					isValid: false,
					blockingErrors: ["WORKSPACE_CHILD_REPO_REQUIRED"],
					root: repoStatus("/repo/workspace", { needsGitInit: true, requiredActions: ["git_init", "git_commit", "set_remote"] }),
					nextStep: "error",
				}),
				error: undefined,
		});
		window.ao!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/workspace",
			repos: [
				{ name: "app", path: "/repo/workspace/app", relativePath: "app", branch: "", remote: "", hasRemote: false, isRepo: false, hasCommit: false, status: "ok", needsGitInit: true },
				{ name: "docs", path: "/repo/workspace/docs", relativePath: "docs", branch: "", remote: "", hasRemote: false, isRepo: false, hasCommit: false, status: "ok", needsGitInit: true },
			],
		});
		renderSidebar({ onCreateProject: vi.fn().mockResolvedValue(undefined) as CreateProjectHandler });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		expect(screen.queryByRole("button", { name: "Continue" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Import as project" })).toBeInTheDocument();
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
	});

	it("does not rescan folders for non-validation create failures", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("Orchestrator daemon is not ready.")) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.ao!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		renderSidebar({ onCreateProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Project setup failed",
			body: "Orchestrator daemon is not ready.",
		}));
		expect(screen.getByRole("dialog", { name: "New project" })).toBeInTheDocument();
		// The initial folder validation is required by the import step. The
		// non-validation create failure must not trigger a second scan.
		expect(window.ao!.app.checkAncestorRepo).toHaveBeenCalledWith("/repo/workspace");
		expect(window.ao!.app.scanImportFolder).toHaveBeenCalledTimes(1);
	});

	it("shows ancestor repo warning in agent sheet for workspace inside existing repo", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue({
			data: { project: { id: "ws-1", name: "My Workspace", kind: "workspace", path: "/repo/inner" } },
			error: null,
		}) as unknown as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/inner");
		window.ao!.app.checkAncestorRepo = vi
			.fn()
			.mockResolvedValue(
				"Selected folder is inside an existing Git repository at /repo. Orchestrator will initialize this folder as a separate repository.",
			);
		renderSidebar({ onCreateProject, onInitializeProject });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		expect(
			screen.getByText(
				"Selected folder is inside an existing Git repository at /repo. Orchestrator will initialize this folder as a separate repository.",
			),
		).toBeInTheDocument();
		expect(
			screen.getByText(
				"If this folder needs Git setup, Orchestrator will initialize it and create the first commit before starting.",
			),
		).toBeInTheDocument();
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
		expect(onCreateProject).toHaveBeenCalledWith(
			expect.objectContaining({ path: "/repo/inner", asWorkspace: true }),
		);
		expect(onInitializeProject).not.toHaveBeenCalled();
		expect(window.ao!.app.checkAncestorRepo).toHaveBeenCalledWith("/repo/inner");
	});

	it("opens global settings from the footer menu when no project is selected", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByRole("button", { name: /project actions/i }));

		expect(await screen.findByRole("menuitem", { name: /settings/i })).toBeInTheDocument();
	});

	it("hides unavailable agents while keeping ready agents selectable", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		mockAgentReadinessResponse({
			data: {
				agents: [
					agentReadiness("claude-code", "Claude Code"),
					agentReadiness("cursor", "Cursor", { authentication: "unauthorized" }),
					agentReadiness("aider", "Aider", { installation: "not_installed", authentication: "unknown" }),
				],
			},
			error: undefined,
		});
		renderSidebar({ onCreateProject, seedAgents: false });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();

		await user.click(screen.getByRole("combobox", { name: "Orchestrator agent" }));
		const options = await screen.findAllByRole("option");
		expect(options.map((option) => option.textContent)).toEqual([
			"Claude Code",
			"Manage agents…",
		]);
		expect(options[1]).not.toHaveAttribute("aria-disabled", "true");
		await user.keyboard("{Escape}");

		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(expect.objectContaining({ orchestratorAgent: "claude-code" })),
		);
	});

	it("in Developer Mode, skips agents that are missing or signed out when picking the project's agent", async () => {
		useUiStore.setState({ developerMode: true });
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		mockAgentReadinessResponse({
			data: {
				agents: [
					agentReadiness("cursor", "Cursor", { authentication: "unauthorized" }),
					agentReadiness("aider", "Aider", { installation: "not_installed", authentication: "unknown" }),
					agentReadiness("opencode", "OpenCode"),
				],
			},
			error: undefined,
		});
		renderSidebar({ onCreateProject, seedAgents: false });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(expect.objectContaining({
				workerAgent: "opencode",
				orchestratorAgent: "opencode",
				reviewerAgent: "opencode",
			})),
		);
	});

	it("updates project agent options when the catalog loads after the dialog opens", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		let resolveAgents!: (value: {
			data: { agents: ReturnType<typeof agentReadiness>[] };
			error: undefined;
		}) => void;
		mockAgentReadinessResponse(
			new Promise((resolve) => {
				resolveAgents = resolve;
			}),
		);
		renderSidebar({ onCreateProject, seedAgents: false });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Create and start" })).toBeDisabled();

		resolveAgents({
			data: {
				agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
			},
			error: undefined,
		});

		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/new-project",
				workerAgent: "claude-code",
				orchestratorAgent: "claude-code",
				trackerIntake: undefined,
				asWorkspace: false,
			}),
		);
	});

	it("in Developer Mode, waits for the agent catalog before choosing the project's agent", async () => {
		useUiStore.setState({ developerMode: true });
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		let resolveAgents!: (value: {
			data: { agents: ReturnType<typeof agentReadiness>[] };
			error: undefined;
		}) => void;
		mockAgentReadinessResponse(
			new Promise((resolve) => {
				resolveAgents = resolve;
			}),
		);
		renderSidebar({ onCreateProject, seedAgents: false });

		await openLocalProjectSources(user);
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		expect(onCreateProject).not.toHaveBeenCalled();

		resolveAgents({
			data: {
				agents: [agentReadiness("codex", "Codex")],
			},
			error: undefined,
		});

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/new-project",
				workerAgent: "codex",
				orchestratorAgent: "codex",
				reviewerAgent: "codex",
				trackerIntake: undefined,
				asWorkspace: false,
			}),
		);
	});

	it("opens settings when the footer Settings button is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar();
		await user.click(screen.getAllByRole("button", { name: "Settings" })[0]);
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global" });
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("opens the Mobile settings page from the footer and starts pairing", async () => {
		const user = userEvent.setup();
		renderSidebar();
		await user.click((await screen.findAllByRole("button", { name: "Mobile" }))[0]);
		await user.click(await screen.findByRole("menuitem", { name: "Connect mobile" }));
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "mobile", startPairing: true });
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("always shows the Mobile menu", () => {
		renderSidebar();

		expect(screen.getByRole("button", { name: "Mobile" })).toBeVisible();
	});

	it("keeps Stay Awake inside the Mobile menu", async () => {
		const user = userEvent.setup();
		renderSidebar();

		expect(screen.queryByRole("button", { name: "Stay Awake" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Mobile" }));
		const awake = await screen.findByTestId("awake-menu-item");
		expect(awake).toHaveTextContent("Stay Awake");
		// A switch row, not a nested control: the row itself carries the checked state.
		expect(awake).toHaveAttribute("role", "menuitemcheckbox");
		expect(awake).toHaveAttribute("aria-checked", "false");
		expect(within(awake).queryByRole("switch")).not.toBeInTheDocument();
	});

	it("opens the command palette when Search is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar();
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(false);
		await user.click(screen.getByRole("button", { name: /Search/ }));
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(true);
		expect(screen.getByRole("button", { name: /Search/ })).toHaveTextContent(/(?:⌘ |Ctrl\+)K/);
	});

	it("defers opening the palette until the Search click has been dispatched", async () => {
		renderSidebar();
		fireEvent.click(screen.getByRole("button", { name: /Search/ }));
		// Still closed inside the click's task: the palette dialog must not mount
		// while the pointer sequence that opened it is still being handled.
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(false);
		await act(async () => {});
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(true);
	});

	it("hides Search when the command palette feature is disabled", () => {
		commandPaletteEnabled.current = false;
		renderSidebar();
		expect(screen.queryByRole("button", { name: /Search/ })).not.toBeInTheDocument();
	});

	it("shows the project name and context in the ConfirmDialog description", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		const dialog = await screen.findByRole("dialog", { name: "Remove project" });
		expect(dialog).toHaveTextContent("Project One");
		expect(dialog).toHaveTextContent("live sessions");
		expect(dialog).toHaveTextContent("repository folder");
	});

	it("names a Cloud project's removal as a permanent delete, unlike a local one", async () => {
		const user = userEvent.setup();
		const cloudProject: WorkspaceSummary = { ...workspace, id: "cloud-1", name: "Cloud One", kind: "cloud", cloudOrgId: "org-1", path: "", sessions: [] };
		const onRemoveProject = renderSidebar({ workspaces: [cloudProject] });

		await user.click(screen.getByLabelText("Project actions for Cloud One"));
		expect(screen.queryByRole("menuitem", { name: "Remove project" })).not.toBeInTheDocument();
		await user.click(await screen.findByRole("menuitem", { name: "Delete project" }));

		const dialog = await screen.findByRole("dialog", { name: "Delete project" });
		expect(dialog).toHaveTextContent("This will permanently delete Cloud One");
		// No grey explainer under the lead: it said what the lead already says.
		expect(dialog).not.toHaveTextContent("sandboxes");
		expect(within(dialog).queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Delete permanently" }));
		await waitFor(() => expect(onRemoveProject).toHaveBeenCalledWith("cloud-1", true));
	});

	it("offers no Delete project on a Cloud project someone else shared: only its own organization deletes it", async () => {
		const user = userEvent.setup();
		const sharedProject: WorkspaceSummary = { ...workspace, id: "their-cloud", name: "Their Cloud", kind: "cloud", cloudOrgId: "org-2", path: "", sessions: [], shared: { source: "link" } };
		renderSidebar({ workspaces: [sharedProject] });

		// The guest's row has no project ⋮ menu; its right-click menu is the way in.
		expect(screen.queryByLabelText("Project actions for Their Cloud")).toBeNull();
		const row = document.querySelector('li[data-project-id="their-cloud"]') as HTMLElement;
		fireEvent.contextMenu(within(row).getAllByText("Their Cloud")[0]!);
		expect(await screen.findByRole("menuitem", { name: /Pin to top/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Delete project" })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Remove project" })).not.toBeInTheDocument();
		await user.keyboard("{Escape}");
	});

	it("warns when removing a project would hide open pull requests", async () => {
		const user = userEvent.setup();
		const workspaceWithPullRequests = {
			...workspace,
			sessions: [
				{ ...session, prs: [sidebarPR()] },
				{
					...session,
					id: "proj-1-2",
					prs: [
						sidebarPR(),
						sidebarPR({ number: 8, state: "draft", url: "https://github.com/acme/project-one/pull/8" }),
						sidebarPR({ number: 9, state: "merged", url: "https://github.com/acme/project-one/pull/9" }),
					],
				},
			],
		};
		renderSidebar({ workspaces: [workspaceWithPullRequests] });

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		expect(await screen.findByRole("dialog", { name: "Remove project" })).toHaveTextContent(
			"2 open pull requests belong to this project. Removing it will hide those pull requests from Orchestrator, but will not close them.",
		);
	});

	it("renames a session inline by double-clicking its name", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.dblClick(screen.getByRole("button", { name: "Open fix login" }));
		expect(navigateMock).toHaveBeenCalledTimes(1);
		const input = screen.getByLabelText("Rename fix login");
		await user.clear(input);
		await user.type(input, "polish login{Enter}");

		await waitFor(() => expect(renameSessionMock).toHaveBeenCalledWith("proj-1-1", "polish login"));
		expect(navigateMock).toHaveBeenCalledTimes(1);
	});

	it("still opens a session after an unpaired single click", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		fireEvent.click(screen.getByRole("button", { name: "Open fix login" }), { detail: 1 });
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-1" },
		});
	});

	it("starts the same inline rename from the session context menu", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		fireEvent.contextMenu(screen.getByRole("button", { name: "Open fix login" }));
		const renameItem = await screen.findByRole("menuitem", { name: "Rename fix login" });
		const menu = renameItem.closest('[role="menu"]');
		if (!menu) throw new Error("Session context menu not found");
		await within(menu as HTMLElement).findByRole("menuitem", { name: "Open in" });
		expect(within(menu as HTMLElement).getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Open in",
			"Copy path",
			"Rename",
			"Pin session",
			"Fork session",
			"Archive session",
		]);
		expect(renameItem).toHaveTextContent(/^Rename$/);
		expect(renameItem.querySelector("svg")).toBeInTheDocument();
		await user.click(renameItem);

		expect(screen.getByRole("textbox", { name: "Rename fix login" })).toHaveFocus();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("lists every open target in the editor row's submenu", async () => {
		const user = userEvent.setup();
		const open = vi.spyOn(aoBridge.editorHandoff, "open");
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		fireEvent.contextMenu(screen.getByRole("button", { name: "Open fix login" }));
		await user.click(await screen.findByRole("menuitem", { name: "Open in" }));
		const finder = await screen.findByRole("menuitem", { name: "Finder" });
		const submenu = finder.closest('[role="menu"]');
		if (!submenu) throw new Error("Editor submenu not found");
		expect(within(submenu as HTMLElement).getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Cursor",
			"Finder",
			"Terminal",
		]);
		// A synthetic pointer path from the trigger to the item can close the submenu
		// in jsdom (no layout for Radix's pointer-grace area), so select directly.
		fireEvent.click(finder);

		await waitFor(() => expect(open).toHaveBeenCalledWith({ sessionId: "proj-1-1", targetId: "file-manager" }));
		open.mockRestore();
	});

	it("asks before archiving from the session context menu", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		fireEvent.contextMenu(screen.getByRole("button", { name: "Open fix login" }));
		await user.click(await screen.findByRole("menuitem", { name: "Archive session" }));

		expect(await screen.findByRole("dialog")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Confirm, archive session" })).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("does not open the project menu when the session menu is right-clicked", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		fireEvent.contextMenu(screen.getByRole("button", { name: "Open fix login" }));
		const renameItem = await screen.findByRole("menuitem", { name: "Rename fix login" });
		fireEvent.contextMenu(renameItem);

		expect(screen.queryByRole("menuitem", { name: "Project settings" })).not.toBeInTheDocument();
		expect(screen.getAllByRole("menu")).toHaveLength(1);
	});

	it("caps the inline rename input at 100 characters", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.dblClick(screen.getByText("fix login"));
		expect(screen.getByLabelText("Rename fix login")).toHaveAttribute("maxlength", "100");
	});

	it("renders rename as an unboxed inline label editor", async () => {
		const user = userEvent.setup();
		const lastUserMessageAt = "2026-06-29T23:55:00Z";
		mockParams.sessionId = session.id;
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [{ ...session, lastUserMessageAt }] }],
		});

		await user.dblClick(screen.getByText("fix login"));
		const input = screen.getByLabelText("Rename fix login");
		const time = input.parentElement?.querySelector("time");

		expect(input).toHaveAttribute("data-session-inline-editor");
		expect(input).toHaveClass("border-0", "bg-transparent!", "p-0", "ring-0");
		expect(input).not.toHaveClass("rounded-xs", "border-accent", "px-1", "focus-visible:ring-1");
		expect(input.parentElement).toHaveAttribute("data-session-row");
		expect(input.parentElement?.className).toContain("group/nav-row");
		expect(input.parentElement).toHaveClass("text-foreground", "pr-1");
		expect(input.parentElement).not.toHaveClass("bg-interactive-active");
		const highlight = input.parentElement?.querySelector("[data-nav-row-highlight]");
		expect(highlight).toBeTruthy();
		expect(highlight?.className).toMatch(/bg-interactive-active/);
		expect(highlight?.className).toMatch(/opacity-100/);
		expect(time).toHaveAttribute("data-session-message-age", "");
		expect(time).toHaveAttribute("datetime", lastUserMessageAt);
		expect(time).toHaveClass("font-sans", "tabular-nums");
		expect(time).not.toHaveClass("font-mono");
	});

	it("offers F2 as a keyboard rename path", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const openSession = screen.getByLabelText("Open fix login");
		expect(openSession).toHaveAttribute("aria-keyshortcuts", "F2");
		openSession.focus();
		await user.keyboard("{F2}");

		expect(screen.getByLabelText("Rename fix login")).toHaveFocus();
	});

	it("retains a double-tap rename path for touch", () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const label = screen.getByText("fix login");
		fireEvent.pointerUp(label, { pointerType: "touch" });
		fireEvent.pointerUp(label, { pointerType: "touch" });

		expect(screen.getByLabelText("Rename fix login")).toBeInTheDocument();
	});

	it("cancels the inline rename on Escape without calling the daemon", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.dblClick(screen.getByText("fix login"));
		const input = screen.getByLabelText("Rename fix login");
		await user.clear(input);
		await user.type(input, "discard me{Escape}");

		expect(renameSessionMock).not.toHaveBeenCalled();
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
	});

	it("always shows action icons and reserves padding for them", () => {
		renderSidebar();

		const projectRow = screen.getByText("Project One").closest('button, [role="button"]');
		const actionCluster = screen.getByLabelText("Project actions for Project One").parentElement;

		if (!projectRow) throw new Error("Project row button not found");
		expect(projectRow).toHaveClass("pr-sidebar-project-actions");
		expect(actionCluster).toHaveAttribute("data-project-actions");
		expect(actionCluster).toHaveClass("right-1", "gap-0.5");
		expect(within(actionCluster as HTMLElement).getAllByRole("button")).toHaveLength(2);
		expect(screen.getByLabelText("Project actions for Project One")).not.toHaveClass("opacity-0");
	});

	it("optically aligns the project folder and label with its action icons", () => {
		renderSidebar();

		const projectRow = screen.getByText("Project One").closest('button, [role="button"]');
		expect(projectRow?.querySelector("[data-project-folder-visual]")).toHaveClass("translate-y-px");
		expect(projectRow?.querySelector("[data-project-label]")).toHaveClass("translate-y-px");
	});

	it("caps the project list at 10 until Show more is clicked", async () => {
		const user = userEvent.setup();
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({ workspaces: manyProjects });

		expect(screen.getByText("Project 10")).toBeInTheDocument();
		expect(screen.queryByText("Project 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 4 more projects" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show 4 more projects" }));

		expect(screen.getByText("Project 11")).toBeInTheDocument();
		expect(screen.getByText("Project 14")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show fewer projects" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show fewer projects" }));

		expect(screen.queryByText("Project 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 4 more projects" })).toBeInTheDocument();
	});

	it("keeps the active project row at the top of the scroller", () => {
		mockParams.projectId = "proj-1";
		const other: WorkspaceSummary = {
			...workspace,
			id: "proj-2",
			name: "Project Two",
			path: "/repo/project-two",
		};

		renderSidebar({ workspaces: [workspace, other] });

		const activeRow = document.querySelector('[data-project-drag-row][data-project-id="proj-1"]');
		expect(activeRow).toHaveClass("sticky", "top-0", "z-20", "bg-sidebar");
	});

	it("caps each project's agent list at 6 until its Show more is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: Array.from({ length: 9 }, (_, index) => ({
						...session,
						id: `proj-1-${index + 1}`,
						title: `Agent ${index + 1}`,
						// Descending so sortedWorkerSessions keeps the fixture order.
						updatedAt: `2026-06-${30 - index}T00:00:00Z`,
					})),
				},
			],
		});

		const list = screen.getByTestId("session-list-proj-1");
		expect(within(list).getByText("Agent 6")).toBeInTheDocument();
		expect(screen.queryByText("Agent 7")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show 3 more agents" }));

		expect(screen.getByText("Agent 7")).toBeInTheDocument();
		expect(screen.getByText("Agent 9")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show fewer agents" }));

		expect(screen.queryByText("Agent 7")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 3 more agents" })).toBeInTheDocument();
	});

	it("lifts a project's agent cap when the open session sits past it", () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-8";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: Array.from({ length: 9 }, (_, index) => ({
						...session,
						id: `proj-1-${index + 1}`,
						title: `Agent ${index + 1}`,
						updatedAt: `2026-06-${30 - index}T00:00:00Z`,
					})),
				},
			],
		});

		expect(screen.getByText("Agent 8")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show fewer agents" })).toBeInTheDocument();
	});

	it("keeps Scratchpad mounted, bounded and un-clipped by a long Projects list", () => {
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({
			workspaces: [
				...manyProjects,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [{ ...session, id: "adhoc-1", title: "adhoc one", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" }],
				},
			],
		});

		const section = document.querySelector<HTMLElement>("[data-scratchpad-section]")!;
		expect(section).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Scratchpad" })).toBeVisible();
		expect(section).toContainElement(screen.getByText("adhoc one"));
		// Both sections sit in auto-sized grid rows: each takes its content height,
		// spare space goes to whichever needs it, and a tie splits the column evenly.
		expect(section).not.toHaveClass("shrink-0");
		expect(section.style.maxHeight).toBe("");
		const projects = screen.getByTestId("sidebar-projects-scroller");
		expect(projects).toHaveClass("min-h-0", "flex-1", "overflow-y-auto");
		expect(projects.style.maxHeight).toBe("");
		expect(section.parentElement).toHaveClass("grid", "min-h-0", "auto-rows-[minmax(0,auto)]");
		expect(section.parentElement?.style.getPropertyValue("--sidebar-scratchpad-reserved-height")).toBe("");
	});

	it("fits the project list to content up to the full available height", async () => {
		const user = userEvent.setup();
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({ workspaces: manyProjects });

		const scroller = screen.getByTestId("sidebar-projects-scroller");
		expect(scroller).toHaveClass("overflow-y-auto");
		expect(scroller).toContainElement(screen.getByText("Project 1"));
		expect(scroller.style.height).toBe("");
		// Height comes from flex layout, not a measured CSS variable.
		expect(scroller.style.maxHeight).toBe("");
		expect(scroller).toHaveClass("min-h-0", "flex-1");

		// Show more stays directly beneath the project list and reveals the remainder.
		const showMore = screen.getByRole("button", { name: "Show 4 more projects" });
		expect(showMore.parentElement).toBe(scroller.parentElement?.parentElement);
		await user.click(showMore);
		expect(scroller.style.maxHeight).toBe("");
		expect(scroller).toHaveClass("overflow-y-auto");
	});

	it("collapses the project list from the Projects header", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByTestId("sidebar-projects-scroller")).not.toBeInTheDocument();
		expect(screen.queryByText("Project One")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Project One")).toBeInTheDocument();
		expect(screen.getByTestId("sidebar-projects-scroller")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Show (more|fewer) projects/ })).not.toBeInTheDocument();
	});

	it("keeps the project list capped behind Show more while the sidebar is toggled off", () => {
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({ workspaces: manyProjects, initialOpen: false });

		// The sidebar slides away (offcanvas); it has no icon rail, so its lists
		// must not lift their caps just because it is collapsed.
		expect(screen.queryByText("Project 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { hidden: true, name: /more projects/ })).toBeInTheDocument();
	});

	it("does not reopen a closed Projects section when the sidebar is toggled off and on", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");
		await waitFor(() => expect(screen.queryByText(workspace.name)).not.toBeInTheDocument());

		await user.click(screen.getByRole("button", { name: "Collapse sidebar" }));
		// While the sidebar is off, the closed section must not be forced open.
		expect(screen.queryByText(workspace.name)).not.toBeInTheDocument();
		await user.click(screen.getAllByRole("button", { hidden: true, name: "Expand sidebar" })[0]);

		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText(workspace.name)).not.toBeInTheDocument();
	});

	it("clamps width at minimum when dragged past the resize floor (no auto-collapse)", async () => {
		renderSidebar();

		const resizeHandle = screen.getByTestId("resize-handle");
		expect(resizeHandle).toBeInTheDocument();
		expect(document.querySelector('[data-slot="sidebar"][data-state="expanded"]')).toBeInTheDocument();
		expect(document.documentElement.style.getPropertyValue("--ao-sidebar-w")).toBe("");

		fireEvent.pointerDown(resizeHandle, { clientX: SIDEBAR_DEFAULT_WIDTH });
		// Drag well past minimum — sidebar should stay expanded and clamp at min.
		fireEvent.pointerMove(window, { clientX: SIDEBAR_MIN_WIDTH - 50 });
		fireEvent.pointerUp(window);

		// Sidebar stays expanded; dragging no longer collapses it.
		expect(document.querySelector('[data-slot="sidebar"][data-state="expanded"]')).toBeInTheDocument();
		expect(
			document
				.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')
				?.style.getPropertyValue("--ao-sidebar-w"),
		).toBe(`${SIDEBAR_MIN_WIDTH}px`);
	});

	it("persists the latest clamped width on pointer-up before the queued frame", () => {
		renderSidebar();
		const resizeHandle = screen.getByTestId("resize-handle");
		const gap = document.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')!;
		const initialWidth = gap.style.getPropertyValue("--ao-sidebar-w");
		fireEvent.pointerDown(resizeHandle, { clientX: SIDEBAR_DEFAULT_WIDTH });
		fireEvent.pointerMove(window, { clientX: SIDEBAR_MIN_WIDTH + 5 });
		expect(gap.style.getPropertyValue("--ao-sidebar-w")).toBe(initialWidth);
		fireEvent.pointerUp(window);
		expect(gap.style.getPropertyValue("--ao-sidebar-w")).toBe(`${SIDEBAR_MIN_WIDTH + 5}px`);
		expect(window.localStorage.getItem("ao-sidebar-w")).toBe(String(SIDEBAR_MIN_WIDTH + 5));
	});

	it("never drops the floor below SIDEBAR_MIN_WIDTH and grows when the brand moves right", async () => {
		// Titlebar brand as in native fullscreen: no traffic-light reserve, so the
		// label alone would fit a sidebar narrower than the footer needs.
		const brand = document.createElement("span");
		brand.setAttribute("data-sidebar-brand", "");
		const label = document.createElement("span");
		label.setAttribute("data-brand-label", "");
		brand.append(label);
		document.body.append(brand);
		brand.getBoundingClientRect = () => ({ width: 130 }) as DOMRect;
		label.getBoundingClientRect = () => ({ left: 60 }) as DOMRect;
		Object.defineProperty(label, "scrollWidth", { value: 100 });
		try {
			renderSidebar();
			const gap = document.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')!;
			fireEvent.pointerDown(screen.getByTestId("resize-handle"), { clientX: SIDEBAR_DEFAULT_WIDTH });
			fireEvent.pointerMove(window, { clientX: 0 });
			fireEvent.pointerUp(window);
			expect(gap.style.getPropertyValue("--ao-sidebar-w")).toBe(`${SIDEBAR_MIN_WIDTH}px`);

			// Leaving fullscreen moves the label right; TitlebarNav's resize nudge
			// must grow the sidebar to fit it (60 → 140, + 100 label + 13 gap).
			label.getBoundingClientRect = () => ({ left: 140 }) as DOMRect;
			window.dispatchEvent(new Event("resize"));
			await waitFor(() => expect(gap.style.getPropertyValue("--ao-sidebar-w")).toBe("253px"));
		} finally {
			brand.remove();
		}
	});

	it("shows a quiet dot when idle, the spinner while working, and markers once the agent stops", () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-idle",
							title: "idle task",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
						{
							...session,
							id: "proj-1-work",
							title: "working task",
							status: "working",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
						{
							...session,
							id: "proj-1-ci",
							title: "ci failed task",
							status: "working",
							scmStatus: "ci_failed",
							kanbanColumn: "validating",
							displayStatus: "Fixing CI failures",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ ci: "failing" })],
						},
						{
							...session,
							id: "proj-1-review",
							title: "review task",
							status: "idle",
							scmStatus: "pr_open",
							kanbanColumn: "needs_review",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR()],
						},
						{
							...session,
							id: "proj-1-ready",
							title: "ready task",
							status: "idle",
							scmStatus: "mergeable",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ mergeability: "mergeable" })],
						},
						{
							...session,
							id: "proj-1-merged",
							title: "merged task",
							status: "idle",
							scmStatus: "merged",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ state: "merged" })],
						},
					],
				},
			],
		});
		const sessionDot = (title: string) =>
			screen.getByLabelText(`Open ${title}`).querySelector<HTMLElement>("[data-session-status]");

		expect(sessionDot("idle task")).toHaveAttribute("data-session-idle", "idle");

		const workingDot = sessionDot("working task");
		expect(workingDot).toHaveAttribute("data-session-spinner");
		expect(workingDot).toHaveClass("animate-spin");

		// A working agent spins even when its PR has a state to flag.
		expect(sessionDot("ci failed task")).toHaveAttribute("data-session-spinner");
		expect(sessionDot("review task")).toHaveAttribute("data-session-marker", "review");
		expect(sessionDot("review task")).toHaveClass("icon-circle-alert", "text-status-in-review");
		expect(sessionDot("ready task")).toHaveAttribute("data-session-marker", "ready");
		expect(sessionDot("merged task")).toHaveAttribute("data-session-marker", "merged");
		expect(sessionDot("merged task")).not.toHaveClass("animate-spin");
	});

	it("shows a red alert icon instead of a dot when a session needs input", () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-input",
							title: "input task",
							status: "needs_input",
							activity: { state: "waiting_input", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
						{
							...session,
							id: "proj-1-ci",
							title: "ci task",
							status: "ci_failed",
							displayStatus: "CI failing",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
					],
				},
			],
		});

		const marker = (title: string) =>
			screen.getByLabelText(`Open ${title}`).querySelector<SVGElement | HTMLElement>("[data-session-status]");

		expect(marker("input task")).toHaveAttribute("data-session-marker", "attention");
		expect(marker("input task")).toHaveClass("text-destructive");
		expect(marker("input task")).not.toHaveClass("rounded-full");
		expect(marker("ci task")).toHaveAttribute("data-session-marker", "attention");
		expect(marker("ci task")).toHaveClass("text-destructive");
	});

	it("gives Scratchpad rows the same status icons and age as project rows", () => {
		const adhoc = { ...session, workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad", lastUserMessageAt: "2026-06-29T23:55:00Z" };
		renderSidebar({
			workspaces: [{
				id: STANDALONE_WORKSPACE_ID,
				name: "Scratchpad",
				kind: STANDALONE_PROJECT_KIND,
				path: "",
				sessions: [
					{ ...adhoc, id: "adhoc-idle", title: "idle chat", activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" } },
					{ ...adhoc, id: "adhoc-working", title: "working chat", activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" } },
					{ ...adhoc, id: "adhoc-input", title: "input chat", status: "needs_input", activity: { state: "waiting_input", lastActivityAt: "2026-06-30T00:00:00Z" } },
				],
			}],
		});
		const row = (title: string) => screen.getByLabelText(`Open ${title}`);
		const dot = (title: string) => row(title).querySelector<HTMLElement>("[data-session-status]");

		expect(dot("idle chat")).toHaveAttribute("data-session-idle", "idle");
		expect(dot("working chat")).toHaveAttribute("data-session-spinner");
		expect(dot("input chat")).toHaveAttribute("data-session-marker", "attention");
		expect(row("idle chat").closest("[data-session-row]")?.querySelector("[data-session-message-age]")).toBeInTheDocument();
		expect(document.querySelector("[data-scratchpad-section] img")).toBeNull();
	});

	it("keeps AO's pending review quiet on the session and its project folder", async () => {
		useUiStore.setState({ projectAvatars: false });
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [{
				...workspace,
				sessions: [{ ...session, status: "commented", scmStatus: "commented", kanbanColumn: "validating", displayStatus: "Review scheduled" }],
			}],
		});
		expect(screen.getByLabelText("Open fix login").querySelector("[data-session-marker]")).not.toBeInTheDocument();
		expect(document.querySelector("[data-project-folder-icon] .icon-folder-open")).toBeInTheDocument();
		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(document.querySelector("[data-project-folder-icon] [data-project-marker]")).not.toBeInTheDocument();
		expect(document.querySelector("[data-project-folder-icon] .icon-folder")).toBeInTheDocument();
	});

	it("replaces the folder with a warning when a collapsed project needs attention", () => {
		useUiStore.setState({ projectAvatars: false });
		renderSidebar({
			expandedProjectIds: [],
			workspaces: [{ ...workspace, sessions: [{ ...session, status: "needs_input" }] }],
		});
		expect(document.querySelector('[data-project-folder-icon] [data-project-marker="attention"]')).toHaveClass("icon-circle-alert", "text-destructive");
		expect(document.querySelector("[data-project-folder-icon] .icon-folder")).not.toBeInTheDocument();
	});

	it.each(["ci_failed", "exited"] as const)("keeps the folder for a collapsed project with a %s session", (status) => {
		useUiStore.setState({ projectAvatars: false });
		renderSidebar({
			expandedProjectIds: [],
			workspaces: [{ ...workspace, sessions: [{ ...session, status }] }],
		});
		expect(document.querySelector("[data-project-folder-icon] [data-project-marker]")).not.toBeInTheDocument();
		expect(document.querySelector("[data-project-folder-icon] .icon-folder")).toBeInTheDocument();
	});

	it("shows a first-letter tile for a project without a repository avatar", () => {
		useUiStore.setState({ projectAvatars: true });
		renderSidebar({ expandedProjectIds: [], workspaces: [{ ...workspace, sessions: [] }] });
		const icon = document.querySelector("[data-project-folder-icon]");
		expect(icon).toHaveTextContent("P");
		expect(icon?.querySelector(".icon-folder")).not.toBeInTheDocument();
	});

	it("previews the board card to the right of a hovered session row", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-preview",
							title: "preview task",
							status: "needs_input",
							displayStatus: "Blocked",
							kanbanColumn: "building",
							activity: { state: "waiting_input", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
					],
				},
			],
		});

		resetSessionPreviewWarmth();
		expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument();
		await user.hover(screen.getByLabelText("Open preview task"));
		// The first preview waits out a short hover before it opens.
		expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument();

		const card = await screen.findByTestId("board-session-card");
		expect(card).toHaveTextContent("preview task");
		expect(within(card).getByTestId("session-status")).toHaveTextContent("Blocked");
		// Static: attention styling without the pulse, and no card-level click target.
		expect(card).not.toHaveClass("animate-attention-card-pulse");
		expect(card).not.toHaveClass("cursor-pointer");
		// One elevated surface with no outline, even for a session that needs input.
		expect(card.className).toMatch(/bg-popover|var\(--popover\)/);
		expect(card).not.toHaveClass("border", "border-status-needs-you");
		expect(card.querySelector(".border-t")).toBeNull();
		// The first preview appears instantly: no fade or zoom in, and none out.
		const content = card.closest('[data-slot="hover-card-content"]');
		expect(content).not.toBeNull();
		expect(content?.className).not.toMatch(/animate-in|zoom-|animate-out/);

		await user.unhover(screen.getByLabelText("Open preview task"));
		await waitFor(() => expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument());
	});

	it("replaces the preview straight away when the pointer moves to another row", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-first", title: "first task", status: "working" },
						{ ...session, id: "proj-1-second", title: "second task", status: "idle" },
					],
				},
			],
		});

		resetSessionPreviewWarmth();
		await user.hover(screen.getByLabelText("Open first task"));
		expect(await screen.findByTestId("board-session-card")).toHaveTextContent("first task");

		// Well inside the first-open delay: only a warm preview can appear this fast.
		await user.hover(screen.getByLabelText("Open second task"));
		await waitFor(
			() => {
				const cards = screen.getAllByTestId("board-session-card");
				expect(cards).toHaveLength(1);
				expect(cards[0]).toHaveTextContent("second task");
			},
			{ timeout: 150 },
		);
		const content = screen.getByTestId("board-session-card").closest('[data-slot="hover-card-content"]');
		expect(content?.className).not.toMatch(/animate-|zoom-|fade-|slide-/);
	});

	it("keeps the preview open while the pointer is on the card, and closes it once it leaves", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{ ...workspace, sessions: [{ ...session, id: "proj-1-card", title: "card task", status: "working" }] },
			],
		});

		resetSessionPreviewWarmth();
		const row = screen.getByLabelText("Open card task");
		await user.hover(row);
		const card = await screen.findByTestId("board-session-card");

		// Off the row and onto the card: past the leave grace, it is still there.
		await user.hover(card);
		await new Promise((resolve) => setTimeout(resolve, 300));
		expect(screen.getByTestId("board-session-card")).toBeInTheDocument();
		// The card takes the pointer: it is not click-through.
		expect(card.closest('[data-slot="hover-card-content"]')?.className).not.toMatch(/pointer-events-none/);

		await user.unhover(card);
		await waitFor(() => expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument());
	});

	it("shows only the last row's preview after a quick sweep across cold rows", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-a", title: "sweep a", status: "working" },
						{ ...session, id: "proj-1-b", title: "sweep b", status: "idle" },
						{ ...session, id: "proj-1-c", title: "sweep c", status: "idle" },
					],
				},
			],
		});

		resetSessionPreviewWarmth();
		// All three hovers land inside the first-open delay, so none has opened yet.
		await user.hover(screen.getByLabelText("Open sweep a"));
		await user.hover(screen.getByLabelText("Open sweep b"));
		await user.hover(screen.getByLabelText("Open sweep c"));

		const card = await screen.findByTestId("board-session-card");
		expect(card).toHaveTextContent("sweep c");
		// Past every row's delay: nothing from the rows swept over may open late.
		await new Promise((resolve) => setTimeout(resolve, 600));
		const cards = screen.getAllByTestId("board-session-card");
		expect(cards).toHaveLength(1);
		expect(cards[0]).toHaveTextContent("sweep c");
	});

	it("goes back to a delayed first preview after the pointer has left every row", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{ ...workspace, sessions: [{ ...session, id: "proj-1-cool", title: "cool task", status: "working" }] },
			],
		});

		resetSessionPreviewWarmth();
		const row = screen.getByLabelText("Open cool task");
		await user.hover(row);
		await screen.findByTestId("board-session-card");
		await user.unhover(row);
		await waitFor(() => expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument());

		await new Promise((resolve) => setTimeout(resolve, 400));
		await user.hover(row);
		expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument();
		const card = await screen.findByTestId("board-session-card");
		expect(card.closest('[data-slot="hover-card-content"]')?.className).not.toMatch(/animate-in/);
	});

	it("closes the preview when the row's archive confirm opens", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		resetSessionPreviewWarmth();
		const row = screen.getByLabelText("Open fix login").closest<HTMLElement>("[data-session-row]")!;
		await user.hover(screen.getByLabelText("Open fix login"));
		await screen.findByTestId("board-session-card");

		await user.click(within(row).getByLabelText("Archive session"));
		const dialog = await screen.findByRole("dialog", { name: "Are you sure you want to archive this session?" });
		await waitFor(() => expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument());

		await user.click(within(dialog).getByRole("button", { name: "No" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		await waitFor(() => expect(within(row).getByLabelText("Archive session")).toHaveFocus());
		await new Promise((resolve) => setTimeout(resolve, 300));
		expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument();
	});

	it("closes the row's archive confirm on the first Escape", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const row = screen.getByLabelText("Open fix login").closest<HTMLElement>("[data-session-row]")!;
		await user.click(within(row).getByLabelText("Archive session"));
		await screen.findByRole("dialog");

		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("keeps the preview closed through the archive confirm opened from the context menu", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		resetSessionPreviewWarmth();
		const row = screen.getByLabelText("Open fix login");
		await user.hover(row);
		await screen.findByTestId("board-session-card");

		fireEvent.contextMenu(row);
		await user.click(await screen.findByRole("menuitem", { name: "Archive session" }));
		const dialog = await screen.findByRole("dialog", { name: "Are you sure you want to archive this session?" });
		await new Promise((resolve) => setTimeout(resolve, 300));
		expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument();

		await user.click(within(dialog).getByRole("button", { name: "No" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		await new Promise((resolve) => setTimeout(resolve, 300));
		expect(screen.queryByTestId("board-session-card")).not.toBeInTheDocument();
	});

	it("previews a row again after renaming it from the context menu", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		resetSessionPreviewWarmth();
		fireEvent.contextMenu(screen.getByLabelText("Open fix login"));
		await user.click(await screen.findByRole("menuitem", { name: "Rename fix login" }));
		const input = screen.getByRole("textbox", { name: "Rename fix login" });
		await user.clear(input);
		await user.type(input, "polish login{Enter}");
		await waitFor(() => expect(renameSessionMock).toHaveBeenCalledWith("proj-1-1", "polish login"));

		await user.hover(screen.getByLabelText("Open fix login"));
		expect(await screen.findByTestId("board-session-card")).toHaveTextContent("fix login");
	});

	it("previews a row when focus moves to it from another control", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		resetSessionPreviewWarmth();
		act(() => {
			screen.getByRole("button", { name: "Search" }).focus();
			screen.getByLabelText("Open fix login").focus();
		});

		await screen.findByTestId("board-session-card");
	});

	it("lists sessions that need input above the rest", () => {
		const at = "2026-06-30T00:00:00Z";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-a", title: "newest calm task", status: "working", updatedAt: "2026-06-30T03:00:00Z", activity: { state: "active", lastActivityAt: at } },
						{ ...session, id: "proj-1-b", title: "older input task", status: "needs_input", updatedAt: "2026-06-30T01:00:00Z", activity: { state: "waiting_input", lastActivityAt: at } },
						{ ...session, id: "proj-1-c", title: "middle calm task", status: "idle", updatedAt: "2026-06-30T02:00:00Z", activity: { state: "idle", lastActivityAt: at } },
					],
				},
			],
		});

		const titles = screen
			.getAllByLabelText(/^Open (newest calm|older input|middle calm) task$/)
			.map((row) => row.getAttribute("aria-label"));
		expect(titles).toEqual(["Open older input task", "Open newest calm task", "Open middle calm task"]);
	});

	it("shows a spinner when an idle-section session has working activity", () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-idle-working",
							title: "idle task receiving work",
							status: "idle",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
					],
				},
			],
		});

		const dot = screen
			.getByLabelText("Open idle task receiving work")
			.querySelector<HTMLElement>("[data-session-status]");
		expect(dot).toHaveAttribute("data-session-spinner");
		expect(dot).toHaveClass("animate-spin");
	});

	it("keeps idle sessions quiet and shows a review marker without motion", async () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-idle-activity",
							title: "idle activity task",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
						{
							...session,
							id: "proj-1-idle-draft",
							title: "idle draft task",
							status: "draft",
							scmStatus: "draft",
							kanbanColumn: "needs_review",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ state: "draft" })],
						},
					],
				},
			],
		});


		const idleActivityDot = screen.getByLabelText("Open idle activity task").querySelector("[data-session-idle]");
		const idleDraft = screen.getByLabelText("Open idle draft task");

		expect(idleActivityDot).toHaveAttribute("data-session-idle", "idle");
		// A draft is still the author's, so it gets the quiet dot, not a review marker.
		expect(idleDraft.querySelector("[data-session-marker]")).not.toBeInTheDocument();
		expect(idleDraft.querySelector("[data-session-idle]")).toBeInTheDocument();
	});

	it("shows sessions on load and hides them once collapsed", async () => {
		const user = userEvent.setup();
		const workspaceWithSessions = {
			...workspace,
			sessions: [session, { ...session, id: "proj-1-2", title: "second task" }],
		};
		renderSidebar({ workspaces: [workspaceWithSessions] });

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByLabelText("Open second task")).toBeInTheDocument();

		// Collapse via folder icon
		const folder = screen.getByLabelText("Toggle Project One sessions");
		expect(folder).toBeTruthy();
		await user.click(folder);

		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();
	});

	it("starts every project collapsed when the expanded-project store is empty", () => {
		renderSidebar({
			expandedProjectIds: [],
			workspaces: [{ ...workspace, sessions: [session] }],
		});

		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute(
			"aria-expanded",
			"false",
		);
	});

	it("expands a project once chats are imported into it", () => {
		renderSidebar({
			expandedProjectIds: [],
			workspaces: [{ ...workspace, sessions: [session] }],
		});
		act(() => useSessionImportStore.getState().recordImport(workspace.id, 1));

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(window.localStorage.getItem("ao.sidebar.expanded-projects")).toBe(JSON.stringify([workspace.id]));
	});

	it("reveals the active project when opening a worker-session deep link", async () => {
		const user = userEvent.setup();
		mockParams.projectId = workspace.id;
		mockParams.sessionId = session.id;
		renderSidebar({
			expandedProjectIds: [],
			workspaces: [{ ...workspace, sessions: [session] }],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute(
			"aria-expanded",
			"true",
		);

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute(
			"aria-expanded",
			"false",
		);
	});

	it("restores only the projects saved as expanded and persists toggles", async () => {
		const user = userEvent.setup();
		const secondWorkspace = {
			...workspace,
			id: "proj-2",
			name: "Project Two",
			sessions: [{ ...session, id: "proj-2-1", title: "second task" }],
		};
		renderSidebar({
			expandedProjectIds: [workspace.id],
			workspaces: [{ ...workspace, sessions: [session] }, secondWorkspace],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.expanded-projects") ?? "null")).toEqual([]);
	});

	it("hides all sessions when project is collapsed via folder icon", async () => {
		const user = userEvent.setup();
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-2";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [session, { ...session, id: "proj-1-2", title: "second task" }],
				},
			],
		});

		const projectRow = screen.getByText("Project One").closest('button, [role="button"]')!;
		// Project starts expanded — sessions visible
		expect(screen.getByLabelText("Open second task")).toBeInTheDocument();
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(projectRow).toHaveAttribute("aria-expanded", "true");

		// Collapse via folder icon
		const folder = screen.getByLabelText("Toggle Project One sessions");
		expect(folder).toBeTruthy();
		await user.click(folder);

		expect(projectRow).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
	});

	it("keeps merged sessions in the list until they are terminated", async () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "merged-live", title: "merged live task", status: "merged", isTerminated: false },
						{ ...session, id: "merged-done", title: "merged terminated task", status: "merged", isTerminated: true },
					],
				},
			],
		});


		expect(screen.getByLabelText("Open merged live task")).toBeInTheDocument();
		expect(screen.queryByLabelText("Open merged terminated task")).not.toBeInTheDocument();
	});

	it("confirms before archiving a session and names the action archive, not delete", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const row = screen.getByLabelText("Open fix login").closest<HTMLElement>("[data-session-row]")!;
		const archiveButton = within(row).getByLabelText("Archive session");
		expect(archiveButton.querySelector("svg")).toHaveClass("icon-archive");

		fireEvent.click(archiveButton);
		expect(postMock).not.toHaveBeenCalled();

		const dialog = await screen.findByRole("dialog", {
			name: "Are you sure you want to archive this session?",
		});
		expect(dialog).toHaveTextContent("You can always restore it from the Archive section later.");
		fireEvent.click(within(dialog).getByRole("button", { name: "No" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(postMock).not.toHaveBeenCalled();

		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);
	});

	it("shifts to the adjacent session when deleting the active session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-2";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "first task", updatedAt: "2026-06-30T00:00:00Z" },
						{ ...session, id: "proj-1-2", title: "second task", updatedAt: "2026-06-30T01:00:00Z" },
					],
				},
			],
		});

		const row = screen.getByLabelText("Open second task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-2" } } }),
			),
		);
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-1" },
			}),
		);
	});

	it("shifts to the orchestrator when killing the only worker session in a project with an active orchestrator", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "sole worker" },
						{
							...session,
							id: "orch-1",
							title: "Orchestrator",
							kind: "orchestrator",
							isTerminated: false,
						},
					],
				},
			],
		});

		const row = screen.getByLabelText("Open sole worker").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);

		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "orch-1" },
			}),
		);
	});

	it("shifts to the project board when killing the only worker session in a project without an orchestrator", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [{ ...session, id: "proj-1-1", title: "sole worker" }],
				},
			],
		});

		const row = screen.getByLabelText("Open sole worker").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);

		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId",
				params: { projectId: "proj-1" },
			}),
		);
	});

	it("does not navigate away when killing an inactive session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "active task", updatedAt: "2026-06-30T00:00:00Z" },
						{ ...session, id: "proj-1-2", title: "inactive task", updatedAt: "2026-06-30T01:00:00Z" },
					],
				},
			],
		});

		const row = screen.getByLabelText("Open inactive task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-2" } } }),
			),
		);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("shifts to the next session when killing an active pinned session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-2";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "first task", isPinned: false, updatedAt: "2026-06-30T00:00:00Z" },
						{ ...session, id: "proj-1-2", title: "pinned task", isPinned: true, pinnedAt: "2026-06-30T01:00:00Z", updatedAt: "2026-06-30T01:00:00Z" },
					],
				},
			],
		});

		const pinnedList = screen.getByTestId("pinned-session-list");
		const row = within(pinnedList).getByLabelText("Open pinned task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-2" } } }),
			),
		);
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-1" },
			}),
		);
	});

	it("goes Home, not to a board, when archiving the active pinned Cloud standalone agent", async () => {
		mockParams.projectId = "agent-1";
		mockParams.sessionId = "cloud-agent";
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: "agent-1",
					name: "Fix the flake",
					kind: "cloud",
					cloudOrgId: "org-1",
					standaloneAgent: true,
					path: "",
					sessions: [{
						...session, id: "cloud-agent", title: "Fix the flake", workspaceId: "agent-1", workspaceName: "Fix the flake",
						cloud: { orgId: "org-1" }, isPinned: true, pinnedAt: "2026-06-30T01:00:00Z",
					}],
				},
			],
		});

		const pinnedList = screen.getByTestId("pinned-session-list");
		const row = within(pinnedList).getByLabelText("Open Fix the flake").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({ to: "/" }));
		expect(navigateMock).not.toHaveBeenCalledWith(expect.objectContaining({ to: "/projects/$projectId" }));
	});

	it("keeps pinned sessions with the same ID distinct across local and two hosts", () => {
		mockParams.hostId = "box-b";
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "shared";
		const pinned = { ...session, id: "shared", title: "shared task", isPinned: true, pinnedAt: "2026-06-30T01:00:00Z" };
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [pinned] }],
			remoteHosts: [
				{ hostId: "box-a", label: "Host A", url: "http://box-a:3011", status: "connected" },
				{ hostId: "box-b", label: "Host B", url: "http://box-b:3011", status: "connected" },
			],
			remoteWorkspaces: [
				{ ...workspace, hostId: "box-a", sessions: [{ ...pinned, hostId: "box-a" }] },
				{ ...workspace, hostId: "box-b", sessions: [{ ...pinned, hostId: "box-b" }] },
			],
		});
		const list = screen.getByTestId("pinned-session-list");
		const hostA = within(list).getByText("Host A").closest<HTMLElement>("[data-session-row]")!;
		const hostB = within(list).getByText("Host B").closest<HTMLElement>("[data-session-row]")!;
		const local = within(list).getByRole("button", { name: "Open shared task" });
		expect(within(hostA).getByRole("button", { name: "Open shared task · Host A" })).not.toHaveAttribute("aria-current");
		expect(within(hostB).getByRole("button", { name: "Open shared task · Host B" })).toHaveAttribute("aria-current", "page");
		fireEvent.click(within(hostA).getByRole("button", { name: "Open shared task · Host A" }));
		expect(navigateMock).toHaveBeenLastCalledWith({ to: "/host/$hostId/project/$projectId/session/$sessionId", params: { hostId: "box-a", projectId: "proj-1", sessionId: "shared" } });
		fireEvent.click(within(hostB).getByRole("button", { name: "Open shared task · Host B" }));
		expect(navigateMock).toHaveBeenLastCalledWith({ to: "/host/$hostId/project/$projectId/session/$sessionId", params: { hostId: "box-b", projectId: "proj-1", sessionId: "shared" } });
		fireEvent.click(local);
		expect(navigateMock).toHaveBeenLastCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "proj-1", sessionId: "shared" } });
	});

	it("shifts to the adjacent session when killing an active pinned session with multiple remaining sessions", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-3", title: "newest task", updatedAt: "2026-06-30T02:00:00Z" },
						{ ...session, id: "proj-1-2", title: "middle task", updatedAt: "2026-06-30T01:00:00Z" },
						{ ...session, id: "proj-1-1", title: "oldest pinned task", isPinned: true, pinnedAt: "2026-06-30T00:00:00Z", updatedAt: "2026-06-30T00:00:00Z" },
					],
				},
			],
		});

		const pinnedList = screen.getByTestId("pinned-session-list");
		const row = within(pinnedList).getByLabelText("Open oldest pinned task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-2" },
			}),
		);
	});

	it("navigates optimistically when killing the active session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		let resolveKill: (() => void) | undefined;
		postMock.mockImplementation(
			() =>
				new Promise((resolve) => {
					resolveKill = () => resolve({ data: { ok: true }, error: undefined, response: new Response(null, { status: 200 }) });
				}),
		);
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "first task", updatedAt: "2026-06-30T00:00:00Z" },
					],
				},
			],
		});

		const row = screen.getByLabelText("Open first task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		// Navigation occurs optimistically on click rather than waiting for daemon round-trip.
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-1" },
		});

		resolveKill?.();
	});

	it("downloads the update from the available row's pill", async () => {
		updateStatusMock.mockResolvedValue({ state: "available", version: "9.9.9", releaseNotes: "### Fixes\n- Faster boot" });
		renderSidebar();

		const availableRow = await screen.findByTestId("sidebar-update-available");
		expect(availableRow).toHaveTextContent(/^Update available/);
		// The whole row is the one action, and its label names the version.
		const pill = availableRow;
		expect(pill).toHaveRole("button");
		expect(pill).toHaveAccessibleName("Download update v9.9.9");
		// No dismiss control or second button inside it.
		expect(within(availableRow).queryAllByRole("button")).toHaveLength(0);
		// Nothing is staged yet, so the restart action must not be offered.
		expect(screen.queryByLabelText(/Restart to install update/)).not.toBeInTheDocument();

		await userEvent.click(pill);
		expect(downloadUpdateMock).toHaveBeenCalledTimes(1);
	});

	it("says \"Update available\" in plain text with the dimmed base version beside it", async () => {
		updateStatusMock.mockResolvedValue({ state: "available", version: "0.12.11-nightly.202609021713", releaseNotes: "Faster boot" });
		renderSidebar();

		const availableRow = await screen.findByTestId("sidebar-update-available");
		const word = within(availableRow).getByText(/^Update available/);
		expect(word).not.toHaveClass("decoration-dotted");
		// The nightly's base version, one "v", no build stamp.
		expect(within(availableRow).getByText("v0.12.11", { exact: true })).toHaveClass("text-passive");
		expect(availableRow).not.toHaveTextContent(/Nightly|202609021713/);
		await userEvent.hover(word);
		expect(screen.queryByText("Faster boot")).not.toBeInTheDocument();
		expect(document.querySelector('[data-slot="hover-card-content"]')).toBeNull();
	});

	it("shows download progress as a bar, not a percentage", async () => {
		updateStatusMock.mockResolvedValue({ state: "downloading", version: "9.9.9", percent: 42 });
		renderSidebar();

		const downloadingRow = await screen.findByTestId("sidebar-update-downloading");
		expect(within(downloadingRow).getByText("Downloading update")).toBeVisible();
		expect(within(downloadingRow).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "42");
		expect(downloadingRow).not.toHaveTextContent("%");
		// A download already in flight must not offer a second one.
		expect(screen.queryByLabelText(/Download update/)).not.toBeInTheDocument();
		expect(screen.queryByLabelText(/Restart to install update/)).not.toBeInTheDocument();
	});

	it("keeps failing update checks out of the sidebar", async () => {
		updateStatusMock.mockResolvedValue({ state: "idle", checksFailing: true });
		renderSidebar();

		await waitFor(() => expect(updateStatusMock).toHaveBeenCalled());
		expect(screen.queryByTestId("sidebar-update-failed")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Retry update")).not.toBeInTheDocument();
	});

	it("keeps a failed check's error out of the sidebar", async () => {
		updateStatusMock.mockResolvedValue({
			state: "error",
			message: "net::ERR_SSL_PROTOCOL_ERROR",
			netError: true,
			errorInfo: { kind: "unreachable", phase: "check" },
		});
		renderSidebar();

		await waitFor(() => expect(updateStatusMock).toHaveBeenCalled());
		expect(screen.queryByText("net::ERR_SSL_PROTOCOL_ERROR")).not.toBeInTheDocument();
		expect(screen.queryByTestId("sidebar-update-failed")).not.toBeInTheDocument();
	});

	it("offers a retry when a download fails", async () => {
		updateStatusMock.mockResolvedValue({
			state: "error",
			message: "Download failed.",
			version: "9.9.9",
			errorInfo: { kind: "generic", phase: "download" },
		});
		renderSidebar();

		const failedRow = await screen.findByTestId("sidebar-update-failed");
		// One row: the reason is in the row's tooltip and screen-reader text.
		expect(failedRow).toHaveTextContent("Update failed · Couldn't download");
		await userEvent.click(failedRow);
		expect(downloadUpdateMock).toHaveBeenCalledTimes(1);
	});

	it("retries a failed install through the restart confirmation", async () => {
		updateStatusMock.mockResolvedValue({
			state: "error",
			message: "Install failed.",
			errorInfo: { kind: "generic", phase: "install" },
			staged: { version: "9.9.9", stagedAt: Date.now(), escalated: false },
		});
		renderSidebar();

		const failedRow = await screen.findByTestId("sidebar-update-failed");
		expect(failedRow).toHaveTextContent("Update failed · Couldn't install");
		await userEvent.click(failedRow);
		expect(useUiStore.getState().updateInstallPromptOpen).toBe(true);
		expect(downloadUpdateMock).not.toHaveBeenCalled();
	});

	it("checks for updates again when a failed install left nothing staged", async () => {
		updateStatusMock.mockResolvedValue({
			state: "error",
			message: "Couldn't prepare the update.",
			errorInfo: { kind: "generic", phase: "install" },
		});
		renderSidebar();

		const failedRow = await screen.findByTestId("sidebar-update-failed");
		await userEvent.click(failedRow);
		expect(checkUpdateMock).toHaveBeenCalledTimes(1);
		expect(useUiStore.getState().updateInstallPromptOpen).toBe(false);
		expect(downloadUpdateMock).not.toHaveBeenCalled();
	});

	it("keeps a ready install action when a later check fails", async () => {
		updateStatusMock.mockResolvedValue({
			state: "error",
			message: "net::ERR_SSL_PROTOCOL_ERROR",
			staged: { version: "9.9.9", stagedAt: Date.now(), escalated: false },
		});
		renderSidebar();

		expect(await screen.findByTestId("sidebar-update-ready")).toBeVisible();
		expect(screen.getAllByLabelText("Restart to install update v9.9.9")).not.toHaveLength(0);
		expect(screen.queryByText("net::ERR_SSL_PROTOCOL_ERROR")).not.toBeInTheDocument();
	});

	it("keeps restart available for a stalled native stage", async () => {
		updateStatusMock.mockResolvedValue({
			state: "downloaded",
			version: "9.9.9",
			staged: { version: "9.9.9", stagedAt: Date.now(), escalated: false, ready: false },
		});
		renderSidebar();

		expect(await screen.findByTestId("sidebar-update-ready")).toBeVisible();
		expect(screen.getAllByLabelText("Restart to install update v9.9.9")).not.toHaveLength(0);
	});

	it("asks to restart a staged build from the same tinted pill", async () => {
		updateStatusMock.mockResolvedValue({
			state: "downloaded",
			version: "9.9.9",
			stagedAt: Date.now(),
			escalated: true,
			checksFailing: true,
		});
		renderSidebar();

		const readyRow = await screen.findByTestId("sidebar-update-ready");
		expect(within(readyRow).getByText("Ready to install")).toBeVisible();
		expect(readyRow).not.toHaveTextContent("Restart to finish");
		const pill = readyRow;
		expect(pill).toHaveRole("button");
		expect(pill).toHaveAccessibleName("Restart to install update v9.9.9");
		expect(screen.queryByTestId("sidebar-update-failed")).not.toBeInTheDocument();
		// Sits above the account and Settings rows, not overlaid on them.
		const settings = screen.getAllByRole("button", { name: "Settings" })[0];
		expect(readyRow.compareDocumentPosition(settings) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

		await userEvent.click(pill);
		expect(useUiStore.getState().updateInstallPromptOpen).toBe(true);
	});

	it("keeps the staged restart row up while a background check runs", async () => {
		// Regression: the row keyed off `state`, which a routine check drives
		// through checking/available/not-available while the staged build is
		// untouched, so the row blinked out of existence every 15 minutes on
		// nightly. `staged` is stamped on every status for exactly this reason.
		const stagedAt = Date.now();
		updateStatusMock.mockResolvedValue({
			state: "checking",
			staged: { version: "9.9.9", stagedAt, escalated: false },
		});
		renderSidebar();

		expect(await screen.findAllByLabelText("Restart to install update v9.9.9")).not.toHaveLength(0);
		expect(screen.getByTestId("sidebar-update-ready")).toBeVisible();
	});

	it("names a staged nightly by its base version", async () => {
		updateStatusMock.mockResolvedValue({
			state: "downloaded",
			version: "0.12.11-nightly.202609021713",
			stagedAt: Date.now(),
		});
		renderSidebar();

		const readyRow = await screen.findByTestId("sidebar-update-ready");
		expect(within(readyRow).queryByText(/Nightly|Sep/)).not.toBeInTheDocument();
		expect(screen.getAllByLabelText("Restart to install update v0.12.11")).not.toHaveLength(0);
	});

	it("shows nothing while AO is up to date", async () => {
		updateStatusMock.mockResolvedValue({ state: "idle" });
		renderSidebar();

		await waitFor(() => expect(updateStatusMock).toHaveBeenCalled());
		expect(screen.queryByTestId(/^sidebar-update-/)).not.toBeInTheDocument();
	});

	it("commits a project drop", () => {
		renderSidebar({
			workspaces: [
				{ ...workspace, id: "alpha", name: "Alpha" },
				{ ...workspace, id: "bravo", name: "Bravo" },
			],
		});

		const bravoRow = document.querySelector('[data-project-drag-row][data-project-id="bravo"]')!;
		const alphaTarget = document.querySelector('li[data-project-drop-target][data-project-id="alpha"]')!;
		fireDrag("dragStart", bravoRow, {});
		// jsdom rows measure as zero-height, so clientY 0 lands in the top half — drop before Alpha.
		fireDrag("dragOver", alphaTarget, { clientY: 0 });
		fireDrag("drop", alphaTarget, {});

		expect(Array.from(document.querySelectorAll("[data-project-label]"), (node) => node.textContent)).toEqual(["Bravo", "Alpha"]);
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.project-order") ?? "null")).toEqual(["bravo", "alpha"]);
	});

	it("pins a project to the top from its context menu and unpins it from the row", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{ ...workspace, id: "alpha", name: "Alpha" },
				{ ...workspace, id: "bravo", name: "Bravo" },
			],
		});
		const labels = () => Array.from(document.querySelectorAll("[data-project-label]"), (node) => node.textContent);

		fireEvent.contextMenu(document.querySelector('[data-project-id="bravo"] [data-project-label]')!);
		await user.click(await screen.findByRole("menuitem", { name: "Pin to top" }));

		expect(labels()).toEqual(["Bravo", "Alpha"]);
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.pinned-projects") ?? "null")).toEqual(["bravo"]);
		const unpin = screen.getByRole("button", { name: "Unpin" });
		expect(unpin.closest("[data-project-id]")).toHaveAttribute("data-project-id", "bravo");

		await user.click(unpin);
		expect(labels()).toEqual(["Bravo", "Alpha"]);
		expect(screen.queryByRole("button", { name: "Unpin" })).not.toBeInTheDocument();
		expect(useSidebarOrderStore.getState().pinnedProjectIds).toEqual([]);
	});

	it("does not drop an unpinned project into the pinned group", () => {
		useSidebarOrderStore.setState({ pinnedProjectIds: ["alpha"] });
		renderSidebar({
			workspaces: [
				{ ...workspace, id: "alpha", name: "Alpha" },
				{ ...workspace, id: "bravo", name: "Bravo" },
			],
		});

		const bravoRow = document.querySelector('[data-project-drag-row][data-project-id="bravo"]')!;
		const alphaTarget = document.querySelector('li[data-project-drop-target][data-project-id="alpha"]')!;
		fireDrag("dragStart", bravoRow, {});
		fireDrag("dragOver", alphaTarget, { clientY: 0 });
		fireDrag("drop", alphaTarget, {});

		expect(Array.from(document.querySelectorAll("[data-project-label]"), (node) => node.textContent)).toEqual(["Alpha", "Bravo"]);
	});

	it("keeps the ad hoc group out of the project list and its drag ordering", () => {
		renderSidebar({
			workspaces: [
				{ ...workspace, id: "alpha", name: "Alpha" },
				{ ...workspace, id: "bravo", name: "Bravo" },
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [],
				},
			],
		});
		const labels = () => Array.from(document.querySelectorAll("[data-project-label]"), (node) => node.textContent);

		// The ad hoc group is its own section, never a row in the project list.
		expect(labels()).toEqual(["Alpha", "Bravo"]);
		expect(document.querySelector(`li[data-project-id="${STANDALONE_WORKSPACE_ID}"]`)).toBeNull();
		expect(screen.getByRole("button", { name: "Scratchpad" })).toBeInTheDocument();

		const bravoRow = document.querySelector('[data-project-drag-row][data-project-id="bravo"]')!;
		const alphaTarget = document.querySelector('li[data-project-drop-target][data-project-id="alpha"]')!;
		fireDrag("dragStart", bravoRow, {});
		// jsdom rows measure as zero-height, so clientY 0 lands in the top half — drop before Alpha.
		fireDrag("dragOver", alphaTarget, { clientY: 0 });
		fireDrag("drop", alphaTarget, {});
		expect(labels()).toEqual(["Bravo", "Alpha"]);
	});

	it("commits a session drop within its project", () => {
		renderSidebar({
			workspaces: [{
				...workspace,
				sessions: [
					{ ...session, id: "first", title: "First", updatedAt: "2026-06-30T01:00:00Z" },
					{ ...session, id: "second", title: "Second", updatedAt: "2026-06-30T00:00:00Z" },
				],
			}],
		});

		act(() => dragEnds.get("sidebar-sessions-proj-1")?.({ active: { id: "second" }, over: { id: "first" } }));

		expect(Array.from(document.querySelectorAll('[data-testid="session-list-proj-1"] button[aria-label^="Open "]'), (node) => node.getAttribute("aria-label"))).toEqual([
			"Open Second",
			"Open First",
		]);
	});

	it.each(["light", "dark"] as const)("uses a visible project drop indicator in the %s theme", (theme) => {
		document.documentElement.classList.toggle("dark", theme === "dark");
		try {
			renderSidebar({
				workspaces: [
					{ ...workspace, id: "alpha", name: "Alpha" },
					{ ...workspace, id: "bravo", name: "Bravo" },
				],
			});

			const bravoRow = document.querySelector('[data-project-drag-row][data-project-id="bravo"]')!;
			const alphaTarget = document.querySelector('li[data-project-drop-target][data-project-id="alpha"]')!;
			fireDrag("dragStart", bravoRow, {});
			fireDrag("dragOver", alphaTarget, { clientY: 0 });

			const indicator = document.querySelector("[data-project-drop-line]");
			expect(indicator).not.toBeNull();
			expect(indicator).toHaveClass("bg-foreground");
			expect(indicator).not.toHaveClass("bg-white");
		} finally {
			document.documentElement.classList.remove("dark");
		}
	});
});

describe("resolveNextNavigationAfterSessionKill", () => {
	const ws: WorkspaceSummary = {
		...workspace,
		sessions: [
			{ ...session, id: "s-1", title: "Task 1", updatedAt: "2026-06-30T00:00:00Z" },
			{ ...session, id: "s-2", title: "Task 2", updatedAt: "2026-06-30T01:00:00Z" },
			{ ...session, id: "s-3", title: "Task 3", updatedAt: "2026-06-30T02:00:00Z" },
		],
	};

	it("selects previous session when killing middle session in default order", () => {
		// sortedWorkerSessions orders newest first: [s-3, s-2, s-1]
		const route = resolveNextNavigationAfterSessionKill(ws, "s-2");
		expect(route).toEqual({ target: "session", sessionId: "s-3" });
	});

	it("selects first remaining session when killing the first session", () => {
		const route = resolveNextNavigationAfterSessionKill(ws, "s-3");
		expect(route).toEqual({ target: "session", sessionId: "s-2" });
	});

	it("respects custom display order when provided", () => {
		const customOrder: WorkspaceSession[] = [
			{ ...session, id: "s-1", title: "Task 1" },
			{ ...session, id: "s-2", title: "Task 2" },
			{ ...session, id: "s-3", title: "Task 3" },
		];
		const route = resolveNextNavigationAfterSessionKill(ws, "s-2", customOrder);
		expect(route).toEqual({ target: "session", sessionId: "s-1" });
	});

	it("falls back to active orchestrator if no worker sessions remain", () => {
		const wsWithOrch: WorkspaceSummary = {
			...workspace,
			sessions: [
				{ ...session, id: "s-1", title: "Task 1" },
				{ ...session, id: "orch-1", kind: "orchestrator", isTerminated: false },
			],
		};
		const route = resolveNextNavigationAfterSessionKill(wsWithOrch, "s-1");
		expect(route).toEqual({ target: "session", sessionId: "orch-1" });
	});

	it("falls back to project board if orchestrator is terminated", () => {
		const wsWithDeadOrch: WorkspaceSummary = {
			...workspace,
			sessions: [
				{ ...session, id: "s-1", title: "Task 1" },
				{ ...session, id: "orch-1", kind: "orchestrator", isTerminated: true },
			],
		};
		const route = resolveNextNavigationAfterSessionKill(wsWithDeadOrch, "s-1");
		expect(route).toEqual({ target: "project" });
	});

	it("falls back to project board if workspace is undefined", () => {
		const route = resolveNextNavigationAfterSessionKill(undefined, "s-1");
		expect(route).toEqual({ target: "project" });
	});
});

describe("Sidebar session switch rendering", () => {
	it("re-renders only the rows whose active state changed when switching across projects", () => {
		const noop = () => vi.fn().mockResolvedValue(undefined);
		const p1 = { ...workspace, sessions: [{ ...session, id: "p1-a", title: "one a" }, { ...session, id: "p1-b", title: "one b" }] };
		const p2: WorkspaceSummary = {
			...workspace,
			id: "proj-2",
			name: "Project Two",
			path: "/repo/project-two",
			sessions: [
				{ ...session, id: "p2-a", workspaceId: "proj-2", workspaceName: "Project Two", title: "two a" },
				{ ...session, id: "p2-b", workspaceId: "proj-2", workspaceName: "Project Two", title: "two b" },
			],
		};
		let workspaces = [p1, p2];
		window.localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify([p1.id, p2.id]));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const handlers = {
			onCloneProject: noop(),
			onCreateProject: noop(),
			onInitializeProject: noop(),
			onRemoveProject: noop(),
			onCreateRemoteProject: noop(),
			onInitializeRemoteProject: noop(),
		};
		const tree = () => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<SidebarProvider>
						<Sidebar workspaces={workspaces} {...handlers} />
					</SidebarProvider>
				</TooltipProvider>
			</QueryClientProvider>
		);
		mockParams.projectId = p1.id;
		mockParams.sessionId = "p1-a";
		const view = render(tree());
		expect(screen.getByLabelText("Open two b")).toBeInTheDocument();

		sessionRowRenders.clear();
		mockParams.projectId = p2.id;
		mockParams.sessionId = "p2-a";
		act(() => notifyRouteChange());

		expect([...sessionRowRenders.keys()].sort()).toEqual(["p1-a", "p2-a"]);

		// Opening a session refreshes its project (its viewed state changes). Only
		// the changed row may re-render, not every sortable row in that project.
		const refreshed = { ...p2, sessions: [{ ...p2.sessions[0]!, updatedAt: "2026-07-01T00:00:00Z" }, p2.sessions[1]!] };
		workspaces = [p1, refreshed];
		sessionRowRenders.clear();
		view.rerender(tree());

		expect([...sessionRowRenders.keys()]).toEqual(["p2-a"]);
	});
});

describe("Sidebar active-session reveal", () => {
	const noop = () => vi.fn().mockResolvedValue(undefined);
	const other: WorkspaceSession = { ...session, id: "proj-1-2", title: "second task" };
	const orchestrator: WorkspaceSession = { ...session, id: "proj-1-orc", kind: "orchestrator", title: "Orchestrator" };
	const scratch: WorkspaceSession = {
		...session,
		id: "adhoc-1",
		title: "scratch note",
		workspaceId: STANDALONE_WORKSPACE_ID,
		workspaceName: "Scratchpad",
	};
	const scratchpad = {
		id: STANDALONE_WORKSPACE_ID,
		name: "Scratchpad",
		kind: STANDALONE_PROJECT_KIND,
		path: "",
		sessions: [scratch],
	};

	function mount(expandedProjectIds: string[]) {
		window.localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify(expandedProjectIds));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const tree = () => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<SidebarProvider>
						<Sidebar
							workspaces={[{ ...workspace, sessions: [session, other, orchestrator] }, scratchpad]}
							onCloneProject={noop()}
							onCreateProject={noop()}
							onInitializeProject={noop()}
							onRemoveProject={noop()}
							onCreateRemoteProject={noop()}
							onInitializeRemoteProject={noop()}
						/>
					</SidebarProvider>
				</TooltipProvider>
			</QueryClientProvider>
		);
		const view = render(tree());
		return {
			// Pass null for a projectless route such as /sessions/$sessionId.
			go: (sessionId: string | undefined, projectId: string | null = sessionId ? workspace.id : null) => {
				mockParams.projectId = projectId ?? undefined;
				mockParams.sessionId = sessionId;
				view.rerender(tree());
			},
		};
	}

	it("keeps a closed Scratchpad closed when a project's orchestrator opens", async () => {
		window.localStorage.setItem("ao.sidebar.sections", JSON.stringify({ scratchpad: false }));
		const { go } = mount([workspace.id]);
		expect(screen.getByRole("button", { name: "Scratchpad" })).toHaveAttribute("aria-expanded", "false");

		go(orchestrator.id);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 50));
		});

		expect(screen.getByRole("button", { name: "Scratchpad" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("scratch note")).not.toBeInTheDocument();
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.sections") ?? "null")).toMatchObject({ scratchpad: false });
	});

	it("reopens a closed Scratchpad, not Projects, to reveal an active Scratchpad session", async () => {
		window.localStorage.setItem("ao.sidebar.sections", JSON.stringify({ projects: false, scratchpad: false }));
		const { go } = mount([workspace.id]);

		go(scratch.id, null);

		expect(await screen.findByLabelText("Open scratch note")).toHaveAttribute("aria-current", "page");
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");
	});

	it("expands the collapsed project and scrolls the active row into view when the session changes", async () => {
		const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
		const { go } = mount([]);
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();

		go(other.id);

		const row = await screen.findByLabelText("Open second task");
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute("aria-expanded", "true");
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.expanded-projects") ?? "null")).toEqual([workspace.id]);
		await waitFor(() => expect(scroll).toHaveBeenCalledWith({ block: "nearest" }));
		expect(scroll.mock.contexts.at(-1)).toBe(row);
	});

	it("reopens a closed Projects section to reveal the active session", async () => {
		const user = userEvent.setup();
		const { go } = mount([workspace.id]);
		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");

		go(session.id);

		await waitFor(() => expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "true"));
		expect(await screen.findByLabelText("Open fix login")).toHaveAttribute("aria-current", "page");
	});

	it("does not re-expand a project the user collapses while staying on the same session", async () => {
		const user = userEvent.setup();
		const { go } = mount([workspace.id]);
		go(session.id);
		await screen.findByLabelText("Open fix login");

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();

		go(session.id);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 50));
		});
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
	});
});

// The shell's re-renders no longer reach the memoized Sidebar, so it has to
// follow every store it reads by itself. Each test renders it once with props
// that keep their identity, then changes only the real store.
describe("Memoized Sidebar with unchanged props", () => {
	const handlers = {
		onCloneProject: vi.fn().mockResolvedValue(undefined),
		onCreateProject: vi.fn().mockResolvedValue(undefined),
		onInitializeProject: vi.fn().mockResolvedValue(undefined),
		onRemoveProject: vi.fn().mockResolvedValue(undefined),
		onCreateRemoteProject: vi.fn().mockResolvedValue(undefined),
		onInitializeRemoteProject: vi.fn().mockResolvedValue(undefined),
	};
	const alpha: WorkspaceSummary = {
		...workspace,
		id: "alpha",
		name: "Alpha",
		path: "/repo/alpha",
		sessions: [{ ...session, id: "alpha-1", workspaceId: "alpha", workspaceName: "Alpha", title: "alpha task" }],
	};
	const bravo: WorkspaceSummary = {
		...workspace,
		id: "bravo",
		name: "Bravo",
		path: "/repo/bravo",
		sessions: [{ ...session, id: "bravo-1", workspaceId: "bravo", workspaceName: "Bravo", title: "bravo task" }],
	};

	function newClient() {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(agentReadinessQueryKey, {
			agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
		});
		return client;
	}

	function shell(client: QueryClient, children: React.ReactNode) {
		return (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<SidebarProvider>{children}</SidebarProvider>
				</TooltipProvider>
			</QueryClientProvider>
		);
	}

	afterEach(() => {
		useRemoteUnpinStore.setState({ unpins: {} });
	});

	it("moves a project pinned through the order store to the top, and back", () => {
		const workspaces = [alpha, bravo];
		render(shell(newClient(), <Sidebar workspaces={workspaces} {...handlers} />));
		const labels = () => Array.from(document.querySelectorAll("[data-project-label]"), (node) => node.textContent);
		expect(labels()).toEqual(["Alpha", "Bravo"]);

		act(() => useSidebarOrderStore.getState().setProjectPinned("bravo", true));

		expect(labels()).toEqual(["Bravo", "Alpha"]);
		expect(screen.getByRole("button", { name: "Unpin" }).closest("[data-project-id]")).toHaveAttribute("data-project-id", "bravo");

		act(() => useSidebarOrderStore.getState().setProjectPinned("bravo", false));

		expect(screen.queryByRole("button", { name: "Unpin" })).not.toBeInTheDocument();
	});

	it("opens a collapsed project when the import store reports sessions imported into it", () => {
		const workspaces = [alpha];
		render(shell(newClient(), <Sidebar workspaces={workspaces} {...handlers} />));
		expect(screen.queryByLabelText("Open alpha task")).not.toBeInTheDocument();

		act(() => useSessionImportStore.getState().recordImport("alpha", 1));

		expect(screen.getByLabelText("Open alpha task")).toBeInTheDocument();
		expect(screen.getByLabelText("Toggle Alpha sessions")).toHaveAttribute("aria-expanded", "true");
	});

	it("opens and closes projects in turn, each click seeing the state the last one left", async () => {
		const user = userEvent.setup();
		const workspaces = [alpha, bravo];
		render(shell(newClient(), <Sidebar workspaces={workspaces} {...handlers} />));

		await user.click(screen.getByLabelText("Toggle Alpha sessions"));
		await user.click(screen.getByLabelText("Toggle Bravo sessions"));
		expect(screen.getByLabelText("Open alpha task")).toBeInTheDocument();
		expect(screen.getByLabelText("Open bravo task")).toBeInTheDocument();

		await user.click(screen.getByLabelText("Toggle Alpha sessions"));

		expect(screen.queryByLabelText("Open alpha task")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Open bravo task")).toBeInTheDocument();
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.expanded-projects") ?? "null")).toEqual(["bravo"]);
	});

	it("counts archived sessions from the latest workspaces and opens that archive in place", async () => {
		const user = userEvent.setup();
		useUiStore.setState({ archiveModal: null });
		const client = newClient();
		const view = render(shell(client, <Sidebar workspaces={[alpha]} {...handlers} />));
		fireEvent.contextMenu(document.querySelector('[data-project-id="alpha"] [data-project-label]')!);
		expect(await screen.findByRole("menuitem", { name: "Open dashboard" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /^Archived sessions/ })).not.toBeInTheDocument();
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("menuitem", { name: "Open dashboard" })).not.toBeInTheDocument());

		const archived = { ...alpha, sessions: [...alpha.sessions, { ...alpha.sessions[0]!, id: "alpha-2", title: "old task", kanbanColumn: "archive" as const }] };
		view.rerender(shell(client, <Sidebar workspaces={[archived]} {...handlers} />));
		fireEvent.contextMenu(document.querySelector('[data-project-id="alpha"] [data-project-label]')!);
		await user.click(await screen.findByRole("menuitem", { name: "Archived sessions, 1" }));

		expect(useUiStore.getState().archiveModal).toMatchObject({ projectId: "alpha" });
		// The modal opens over the current page; the menu never goes to the board.
		expect(navigateMock).not.toHaveBeenCalled();
	});

	describe("remote pins through the real remote hook", () => {
		const hosts: RemoteHost[] = [{ hostId: "box-a", label: "Box A", url: "http://box-a:3011", status: "offline" }];
		const savedHostIds = ["box-a"];
		const noLocalWorkspaces: WorkspaceSummary[] = [];
		const pinnedAt = "2026-11-01T00:00:00Z";
		// The host's own project row may list the same session; look only in Pinned.
		const pinnedRow = () => {
			const list = screen.queryByTestId("pinned-session-list");
			return list ? within(list).queryByRole("button", { name: "Open remote task · Box A" }) : null;
		};

		// The shell's wiring: a saved, offline host's cached lists go through
		// useRemoteWorkspaces into the memoized Sidebar.
		function RemoteShell() {
			const remote = useRemoteWorkspaces({ cachedHostIds: savedHostIds });
			return <Sidebar workspaces={noLocalWorkspaces} remoteHosts={hosts} remoteWorkspaces={remote.data} {...handlers} />;
		}

		function renderRemote() {
			const client = newClient();
			client.setQueryData(["remote-workspaces", "box-a", "projects"], [{ id: "remote-project", name: "Remote Project", path: "/remote" }]);
			client.setQueryData(["remote-workspaces", "box-a", "sessions"], [{
				id: "remote-1",
				projectId: "remote-project",
				harness: "codex",
				status: "working",
				prs: [],
				displayName: "remote task",
				isPinned: true,
				pinnedAt,
			}]);
			render(shell(client, <RemoteShell />));
		}

		it("drops a pinned row unpinned here while its host is offline, and restores it when the unpin is cleared", () => {
			renderRemote();
			expect(pinnedRow()).toBeInTheDocument();

			act(() => useRemoteUnpinStore.getState().add("box-a", "remote-1", pinnedAt));

			expect(pinnedRow()).not.toBeInTheDocument();

			act(() => useRemoteUnpinStore.getState().remove([sessionUiKey("remote-1", "box-a")]));

			expect(pinnedRow()).toBeInTheDocument();
		});

		it("drops a pinned remote row while its kill is in flight, before the host's lists change", () => {
			renderRemote();
			expect(pinnedRow()).toBeInTheDocument();
			try {
				act(() => trackOptimisticSessionKill("remote-1", "box-a"));

				expect(pinnedRow()).not.toBeInTheDocument();
			} finally {
				act(() => clearOptimisticSessionKill("remote-1", "box-a"));
			}
			expect(pinnedRow()).toBeInTheDocument();
		});
	});
});
