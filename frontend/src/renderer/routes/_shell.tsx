import { apiErrorException } from "../lib/api-error-exception";
import { useWindowZoomFactor } from "../hooks/useWindowZoomFactor";
import { useAutoSpawnPauseNotice } from "../hooks/useAutoSpawnPauseNotice";
import { AppBrowserLinkContext } from "../components/AppLink";
import { useSessionBrowserLink } from "../hooks/useSessionBrowserLink";
import { CatchBoundary, createFileRoute, Outlet, useMatch, useMatchRoute, useNavigate, useParams, useRouter, useRouterState } from "@tanstack/react-router";
import { isCancelledError, useQueryClient } from "@tanstack/react-query";
import { lazy, memo, Suspense, type ComponentProps, type CSSProperties, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { FolderPlus } from "@/components/app-icons";
import { motion, useReducedMotion } from "motion/react";
import { useTranslation } from "react-i18next";
import { SHELL_PANEL_SPRING } from "@/lib/motion-spring";
import { CommandPalette } from "../components/CommandPalette";
import { useLatestCallback } from "../hooks/useStable";
import { isRemoveTarget, useRemoveProject } from "../hooks/useRemoveProject";
import { clearOptimisticProjectRemoval } from "../hooks/optimistic-project-removals";
import { CenterPanelShell } from "../components/CenterPanelShell";
import { DaemonFailureBanner } from "../components/DaemonFailureBanner";
import { DaemonStartupLoader } from "../components/DaemonStartupLoader";
import { NotificationRuntime } from "../components/NotificationCenter";
import { HarnessUpdateNotice } from "../components/HarnessUpdateNotice";
import { TrayRuntime } from "../components/TrayRuntime";
import { SharedWithYouNotifier } from "../components/teams/SharedWithYouNotifier";
import { NewTaskNavigator } from "../components/NewTaskNavigator";
import { ArchiveModalHost } from "../components/ArchiveModalHost";
import { GlobalToast } from "../components/GlobalToast";
import { SettingsPane, SettingsProvider, useSettingsPage } from "../components/SettingsDialog";
import { ShareLinkHost } from "../components/OpenShareLinkDialog";
import { KeyboardShortcutsDialog } from "../components/KeyboardShortcutsDialog";
import { QuitConfirmDialog } from "../components/QuitConfirmDialog";
import { PageTopbar } from "../components/PageTopbar";
import { SessionTopbarProvider } from "../components/SessionTopbarPortal";
import { OrchestratorReplacementDialog } from "../components/OrchestratorReplacementDialog";
import { RestartToUpdateDialog } from "../components/RestartToUpdateDialog";
import { SessionImportDialog } from "../components/SessionImportDialog";
import { Sidebar } from "../components/Sidebar";
import type { CreateProjectInput } from "../components/CreateProjectFlow";
import { useRemoteHosts } from "../hooks/useRemoteHosts";
import { SidebarProvider } from "../components/ui/sidebar";
import { TitlebarNav, useCanGoForward } from "../components/TitlebarNav";
import { SwipeNavigation } from "../components/SwipeNavigation";
import { WindowTitlebar } from "../components/WindowTitlebar";
import { preloadXtermTerminal, TerminalCacheProvider } from "../components/TerminalPane";
import { scheduleIdle } from "../lib/idle";
import { LazyChunkBoundary, LazyChunkErrorPanel } from "../components/LazyChunkBoundary";
import { loadedSessionView, loadSessionView } from "../lib/session-view-chunk";
import type { SessionView as LoadedSessionView } from "../components/SessionView";
import { agentModelsQueryOptions } from "../hooks/useAgentModelsQuery";
import { useDaemonStatus } from "../hooks/useDaemonStatus";
import { useOpenShellTerminal } from "../hooks/useShellTerminals";
import { useDocumentFlag } from "../hooks/useElementFlag";
import { useWindowFullScreen } from "../hooks/useWindowFullScreen";
import { useRemoteWorkspaces, useWorkspacePolling, useWorkspaceQuery, workspaceQueryKey, toWorkspaceSession, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { apiClient, apiErrorCode, apiErrorDetails, apiErrorMessage, apiErrorRequestId, hasTrustedApiBaseUrl } from "../lib/api-client";
import { cachedCloudBillingMode } from "../lib/cloud-offering";
import { resolveCssColorToHex } from "../lib/css-color";
import { refreshDaemonStatus } from "../lib/daemon-status";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { addRendererExceptionStep, captureRendererEvent, captureRendererException } from "../lib/telemetry";
import { captureProjectAddFailed } from "../lib/project-lifecycle-telemetry";
import { ShellProvider } from "../lib/shell-context";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { captureOrchestratorReplacementFailure } from "../lib/orchestrator-replacement-telemetry";
import { applyDocumentTheme, applyDocumentThemeStyle } from "../lib/theme";
import { applyWindowTranslucency } from "../lib/window-translucency";
import { aoBridge } from "../lib/bridge";
import { handleModifierLinkClick } from "../lib/external-link-policy";
import { recordProjectOpened } from "../lib/project-history";
import { cn } from "../lib/utils";
import {
	isLinuxPlatform,
	isMacPlatform,
	isWindowsPlatform,
	hidesShellTopbar,
} from "../lib/platform";
import { sidebarIsVisible, sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, hasConfiguredOrchestratorAgent, newestActiveOrchestrator, isScratchpadWorkspace, sessionIsActive, STANDALONE_WORKSPACE_ID, toProjectKind, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import type { components } from "../../api/schema";
import { parseArtifactLink, parseSessionLink, resolveSessionLink } from "../lib/session-links";
import { useAgentInventoryTelemetry, useSetupCheckedTelemetry } from "../hooks/useAgentInventoryTelemetry";
import { useSessionSwitchSnap } from "../hooks/useSessionSwitchSnap";
import { useHeldSession } from "../hooks/useHeldSession";
import { useLinkThisComputerOnSignIn } from "../hooks/useLinkThisComputerOnSignIn";
import { remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { clientForHost } from "../lib/host-clients";
import { useCloudSession } from "../lib/cloud-session";
import { useCloudAccountSwitchReset } from "../hooks/useCloudAccountSwitchReset";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { sessionUiKey } from "../lib/hosts";
import { pageHasTopbar } from "../lib/shell-topbar";
import { forgetRemoteHost, syncRemoteCache } from "../lib/remote-chat-cache";
import { watchRemoteUnpins } from "../lib/remote-session-unpins";
import { useRemoteUnpinStore } from "../stores/remote-unpin-store";
import { buildSidebarSessionOrder, sessionWantsAttention, stepSessionNav, type SidebarNavSession } from "../lib/sidebar-order";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useAppMenuAvailable } from "../hooks/useAppMenuCommand";
import { onDedicatedShortcut, useAppShortcut } from "../hooks/useAppShortcut";
import { recordManualWorkerOpen, recordSessionSurface } from "../lib/session-management-telemetry";

const SHELL_LOADER_STALE_MS = 2_000;

const loadOrchestratorStartingChat = () => import("../components/chat/OrchestratorStartingChat");
const OrchestratorStartingChat = lazy(() =>
	loadOrchestratorStartingChat().then((module) => ({ default: module.OrchestratorStartingChat })),
);

const EMPTY_PAUSE_PROJECTS: never[] = [];
function preloadOrchestratorStartingChat(): void {
	void loadOrchestratorStartingChat().catch(() => undefined);
}
const LazySessionView = lazy(() => loadSessionView().then((module) => ({ default: module.SessionView })));
function SessionView(props: ComponentProps<typeof LoadedSessionView>) {
	const [Loaded] = useState(() => loadedSessionView()?.SessionView);
	return Loaded ? <Loaded {...props} /> : <LazySessionView {...props} />;
}
function preloadSessionView(): void {
	void loadSessionView().catch(() => undefined);
}

const LAST_PROJECT_KEY = "ao.newTask.lastProject";

function lastProjectId(workspaces: WorkspaceSummary[]): string | undefined {
	let id: string | null = null;
	try {
		id = localStorage.getItem(LAST_PROJECT_KEY);
	} catch {
		return undefined;
	}
	return workspaces.some((workspace) => workspace.id === id && !isScratchpadWorkspace(workspace)) ? id ?? undefined : undefined;
}

export const Route = createFileRoute("/_shell")({
	loader: async ({ context }) => {
		await refreshDaemonStatus().catch(() => undefined);
		if (!usesPreviewWorkspaceData && !hasTrustedApiBaseUrl()) return;
		return context.queryClient
			.fetchQuery({ ...workspaceQueryOptions, staleTime: SHELL_LOADER_STALE_MS })
			.catch((error: unknown) => {
				if (isCancelledError(error)) return undefined;
				throw error;
			});
	},
	component: ShellLayoutWithSettings,
});

function ShellLayoutWithSettings() {
	return (
		<SettingsProvider>
			<ShellLayout />
		</SettingsProvider>
	);
}

function ShellOutlet({ startingChat }: { startingChat: ReactNode }) {
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	return (
		<>
			<div className={cn("relative flex min-h-0 flex-1 flex-col", settingsOpen && "hidden")}>
				<div className={startingChat ? "absolute inset-0 invisible pointer-events-none" : "contents"} inert={startingChat ? true : undefined}><Outlet /><LocalSessionHost /></div>
				{startingChat}
			</div>
			<SettingsPane />
			<ShareLinkHost />
		</>
	);
}

function captureSessionViewCrash(error: Error) {
	void captureRendererException(error, { source: "react-error-boundary", operation: "session_view" });
}

function LocalSessionHost() {
	const scratchpadSessionId = useMatch({
		from: "/_shell/sessions/$sessionId",
		shouldThrow: false,
		select: (match) => match.params.sessionId,
	});
	const projectSession = useMatch({
		from: "/_shell/projects/$projectId_/sessions/$sessionId",
		shouldThrow: false,
		select: (match) => match.params,
	});
	const { org } = useCloudOrg();
	const cloudOrgId = org?.id;
	const liveSessionId = projectSession?.sessionId ?? scratchpadSessionId;
	const liveProjectId = projectSession?.projectId;
	const { sessionId, projectId } = useHeldSession(liveSessionId, liveProjectId);
	const view = useMemo(
		() => (sessionId ? <SessionView cloudOrgId={cloudOrgId} projectId={projectId} sessionId={sessionId} /> : null),
		[cloudOrgId, projectId, sessionId],
	);
	if (!sessionId) return null;
	return (
		<CatchBoundary errorComponent={LazyChunkErrorPanel} getResetKey={() => sessionId} onCatch={captureSessionViewCrash}>
			<Suspense fallback={null}>{view}</Suspense>
		</CatchBoundary>
	);
}

function errorMessage(error: unknown) {
	return error instanceof Error ? error.message : "Could not load projects";
}

function normalizeProjectPath(path: string): string {
	if (!path) return path;
	const trimmed = path.trim().replace(/[\\/]+$/, "");
	return trimmed === "" ? path : trimmed;
}

function findRegisteredWorkspaceByPath(workspaces: WorkspaceSummary[], path: string): WorkspaceSummary | undefined {
	const normalizedPath = normalizeProjectPath(path);
	return workspaces.find((workspace) => normalizeProjectPath(workspace.path) === normalizedPath);
}

type CreateProjectConfigInput = {
	workerAgent: string;
	orchestratorAgent: string;
	reviewerAgent?: string;
	trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
	defaultBranch?: string;
};

export function createProjectConfig(input: CreateProjectConfigInput): components["schemas"]["ProjectConfig"] {
	return {
		...(input.defaultBranch ? { defaultBranch: input.defaultBranch } : {}),
		worker: { agent: input.workerAgent as components["schemas"]["RoleOverride"]["agent"] },
		orchestrator: { agent: input.orchestratorAgent as components["schemas"]["RoleOverride"]["agent"] },
		...(input.reviewerAgent ? { reviewers: [{ harness: input.reviewerAgent }] } : {}),
		...(input.trackerIntake ? { trackerIntake: input.trackerIntake } : {}),
	};
}

const PROVISIONING_TIMEOUT_MS = 120_000;

const isMac = isMacPlatform();
const isWindows = isWindowsPlatform();
const isLinux = isLinuxPlatform();
const macTrafficLightBand = hidesShellTopbar();

const ShellCenter = memo(function ShellCenter({
	isSessionRoute,
	isHomeRoute,
	isNewChatRoute,
	selfFramedCenterPanel,
	startingOrchestrator,
	startingCloudOrchestrator,
	startingSteps,
	startingCoderWorkspaceUrl,
	startingHarness,
}: {
	isSessionRoute: boolean;
	isHomeRoute: boolean;
	isNewChatRoute: boolean;
	selfFramedCenterPanel: boolean;
	startingOrchestrator: boolean;
	startingCloudOrchestrator: boolean;
	startingSteps?: WorkspaceSession["provisionSteps"];
	startingCoderWorkspaceUrl?: string;
	startingHarness?: string;
}) {
	const panelClassName = isSessionRoute || startingOrchestrator ? "center-panel-shell--session" : undefined;
	const newChatSending = useUiStore((state) => state.newChatSending);
	const startingChat = startingOrchestrator ? <LazyChunkBoundary><Suspense fallback={null}><OrchestratorStartingChat steps={startingSteps} cloud={startingCloudOrchestrator} coderWorkspaceUrl={startingCoderWorkspaceUrl} harness={startingHarness} /></Suspense></LazyChunkBoundary> : null;
	const selfFramedOutlet = <>
		<div className={startingOrchestrator ? "absolute inset-0 invisible pointer-events-none" : "contents"} inert={startingOrchestrator || undefined}><Outlet /></div>
		{startingChat ? <CenterPanelShell notifications>{startingChat}</CenterPanelShell> : null}
	</>;
	if (selfFramedCenterPanel) return selfFramedOutlet;
	return (
		<CenterPanelShell
			className={panelClassName}
			notifications={!isHomeRoute || startingOrchestrator}
			topbar={pageHasTopbar({ isHomeRoute, isNewChatRoute: isNewChatRoute && !newChatSending, startingOrchestrator })}
		>
			<ShellOutlet startingChat={startingChat} />
		</CenterPanelShell>
	);
});

function ShellLayout() {
	useAgentInventoryTelemetry();
	useSetupCheckedTelemetry();
	useSessionSwitchSnap();
	useLinkThisComputerOnSignIn();
	const { t } = useTranslation();
	const navigate = useNavigate();
	const router = useRouter();
	const canGoForward = useCanGoForward();
	const settingsPage = useSettingsPage();
	const matchRoute = useMatchRoute();
	const queryClient = useQueryClient();
	const { client: cloudClient } = useCloudCp();
	const { org: cloudOrg } = useCloudOrg();
	useWorkspacePolling();
	const workspaceQuery = useWorkspaceQuery();
	const workspaces = workspaceQuery.data ?? [];
	const { hosts: remoteHosts, listed: remoteHostsListed, settled: remoteHostsSettled, retryNow: retryRemoteHostsNow } = useRemoteHosts();
	const remoteHostIdsKey = remoteHosts.map((host) => host.hostId).join("\0");
	const remoteHostIds = useMemo(() => remoteHostIdsKey ? remoteHostIdsKey.split("\0") : [], [remoteHostIdsKey]);
	useEffect(() => {
		if (!remoteHostsListed) return;
		void syncRemoteCache(queryClient, remoteHostIds);
		useRemoteUnpinStore.getState().keepHosts(remoteHostIds);
	}, [queryClient, remoteHostIds, remoteHostsListed]);
	useEffect(() => watchRemoteUnpins(queryClient), [queryClient]);
	const refusedRemoteHostKey = remoteHosts.filter((host) => host.failureReason === "unauthorized").map((host) => host.hostId).join("\0");
	useEffect(() => {
		for (const hostId of refusedRemoteHostKey ? refusedRemoteHostKey.split("\0") : []) void forgetRemoteHost(queryClient, hostId);
	}, [queryClient, refusedRemoteHostKey]);
	const { data: remoteWorkspaces, failedHostIds: remoteFailedHostIds, loadedProjectHostIds: remoteLoadedProjectHostIds, loadedSessionHostIds: remoteLoadedSessionHostIds } = useRemoteWorkspaces({ cachedHostIds: remoteHostIds });
	useAutoSpawnPauseNotice([...(workspaceQuery.data ?? EMPTY_PAUSE_PROJECTS), ...(remoteWorkspaces ?? EMPTY_PAUSE_PROJECTS)]);
	const workspacesRef = useRef(workspaces);
	workspacesRef.current = workspaces;
	const remoteWorkspacesRef = useRef(remoteWorkspaces);
	remoteWorkspacesRef.current = remoteWorkspaces;
	const daemonStatus = useDaemonStatus(queryClient);
	const daemonIsReady = daemonStatus.state === "ready";
	useEffect(() => {
		if (!daemonIsReady) return;
		return scheduleIdle(() => {
			preloadXtermTerminal();
			preloadOrchestratorStartingChat();
			preloadSessionView();
		});
	}, [daemonIsReady]);
	const [workspaceStartupState, setWorkspaceStartupState] = useState<"loading" | "ready" | "error">("loading");
	const workspaceStartupBaselineRef = useRef(0);
	const sidebarDragStripRef = useRef<HTMLDivElement>(null);
	const centerPaneRef = useRef<HTMLDivElement>(null);
	const prefersReducedMotion = useReducedMotion();
	const themePreference = useUiStore((state) => state.themePreference);
	const firstRunPromptActive = useUiStore((state) => state.firstRunPromptActive);
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const themeStyle = useUiStore((state) => state.themeStyle);
	const windowTranslucency = useUiStore((state) => state.windowTranslucency);
	const developerMode = useUiStore((state) => state.developerMode);
	const chatHibernationSyncRef = useRef<Promise<void>>(Promise.resolve());
	const isSidebarOpen = useUiStore(sidebarIsVisible);
	const toggleSidebar = useUiStore((state) => state.toggleSidebar);
	const sidebarHasLayout = useUiStore(sidebarOccupiesLayout);
	const [sidebarWidthPx, setSidebarWidthPx] = useState<number | null>(null);
	const syncSystemTheme = useUiStore((state) => state.syncSystemTheme);
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const openProjectSettings = useUiStore((state) => state.openProjectSettings);
	const requestCreateProjectFromPath = useUiStore((state) => state.requestCreateProjectFromPath);
	const requestNewShellTerminal = useUiStore((state) => state.requestNewShellTerminal);
	const newShellTerminalNonce = useUiStore((state) => state.newShellTerminalNonce);
	const setActiveShellTerminal = useUiStore((state) => state.setActiveShellTerminal);
	const isFullScreen = useWindowFullScreen();
	useDocumentFlag("data-shell-windows", isWindows);
	useDocumentFlag("data-shell-linux", isLinux);
	const windowZoomFactor = useWindowZoomFactor();
	const [trafficLightDragActive, setTrafficLightDragActive] = useState(isMac);
	const leftFullScreenRef = useRef(false);
	useEffect(() => {
		if (!isMac) return;
		if (isFullScreen) {
			leftFullScreenRef.current = true;
			setTrafficLightDragActive(false);
			return;
		}
		if (!leftFullScreenRef.current) {
			setTrafficLightDragActive(true);
			return;
		}
		const reducedMotion =
			typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		if (reducedMotion) {
			setTrafficLightDragActive(true);
			return;
		}
		const timer = window.setTimeout(() => setTrafficLightDragActive(true), 200);
		return () => window.clearTimeout(timer);
	}, [isFullScreen]);
	const handledShellNonceRef = useRef(newShellTerminalNonce);
	const [isKeyboardShortcutsOpen, setIsKeyboardShortcutsOpen] = useState(false);
	const customizeKeyboardShortcuts = useCallback(() => {
		setIsKeyboardShortcutsOpen(false);
		useUiStore.getState().openGlobalSettings("shortcuts", { preserveProject: true });
	}, []);
	const routeParams = useParams({ strict: false }) as { hostId?: string; projectId?: string; sessionId?: string };
	const openShellTerminal = useOpenShellTerminal(routeParams.hostId);
	const routeParamsRef = useRef(routeParams);
	routeParamsRef.current = routeParams;
	const remoteHostsEnabled = useUiStore((state) => state.remoteHosts);
	const { status: accountStatus, session: cloudAccount } = useCloudSession();
	useEffect(() => {
		if ((!remoteHostsEnabled || accountStatus === "unauthenticated") && routeParams.hostId) void navigate({ to: "/", replace: true });
	}, [accountStatus, navigate, remoteHostsEnabled, routeParams.hostId]);
	useCloudAccountSwitchReset({
		userId: cloudAccount?.user.id ?? null,
		ready: accountStatus !== "loading",
		queryClient,
		onLocalPage: !routeParams.projectId ||
			workspaces.some((workspace) => workspace.id === routeParams.projectId && workspace.kind !== CLOUD_PROJECT_KIND),
		leavePage: () => void navigate({ to: "/", replace: true }),
	});
	const linkSession = (routeParams.hostId ? remoteWorkspaces ?? [] : workspaces)
		.flatMap((workspace) => workspace.sessions)
		.find((session) => session.id === routeParams.sessionId && session.hostId === routeParams.hostId);
	const openBrowserLink = useSessionBrowserLink(linkSession);
	const canOpenBrowserLink = linkSession?.kind === "worker" && sessionIsActive(linkSession);
	useEffect(() => {
		document.addEventListener("click", handleModifierLinkClick);
		return () => document.removeEventListener("click", handleModifierLinkClick);
	}, []);
	const [isDragActive, setIsDragActive] = useState(false);
	const dragDepthRef = useRef(0);
	useEffect(() => {
		const isFileDrag = (event: DragEvent) => Array.from(event.dataTransfer?.types ?? []).includes("Files");
		const firstEntryIsDirectory = (event: DragEvent) => {
			const item = event.dataTransfer?.items?.[0];
			return item?.webkitGetAsEntry?.()?.isDirectory ?? false;
		};
		const handleDragEnter = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			event.preventDefault();
			dragDepthRef.current += 1;
			if (dragDepthRef.current === 1 && firstEntryIsDirectory(event)) setIsDragActive(true);
		};
		const handleDragOver = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			event.preventDefault();
			if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
		};
		const handleDragLeave = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			dragDepthRef.current = Math.max(0, dragDepthRef.current - 1);
			if (dragDepthRef.current === 0) setIsDragActive(false);
		};
		const handleDrop = (event: DragEvent) => {
			if (!isFileDrag(event)) return;
			event.preventDefault();
			dragDepthRef.current = 0;
			setIsDragActive(false);
			const item = event.dataTransfer?.items?.[0];
			if (!item?.webkitGetAsEntry?.()?.isDirectory) return;
			const file = item.getAsFile();
			if (!file) return;
			const path = aoBridge.app.getPathForFile(file);
			if (path) requestCreateProjectFromPath(path);
		};
		window.addEventListener("dragenter", handleDragEnter);
		window.addEventListener("dragover", handleDragOver);
		window.addEventListener("dragleave", handleDragLeave);
		window.addEventListener("drop", handleDrop);
		return () => {
			window.removeEventListener("dragenter", handleDragEnter);
			window.removeEventListener("dragover", handleDragOver);
			window.removeEventListener("dragleave", handleDragLeave);
			window.removeEventListener("drop", handleDrop);
		};
	}, [requestCreateProjectFromPath]);
	const scopedProjectId = routeParams.hostId ? undefined : routeParams.projectId
		? routeParams.projectId
		: routeParams.sessionId
			? workspaces.find((workspace) => workspace.sessions.some((session) => session.id === routeParams.sessionId))?.id
			: undefined;
	const scopedIsProject = Boolean(scopedProjectId) && workspaces.some(
		(workspace) => workspace.id === scopedProjectId && !isScratchpadWorkspace(workspace),
	);
	useEffect(() => {
		if (!scopedIsProject || !scopedProjectId || scopedProjectId === STANDALONE_WORKSPACE_ID) return;
		try {
			localStorage.setItem(LAST_PROJECT_KEY, scopedProjectId);
		} catch {
		}
	}, [scopedIsProject, scopedProjectId]);
	const scopedSession = routeParams.sessionId
		? (routeParams.hostId ? remoteWorkspaces : workspaces)
			.flatMap((workspace) => workspace.sessions)
			.find((session) => session.id === routeParams.sessionId)
		: undefined;
	useEffect(() => {
		recordSessionSurface(
			scopedSession?.kind === "orchestrator" || scopedSession?.kind === "worker"
				? { kind: scopedSession.kind, sessionId: sessionUiKey(scopedSession.id, routeParams.hostId) }
				: null,
		);
	}, [routeParams.hostId, scopedSession?.id, scopedSession?.kind]);
	useEffect(() => {
		if (!scopedProjectId || scopedProjectId === STANDALONE_WORKSPACE_ID) return;
		const projectQueryKey = ["project", scopedProjectId];
		void queryClient
			.prefetchQuery({
				queryKey: projectQueryKey,
				queryFn: async () => {
					const { data, error: apiError } = await apiClient.GET("/api/v1/projects/{id}", {
						params: { path: { id: scopedProjectId } },
					});
					if (apiError) throw apiErrorException(apiError);
					if (data?.status !== "ok") throw new Error("Project config unavailable");
					return data.project as components["schemas"]["Project"];
				},
			})
			.then(() => {
				const project = queryClient.getQueryData<components["schemas"]["Project"]>(projectQueryKey);
				const defaultWorkerAgent = project?.config?.worker?.agent || project?.agent || "";
				if (defaultWorkerAgent) {
					void queryClient.prefetchQuery(agentModelsQueryOptions(defaultWorkerAgent, ""));
				}
			});
	}, [queryClient, scopedProjectId]);
	const isHomeRoute = Boolean(matchRoute({ to: "/" }));
	const isNewChatRoute = Boolean(matchRoute({ to: "/new" }));
	const newChatHost = useRouterState({
		select: (state) => (isNewChatRoute ? (state.location.search as { host?: string }).host : undefined),
	});
	const newChatProject = useRouterState({
		select: (state) => (isNewChatRoute ? (state.location.search as { project?: string }).project : undefined),
	});
	useEffect(() => {
		if (!routeParams.hostId && routeParams.projectId) recordProjectOpened(routeParams.projectId);
	}, [routeParams.hostId, routeParams.projectId]);
	const isSettingsRoute =
		Boolean(matchRoute({ to: "/settings", fuzzy: true })) ||
		Boolean(matchRoute({ to: "/projects/$projectId/settings", fuzzy: true }));
	const selfFramedCenterPanel = isSettingsRoute;
	const trafficLightBand = isHomeRoute || selfFramedCenterPanel || macTrafficLightBand;
	const restartingProjectIds = useUiStore((state) => state.restartingProjectIds);
	const setProjectRestarting = useUiStore((state) => state.setProjectRestarting);
	const orchestratorReplacementErrors = useUiStore((state) => state.orchestratorReplacementErrors);
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const setOrchestratorStartupError = useUiStore((state) => state.setOrchestratorStartupError);
	const setProjectProvisioning = useUiStore((state) => state.setProjectProvisioning);
	const openingOrchestrator = useUiStore((state) =>
		state.projectCreationPending || Boolean(
			routeParams.projectId && state.provisioningProjectIds.has(sessionUiKey(routeParams.projectId, routeParams.hostId)),
		),
	);
	const startingWorkspace = workspaces.find((workspace) => workspace.id === routeParams.projectId);
	const startingOrchestratorSession = openingOrchestrator
		? startingWorkspace?.sessions.find((session) => session.kind === "orchestrator")
		: undefined;
	const startingSteps = startingOrchestratorSession?.provisionSteps;
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const replacementErrorProjectId = Object.keys(orchestratorReplacementErrors)[0] ?? null;
const replacementErrorCount = Object.keys(orchestratorReplacementErrors).length;
	const isStartupLoading =
		!usesPreviewWorkspaceData &&
		!daemonStatus.code &&
		(daemonStatus.state !== "ready" || workspaceStartupState === "loading" || (!workspaceQuery.isSuccess && !workspaceQuery.isError));
	const workspaceShown = !isStartupLoading && !daemonStatus.code && workspaceQuery.data !== undefined;
	useEffect(() => {
		if (!workspaceShown) return;
		let frame = requestAnimationFrame(() => {
			frame = requestAnimationFrame(() => aoBridge.app.reportSessionsShown());
		});
		return () => cancelAnimationFrame(frame);
	}, [workspaceShown]);
	useEffect(() => {
		if (!isMac || !sidebarHasLayout || isStartupLoading) return;
		let observer: ResizeObserver | undefined;
		const frame = requestAnimationFrame(() => {
			const el = document.querySelector<HTMLElement>('[data-slot="sidebar-container"]');
			if (!el) return;
			observer = new ResizeObserver(([entry]) => setSidebarWidthPx(entry.target.getBoundingClientRect().width));
			observer.observe(el);
		});
		return () => {
			cancelAnimationFrame(frame);
			observer?.disconnect();
		};
	}, [sidebarHasLayout, isStartupLoading]);
	const sidebarSessionOrder = useCallback(() => {
		const { projectOrder, pinnedProjectIds, sessionOrders } = useSidebarOrderStore.getState();
		return buildSidebarSessionOrder({
			workspaces: workspacesRef.current,
			remoteWorkspaces: remoteWorkspacesRef.current,
			projectOrder,
			pinnedProjectIds,
			sessionOrders,
		});
	}, []);
	const openNavSession = useCallback(
		(entry: SidebarNavSession) => {
			if (entry.session.kind === "worker") recordManualWorkerOpen(sessionUiKey(entry.sessionId, entry.hostId));
			void navigate(sessionNavigateTarget(entry.projectId, entry.sessionId, entry.hostId));
		},
		[navigate],
	);
	const navigateSession = useCallback(
		(direction: -1 | 1, onlyNeedingAttention = false) => {
			if (useUiStore.getState().settingsModal?.scope === "global") return;
			const target = stepSessionNav(
				sidebarSessionOrder(),
				{ sessionId: routeParams.sessionId, hostId: routeParams.hostId },
				direction,
				onlyNeedingAttention ? (entry) => sessionWantsAttention(entry.session) : undefined,
			);
			if (target) {
				openNavSession(target);
			} else if (onlyNeedingAttention) {
				useUiStore.getState().showGlobalToast(t("shortcut.noAttentionSessions"));
			}
		},
		[openNavSession, routeParams.hostId, routeParams.sessionId, sidebarSessionOrder, t],
	);

	const updateWorkspaces = useCallback(
		(updater: (workspaces: WorkspaceSummary[]) => WorkspaceSummary[]) => {
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = []) => updater(current));
		},
		[queryClient],
	);

	const provisionOrchestrator = useCallback(
		async (
			workspace: WorkspaceSummary,
			input: CreateProjectConfigInput,
			source: "project_add" | "project_clone",
		) => {
		const provisioningGuard = window.setTimeout(() => {
			if (!useUiStore.getState().provisioningProjectIds.has(workspace.id)) return;
			setProjectProvisioning(workspace.id, false);
			setOrchestratorStartupError(
				workspace.id,
				"Project added, but the orchestrator is taking longer than expected to start. Retry from the board if it does not appear.",
			);
		}, PROVISIONING_TIMEOUT_MS);
		try {
			const {
				data: spawnData,
				error: spawnError,
				response: spawnResponse,
			} = await apiClient.POST("/api/v1/sessions", {
				body: {
					projectId: workspace.id,
					kind: "orchestrator",
					harness: input.orchestratorAgent as components["schemas"]["SpawnSessionRequest"]["harness"],
					async: true,
				},
			});
			if (spawnError || !spawnData?.session?.id) {
				const message = spawnError
					? apiErrorMessage(spawnError, `Failed to spawn orchestrator (${spawnResponse.status})`)
					: `Failed to spawn orchestrator (${spawnResponse.status})`;
				throw new Error(message);
			}
			void captureRendererEvent("ao.orchestrators.spawned", {
				project_id: workspace.id,
				source,
			});
			const sessionId = spawnData.session.id;
			await queryClient.cancelQueries({ queryKey: workspaceQueryKey });
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = []) =>
				current.map((item) => item.id === workspace.id
					? { ...item, sessions: [toWorkspaceSession(spawnData.session, { id: workspace.id, name: workspace.name }), ...item.sessions.filter((session) => session.id !== sessionId)] }
					: item),
			);
			const current = routeParamsRef.current;
			if (current.projectId === workspace.id && !current.sessionId && !current.hostId) {
				await navigate({
					to: "/projects/$projectId/sessions/$sessionId",
					params: { projectId: workspace.id, sessionId },
				}).catch(() => undefined);
			}
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(workspace.id, false);
		} catch (spawnError) {
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(workspace.id, false);
			void captureRendererEvent("ao.orchestrators.spawn_failed", {
				project_id: workspace.id,
				source,
			});
			const message = spawnError instanceof Error ? spawnError.message : "Could not start orchestrator";
			const startupMessage = `Project added, but orchestrator did not start: ${message}`;
			setOrchestratorStartupError(workspace.id, startupMessage);
		}
	},
	[navigate, queryClient, setOrchestratorStartupError, setProjectProvisioning],
);

	const completeProjectCreation = useCallback(
		async (
			project: components["schemas"]["Project"],
			input: CreateProjectConfigInput,
			source: "project_add" | "project_clone",
		) => {
			const workspace: WorkspaceSummary = {
				id: project.id,
				name: project.name,
				kind: toProjectKind(project.kind),
				path: project.path,
				workspaceRepos: project.workspaceRepos,
				type: "main",
				orchestratorAgent: input.orchestratorAgent as WorkspaceSummary["orchestratorAgent"],
				sessions: [],
			};
			void captureRendererEvent(source === "project_add" ? "ao.projects.added" : "ao.renderer.project_clone_succeeded", { project_id: workspace.id });
			await queryClient.cancelQueries({ queryKey: workspaceQueryKey });
			clearOptimisticProjectRemoval(workspace.id);
			useSidebarOrderStore.getState().moveProjectToTop(workspace.id);
			updateWorkspaces((current) => [workspace, ...current.filter((item) => item.id !== workspace.id)]);
			setOrchestratorStartupError(workspace.id, null);
			setProjectProvisioning(workspace.id, true);
			await navigate({ to: "/projects/$projectId", params: { projectId: workspace.id } });
			void provisionOrchestrator(workspace, input, source);
		},
		[navigate, provisionOrchestrator, queryClient, setOrchestratorStartupError, setProjectProvisioning, updateWorkspaces],
	);

	const createProject = useCallback(
		async (input: {
			path: string;
			workerAgent: string;
			orchestratorAgent: string;
			reviewerAgent?: string;
			trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
			asWorkspace?: boolean;
			clonePreparationId?: string;
		}) => {
			void addRendererExceptionStep("Project add requested", {
				source: "project-add",
				operation: "project_add",
				surface: "project_board",
			});
			const reportFailure = (code: string | undefined) => captureProjectAddFailed(
				(event, properties) => { void captureRendererEvent(event, properties); },
				code,
			);
			const status = await refreshDaemonStatus();
			if (status.state !== "ready" || !status.port) {
				reportFailure("DAEMON_NOT_READY");
				throw new Error(status.message || "Daemon is not ready.");
			}
			const { data, error } = await apiClient.POST("/api/v1/projects", {
				body: {
					path: input.path,
					asWorkspace: input.asWorkspace || undefined,
					clonePreparationId: input.clonePreparationId,
					config: createProjectConfig(input),
				},
			}).catch((requestError: unknown) => {
				reportFailure("REQUEST_FAILED");
				throw requestError;
			});
			if (error) {
				const failure = apiErrorException(error) as Error & {
					code?: string;
					details?: Record<string, unknown>;
					requestId?: string;
				};
				failure.code = apiErrorCode(error);
				failure.details = apiErrorDetails(error);
				failure.requestId = apiErrorRequestId(error);
				if (failure.code === "PATH_ALREADY_REGISTERED") {
					const existingProjectId = failure.details?.existingProjectId;
					const findRegisteredWorkspace = (items: WorkspaceSummary[]) => {
						const local = items.filter((workspace) => workspace.kind !== "cloud");
						return typeof existingProjectId === "string" && existingProjectId !== ""
							? local.find((workspace) => workspace.id === existingProjectId)
							: findRegisteredWorkspaceByPath(local, input.path);
					};
					let registeredWorkspace = findRegisteredWorkspace(
						queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey) ?? workspacesRef.current,
					);
					if (!registeredWorkspace) {
						try {
							registeredWorkspace = findRegisteredWorkspace(await queryClient.fetchQuery({
								...workspaceQueryOptions, staleTime: 0, retry: false,
							}));
						} catch {
						}
					}
					if (registeredWorkspace) {
						if (input.clonePreparationId) {
							setOrchestratorStartupError(registeredWorkspace.id, null);
							setProjectProvisioning(registeredWorkspace.id, true);
							await navigate({ to: "/projects/$projectId", params: { projectId: registeredWorkspace.id } });
							void provisionOrchestrator(registeredWorkspace, input, "project_add");
							return;
						}
						showGlobalToast("Project already added", "Opened the registered project for this folder.");
						void navigate({ to: "/projects/$projectId", params: { projectId: registeredWorkspace.id } });
						return;
					}
				}
				reportFailure(failure.code);
				void captureRendererException(failure, {
					source: "project-add",
					operation: "project_add",
					surface: "project_board",
				});
				throw failure;
			}
			if (!data?.project) {
				reportFailure("NO_PROJECT");
				throw new Error("Project creation returned no project");
			}
			await completeProjectCreation(data.project, input, "project_add");
		},
		[completeProjectCreation, navigate, provisionOrchestrator, queryClient, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast],
	);

	const cloneProject = useCallback(
		async (input: {
			remoteUrl: string;
			destinationParent: string;
			workerAgent: string;
			orchestratorAgent: string;
			reviewerAgent?: string;
			trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
			signal?: AbortSignal;
		}) => {
			void addRendererExceptionStep("Project clone requested", {
				source: "project-clone",
				operation: "project_clone",
				surface: "project_board",
			});
			const reportFailure = (code: string | undefined) => {
				void captureRendererEvent("ao.projects.clone_failed", { error_code: code || "unknown" });
			};
			const status = await refreshDaemonStatus();
			if (status.state !== "ready" || !status.port) {
				reportFailure("DAEMON_NOT_READY");
				throw new Error(status.message || "Daemon is not ready.");
			}
			const { data, error } = await apiClient.POST("/api/v1/projects/clone", {
				signal: input.signal,
				body: {
					remoteUrl: input.remoteUrl,
					destinationParent: input.destinationParent,
					config: createProjectConfig(input),
				},
			}).catch((requestError: unknown) => {
				if (!input.signal?.aborted) reportFailure("REQUEST_FAILED");
				throw requestError;
			});
			if (error) {
				const failure = apiErrorException(error) as Error & {
					code?: string;
					details?: Record<string, unknown>;
					requestId?: string;
				};
				failure.code = apiErrorCode(error);
				failure.details = apiErrorDetails(error);
				failure.requestId = apiErrorRequestId(error);
				reportFailure(failure.code);
				void captureRendererException(failure, {
					source: "project-clone",
					operation: "project_clone",
					surface: "project_board",
				});
				throw failure;
			}
			if (!data?.project) {
				reportFailure("NO_PROJECT");
				throw new Error("Project clone returned no project");
			}
			await completeProjectCreation(data.project, input, "project_clone");
		},
		[completeProjectCreation],
	);

	const initializeProjectRepository = useCallback(async (path: string) => {
		const { error } = await apiClient.POST("/api/v1/projects/initialize", {
			body: { path },
		});
		if (error) {
			const failure = apiErrorException(error) as Error & { code?: string };
			failure.code = apiErrorCode(error);
			throw failure;
		}
	}, []);
	const createRemoteProject = useCallback(async (hostId: string, input: CreateProjectInput) => {
		const host = remoteHosts.find((candidate) => candidate.hostId === hostId);
		if (host?.status !== "connected") throw new Error(`Connect to ${host?.label ?? hostId} before adding a project.`);
		const client = clientForHost(hostId);
		const { data, error } = await client.POST("/api/v1/projects", { body: {
			path: input.path,
			asWorkspace: input.asWorkspace || undefined,
			clonePreparationId: input.clonePreparationId,
			config: createProjectConfig(input),
		} });
		let projectId = error ? undefined : data?.project.id;
		if (input.clonePreparationId && apiErrorCode(error) === "PATH_ALREADY_REGISTERED") {
			const existingProjectId = apiErrorDetails(error)?.existingProjectId;
			if (typeof existingProjectId === "string" && existingProjectId !== "") {
				const existing = await client.GET("/api/v1/projects/{id}", { params: { path: { id: existingProjectId } } });
				if (!existing.error && existing.data?.status === "ok" && existing.data.project.id === existingProjectId) projectId = existingProjectId;
			}
		}
		if (!projectId) throw Object.assign(apiErrorException(error, "Could not add project on this host."), { code: apiErrorCode(error) });
		clearOptimisticProjectRemoval(projectId, hostId);
		const showProject = (sessionId?: string) => {
			void queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) });
			void navigate(sessionId ? sessionNavigateTarget(projectId, sessionId, hostId) : projectNavigateTarget(projectId, hostId));
		};
		showProject();
		setOrchestratorStartupError(projectId, null, hostId);
		setProjectProvisioning(projectId, true, hostId);
		const provisioningGuard = window.setTimeout(() => {
			setProjectProvisioning(projectId, false, hostId);
			setOrchestratorStartupError(projectId, "Project added, but orchestrator startup timed out. Try starting it again.", hostId);
		}, PROVISIONING_TIMEOUT_MS);
		void client.POST("/api/v1/orchestrators", { body: { projectId } }).then(({ data, error }) => {
			if (error || !data?.orchestrator?.id) throw apiErrorException(error, "Could not start the orchestrator.");
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(projectId, false, hostId);
			setOrchestratorStartupError(projectId, null, hostId);
			showProject(data.orchestrator.id);
		}).catch((cause) => {
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(projectId, false, hostId);
			const message = cause instanceof Error ? cause.message : "Try starting it from project settings.";
			setOrchestratorStartupError(projectId, `Project added, but orchestrator did not start: ${message}`, hostId);
			showGlobalToast("Orchestrator did not start", message, "error");
		});
	}, [navigate, queryClient, remoteHosts, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast]);
	const initializeRemoteProject = useCallback(async (hostId: string, path: string) => {
		const { error } = await clientForHost(hostId).POST("/api/v1/projects/initialize", { body: { path } });
		if (error) throw Object.assign(apiErrorException(error), { code: apiErrorCode(error) });
	}, []);

	const validateImport = useCallback(
		async (input: { path: string; importKind: "project" | "workspace" }) => {
			const { data, error } = await apiClient.POST("/api/v1/imports/validate", { body: input });
			if (error) throw apiErrorException(error);
			if (!data) throw new Error("Import validation returned no result");
			return data;
		},
		[],
	);

	const findWorkspaceToRemove = useCallback((projectId: string, hostId?: string, cloud?: boolean) =>
		(hostId ? remoteWorkspacesRef.current ?? [] : workspacesRef.current)
			.find((workspace) => isRemoveTarget(workspace, projectId, hostId, cloud)), []);
	const isViewingProject = useCallback((workspace: WorkspaceSummary) => {
		const route = routeParamsRef.current;
		if (route.hostId !== workspace.hostId) return false;
		return route.projectId === workspace.id ||
			(route.sessionId !== undefined && workspace.sessions.some((session) => session.id === route.sessionId));
	}, []);
	const removeProjectById = useRemoveProject({
		findWorkspace: findWorkspaceToRemove,
		isViewing: isViewingProject,
		cloud: { client: cloudClient, orgId: cloudOrg?.id },
	});
	const removeProject = useCallback(
		(projectId: string, cloud: boolean) => removeProjectById(projectId, undefined, cloud),
		[removeProjectById],
	);
	const removeRemoteProjectFromSidebar = useCallback(
		(hostId: string, projectId: string) => removeProjectById(projectId, hostId),
		[removeProjectById],
	);
	const openRemoteProjectOrchestrator = useCallback(async (hostId: string, projectId: string) => {
		const workspace = remoteWorkspaces.find((item) => item.hostId === hostId && item.id === projectId);
		if (!workspace) return;
		const projectKey = sessionUiKey(projectId, hostId);
		const uiState = useUiStore.getState();
		if (uiState.provisioningProjectIds.has(projectKey) || uiState.restartingProjectIds.has(projectKey)) return;
		if (!hasConfiguredOrchestratorAgent(workspace)) {
			openProjectSettings(projectId, hostId);
			return;
		}
		setOrchestratorStartupError(projectId, null, hostId);
		try {
			const sessionId = await openRemoteOrchestrator(hostId, projectId, newestActiveOrchestrator(workspace.sessions));
			const refresh = queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) });
			if (!workspace.sessions.some((session) => session.id === sessionId)) await refresh;
			void navigate(sessionNavigateTarget(projectId, sessionId, hostId));
		} catch (cause) {
			const message = cause instanceof Error ? cause.message : t("shell.couldNotSpawn");
			setOrchestratorStartupError(projectId, message, hostId);
			showGlobalToast(t("shell.couldNotSpawn"), message, "error");
		}
	}, [navigate, openProjectSettings, queryClient, remoteWorkspaces, setOrchestratorStartupError, showGlobalToast, t]);
	const openRemoteProjectFromSidebar = useCallback((hostId: string, projectId: string) => {
		void navigate(projectNavigateTarget(projectId, hostId));
	}, [navigate]);
	const openRemoteOrchestratorFromSidebar = useLatestCallback((hostId: string, projectId: string) => {
		void openRemoteProjectOrchestrator(hostId, projectId);
	});
	const configureRemoteProjectFromSidebar = useCallback(
		(hostId: string, projectId: string) => openProjectSettings(projectId, hostId),
		[openProjectSettings],
	);
	const retryRemoteHosts = useCallback(() => {
		void retryRemoteHostsNow();
		void queryClient.invalidateQueries({ queryKey: ["remote-workspaces"] });
	}, [queryClient, retryRemoteHostsNow]);
	const handleSidebarOpenChange = useCallback((open: boolean) => {
		if (open !== isSidebarOpen) toggleSidebar();
	}, [isSidebarOpen, toggleSidebar]);

	const restartOrchestrator = useCallback(
		async (projectId: string, mode?: "chat" | "tui", approvalMode?: "bypass-permissions") => {
			await restartProjectOrchestrator({
				projectId,
				queryClient,
				navigate,
				setProjectRestarting,
				setOrchestratorReplacementError,
				mode,
				approvalMode,
				onError: (error) => {
					captureOrchestratorReplacementFailure(error, projectId);
				},
			});
		},
		[navigate, queryClient, setOrchestratorReplacementError, setProjectRestarting],
	);

	useEffect(() => {
		applyDocumentTheme(resolvedTheme);
	}, [resolvedTheme]);

	useEffect(() => {
		applyDocumentThemeStyle(themeStyle);
	}, [themeStyle]);

	useEffect(() => {
		if (usesPreviewWorkspaceData || daemonStatus.state !== "ready" || !daemonStatus.port) return;
		let cancelled = false;
		let retry: ReturnType<typeof setTimeout> | undefined;
		const sync = () => {
			chatHibernationSyncRef.current = chatHibernationSyncRef.current.then(async () => {
				if (cancelled) return;
				const { error } = await apiClient.PATCH("/api/v1/settings/chat-hibernation", {
					body: { enabled: developerMode },
				});
				if (error) throw error;
				const { error: limitHistoryError } = await apiClient.PUT("/api/v1/usage/limit-history", {
					body: { enabled: developerMode },
				});
				if (limitHistoryError) throw limitHistoryError;
			}).catch(() => {
				if (!cancelled) retry = setTimeout(sync, 5_000);
			});
		};
		sync();
		return () => {
			cancelled = true;
			clearTimeout(retry);
		};
	}, [daemonStatus.pid, daemonStatus.port, daemonStatus.state, developerMode]);

	useEffect(() => {
		let active = true;
		if (usesPreviewWorkspaceData) {
			workspaceStartupBaselineRef.current = 0;
			setWorkspaceStartupState("ready");
			return () => {
				active = false;
			};
		}
		if (daemonStatus.state !== "ready" || !daemonStatus.port) {
			workspaceStartupBaselineRef.current = 0;
			setWorkspaceStartupState("loading");
			return () => {
				active = false;
			};
		}

		workspaceStartupBaselineRef.current =
			queryClient.getQueryState(workspaceQueryKey)?.dataUpdatedAt ?? 0;
		setWorkspaceStartupState("loading");
		void queryClient
			.fetchQuery({ ...workspaceQueryOptions, staleTime: 0 })
			.then(() => {
				if (active) setWorkspaceStartupState("ready");
			})
			.catch((error) => {
				if (active && !isCancelledError(error)) setWorkspaceStartupState("error");
			});

		return () => {
			active = false;
		};
	}, [daemonStatus.port, daemonStatus.state, queryClient]);

	useEffect(() => {
		if (
			usesPreviewWorkspaceData ||
			daemonStatus.state !== "ready" ||
			workspaceStartupState === "ready" ||
			!workspaceQuery.isSuccess ||
			workspaceQuery.dataUpdatedAt <= workspaceStartupBaselineRef.current
		) {
			return;
		}
		setWorkspaceStartupState("ready");
	}, [
		daemonStatus.state,
		workspaceQuery.dataUpdatedAt,
		workspaceQuery.isSuccess,
		workspaceStartupState,
	]);

	useEffect(() => {
		void aoBridge.theme?.persistTerminal(resolvedTheme);
	}, [resolvedTheme]);

	const windowTranslucent = isMac && windowTranslucency;

	useEffect(() => {
		if (windowTranslucent) return;
		const frame = requestAnimationFrame(() => {
			const color = resolveCssColorToHex("--sidebar");
			if (color) void aoBridge.theme?.setWindowBackground(color);
		});
		return () => cancelAnimationFrame(frame);
	}, [resolvedTheme, windowTranslucent]);
	useEffect(() => {
		if (!isMac) return;
		applyWindowTranslucency(windowTranslucent);
	}, [windowTranslucent]);

	useEffect(() => {
		if (themePreference !== "system") return;

		const mediaQuery = window.matchMedia("(prefers-color-scheme: light)");
		const handleChange = () => syncSystemTheme();
		mediaQuery.addEventListener("change", handleChange);
		return () => mediaQuery.removeEventListener("change", handleChange);
	}, [themePreference, syncSystemTheme]);

	useAppShortcut("toggle-sidebar", () => toggleSidebar());
	useAppShortcut("jump-to-session", ({ index }) => {
		if (useUiStore.getState().isCommandPaletteOpen) return;
		if (useUiStore.getState().settingsModal?.scope === "global") return;
		const target = index === undefined ? undefined : sidebarSessionOrder()[index];
		if (target) openNavSession(target);
	});
	useAppShortcut("previous-attention-session", () => navigateSession(-1, true));
	useAppShortcut("next-attention-session", () => navigateSession(1, true));
	useAppShortcut("history-back", () => {
		if (settingsPage) {
			settingsPage.close();
			return;
		}
		if (router.history.canGoBack()) router.history.back();
	});
	useAppShortcut("history-forward", () => router.history.forward());
	useAppMenuAvailable("new-session", true);
	useAppMenuAvailable("toggle-sidebar", true);
	useAppMenuAvailable("history-back", true);
	useAppMenuAvailable("history-forward", true);

	useEffect(
		() =>
			onDedicatedShortcut("new-session", (handler) => aoBridge.app.onNewSessionShortcut(handler), () => {
			if (newChatHost) {
				requestNewTask(newChatProject ?? STANDALONE_WORKSPACE_ID, newChatHost);
				return;
			}
			if (isNewChatRoute) {
				requestNewTask(newChatProject ?? STANDALONE_WORKSPACE_ID);
				return;
			}
			if (routeParams.hostId) {
				if (routeParams.projectId) requestNewTask(routeParams.projectId, routeParams.hostId);
				else requestNewTask(STANDALONE_WORKSPACE_ID, routeParams.hostId);
				return;
			}
				if (scopedProjectId) {
					requestNewTask(scopedProjectId);
				} else {
					requestNewTask(lastProjectId(workspaces) ?? STANDALONE_WORKSPACE_ID);
				}
			}),
		[isNewChatRoute, newChatHost, newChatProject, routeParams.hostId, routeParams.projectId, scopedProjectId, requestNewTask, workspaces],
	);

	useEffect(() => aoBridge.app.onKeyboardShortcutsHelp(() => setIsKeyboardShortcutsOpen((open) => !open)), []);
	useAppShortcut("keyboard-shortcuts", () => setIsKeyboardShortcutsOpen((open) => !open));

	const [isQuitConfirmOpen, setIsQuitConfirmOpen] = useState(false);
	const [quitKeepsRunning, setQuitKeepsRunning] = useState(false);
	useEffect(
		() =>
			aoBridge.app.onQuitConfirmRequest(({ keepsRunning }) => {
				setQuitKeepsRunning(keepsRunning);
				setIsQuitConfirmOpen(true);
			}),
		[],
	);
	const resolveQuitConfirm = useCallback((confirmed: boolean) => {
		setIsQuitConfirmOpen(false);
		aoBridge.app.resolveQuitConfirm(confirmed);
	}, []);

	useEffect(
		() => aoBridge.app.onOpenFolderPath((path) => requestCreateProjectFromPath(path)),
		[requestCreateProjectFromPath],
	);

	useEffect(
		() =>
			onDedicatedShortcut("new-shell-terminal", (handler) => aoBridge.app.onNewShellTerminalShortcut(handler), () => {
				if (routeParams.sessionId) requestNewShellTerminal();
			}),
		[requestNewShellTerminal, routeParams.sessionId],
	);

	useEffect(() => {
		if (handledShellNonceRef.current === newShellTerminalNonce) return;
		handledShellNonceRef.current = newShellTerminalNonce;
		if (!routeParams.sessionId) return;
		const shell = openShellTerminal.open({
			projectId: routeParams.hostId ? routeParams.projectId : scopedProjectId,
			sessionId: routeParams.sessionId,
			cloud: scopedSession?.cloud,
		});
		if (!shell) return;
		setActiveShellTerminal(shell.handleId);
	}, [
		newShellTerminalNonce,
		openShellTerminal,
		scopedProjectId,
		scopedSession?.cloud,
		routeParams.sessionId,
		routeParams.hostId,
		routeParams.projectId,
		setActiveShellTerminal,
	]);

	useEffect(
		() => aoBridge.app.onOpenSettingsShortcut((section) =>
			useUiStore.getState().openGlobalSettings(
				section === "billing" && cachedCloudBillingMode(queryClient) === "self_serve" ? "billing" : undefined,
			)),
		[queryClient],
	);

	const remoteWorkspaceLoadSettled = remoteHostsSettled
		&& (!remoteHostsListed || remoteHostIds.every((hostId) => remoteHosts.some((host) => host.hostId === hostId && host.status === "offline")
			|| (remoteLoadedProjectHostIds.includes(hostId) && remoteLoadedSessionHostIds.includes(hostId))
			|| remoteFailedHostIds.includes(hostId)));
	const sessionDeepLinksReady = workspaceQuery.isSuccess && workspaceStartupState === "ready" && remoteWorkspaceLoadSettled;
	const pendingSessionDeepLinkRef = useRef<string | null>(null);
	const handleSessionDeepLink = useCallback(
		(rawUrl: string) => {
			const link = `ao://${rawUrl.slice("ao-app://".length)}`;
			const artifact = parseArtifactLink(link);
			const target = parseSessionLink(link) ?? artifact;
			const workspaces = workspacesRef.current;
			const remoteWorkspaces = remoteWorkspacesRef.current;
			const resolvedLocal = target ? resolveSessionLink(link, workspaces) : undefined;
			const resolvedRemote = target ? resolveSessionLink(link, remoteWorkspaces) : undefined;
			if (resolvedLocal && resolvedRemote) {
				useUiStore.getState().showGlobalToast(t("deepLink.sessionAmbiguous"));
				return;
			}
			const resolved = resolvedLocal ?? resolvedRemote;
			const workspace = resolvedLocal
				? workspaces.find((entry) => entry.id === resolvedLocal.projectId)
				: resolvedRemote
					? remoteWorkspaces.find((entry) => entry.id === resolvedRemote.projectId && entry.sessions.some((session) => session.id === resolvedRemote.sessionId))
					: undefined;
			if (!resolved || !workspace) {
				const missOnFailedHost = target !== undefined && remoteFailedHostIds.some((hostId) =>
					remoteWorkspacesRef.current.some((entry) => entry.hostId === hostId && entry.id === target.projectId));
				useUiStore.getState().showGlobalToast(t(missOnFailedHost ? "deepLink.sessionHostUnavailable" : "deepLink.sessionNotFound"));
				return;
			}
			if (resolved.isTerminated && !artifact) {
				useUiStore.getState().showGlobalToast(t("deepLink.sessionTerminated", { sessionId: resolved.sessionId }));
				return;
			}
			if (artifact) useUiStore.getState().requestWorkspaceFileOpen(resolved.sessionId, artifact.path, workspace.hostId, "artifact");
			void navigate(sessionNavigateTarget(resolved.projectId, resolved.sessionId, workspace.hostId));
		},
		[navigate, remoteFailedHostIds, t],
	);
	useEffect(() => {
		return aoBridge.app.onOpenSessionDeepLink((rawUrl) => {
			if (!sessionDeepLinksReady) {
				pendingSessionDeepLinkRef.current = rawUrl;
				return;
			}
			handleSessionDeepLink(rawUrl);
		});
	}, [handleSessionDeepLink, sessionDeepLinksReady]);
	useEffect(() => {
		if (!sessionDeepLinksReady) return;
		const pending = pendingSessionDeepLinkRef.current;
		if (!pending) return;
		pendingSessionDeepLinkRef.current = null;
		handleSessionDeepLink(pending);
	}, [handleSessionDeepLink, sessionDeepLinksReady]);

	useEffect(() => {
		const disposePrevious = onDedicatedShortcut("previous-session", (handler) => aoBridge.app.onPreviousSessionShortcut(handler), () => navigateSession(-1));
		const disposeNext = onDedicatedShortcut("next-session", (handler) => aoBridge.app.onNextSessionShortcut(handler), () => navigateSession(1));
		return () => {
			disposePrevious();
			disposeNext();
		};
	}, [navigateSession]);

	const openProject = useCallback(
		(projectId: string) => void navigate({ to: "/projects/$projectId", params: { projectId } }),
		[navigate],
	);
	const shellContextValue = useMemo(
		() => ({
			daemonStatus,
			workspaceStartupState,
			cloneProject,
			createProject,
			initializeProjectRepository,
			openRemoteProjectSettings: (hostId: string, projectId: string) => openProjectSettings(projectId, hostId),
			openProject,
			remoteHosts,
			removeProject,
			retryRemoteHosts,
			validateImport,
		}),
		[
			cloneProject,
			createProject,
			daemonStatus,
			initializeProjectRepository,
			openProjectSettings,
			openProject,
			remoteHosts,
			removeProject,
			retryRemoteHosts,
			validateImport,
			workspaceStartupState,
		],
	);

	const quitConfirmDialog = (
		<QuitConfirmDialog open={isQuitConfirmOpen} keepsRunning={quitKeepsRunning} onResolve={resolveQuitConfirm} />
	);
	if (isStartupLoading) {
		return (
			<>
				<div className="flex h-full min-h-0 flex-col"><PageTopbar /><DaemonStartupLoader /></div>
				{quitConfirmDialog}
			</>
		);
	}

	return (
		<ShellProvider
			value={shellContextValue}
		>
			<AppBrowserLinkContext.Provider value={canOpenBrowserLink ? openBrowserLink : undefined}>
			<SessionTopbarProvider>
				<NotificationRuntime />
				<HarnessUpdateNotice enabled={daemonStatus.state === "ready" && !firstRunPromptActive} />
				<TrayRuntime />
				<SharedWithYouNotifier />
				{isDragActive ? (
					<div
						aria-hidden="true"
						className="dialog-overlay pointer-events-none flex items-center justify-center"
						data-testid="folder-drop-overlay"
					>
						<div className="relative isolate flex w-[min(420px,calc(100vw-48px))] flex-col items-center gap-3 rounded-welcome-panel border border-dashed border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] p-8 text-center shadow-[var(--shadow-import-modal)]">
							<span className="grid size-11 place-items-center rounded-xl bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-muted)]">
								<FolderPlus className="size-5" aria-hidden="true" />
							</span>
							<p className="text-[15px] font-semibold text-[var(--color-text-import-title)]">
								{t("createProject.dropToAdd")}
							</p>
						</div>
					</div>
				) : null}
				<NewTaskNavigator />
				<ArchiveModalHost />
				<GlobalToast />
				<RestartToUpdateDialog />
				<SessionImportDialog />
				{quitConfirmDialog}
				<KeyboardShortcutsDialog
					open={isKeyboardShortcutsOpen}
					onOpenChange={setIsKeyboardShortcutsOpen}
					onCustomize={customizeKeyboardShortcuts}
				/>
				<TerminalCacheProvider
					daemonReady={daemonStatus.state === "ready"}
					theme={resolvedTheme}
				>

			<div
				className={cn(
					"app-shell-root flex h-screen min-h-0 flex-col bg-sidebar text-foreground",
					isWindows && "platform-windows",
					isLinux && "platform-linux",
					isFullScreen && "native-fullscreen",
				)}
			>
				<WindowTitlebar onBack={settingsPage?.close} />
				<SidebarProvider
					className="min-h-0 flex-1 flex-col overflow-x-hidden"
					keyboardShortcut={false}
					onOpenChange={handleSidebarOpenChange}
					open={!isStartupLoading && isSidebarOpen}
					style={
						{
							"--sidebar-width": "var(--size-sidebar-default)",
							"--sidebar-width-icon": "var(--size-sidebar-icon)",
						} as CSSProperties
					}
				>
					<div
						className="flex min-h-0 w-full flex-1 overflow-x-hidden"
						data-testid="shell-content-row"
					>
					<Sidebar
						underTopbar={isMac || isWindows || isLinux}
						topbarOffset={isWindows ? "titlebar" : trafficLightBand ? "trafficLights" : "toolbar"}
						onCloneProject={cloneProject}
						onCreateProject={createProject}
						onInitializeProject={initializeProjectRepository}
						onRemoveProject={removeProject}
						resizeAuxiliaryTargetRef={sidebarDragStripRef}
						workspaceError={workspaceQuery.isError ? errorMessage(workspaceQuery.error) : undefined}
						workspaces={workspaces}
						remoteHosts={remoteHosts}
						onCreateRemoteProject={createRemoteProject}
						onInitializeRemoteProject={initializeRemoteProject}
						onOpenRemoteProject={openRemoteProjectFromSidebar}
						onOpenRemoteOrchestrator={openRemoteOrchestratorFromSidebar}
						onConfigureRemoteProject={configureRemoteProjectFromSidebar}
						onRemoveRemoteProject={removeRemoteProjectFromSidebar}
						onRetryRemoteHosts={retryRemoteHosts}
						remoteWorkspaces={remoteWorkspaces}
						remoteFailedHostIds={remoteFailedHostIds}
					/>
					<main className={cn("flex min-w-0 flex-1 flex-col overflow-x-hidden", !sidebarHasLayout && "sidebar-hidden")}>
						<div className="relative min-h-0 flex-1 overflow-x-hidden" ref={centerPaneRef}>
							<ShellCenter
								isSessionRoute={Boolean(routeParams.sessionId) || isNewChatRoute}
								isHomeRoute={isHomeRoute}
								isNewChatRoute={isNewChatRoute}
								selfFramedCenterPanel={selfFramedCenterPanel}
								startingOrchestrator={openingOrchestrator}
								startingCloudOrchestrator={!routeParams.hostId && startingWorkspace?.kind === CLOUD_PROJECT_KIND}
								startingSteps={startingSteps}
								startingCoderWorkspaceUrl={startingOrchestratorSession?.cloud?.coderWorkspaceUrl}
								startingHarness={startingWorkspace?.orchestratorAgent ?? startingOrchestratorSession?.provider}
							/>
							<SwipeNavigation containerRef={centerPaneRef} navigation={settingsPage?.navigation ?? {
								canGoBack: !isHomeRoute && router.history.canGoBack(),
								canGoForward,
								back: () => router.history.back(),
								forward: () => router.history.forward(),
							}} />
						</div>
						</main>
					</div>
					<DaemonFailureBanner status={daemonStatus} />
					{trafficLightBand && isMac ? (
						<div
							aria-hidden="true"
							data-slot="titlebar-drag-region"
							className={cn(
								"fixed top-0 left-0 z-chrome transition-[height] duration-200 ease-out motion-reduce:transition-none",
								sidebarHasLayout ? "w-(--ao-sidebar-w,var(--size-sidebar-default))" : "w-titlebar-content-offset",
								isFullScreen ? "pointer-events-none h-0" : sidebarHasLayout ? "h-traffic-light-clearance" : "h-mac-panel-header-bottom",
							)}
							ref={sidebarDragStripRef}
							style={{
								...(sidebarHasLayout && sidebarWidthPx ? { width: sidebarWidthPx } : null),
								...(trafficLightDragActive ? ({ WebkitAppRegion: "drag" } as CSSProperties) : null),
							}}
						>
							<motion.div
								animate={{ x: sidebarHasLayout ? "0%" : "-100%" }}
								className="pointer-events-none absolute inset-y-0 left-0 w-(--ao-sidebar-w,var(--size-sidebar-default)) bg-sidebar"
								style={sidebarWidthPx ? { width: sidebarWidthPx } : undefined}
								initial={false}
								transition={prefersReducedMotion ? { duration: 0 } : SHELL_PANEL_SPRING}
							/>
						</div>
					) : null}
					<TitlebarNav
						onBack={settingsPage?.close}
						historyLocked={isHomeRoute}
						isFullScreen={isFullScreen}
						zoomFactor={windowZoomFactor}
					/>
				</SidebarProvider>
				<OrchestratorReplacementDialog
					pending={Boolean(replacementErrorProjectId && restartingProjectIds.has(replacementErrorProjectId))}
					pendingCount={replacementErrorCount}
					error={replacementErrorProjectId ? orchestratorReplacementErrors[replacementErrorProjectId] : undefined}
					onOpenChange={(open) => {
						if (!open && replacementErrorProjectId) setOrchestratorReplacementError(replacementErrorProjectId, null);
					}}
					onRetry={(projectId) => void restartOrchestrator(projectId)}
					onRetryAsTui={(projectId) => void restartOrchestrator(projectId, "tui")}
					onRetryWithoutApprovals={(projectId) =>
						void restartOrchestrator(projectId, undefined, "bypass-permissions")
					}
					projectId={replacementErrorProjectId}
					workspaces={workspaces}
				/>
					<CommandPalette />
				</div>
				</TerminalCacheProvider>
			</SessionTopbarProvider>
			</AppBrowserLinkContext.Provider>
		</ShellProvider>
	);
}
