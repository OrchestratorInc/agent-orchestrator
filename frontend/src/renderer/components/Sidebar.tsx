import { useQueryClient } from "@tanstack/react-query";
import { useProjectAvatars } from "../hooks/useProjectAvatars";
import { paidPlanBadge, useCloudBilling } from "../hooks/useCloudBilling";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import {
	DndContext,
	KeyboardSensor,
	PointerSensor,
	closestCenter,
	useSensor,
	useSensors,
	type Modifier,
	type DragEndEvent,
} from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
	Archive,
	CalendarClock,
	ChevronRight,
	CircleAlert,
	Copy,
	GitMerge,
	GitPullRequest,
	LoaderCircle,
	LayoutGrid,
	Package,
	Gauge,
	LogIn,
	LogOut,
	MoreVertical,
	PanelLeft,
	Pin,
	PinOff,
	Plus,
	Search,
	Settings,
	Smartphone,
	Trash2,
	TriangleAlert,
	User,
	ArrowLeft,
} from "@/components/app-icons";
import {
	useCallback,
	useEffect,
	useLayoutEffect,
	memo,
	useMemo,
	useRef,
	useState,
	type CSSProperties,
	type DragEvent as ReactDragEvent,
	type MouseEvent,
	type ReactNode,
	type RefObject,
} from "react";
import { flushSync } from "react-dom";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { artifactCount, groupArtifactsByProject } from "../lib/artifacts";
import {
	hasConfiguredOrchestratorAgent,
	newestActiveOrchestrator,
	openPRs,
	type WorkspaceSession,
	type WorkspaceSummary,
	sortedWorkerSessions,
	isOrchestratorSession,
	resolveNextNavigationAfterSessionKill,
	workerSessions,
	CLOUD_PROJECT_KIND,
	ownScratchpadWorkspaces,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
} from "../types/workspace";
import {
	getSessionRowIndicator,
	sessionNeedsInput,
	type SessionRowMarker,
} from "../lib/session-presentation";
import { SessionContextMenuItems, useCopyToClipboard } from "./SessionContextMenuItems";
import { dropdownMenuKit, MenuEntries, MenuKitContext, type MenuEntry } from "./menu-model";
import { isArchivedSession } from "../lib/standalone-archive";
import { hasTrustedApiBaseUrl } from "../lib/api-client";
import { useCommandPaletteEnabled } from "../hooks/useCommandPaletteEnabled";
import { useEdgeFade } from "../hooks/useEdgeFade";
import { canResumeAgent, resumeAgentOnOpen, useCanResumeAgent } from "../hooks/useCanResumeAgent";
import { cloudSessionsQueryKey, workspaceQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { conversationQueryKey, conversationQueryOptions } from "../hooks/useConversation";
import { usePinSession, useUnpinSession } from "../hooks/usePinSession";
import { spawnCloudOrchestrator } from "../lib/cloud-orchestrator";
import { resumeOrchestrator, spawnOrchestrator } from "../lib/spawn-orchestrator";
import { formatTimeCompact, formatTimeTerse } from "../lib/format-time";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { useResizable } from "../hooks/useResizable";
import { useLatestCallback, useStableArray } from "../hooks/useStable";
import { useCloudLocalAuth } from "../hooks/useCloudLocalAuth";
import { useLocalSignInDialogStore } from "../stores/local-signin-dialog-store";
import { useShellMaybe } from "../lib/shell-context";
import { useUpdateStatus } from "../hooks/useUpdateStatus";
import { MAX_SESSION_DISPLAY_NAME_LEN, useSessionRename } from "../hooks/useSessionRename";
import { useSessionPreviewHover } from "../hooks/useSessionPreviewHover";
import { effectiveShortcutBindings, shortcutBindingKeys } from "../../shared/shortcuts";
import {
	ContextMenu,
	ContextMenuContent,
	ContextMenuItem,
	ContextMenuTrigger,
} from "./ui/context-menu";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
import { SessionPreviewCardAdapter } from "./SessionsBoardAdapters";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import {
	Sidebar as SidebarRoot,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupContent,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarRail,
	SidebarMenuSub,
	SidebarMenuSubItem,
	useSidebar,
} from "./ui/sidebar";
import { BuildInfoTooltip } from "./BuildInfoTooltip";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";
import { menuFocusReturnTarget } from "./ui/menu-focus";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { SharedProjectMark } from "./teams/SharedProjectMark";
import { CloudMark } from "./CloudMark";
import { ProjectIcon } from "./ProjectIcon";
import { OrchestratorIcon } from "./icons";
import { OrchestratorReplyIcon, orchestratorReplyLabel } from "./OrchestratorReplyIcon";
import { Badge } from "./ui/badge";
import { cn } from "../lib/utils";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import { captureRendererException } from "../lib/telemetry";
import { AwakeMenuItem, awakeNeedsAttention, useFooterStayAwake } from "./AwakeControl";
import { FOOTER_RAIL_BUTTON_CLASS, SidebarUpdateRail, SidebarUpdateRow } from "./SidebarUpdate";
import { SettingsSaveStatus, useSettingsPage } from "./SettingsDialog";
import { useUiStore } from "../stores/ui-store";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useSessionImportStore } from "../stores/session-import-store";
import { sessionReadKey, useSessionReadStore, useSessionUnread } from "../stores/session-read-store";
import { useOrchestratorReplyState } from "../stores/orchestrator-reply-store";
import { applyOrder, pinnedFirst } from "../lib/sidebar-order";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { ConfirmDialog } from "./ConfirmDialog";
import { SessionArchiveDialog, type SessionArchiveChoice } from "./SessionArchiveDialog";
import { CreateProjectFlow, type CloneProjectInput, type CreateProjectInput } from "./CreateProjectFlow";
import { ResizeHandle } from "./ResizeHandle";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { useCloudSession } from "../lib/cloud-session";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { sessionUiKey } from "../lib/hosts";
import { RemoteHostsSection } from "./RemoteHostsSection";

const isMac = isMacPlatform();
const brandInTitlebar = isMac || isLinuxPlatform();
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

const ROW_ACTIONS_CLASS = "absolute inset-y-0 right-1 flex items-center gap-0.5";
const AGENT_WARM_UP_HOVER_MS = 300;
const ROW_ACTION_BUTTON_CLASS = "sidebar-icon-action [&_svg]:size-icon-md";
const HOVER_ACTION_CLASS = ROW_ACTION_BUTTON_CLASS;
const SESSION_ACTION_CLASS = ROW_ACTION_BUTTON_CLASS;

const NAV_ROW_CLASS =
	"h-9 gap-2.5 rounded-lg px-2.5 text-sm font-medium text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground active:bg-interactive-hover active:text-foreground data-[active=true]:bg-interactive-active data-[active=true]:font-medium data-[active=true]:text-foreground";

const FOOTER_NAV_BUTTON_CLASS = cn(
	NAV_ROW_CLASS,
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"flex h-9 w-full items-center text-left transition-none",
);

const FOOTER_UPDATES_CLASS = "flex flex-col gap-0.5 pt-2 empty:hidden";

const FOOTER_BOTTOM_ROW_CLASS = "flex w-full items-center gap-0.5 pt-2";

const FOOTER_ICON_BUTTON_CLASS = cn(
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"inline-flex size-9 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground [&_svg]:size-icon-md",
);

const SIDEBAR_ROW_CLASS = "h-8 gap-2 [&_svg]:size-icon-md";

const SECTION_ROW_CLASS =
	"flex h-8 w-full min-w-0 items-center gap-2 rounded-md px-2.5 text-sm font-medium text-muted-foreground [&_svg]:size-icon-md [&_svg]:shrink-0";


const REORDER_ACTIVATION_DISTANCE = 8;
const EMPTY_DRAG_IMAGE = typeof Image === "undefined" ? null : new Image();
if (EMPTY_DRAG_IMAGE) EMPTY_DRAG_IMAGE.src = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";

export const sessionDndId = (projectId: string) => `sidebar-sessions-${projectId}`;

const POINTER_SENSOR_OPTIONS = { activationConstraint: { distance: REORDER_ACTIVATION_DISTANCE } };
const KEYBOARD_SENSOR_OPTIONS = {
	coordinateGetter: sortableKeyboardCoordinates,
	keyboardCodes: { start: ["Space"], cancel: ["Escape"], end: ["Space"] },
};

function useReorderSensors() {
	return useSensors(
		useSensor(PointerSensor, POINTER_SENSOR_OPTIONS),
		useSensor(KeyboardSensor, KEYBOARD_SENSOR_OPTIONS),
	);
}

function usePostDragClickGuard() {
	const guardedIdRef = useRef<string | null>(null);
	const clearTimerRef = useRef<number | null>(null);

	const markDragEnded = useCallback((id: string) => {
		guardedIdRef.current = id;
		if (clearTimerRef.current !== null) window.clearTimeout(clearTimerRef.current);
		clearTimerRef.current = window.setTimeout(() => {
			guardedIdRef.current = null;
			clearTimerRef.current = null;
		}, 0);
	}, []);

	const consumeClick = useCallback((id: string) => {
		if (guardedIdRef.current !== id) return false;
		guardedIdRef.current = null;
		if (clearTimerRef.current !== null) {
			window.clearTimeout(clearTimerRef.current);
			clearTimerRef.current = null;
		}
		return true;
	}, []);

	useEffect(() => () => {
		if (clearTimerRef.current !== null) window.clearTimeout(clearTimerRef.current);
	}, []);

	return useMemo(() => ({ consumeClick, markDragEnded }), [consumeClick, markDragEnded]);
}

type SortableRow = ReturnType<typeof useSortable>;

function sortableRowStyle({ transform, transition, isDragging, dropTransitionDisabled }: Pick<SortableRow, "transform" | "transition" | "isDragging"> & { dropTransitionDisabled?: boolean }): CSSProperties {
	return {
		transform: transform ? CSS.Transform.toString({ ...transform, x: 0, scaleX: 1, scaleY: 1 }) : undefined,
		transition: isDragging || dropTransitionDisabled ? "none" : (transition ?? "transform 180ms cubic-bezier(0.22, 1, 0.36, 1)"),
	};
}

const restrictToListBounds: Modifier = ({ activeNodeRect, containerNodeRect, transform }) => {
	if (!activeNodeRect || !containerNodeRect) return transform;
	const minY = containerNodeRect.top - activeNodeRect.top;
	const maxY = containerNodeRect.bottom - activeNodeRect.bottom;
	return {
		...transform,
		x: 0,
		y: Math.min(maxY, Math.max(minY, transform.y)),
	};
};

type ProjectDropPlacement = "before" | "after";

function reorderAtProjectBoundary(
	ids: string[],
	activeId: string,
	targetId: string,
	placement: ProjectDropPlacement,
): string[] | null {
	if (activeId === targetId || !ids.includes(activeId) || !ids.includes(targetId)) return null;
	const next = ids.filter((id) => id !== activeId);
	const targetIndex = next.indexOf(targetId);
	next.splice(targetIndex + (placement === "after" ? 1 : 0), 0, activeId);
	return next.every((id, index) => id === ids[index]) ? null : next;
}

function reorderById(ids: string[], activeId: string, overId: string): string[] | null {
	if (activeId === overId) return null;
	const from = ids.indexOf(activeId);
	const to = ids.indexOf(overId);
	if (from < 0 || to < 0) return null;
	const next = [...ids];
	const [moved] = next.splice(from, 1);
	next.splice(to, 0, moved);
	return next;
}

function useGrabbingCursor(active: boolean) {
	useEffect(() => {
		if (!active) return;
		document.documentElement.classList.add("sidebar-reordering");
		return () => document.documentElement.classList.remove("sidebar-reordering");
	}, [active]);
}

const SIDEBAR_COLLAPSIBLE = "offcanvas" as "offcanvas" | "icon";
export const SIDEBAR_DEFAULT_WIDTH = 240;
export const SIDEBAR_MIN_WIDTH = 200;
export const SIDEBAR_MAX_WIDTH = 420;
const SIDEBAR_BRAND_TRAILING_GAP = 12;
function sidebarMinWidth(): number {
	const brand = Array.from(document.querySelectorAll<HTMLElement>("[data-sidebar-brand]")).find(
		(el) => el.getBoundingClientRect().width > 0,
	);
	if (!brand) return SIDEBAR_MIN_WIDTH;
	const label = brand.querySelector<HTMLElement>("[data-brand-label]");
	const fit = label
		? Math.ceil(label.getBoundingClientRect().left + label.scrollWidth + SIDEBAR_BRAND_TRAILING_GAP + 1)
		: Math.ceil(brand.getBoundingClientRect().left + brand.scrollWidth + SIDEBAR_BRAND_TRAILING_GAP);
	return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, fit));
}
const SIDEBAR_INITIAL_SECTION_LIMIT = 10;
const SIDEBAR_PROJECT_SESSION_LIMIT = 6;
const SIDEBAR_SESSIONS_LIMIT = 5;
const SECTION_SCROLLER_CLASS =
	"scrollbar-none overflow-y-auto overflow-x-hidden overscroll-contain group-data-[collapsible=icon]:overflow-visible";

function useShowMoreCap<T extends { id: string }>(
	items: T[],
	limit: number,
	activeId: string | undefined,
	isCollapsed = false,
) {
	const [showAll, setShowAll] = useState(false);
	const [showAllDismissed, setShowAllDismissed] = useState(false);
	const activeBeyondLimit = useMemo(() => {
		if (showAll || items.length <= limit || !activeId) return false;
		return items.findIndex((item) => item.id === activeId) >= limit;
	}, [activeId, items, limit, showAll]);
	useEffect(() => setShowAllDismissed(false), [activeId]);
	useEffect(() => {
		if (activeBeyondLimit && !showAllDismissed) setShowAll(true);
	}, [activeBeyondLimit, showAllDismissed]);
	const listed = useMemo(
		() => (isCollapsed || showAll || items.length <= limit ? items : items.slice(0, limit)),
		[isCollapsed, items, limit, showAll],
	);
	const toggleShowAll = () => {
		const next = !showAll;
		setShowAll(next);
		setShowAllDismissed(!next);
	};
	return { listed, hiddenCount: Math.max(0, items.length - limit), showAll, toggleShowAll };
}

function SidebarSectionScroller({
	children,
	className,
	style,
	testId,
	wrapperClassName,
}: {
	children: ReactNode;
	className: string;
	style?: CSSProperties;
	testId: string;
	wrapperClassName?: string;
}) {
	const scrollerRef = useRef<HTMLDivElement>(null);
	useEdgeFade(scrollerRef);

	return (
		<div className={`relative min-h-0 ${wrapperClassName ?? ""}`}>
			<div ref={scrollerRef} className={`edge-fade [--edge-fade-size:var(--space-4)] ${className}`} data-sidebar-section-scroller="" data-testid={testId} style={style}>
				{children}
			</div>
		</div>
	);
}

function AnimatedSectionBody({ open, children, className }: { open: boolean; children: ReactNode; className?: string }) {
	const prefersReducedMotion = useReducedMotion();
	return (
		<AnimatePresence initial={false}>
			{open ? (
				<motion.div
					key="section-body"
					initial={{ gridTemplateRows: "0fr", opacity: 0 }}
					animate={{ gridTemplateRows: "1fr", opacity: 1 }}
					exit={{ gridTemplateRows: "0fr", opacity: 0 }}
					transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.18, ease: [0.25, 0.46, 0.45, 0.94] }}
					style={{ display: "grid" }}
					className={className}
				>
					<div className="flex min-h-0 flex-col overflow-hidden">{children}</div>
				</motion.div>
			) : null}
		</AnimatePresence>
	);
}
const expandedProjectsStorageKey = "ao.sidebar.expanded-projects";

function readExpandedProjectIds(): ReadonlySet<string> {
	if (typeof window === "undefined" || !window.localStorage) return new Set();
	try {
		const value: unknown = JSON.parse(window.localStorage.getItem(expandedProjectsStorageKey) ?? "null");
		return new Set(Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : []);
	} catch {
		return new Set();
	}
}

const sectionsStorageKey = "ao.sidebar.sections";
type SidebarSection = "pinned" | "projects" | "scratchpad";

function readSectionsOpen(): Record<SidebarSection, boolean> {
	const open = { pinned: true, projects: true, scratchpad: true };
	if (typeof window === "undefined" || !window.localStorage) return open;
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(sectionsStorageKey) ?? "null");
		if (saved && typeof saved === "object") {
			for (const key of Object.keys(open) as SidebarSection[]) {
				if ((saved as Record<string, unknown>)[key] === false) open[key] = false;
			}
		}
	} catch {}
	return open;
}

function saveSectionOpen(section: SidebarSection, open: boolean) {
	try {
		window.localStorage?.setItem(sectionsStorageKey, JSON.stringify({ ...readSectionsOpen(), [section]: open }));
	} catch {}
}

type SidebarProps = {
	underTopbar?: boolean;
	topbarOffset?: "toolbar" | "titlebar" | "trafficLights" | "session";
	workspaceError?: string;
	workspaces: WorkspaceSummary[];
	remoteHosts?: RemoteHost[];
	onCreateRemoteProject: (hostId: string, input: CreateProjectInput) => Promise<void>;
	onInitializeRemoteProject: (hostId: string, path: string) => Promise<void>;
	onOpenRemoteProject?: (hostId: string, projectId: string) => void;
	onOpenRemoteOrchestrator?: (hostId: string, projectId: string) => void;
	onConfigureRemoteProject?: (hostId: string, projectId: string) => void;
	onRemoveRemoteProject?: (hostId: string, projectId: string) => void;
	onRetryRemoteHosts?: () => void;
	remoteWorkspaces?: WorkspaceSummary[];
	remoteFailedHostIds?: string[];
	onCloneProject: (input: CloneProjectInput) => Promise<void>;
	onCreateProject: (input: CreateProjectInput) => Promise<void>;
	onInitializeProject: (path: string) => Promise<void>;
	onRemoveProject: (projectId: string, cloud: boolean) => void;
	resizeAuxiliaryTargetRef?: RefObject<HTMLElement | null>;
};

function useSelection() {
	const navigate = useNavigate();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const openProjectSettings = useUiStore((state) => state.openProjectSettings);
	const params = useParams({ strict: false }) as {
		hostId?: string;
		projectId?: string;
		sessionId?: string;
	};
	const pathname = useRouterState({
		select: (state) => state.location.pathname,
	});
	const goHome = useCallback(() => void navigate({ to: "/" }), [navigate]);
	const goAutomations = useCallback(() => void navigate({ to: "/automations" }), [navigate]);
	const goArtifacts = useCallback(() => void navigate({ to: "/artifacts" }), [navigate]);
	const goUsage = useCallback(() => void navigate({ to: "/usage" }), [navigate]);
	const goStandaloneBoard = useCallback(() => void navigate({ to: "/sessions" }), [navigate]);
	const goGlobalSettings = useCallback(() => openGlobalSettings(), [openGlobalSettings]);
	const goConnectMobile = useCallback(() => openGlobalSettings("mobile", { startPairing: true }), [openGlobalSettings]);
	const goSettings = useCallback((projectId: string, cloudOrgId?: string) => openProjectSettings(projectId, { cloudOrgId }), [openProjectSettings]);
	const goProject = useCallback(
		(projectId: string) => void navigate({ to: "/projects/$projectId", params: { projectId } }),
		[navigate],
	);
	const goSession = useCallback(
		(projectId: string, sessionId: string) => {
			if (projectId === STANDALONE_WORKSPACE_ID) {
				void navigate({ to: "/sessions/$sessionId", params: { sessionId } });
				return;
			}
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId },
			});
		},
		[navigate],
	);
	return useMemo(() => ({
		isHome: pathname === "/",
		isAutomations: pathname === "/automations",
		isArtifacts: pathname === "/artifacts",
		isUsage: pathname === "/usage",
		activeRemoteHostId: params.hostId,
		activeRemoteProjectId: params.hostId ? params.projectId : undefined,
		activeRemoteSessionId: params.hostId ? params.sessionId : undefined,
		activeProjectId: params.hostId ? undefined : params.projectId,
		activeSessionId: params.hostId ? undefined : params.sessionId,
		goHome,
		goAutomations,
		goArtifacts,
		goUsage,
		goStandaloneBoard,
		goGlobalSettings,
		goConnectMobile,
		goSettings,
		goProject,
		goSession,
	}), [goArtifacts, goAutomations, goUsage, goConnectMobile, goGlobalSettings, goHome, goProject, goSession, goSettings, goStandaloneBoard, params.hostId, params.projectId, params.sessionId, pathname]);
}

function floatAttention(sessions: WorkspaceSession[], hasManualOrder: boolean): WorkspaceSession[] {
	if (hasManualOrder) return sessions;
	const flagged = sessions.filter((session) => getSessionRowIndicator(session) === "attention");
	if (flagged.length === 0 || flagged.length === sessions.length) return sessions;
	return [...flagged, ...sessions.filter((session) => !flagged.includes(session))];
}

const SESSION_MARKER_ICONS = {
	attention: { Icon: CircleAlert, tone: "text-destructive" },
	ready: { Icon: GitPullRequest, tone: "text-status-ready" },
	review: { Icon: CircleAlert, tone: "text-status-in-review" },
	merged: { Icon: GitMerge, tone: "text-status-merged opacity-70" },
} as const satisfies Record<SessionRowMarker, { Icon: unknown; tone: string }>;

function SessionMarkerIcon({ marker, popped, onPopEnd, ...rest }: {
	marker: SessionRowMarker;
	popped?: boolean;
	onPopEnd?: () => void;
} & Record<`data-${string}`, string | undefined>) {
	const { Icon, tone } = SESSION_MARKER_ICONS[marker];
	return (
		<Icon
			className={cn("-mx-0.5 size-3", tone, popped && "session-marker-pop")}
			data-session-marker={marker}
			onAnimationEnd={onPopEnd}
			{...rest}
		/>
	);
}

export function SessionStatusDot({ session, indicator }: {
	session: WorkspaceSession;
	indicator: ReturnType<typeof getSessionRowIndicator>;
}) {
	const working = indicator === "working";
	const marker = working ? null : indicator;
	const unread = useSessionUnread(session);
	const previous = useRef(marker);
	const [popped, setPopped] = useState(false);
	useEffect(() => {
		if (previous.current !== marker && marker) setPopped(true);
		previous.current = marker;
	}, [marker]);
	const stopped =
		session.activity?.state === "exited" || session.status === "exited" || session.status === "no_signal";
	return (
		<span
			aria-hidden="true"
			className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1.5"
		>
			{marker ? (
				<SessionMarkerIcon
					data-session-status={session.status}
					marker={marker}
					onPopEnd={() => setPopped(false)}
					popped={popped}
				/>
			) : working ? (
				<LoaderCircle
					className="-mx-0.5 size-3 animate-spin text-muted-foreground motion-reduce:animate-none"
					data-session-status={session.status}
					data-session-spinner=""
				/>
			) : (
				<span
					className="-mx-0.5 inline-flex size-3 items-center justify-center"
					data-session-idle={stopped ? "stopped" : "idle"}
					data-session-status={session.status}
				>
					<span
						className={cn(
							"size-2 rounded-full",
							unread ? "bg-muted-foreground/60" : "border border-muted-foreground/60",
						)}
					/>
				</span>
			)}
		</span>
	);
}

export {
	resolveNextNavigationAfterSessionKill,
	type NextSessionNavigation,
} from "../types/workspace";

export const Sidebar = memo(function Sidebar({
	underTopbar = true,
	topbarOffset = "toolbar",
	workspaceError,
	workspaces,
	remoteHosts = [],
	onCreateRemoteProject,
	onInitializeRemoteProject,
	onOpenRemoteProject = () => undefined,
	onOpenRemoteOrchestrator = () => undefined,
	onConfigureRemoteProject = () => undefined,
	onRemoveRemoteProject = () => undefined,
	onRetryRemoteHosts = () => undefined,
	remoteWorkspaces = [],
	remoteFailedHostIds = [],
	onCloneProject,
	onCreateProject,
	onInitializeProject,
	onRemoveProject,
	resizeAuxiliaryTargetRef,
}: SidebarProps) {
	const { t } = useTranslation();
	const remoteNavigate = useNavigate();
	const selection = useSelection();
	const { state, setOpen, toggleSidebar } = useSidebar();
	const isCollapsed = state === "collapsed";
	const isIconRail = isCollapsed && SIDEBAR_COLLAPSIBLE === "icon";
	const [expandedChromeVisible, setExpandedChromeVisible] = useState(!isCollapsed);
	const updateStatus = useUpdateStatus();
	const openUpdateInstallPrompt = useUiStore((state) => state.openUpdateInstallPrompt);
	const daemonStatus = useShellMaybe()?.daemonStatus ?? null;
	const commandPaletteEnabled = useCommandPaletteEnabled();
	const settingsPage = useSettingsPage();
	const setCommandPaletteOpen = useUiStore((s) => s.setCommandPaletteOpen);
	const developerMode = useUiStore((s) => s.developerMode);
	const cloudArtifacts = useCloudOffering().artifacts;
	const artifactTotal = useMemo(
		() => artifactCount(groupArtifactsByProject(cloudArtifacts ? workspaces : workspaces.filter((workspace) => workspace.kind !== "cloud"))),
		[cloudArtifacts, workspaces],
	);
	const existingProjectPaths = useMemo(
		() => workspaces
			.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND)
			.map((workspace) => workspace.path)
			.filter((path): path is string => Boolean(path)),
		[workspaces],
	);
	const openExistingProject = useLatestCallback((path: string) => {
		const workspace = workspaces.find(
			(candidate) => candidate.kind !== STANDALONE_PROJECT_KIND && candidate.path === path,
		);
		if (workspace) selection.goProject(workspace.id);
	});
	const initialActiveSessionProjectId = useRef(
		selection.activeSessionId ? selection.activeProjectId : undefined,
	).current;
	useLayoutEffect(() => {
		if (!isCollapsed) {
			setExpandedChromeVisible(true);
		}
	}, [isCollapsed]);

	const [expandedIds, setExpandedIds] = useState<ReadonlySet<string>>(() => readExpandedProjectIds());
	const [collapsedRemoteProjects, setCollapsedRemoteProjects] = useState<ReadonlySet<string>>(() => new Set());
	const [dismissedInitialActiveProjectIds, setDismissedInitialActiveProjectIds] = useState<ReadonlySet<string>>(
		() => new Set(),
	);
	const toggleProjectDisclosure = useLatestCallback((id: string) => {
		const routeFallbackActive =
			initialActiveSessionProjectId === id && !dismissedInitialActiveProjectIds.has(id);
		const currentlyExpanded = expandedIds.has(id) || routeFallbackActive;
		setExpandedIds((prev) => {
			const next = new Set(prev);
			currentlyExpanded ? next.delete(id) : next.add(id);
			if (typeof window !== "undefined") {
				window.localStorage?.setItem(expandedProjectsStorageKey, JSON.stringify([...next]));
			}
			return next;
		});
		setDismissedInitialActiveProjectIds((prev) => {
			if (initialActiveSessionProjectId !== id) return prev;
			const next = new Set(prev);
			currentlyExpanded ? next.add(id) : next.delete(id);
			return next;
		});
	});
	const importedProjects = useSessionImportStore((state) => state.importedCounts);
	const revealedImports = useRef(new Set<string>());
	useEffect(() => {
		const fresh = Object.keys(importedProjects).filter((id) => !revealedImports.current.has(id));
		if (!fresh.length) return;
		for (const id of fresh) revealedImports.current.add(id);
		setExpandedIds((prev) => {
			const next = new Set([...prev, ...fresh]);
			window.localStorage?.setItem(expandedProjectsStorageKey, JSON.stringify([...next]));
			return next;
		});
	}, [importedProjects]);
	const [pinnedOpen, setPinnedOpen] = useState(() => readSectionsOpen().pinned);
	const [projectsOpen, setProjectsOpen] = useState(() => readSectionsOpen().projects);
	const [scratchpadOpen, setScratchpadOpen] = useState(() => readSectionsOpen().scratchpad);
	const toggleSection = (section: SidebarSection, open: boolean, setOpen: (open: boolean) => void) => {
		setOpen(!open);
		saveSectionOpen(section, !open);
	};

	const activeSessionKey = selection.activeSessionId ?? selection.activeRemoteSessionId;
	const activeProjectKey = selection.activeProjectId;
	useEffect(() => {
		if (!activeSessionKey || isCollapsed) return;
		if (activeProjectKey) {
			setExpandedIds((prev) => {
				if (prev.has(activeProjectKey)) return prev;
				const next = new Set(prev);
				next.add(activeProjectKey);
				window.localStorage?.setItem(expandedProjectsStorageKey, JSON.stringify([...next]));
				return next;
			});
		}
		let frame = 0;
		let attempts = 0;
		const reveal = () => {
			const row = document.querySelector<HTMLElement>(
				"[data-slot='sidebar-container'] [data-session-row] button[aria-current='page']",
			);
			if (row) {
				row.scrollIntoView?.({ block: "nearest" });
				return;
			}
			if (attempts === 0) {
				if (pinnedSessions.some((session) => session.id === activeSessionKey)) setPinnedOpen(true);
				if (standaloneWorkspace.sessions.some((session) => session.id === activeSessionKey)) {
					setScratchpadOpen(true);
				} else {
					setProjectsOpen(true);
				}
			}
			if (attempts++ < 3) frame = requestAnimationFrame(reveal);
		};
		frame = requestAnimationFrame(() => requestAnimationFrame(reveal));
		return () => cancelAnimationFrame(frame);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [activeSessionKey]);

	const resizeScopeRef = useRef<HTMLDivElement>(null);
	const getResizeTargets = useCallback(() => {
		const scope = resizeScopeRef.current;
		return [
			scope?.querySelector<HTMLElement>('[data-slot="sidebar-gap"]') ?? null,
			scope?.querySelector<HTMLElement>('[data-slot="sidebar-container"]') ?? null,
			resizeAuxiliaryTargetRef?.current ?? null,
		];
	}, [resizeAuxiliaryTargetRef]);
	const {
		onPointerDown: onResizePointerDown,
		onCollapsedPointerDown: onCollapsedResizePointerDown,
		onDoubleClick: onResizeDoubleClick,
	} = useResizable({
		cssVar: "--ao-sidebar-w",
		getCssTargets: getResizeTargets,
		storageKey: "ao-sidebar-w",
		defaultWidth: SIDEBAR_DEFAULT_WIDTH,
		min: sidebarMinWidth,
		max: SIDEBAR_MAX_WIDTH,
		edge: "right",
		reclampOnWindowResize: true,
		onExpand: () => setOpen(true),
	});

	const projectOrder = useSidebarOrderStore((state) => state.projectOrder);
	const pendingCreatedProjectIds = useSidebarOrderStore((state) => state.pendingCreatedProjectIds);
	const setProjectOrder = useSidebarOrderStore((state) => state.setProjectOrder);
	const pinnedProjectIds = useSidebarOrderStore((state) => state.pinnedProjectIds);
	useLayoutEffect(() => {
		useSidebarOrderStore.getState().reconcileProjectOrder(workspaces.map((workspace) => workspace.id));
	}, [workspaces, pendingCreatedProjectIds]);
	const orderedWorkspaces = useMemo(
		() => pinnedFirst(applyOrder(workspaces, (workspace) => workspace.id, projectOrder, "start"), (workspace) => workspace.id, pinnedProjectIds),
		[pinnedProjectIds, projectOrder, workspaces],
	);

	const projectWorkspaces = useMemo(() => {
		const scratchpads = new Set(ownScratchpadWorkspaces(orderedWorkspaces));
		return orderedWorkspaces.filter((workspace) => !scratchpads.has(workspace));
	}, [orderedWorkspaces]);
	const standaloneWorkspace = useMemo((): WorkspaceSummary => {
		const scratchpads = ownScratchpadWorkspaces(workspaces);
		const local = scratchpads.find((workspace) => workspace.kind === STANDALONE_PROJECT_KIND);
		if (scratchpads.length === 1 && local) return local;
		return {
			...(local ?? scratchpadSummary(t("standalone.workspaceName"))),
			sessions: scratchpads.flatMap((workspace) => workspace.sessions),
		};
	}, [t, workspaces]);
	const projectAvatars = useProjectAvatars(projectWorkspaces);
	const {
		listed: visibleWorkspaces,
		hiddenCount: hiddenProjectCount,
		showAll: showAllProjects,
		toggleShowAll: toggleShowAllProjects,
	} = useShowMoreCap(projectWorkspaces, SIDEBAR_INITIAL_SECTION_LIMIT, selection.activeProjectId, isIconRail);
	const projectContentOpen = (projectWorkspaces.length > 0 || remoteHosts.length > 0) && (projectsOpen || isIconRail);
	const projectIds = useMemo(
		() => projectWorkspaces.map((workspace) => workspace.id),
		[projectWorkspaces],
	);
	const projectDragClickGuard = usePostDragClickGuard();
	const [draggingProjectId, setDraggingProjectId] = useState<string | null>(null);
	const draggingProjectIdRef = useRef<string | null>(null);
	const projectDropTargetRef = useRef<{ overId: string; placement: ProjectDropPlacement } | null>(null);
	useGrabbingCursor(draggingProjectId !== null);
	const [dropLine, setDropLine] = useState<{ top: number; visible: boolean }>({ top: 0, visible: false });

	const clearProjectDropIndicator = useCallback(() => {
		projectDropTargetRef.current = null;
		setDropLine((previous) => ({ ...previous, visible: false }));
	}, []);

	const handleProjectDragStart = useCallback((event: ReactDragEvent<HTMLElement>, projectId: string) => {
		if (!projectIds.includes(projectId)) return;
		event.dataTransfer.effectAllowed = "move";
		event.dataTransfer.setData("text/plain", projectId);
		if (EMPTY_DRAG_IMAGE) event.dataTransfer.setDragImage(EMPTY_DRAG_IMAGE, 0, 0);
		draggingProjectIdRef.current = projectId;
		projectDropTargetRef.current = null;
		setDraggingProjectId(projectId);
	}, [projectIds]);

	const handleProjectDragEnd = useCallback(() => {
		const projectId = draggingProjectIdRef.current;
		if (projectId) projectDragClickGuard.markDragEnded(projectId);
		draggingProjectIdRef.current = null;
		clearProjectDropIndicator();
		setDraggingProjectId(null);
	}, [clearProjectDropIndicator, projectDragClickGuard]);

	const handleProjectDragOver = useCallback((event: ReactDragEvent<HTMLElement>, overId: string) => {
		const activeId = draggingProjectIdRef.current;
		if (!activeId || activeId === overId) {
			clearProjectDropIndicator();
			return;
		}
		const row = event.currentTarget;
		const rect = row.getBoundingClientRect();
		const placement: ProjectDropPlacement = event.clientY <= rect.top + rect.height / 2 ? "before" : "after";
		const pinned = useSidebarOrderStore.getState().pinnedProjectIds;
		if (pinned.includes(activeId) !== pinned.includes(overId) || reorderAtProjectBoundary(projectIds, activeId, overId, placement) === null) {
			clearProjectDropIndicator();
			return;
		}
		event.preventDefault();
		event.dataTransfer.dropEffect = "move";
		const target = projectDropTargetRef.current;
		if (target?.overId === overId && target.placement === placement) return;
		projectDropTargetRef.current = { overId, placement };
		const rows = row.parentElement
			? Array.from(row.parentElement.querySelectorAll<HTMLElement>(":scope > [data-project-drop-target]"))
			: [row];
		const index = rows.indexOf(row);
		const insertAt = placement === "before" ? index : index + 1;
		const last = rows[rows.length - 1];
		const top =
			insertAt <= 0
				? rows[0].offsetTop
				: insertAt >= rows.length
					? last.offsetTop + last.offsetHeight
					: (rows[insertAt - 1].offsetTop + rows[insertAt - 1].offsetHeight + rows[insertAt].offsetTop) / 2;
		setDropLine({ top, visible: true });
	}, [clearProjectDropIndicator, projectIds]);

	const handleProjectDrop = useCallback((event: ReactDragEvent<HTMLElement>) => {
		const activeId = draggingProjectIdRef.current;
		const target = projectDropTargetRef.current;
		if (!activeId || !target) return;
		event.preventDefault();
		const next = reorderAtProjectBoundary(projectIds, activeId, target.overId, target.placement);
		if (next) setProjectOrder(next);
		handleProjectDragEnd();
	}, [handleProjectDragEnd, projectIds, setProjectOrder]);

	const pinnedSessions = useMemo(
		() => [...workspaces, ...remoteWorkspaces]
			.flatMap((w) => workerSessions(w.sessions))
			.filter((s) => s.isPinned && s.isTerminated !== true)
			.sort((a, b) => {
				const aTime = a.pinnedAt ? new Date(a.pinnedAt).getTime() : 0;
				const bTime = b.pinnedAt ? new Date(b.pinnedAt).getTime() : 0;
				return bTime - aTime;
			}),
		[workspaces, remoteWorkspaces],
	);

	const openPinnedSession = useLatestCallback((target: WorkspaceSession) => {
		if (target.kind === "worker") recordManualWorkerOpen(target.id, target.hostId);
		if (target.hostId) void remoteNavigate(sessionNavigateTarget(target.workspaceId, target.id, target.hostId));
		else selection.goSession(target.workspaceId, target.id);
	});
	const handlePinnedSessionKilled = useLatestCallback(
		(killedSession: WorkspaceSession) => {
			if (killedSession.hostId) {
				if (selection.activeRemoteHostId !== killedSession.hostId || selection.activeRemoteSessionId !== killedSession.id) return;
				const workspace = remoteWorkspaces.find((candidate) => candidate.hostId === killedSession.hostId && candidate.id === killedSession.workspaceId);
				const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id);
				if (nextRoute.target === "session") void remoteNavigate(sessionNavigateTarget(killedSession.workspaceId, nextRoute.sessionId, killedSession.hostId));
				else if (killedSession.workspaceId === STANDALONE_WORKSPACE_ID) selection.goHome();
				else onOpenRemoteProject(killedSession.hostId, killedSession.workspaceId);
				return;
			}
			if (selection.activeSessionId !== killedSession.id) return;
			const workspace = workspaces.find((w) => w.id === killedSession.workspaceId);
			const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id);
			if (nextRoute.target === "session") {
				selection.goSession(killedSession.workspaceId, nextRoute.sessionId);
			} else if (workspace?.standaloneAgent) {
				selection.goHome();
			} else {
				selection.goProject(killedSession.workspaceId);
			}
		},
	);

	return (
		<SidebarRoot
			collapsible={SIDEBAR_COLLAPSIBLE}
			resizeScopeRef={resizeScopeRef}
			data-expanded-chrome={expandedChromeVisible ? "visible" : "hidden"}
			data-topbar-offset={underTopbar ? topbarOffset : undefined}
			className={cn(
				"sidebar-focusless",
				"pl-px border-transparent",
				!underTopbar
					? "top-0 bottom-0"
					: "top-(--sidebar-chrome-offset) bottom-0 h-auto!",
			)}
		>
			<SidebarHeader className="gap-0 p-0 px-3 pt-2 group-data-[collapsible=icon]:px-1.5 group-data-[collapsible=icon]:pt-2">
				<Pressable
					aria-label={t("shell.goHome")}
					className={cn(
						"group/brand flex w-full shrink-0 items-center gap-1.5 rounded-md px-0.5 text-left",
						brandInTitlebar && "hidden",
						"group-data-[collapsible=icon]:flex-col group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:gap-1 group-data-[collapsible=icon]:px-0 group-data-[collapsible=icon]:pb-2",
						commandPaletteEnabled ? "pb-2" : "pb-3",
					)}
					data-sidebar-brand=""
					onClick={() => selection.goHome()}
					type="button"
				>
					<span
						className="sidebar-expanded-chrome min-w-0 flex-1 truncate text-base font-semibold leading-tight tracking-tight-lg text-foreground group-data-[collapsible=icon]:hidden"
					>
						<BuildInfoTooltip>Orchestrator</BuildInfoTooltip>
					</span>
				</Pressable>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={isCollapsed ? t("shell.expandSidebar") : t("shell.collapseSidebar")}
							className="hidden size-control-board rounded-lg group-data-[collapsible=icon]:inline-flex [&_svg]:size-icon-base"
							onClick={toggleSidebar}
							size="none"
							type="button"
							variant="quiet"
						>
							<PanelLeft aria-hidden="true" />
						</Button>
					</TooltipTrigger>
					<TooltipContent shortcut="toggle-sidebar" side="right">
						{isCollapsed ? t("shell.expandSidebar") : t("shell.collapseSidebar")}
					</TooltipContent>
				</Tooltip>
			</SidebarHeader>

			{settingsPage ? (
				<SettingsSidebarNav layer={settingsPage} />
			) : (
			<>
			<div className="flex shrink-0 flex-col gap-0 px-2 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
				{commandPaletteEnabled ? (
					<SidebarGroup className="p-0 pb-0.5 group-data-[collapsible=icon]:pb-1">
						<SidebarGroupContent>
							<SidebarMenu className="gap-0.5 group-data-[collapsible=icon]:gap-1">
								<SidebarSearchButton onOpen={() => setCommandPaletteOpen(true)} />
							</SidebarMenu>
						</SidebarGroupContent>
					</SidebarGroup>
				) : null}
				<SidebarMenu className="mb-3 gap-0.5 group-data-[collapsible=icon]:gap-1">
					<SidebarMenuItem>
						<SidebarTopNavRow
							active={selection.isAutomations}
							icon={<CalendarClock aria-hidden="true" />}
							label={t("automations.title")}
							onClick={selection.goAutomations}
							tooltip={isCollapsed ? t("automations.title") : undefined}
						/>
					</SidebarMenuItem>
					<SidebarMenuItem>
						<SidebarTopNavRow
							active={selection.isArtifacts}
							icon={<Package aria-hidden="true" />}
							label={t("artifacts.title")}
							onClick={selection.goArtifacts}
							tooltip={isCollapsed ? t("artifacts.title") : undefined}
							trailing={artifactTotal > 0 ? String(artifactTotal) : undefined}
						/>
					</SidebarMenuItem>
					{developerMode ? (
						<SidebarMenuItem>
							<SidebarTopNavRow
								active={selection.isUsage}
								icon={<Gauge aria-hidden="true" />}
								label={t("usage.title")}
								onClick={selection.goUsage}
								tooltip={isCollapsed ? t("usage.title") : undefined}
							/>
						</SidebarMenuItem>
					) : null}
				</SidebarMenu>

				{pinnedSessions.length > 0 && (
					<div className="sidebar-expanded-chrome flex shrink-0 flex-col group-data-[collapsible=icon]:hidden">
						<SectionDisclosure
							label={t("shell.pinned")}
							open={pinnedOpen}
							onToggle={() => toggleSection("pinned", pinnedOpen, setPinnedOpen)}
							className="mb-1"
						/>
						<AnimatedSectionBody open={pinnedOpen}>
							<SidebarMenuSub
								className="sidebar-expanded-chrome mx-0 ml-0 translate-x-0 gap-0.5 border-l-0 px-0 py-0.5 mb-2"
								data-testid="pinned-session-list"
							>
								{pinnedSessions.map((session) => (
									<PinnedSessionRow
										key={sessionUiKey(session.id, session.hostId)}
										session={session}
										active={session.hostId
											? selection.activeRemoteHostId === session.hostId && selection.activeRemoteSessionId === session.id
											: selection.activeSessionId === session.id}
										hostLabel={session.hostId ? remoteHosts.find((host) => host.hostId === session.hostId)?.label ?? session.hostId : undefined}
										onKilled={handlePinnedSessionKilled}
										onOpenSession={openPinnedSession}
									/>
								))}
							</SidebarMenuSub>
						</AnimatedSectionBody>
					</div>
				)}
			</div>

			<SidebarContent className="scrollbar-none gap-0 px-2 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
				<SidebarGroup className="min-h-0 flex-1 p-0">
					<SidebarGroupContent
						className="sidebar-sections-container grid min-h-0 flex-1 auto-rows-[minmax(0,auto)] content-start group-data-[collapsible=icon]:flex group-data-[collapsible=icon]:flex-col"
					>
						{!workspaceError ? (
							<ScratchpadSection
								workspace={standaloneWorkspace}
								selection={selection}
								isCollapsed={isIconRail}
								open={scratchpadOpen}
								onToggle={() => toggleSection("scratchpad", scratchpadOpen, setScratchpadOpen)}
							/>
						) : null}
						<div className="sidebar-expanded-chrome flex shrink-0 pb-0.5 group-data-[collapsible=icon]:hidden">
							<SectionDisclosure
								label={t("shell.projects")}
								open={projectsOpen}
								onToggle={() => toggleSection("projects", projectsOpen, setProjectsOpen)}
								trailing={
									<CreateProjectButton
										existingProjectPaths={existingProjectPaths}
										remoteHosts={remoteHosts}
										onCreateRemoteProject={onCreateRemoteProject}
										onInitializeRemoteProject={onInitializeRemoteProject}
										onCloneProject={onCloneProject}
										onCreateProject={onCreateProject}
										onInitializeProject={onInitializeProject}
										onOpenExistingProject={openExistingProject}
									/>
								}
							/>
						</div>
						{workspaceError ? (
							<div className="sidebar-expanded-chrome px-2.5 py-3 group-data-[collapsible=icon]:hidden">
								<p className="text-sm text-foreground">{t("shell.couldNotLoadProjects")}</p>
								<p className="mt-1 text-caption text-passive">{workspaceError}</p>
							</div>
						) : null}
						{projectWorkspaces.length > 0 || remoteHosts.length > 0 ? (
							<AnimatedSectionBody open={projectContentOpen} className="min-h-0 flex-initial group-data-[collapsible=icon]:flex-none">
								<SidebarSectionScroller
									className={`${SECTION_SCROLLER_CLASS} min-h-0 flex-1`}
									testId="sidebar-projects-scroller"
									wrapperClassName="flex flex-col flex-1"
								>
									<SidebarMenu className="relative gap-0.5 rounded-lg group-data-[collapsible=icon]:gap-1 group-data-[collapsible=icon]:rounded-none">
										<AnimatePresence initial={false}>
											{!workspaceError && visibleWorkspaces.map((workspace) => (
												<ProjectItem
													key={workspace.id}
													workspace={workspace}
													avatarUrl={projectAvatars.get(workspace.id)}
													expanded={expandedIds.has(workspace.id) || (initialActiveSessionProjectId === workspace.id && !dismissedInitialActiveProjectIds.has(workspace.id))}
													suppressInitialExpandAnimation={expandedIds.has(workspace.id)}
													selection={selection}
													isDragged={draggingProjectId === workspace.id}
													projectDragInProgress={draggingProjectId !== null}
													consumeDragClick={projectDragClickGuard.consumeClick}
													onToggle={toggleProjectDisclosure}
													onRemoveProject={onRemoveProject}
													onProjectDragStart={handleProjectDragStart}
													onProjectDragEnd={handleProjectDragEnd}
													onProjectDragOver={handleProjectDragOver}
													onProjectDrop={handleProjectDrop}
												/>
											))}
										</AnimatePresence>
										<RemoteHostsSection
											hosts={remoteHosts}
											workspaces={remoteWorkspaces}
											failedHostIds={remoteFailedHostIds}
											renderProject={(host, workspace) => {
												const hostId = host.hostId;
												const projectKey = sessionUiKey(workspace.id, hostId);
												const scopedSelection: Selection = {
													...selection,
													activeProjectId: selection.activeRemoteHostId === hostId ? selection.activeRemoteProjectId : undefined,
													activeSessionId: selection.activeRemoteHostId === hostId ? selection.activeRemoteSessionId : undefined,
												goProject: (projectId) => { onOpenRemoteProject(hostId, projectId); return undefined; },
													goSession: (projectId, sessionId) => { void remoteNavigate(sessionNavigateTarget(projectId, sessionId, hostId)); },
													goSettings: (projectId) => onConfigureRemoteProject(hostId, projectId),
												};
												return <ProjectItem
													key={projectKey}
													workspace={workspace}
													hostLabel={host.label}
													expanded={!collapsedRemoteProjects.has(projectKey)}
													selection={scopedSelection}
													isDragged={false}
													projectDragInProgress={false}
													consumeDragClick={() => false}
													onToggle={() => setCollapsedRemoteProjects((previous) => {
														const next = new Set(previous);
														next.has(projectKey) ? next.delete(projectKey) : next.add(projectKey);
														return next;
													})}
													onRemoveProject={(projectId) => onRemoveRemoteProject(hostId, projectId)}
													onOpenOrchestrator={() => onOpenRemoteOrchestrator(hostId, workspace.id)}
													suppressInitialExpandAnimation
													onProjectDragStart={() => undefined}
													onProjectDragEnd={() => undefined}
													onProjectDragOver={() => undefined}
													onProjectDrop={() => undefined}
												/>;
											}}
											onRetry={onRetryRemoteHosts}
										/>
										{isIconRail && <CreateProjectListItem />}
										<div
											aria-hidden="true"
											data-project-drop-line=""
											className="pointer-events-none absolute inset-x-0 z-[70] h-px rounded-full bg-foreground transition-opacity duration-100"
											style={{ top: dropLine.top, opacity: dropLine.visible ? 1 : 0 }}
										/>
									</SidebarMenu>
								</SidebarSectionScroller>
								{!isIconRail && hiddenProjectCount > 0 ? (
									<ShowMoreRow
										expanded={showAllProjects}
										label={
											showAllProjects
												? t("shell.showLessProjects")
												: t("shell.showMoreProjects", { count: hiddenProjectCount })
										}
											onClick={toggleShowAllProjects}
									/>
								) : null}
							</AnimatedSectionBody>
						) : null}
					</SidebarGroupContent>
				</SidebarGroup>
			</SidebarContent>
			</>
			)}

			<SidebarFooter
				className="relative mt-auto gap-0 overflow-hidden px-2 !pb-2 !pt-0 transition-[padding] duration-200 ease-linear group-data-[collapsible=icon]:min-h-20 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:overflow-visible group-data-[collapsible=icon]:px-1.5 group-data-[collapsible=icon]:!pb-2 group-data-[collapsible=icon]:!pt-1.5"
			>
				{daemonStatus && (
					<span aria-hidden="true" className="sr-only" data-testid="daemon-status" data-state={daemonStatus.state}>
						daemon {daemonStatus.state}
					</span>
				)}
				{settingsPage ? (
					<>
						<div className={FOOTER_UPDATES_CLASS}>
							<SidebarUpdateRow
								onRequestInstall={openUpdateInstallPrompt}
								status={updateStatus}
								tabIndex={isCollapsed ? -1 : 0}
							/>
						</div>
						<div className={FOOTER_BOTTOM_ROW_CLASS}>
							<Pressable
								className={FOOTER_NAV_BUTTON_CLASS}
								disabled={settingsPage.cueBusy}
								onClick={settingsPage.close}
								type="button"
							>
								<NavRowHighlight />
								<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2 [&_svg]:size-icon-md [&_svg]:shrink-0">
									<ArrowLeft aria-hidden="true" />
									<span className="tracking-tight">{t("settings.back")}</span>
								</span>
							</Pressable>
						</div>
					</>
				) : (
				<>
				<div
					aria-hidden={isCollapsed || undefined}
					hidden={isIconRail}
					className="sidebar-expanded-chrome relative flex w-full min-w-46.5 flex-col"
				>
					<div className={FOOTER_UPDATES_CLASS}>
						<SidebarUpdateRow
							onRequestInstall={openUpdateInstallPrompt}
							status={updateStatus}
							tabIndex={isCollapsed ? -1 : 0}
						/>
					</div>
					<FooterAccountRow
						onConnectMobile={() => selection.goConnectMobile()}
						onOpenSettings={() => selection.goGlobalSettings()}
						tabIndex={isCollapsed ? -1 : 0}
					/>
				</div>
				<div
					aria-hidden={!isCollapsed || undefined}
					className="pointer-events-none absolute inset-x-1.5 bottom-0 top-auto flex min-h-row-md flex-col items-center justify-end gap-1 opacity-0 transition-opacity duration-150 ease-out group-data-[collapsible=icon]:pointer-events-auto group-data-[collapsible=icon]:!bottom-2 group-data-[collapsible=icon]:opacity-100"
				>
					<SidebarUpdateRail
						onRequestInstall={openUpdateInstallPrompt}
						status={updateStatus}
						tabIndex={isCollapsed ? 0 : -1}
					/>
					<CloudSignInRailButton tabIndex={isCollapsed ? 0 : -1} />
					<CloudAccountRailButton tabIndex={isCollapsed ? 0 : -1} />
					<Tooltip>
						<TooltipTrigger asChild>
							<Pressable
								aria-label={t("settings.connectMobile")}
								className={FOOTER_RAIL_BUTTON_CLASS}
								onClick={() => selection.goConnectMobile()}
								tabIndex={isCollapsed ? 0 : -1}
								type="button"
							>
								<NavRowHighlight />
								<span className="relative z-[1] grid place-items-center [&_svg]:size-icon-base">
									<Smartphone aria-hidden="true" />
								</span>
							</Pressable>
						</TooltipTrigger>
						<TooltipContent side="right">{t("settings.connectMobile")}</TooltipContent>
					</Tooltip>
					<Tooltip>
						<TooltipTrigger asChild>
							<Pressable
								aria-label={t("shell.settings")}
								className={FOOTER_RAIL_BUTTON_CLASS}
								onClick={() => selection.goGlobalSettings()}
								tabIndex={isCollapsed ? 0 : -1}
								type="button"
							>
								<NavRowHighlight />
								<span className="relative z-[1] grid place-items-center [&_svg]:size-icon-base">
									<Settings aria-hidden="true" />
								</span>
							</Pressable>
						</TooltipTrigger>
						<TooltipContent shortcut="open-settings" side="right">{t("shell.settings")}</TooltipContent>
					</Tooltip>
				</div>
				</>
				)}
			</SidebarFooter>

			<ResizeHandle
				className="group-data-[state=collapsed]:hidden"
				onDoubleClick={onResizeDoubleClick}
				onPointerDown={onResizePointerDown}
				side="right"
				style={noDragStyle}
			/>
			<SidebarRail
				aria-label={t("shell.expandSidebar")}
				className="group-data-[state=expanded]:hidden hover:after:bg-transparent"
				onClick={() => setOpen(true)}
				onPointerDown={onCollapsedResizePointerDown}
			/>
		</SidebarRoot>
	);
});

type Selection = ReturnType<typeof useSelection>;

type ProjectItemProps = {
	workspace: WorkspaceSummary;
	avatarUrl?: string;
	hostLabel?: string;
	onOpenOrchestrator?: () => void;
	expanded: boolean;
	selection: Selection;
	isDragged: boolean;
	projectDragInProgress: boolean;
	consumeDragClick: (id: string) => boolean;
	onToggle: (projectId: string) => void;
	onRemoveProject: (projectId: string, cloud: boolean) => void;
	suppressInitialExpandAnimation: boolean;
	onProjectDragStart: (event: ReactDragEvent<HTMLElement>, projectId: string) => void;
	onProjectDragEnd: () => void;
	onProjectDragOver: (event: ReactDragEvent<HTMLElement>, overId: string) => void;
	onProjectDrop: (event: ReactDragEvent<HTMLElement>) => void;
};

function projectSelectionKey(workspace: WorkspaceSummary, selection: Selection): string {
	const sessionId = selection.activeSessionId ?? "";
	if (selection.activeProjectId === workspace.id) return `active\u0000${sessionId}`;
	return workspace.sessions.some((session) => session.id === sessionId) ? sessionId : "";
}

function projectItemPropsEqual(previous: ProjectItemProps, next: ProjectItemProps): boolean {
	const keys = Object.keys(next) as (keyof ProjectItemProps)[];
	if (keys.length !== Object.keys(previous).length) return false;
	for (const key of keys) {
		if (key !== "selection" && !Object.is(previous[key], next[key])) return false;
	}
	const a = previous.selection;
	const b = next.selection;
	return a.goSession === b.goSession && a.goProject === b.goProject && a.goHome === b.goHome &&
		a.goSettings === b.goSettings && projectSelectionKey(previous.workspace, a) === projectSelectionKey(next.workspace, b);
}

function LazyMenuEntries({ build }: { build: () => MenuEntry[] }) {
	return <MenuEntries entries={build()} />;
}

const ProjectItem = memo(function ProjectItem({
	workspace,
	avatarUrl,
	hostLabel,
	onOpenOrchestrator,
	expanded,
	selection,
	isDragged,
	projectDragInProgress,
	consumeDragClick,
	onToggle,
	onRemoveProject,
	suppressInitialExpandAnimation,
	onProjectDragStart,
	onProjectDragEnd,
	onProjectDragOver,
	onProjectDrop,
}: ProjectItemProps) {
	const { t } = useTranslation();
	const nameWithHost = hostLabel ? `${workspace.name} · ${hostLabel}` : workspace.name;
	const isStandalone = workspace.id === STANDALONE_WORKSPACE_ID;
	const isAgent = workspace.standaloneAgent === true;
	const prefersReducedMotion = useReducedMotion();
	const activeProjectMatches = selection.activeProjectId === workspace.id;
	const projectHeaderRef = useRef<HTMLDivElement>(null);
	const projectSessionsRef = useRef<HTMLDivElement>(null);
	const dashboardActive = activeProjectMatches && !selection.activeSessionId;
	const orchestratorActive =
		activeProjectMatches &&
		workspace.sessions.some(
			(session) => session.id === selection.activeSessionId && session.kind === "orchestrator",
		);

	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [isSpawning, setIsSpawning] = useState(false);
	const [animReady, setAnimReady] = useState(false);
	const hasInteractedWithDisclosure = useRef(false);
	useEffect(() => {
		const id = window.setTimeout(() => setAnimReady(true), 500);
		return () => window.clearTimeout(id);
	}, []);
	const projectKey = sessionUiKey(workspace.id, workspace.hostId);
	const isProjectProvisioning = useUiStore((state) => state.provisioningProjectIds.has(projectKey));
	const projectActive = dashboardActive || orchestratorActive || (activeProjectMatches && isProjectProvisioning);
	const isProjectRestarting = useUiStore((state) => state.restartingProjectIds.has(projectKey));
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const projectIsDragging = isDragged;
	const visibleSessions = useMemo(
		() => (workspace.shared
			? [...workspace.sessions].sort((a, b) => Number(isOrchestratorSession(b)) - Number(isOrchestratorSession(a)))
			: sortedWorkerSessions(workspace.sessions)
		).filter((session) => session.isTerminated !== true),
		[workspace.sessions, workspace.shared],
	);
	const projectNeedsInput = useMemo(() => visibleSessions.some(sessionNeedsInput), [visibleSessions]);
	const [sessionOrder, setSessionOrder] = useState<string[]>([]);
	const sessions = useMemo(
		() => {
			const ordered = applyOrder(visibleSessions, (session) => session.id, sessionOrder, "start");
			return floatAttention(ordered, sessionOrder.length > 0);
		},
		[sessionOrder, visibleSessions],
	);
	useEffect(() => {
		const sessionIds = sessions.map((session) => session.id);
		useSidebarOrderStore.getState().setSessionOrder(projectKey, sessionIds);
		return () => useSidebarOrderStore.getState().clearSessionOrder(projectKey);
	}, [projectKey, sessions]);
	const {
		listed: listedSessions,
		hiddenCount: hiddenSessionCount,
		showAll: showAllSessions,
		toggleShowAll: toggleShowAllSessions,
	} = useShowMoreCap(sessions, SIDEBAR_PROJECT_SESSION_LIMIT, selection.activeSessionId);
	useLayoutEffect(() => {
		const header = projectHeaderRef.current;
		const content = projectSessionsRef.current;
		const scroller = header?.closest<HTMLElement>("[data-sidebar-section-scroller]");
		if (!activeProjectMatches || !expanded || !header || !content || !scroller) return;
		const updateClip = () => {
			const covered = Math.max(0, header.getBoundingClientRect().bottom - content.getBoundingClientRect().top);
			content.style.clipPath = `inset(${covered}px 0 0 0)`;
		};
		updateClip();
		scroller.addEventListener("scroll", updateClip, { passive: true });
		const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(updateClip);
		observer?.observe(header);
		observer?.observe(content);
		return () => {
			scroller.removeEventListener("scroll", updateClip);
			observer?.disconnect();
			content.style.removeProperty("clip-path");
		};
	}, [activeProjectMatches, expanded, showAllSessions]);
	const listedSessionIds = useStableArray(useMemo(() => listedSessions.map((session) => session.id), [listedSessions]));
	const commitSessionOrder = useCallback(
		(next: string[] | null) => {
			if (!next) return;
			const listed = new Set(next);
			setSessionOrder([...next, ...sessions.filter((session) => !listed.has(session.id)).map((session) => session.id)]);
		},
		[sessions],
	);
	const openSession = useLatestCallback((sessionId: string) => {
		recordManualWorkerOpen(sessionId);
		selection.goSession(workspace.id, sessionId);
	});
	const handleSessionKilled = useLatestCallback((killedSession: WorkspaceSession) => {
		if (selection.activeSessionId !== killedSession.id) return;
		const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id, sessions);
		if (nextRoute.target === "session") {
			selection.goSession(workspace.id, nextRoute.sessionId);
		} else if (workspace.id === STANDALONE_WORKSPACE_ID || isAgent) {
			selection.goHome();
		} else {
			selection.goProject(workspace.id);
		}
	});
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	const canResumeOrchestrator = useCanResumeAgent(orchestrator, workspace.hostId);
	const orchestratorReply = useOrchestratorReplyState({
		projectId: workspace.id,
		hostId: workspace.hostId,
		orchestrator,
		viewing: orchestratorActive,
	});
	const toggleDisclosure = () => {
		hasInteractedWithDisclosure.current = true;
		onToggle(workspace.id);
	};

	const openOrchestrator = async () => {
		if (isSpawning || isProjectProvisioning || isProjectRestarting) return;
		if (!expanded) toggleDisclosure();
		if (onOpenOrchestrator) {
			onOpenOrchestrator();
			return;
		}
		if (orchestrator) {
			if (canResumeOrchestrator && workspace.kind !== "cloud") {
				setIsSpawning(true);
				try {
					const resume = resumeOrchestrator(orchestrator.id);
					selection.goSession(workspace.id, orchestrator.id);
					await resume;
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
					return;
				} catch (err) {
					console.error("Failed to resume orchestrator:", err);
					showGlobalToast(
						t("inspector.resumeAgent"),
						err instanceof Error ? err.message : t("shell.couldNotSpawn"),
						"error",
					);
					return;
				} finally {
					setIsSpawning(false);
				}
			}
			selection.goSession(workspace.id, orchestrator.id);
			return;
		}
		if (workspace.shared) return;
		if (workspace.kind === "cloud") {
			setIsSpawning(true);
			try {
				const sessionId = await spawnCloudOrchestrator(queryClient, workspace.id);
				await queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
				selection.goSession(workspace.id, sessionId);
			} catch (err) {
				console.error("Failed to spawn cloud orchestrator:", err);
				void captureRendererException(err, { source: "orchestrator-open", operation: "open_orchestrator", surface: "sidebar" });
			} finally {
				setIsSpawning(false);
			}
			return;
		}
		if (!hasConfiguredOrchestratorAgent(workspace)) {
					selection.goSettings(workspace.id, workspace.cloudOrgId);
			return;
		}
		setIsSpawning(true);
		try {
			const sessionId = await spawnOrchestrator(workspace.id, "sidebar");
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			selection.goSession(workspace.id, sessionId);
		} catch (err) {
			console.error("Failed to spawn orchestrator:", err);
			void captureRendererException(err, { source: "orchestrator-open", operation: "open_orchestrator", surface: "sidebar" });
		} finally {
			setIsSpawning(false);
		}
	};

	const onProjectClick = () => {
		if (consumeDragClick(workspace.id)) return;
		if (isStandalone) {
			toggleDisclosure();
			return;
		}
		if (workspace.standaloneAgent) {
			if (!expanded) toggleDisclosure();
			const agentSession = workerSessions(workspace.sessions)[0];
			if (agentSession) selection.goSession(workspace.id, agentSession.id);
			return;
		}
		if (!expanded) {
			toggleDisclosure();
			selection.goProject(workspace.id);
		} else if (dashboardActive) {
			toggleDisclosure();
		} else {
			selection.goProject(workspace.id);
		}
	};

	const onFolderClick = (event: MouseEvent) => {
		event.stopPropagation();
		if (consumeDragClick(workspace.id)) return;
		toggleDisclosure();
	};


	const removeProject = () => setConfirmOpen(true);
	const isCloudProject = !workspace.hostId && workspace.kind === CLOUD_PROJECT_KIND;
	const copyToClipboard = useCopyToClipboard();
	const canPin = !isStandalone && !workspace.hostId;
	const isPinned = useSidebarOrderStore((state) => canPin && state.pinnedProjectIds.includes(workspace.id));
	const setProjectPinned = useSidebarOrderStore((state) => state.setProjectPinned);
	const openPullRequestCount = new Set(
		workspace.sessions.flatMap((session) => openPRs(session).map((pr) => pr.url)),
	).size;
	const openArchive = () => useUiStore.getState().openArchive({
		projectId: workspace.id,
		hostId: workspace.hostId,
		returnFocus: menuFocusReturnTarget(),
	});
	const buildMenuEntries = (): MenuEntry[] => {
		const archivedCount = isStandalone ? 0 : workerSessions(workspace.sessions).filter(isArchivedSession).length;
		return [
			...(!isAgent
				? [{
					kind: "item" as const,
					id: "new-task",
					icon: <Plus aria-hidden="true" />,
					label: t("shell.newTask"),
					disabled: isProjectRestarting,
					onSelect: () => requestNewTask(workspace.id, workspace.hostId),
				}]
				: []),
			...(!isStandalone && !isAgent && (!workspace.shared || orchestrator)
				? [{
					kind: "item" as const,
					id: "orchestrator",
					icon: <OrchestratorIcon aria-hidden="true" />,
					label: orchestrator ? t("shell.openOrchestrator") : t("shell.spawnOrchestratorLower"),
					disabled: isSpawning || isProjectProvisioning || isProjectRestarting,
					onSelect: () => void openOrchestrator(),
				}]
				: []),
			...(!isStandalone && !isAgent
				? [{
					kind: "item" as const,
					id: "board",
					icon: <LayoutGrid aria-hidden="true" />,
					label: t("shell.openDashboard"),
					onSelect: () => selection.goProject(workspace.id),
				}]
				: []),
			...(archivedCount > 0
				? [{
					kind: "item" as const,
					id: "archive",
					icon: <Archive aria-hidden="true" />,
					label: t("shell.archivedSessions"),
					count: archivedCount,
					ariaLabel: t("shell.archivedSessionsAria", { count: archivedCount }),
					onSelect: openArchive,
				}]
				: []),
			{ kind: "separator", id: "path-sep" },
			...(workspace.path && !workspace.hostId
				? [{
					kind: "item" as const,
					id: "copy-path",
					icon: <Copy aria-hidden="true" />,
					label: t("editor.copyPath"),
					onSelect: () => copyToClipboard(workspace.path),
				}]
				: []),
			{ kind: "separator", id: "project-sep" },
			...(canPin
				? [{
					kind: "item" as const,
					id: "pin",
					icon: isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />,
					label: isPinned ? t("shell.unpinProject") : t("shell.pinProject"),
					onSelect: () => setProjectPinned(workspace.id, !isPinned),
				}]
				: []),
			...(!isStandalone && !isAgent
				? [
					{
						kind: "item" as const,
						id: "settings",
						icon: <Settings aria-hidden="true" />,
						label: t("shell.projectSettings"),
						onSelect: () => selection.goSettings(workspace.id, workspace.cloudOrgId),
					},
					...(workspace.shared ? [] : [{
						kind: "item" as const,
						id: "remove",
						icon: <Trash2 aria-hidden="true" />,
						label: isCloudProject ? t("shell.deleteCloudProjectTitle") : t("shell.removeProjectTitle"),
						destructive: true,
						onSelect: removeProject,
					}]),
				]
				: []),
		];
	};

	const handleConfirmRemove = () => {
		setConfirmOpen(false);
		onRemoveProject(workspace.id, workspace.kind === CLOUD_PROJECT_KIND);
	};

	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<motion.li
					className={cn(
						"group/menu-item relative group-data-[collapsible=icon]:mb-0",
						projectIsDragging && "opacity-50",
					)}
					data-dragging={projectIsDragging ? "true" : undefined}
					data-project-drop-target=""
					data-project-id={workspace.id}
					data-remote-project-row={workspace.hostId ? "" : undefined}
					data-host-id={workspace.hostId}
					data-sidebar="menu-item"
					data-slot="sidebar-menu-item"
					initial={false}
					animate={{ opacity: 1, y: 0 }}
					exit={{ opacity: 0, y: -4, transition: { duration: prefersReducedMotion ? 0 : 0.12, ease: "easeIn" } }}
					onDragOver={(event) => onProjectDragOver(event, workspace.id)}
					onDrop={onProjectDrop}
					transition={prefersReducedMotion ? { duration: 0 } : { type: "spring", stiffness: 520, damping: 42, mass: 0.55 }}
				>
					<div
						ref={projectHeaderRef}
						className={cn(
							"relative",
							activeProjectMatches && "sticky top-0 z-20 bg-sidebar group-data-[collapsible=icon]:static",
						)}
						data-project-drag-row=""
						data-project-id={workspace.id}
						draggable={!workspace.hostId}
						onDragStart={(event) => onProjectDragStart(event, workspace.id)}
						onDragEnd={onProjectDragEnd}
					>
						<div className={cn("relative", projectIsDragging && "cursor-grabbing")}>
							<div>
								<SidebarMenuButton
									aria-label={hostLabel ? t(isStandalone ? "shell.toggleProject" : "shell.openProjectDashboard", { name: nameWithHost }) : undefined}
									aria-current={dashboardActive ? "page" : undefined}
									aria-expanded={expanded}
									isActive={projectActive}
									tooltip={nameWithHost}
									onClick={onProjectClick}
									className={cn(
										NAV_ROW_CLASS,
										NAV_ROW_HIGHLIGHT_HOST_CLASS,
										SIDEBAR_ROW_CLASS,
										!workspace.hostId && "cursor-grab active:cursor-grabbing",
										isPinned ? "pr-sidebar-project-actions-pinned" : "pr-sidebar-project-actions",
										"transition-none",
										projectIsDragging && "!cursor-grabbing",
										projectDragInProgress && "hover:text-muted-foreground active:text-muted-foreground",
										"group-data-[collapsible=icon]:size-control-board! group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:rounded-lg group-data-[collapsible=icon]:p-0! group-data-[collapsible=icon]:font-semibold",
									)}
								>
									<NavRowHighlight active={projectActive} disabled={projectIsDragging} />
									<span
										aria-hidden="true"
										className="relative z-[1] inline-flex size-icon-md shrink-0 translate-y-px items-center justify-center group-data-[collapsible=icon]:hidden"
										data-expanded={expanded ? "" : undefined}
										data-project-folder-visual=""
									>
										<span
											className="inline-flex size-icon-md items-center justify-center transition-[scale] duration-normal ease-[var(--ease-out)] motion-reduce:transition-none"
											data-project-folder-icon=""
										>
											{!expanded && projectNeedsInput ? (
												<SessionMarkerIcon data-project-marker="attention" marker="attention" />
											) : <ProjectIcon avatarUrl={avatarUrl} expanded={expanded} name={workspace.name} seed={workspace.path || workspace.id} />}
										</span>
										<span
											className={cn(
												"absolute inline-flex size-icon-md scale-[0.8] items-center justify-center opacity-0",
												"transition-[scale,rotate] duration-normal ease-[var(--ease-out)]",
												"motion-reduce:transition-none",
												expanded && "rotate-90",
											)}
											data-project-chevron-icon=""
										>
											<ChevronRight />
										</span>
									</span>
									<span
										aria-hidden="true"
										className="relative z-[1] hidden size-8 items-center justify-center group-data-[collapsible=icon]:inline-flex"
									>
										<ProjectIcon avatarUrl={avatarUrl} className="size-5" expanded={expanded} name={workspace.name} seed={workspace.path || workspace.id} />
										{projectNeedsInput ? (
											<span
												className="absolute right-0.5 top-0.5 size-2 rounded-full bg-destructive ring-2 ring-sidebar"
												data-project-marker="attention"
											/>
										) : null}
									</span>
									<span
										className="sidebar-expanded-chrome relative z-[1] min-w-0 flex-1 translate-y-px truncate group-data-[collapsible=icon]:hidden"
										data-project-label=""
									>
										{workspace.name}
									</span>
									{workspace.shared ? <SharedProjectMark share={workspace.shared} /> : null}
									{hostLabel && (
										<Badge
											variant="outline"
											className="sidebar-expanded-chrome relative z-[1] h-4 min-w-0 max-w-1/2 shrink justify-start px-1.5 text-2xs group-data-[collapsible=icon]:hidden"
										>
											<span className="truncate">{hostLabel}</span>
										</Badge>
									)}
									{workspace.kind === "cloud" && (
										<CloudMark className="sidebar-expanded-chrome relative z-[1] translate-y-px group-data-[collapsible=icon]:hidden" />
									)}
								</SidebarMenuButton>
								<Pressable
									aria-label={t("shell.toggleProject", {
										name: nameWithHost,
									})}
									aria-expanded={expanded}
									className="absolute inset-y-0 left-0 z-10 w-9 cursor-pointer bg-transparent group-data-[collapsible=icon]:hidden"
									data-project-folder=""
									tabIndex={-1}
									onClick={onFolderClick}
									type="button"
								/>
							</div>
						<div
								className={cn(
									"sidebar-expanded-chrome z-chrome",
									ROW_ACTIONS_CLASS,
									"group-data-[collapsible=icon]:hidden",
									projectDragInProgress && "pointer-events-none",
								)}
								data-project-actions=""
								draggable={false}
								onClick={(event) => event.stopPropagation()}
								onPointerDown={(event) => event.stopPropagation()}
							>
								{isPinned ? (
									<Tooltip>
										<TooltipTrigger asChild>
											<Button
												size="icon-xs"
												variant="quiet"
												aria-label={t("shell.unpinProject")}
												className={HOVER_ACTION_CLASS}
												data-project-pinned=""
												onClick={() => setProjectPinned(workspace.id, false)}
												type="button"
											>
												<Pin aria-hidden="true" />
											</Button>
										</TooltipTrigger>
										<TooltipContent>{t("shell.unpinProject")}</TooltipContent>
									</Tooltip>
								) : null}
								{isStandalone ? (
									<Tooltip>
										<TooltipTrigger asChild>
											<span className="inline-flex">
												<Button
													size="icon-xs"
													variant="quiet"
													aria-label={t("shell.openNewAgent")}
													className={ROW_ACTION_BUTTON_CLASS}
													disabled={isProjectRestarting}
													onClick={() => requestNewTask(workspace.id, workspace.hostId)}
													type="button"
												>
													<Plus aria-hidden="true" />
												</Button>
											</span>
										</TooltipTrigger>
										<TooltipContent>{t("shell.openNewAgent")}</TooltipContent>
									</Tooltip>
								) : null}
								{!isStandalone && (!workspace.shared || orchestrator) ? (
									<>
										<Tooltip>
											<TooltipTrigger asChild>
												<span className="inline-flex">
													<Button
														size="icon-xs"
														variant="quiet"
														aria-current={orchestratorActive ? "page" : undefined}
														aria-label={orchestratorReplyLabel(
															orchestrator
																? t("shell.openProjectOrchestrator", {
																name: nameWithHost,
																	})
																: t("shell.spawnProjectOrchestrator", {
																name: nameWithHost,
																	}),
															orchestratorReply,
															t,
														)}
															className={cn(HOVER_ACTION_CLASS, orchestratorActive && "text-foreground")}
															disabled={isSpawning || isProjectProvisioning || isProjectRestarting}
														onClick={() => void openOrchestrator()}
														type="button"
													>
														<OrchestratorReplyIcon
															reply={orchestratorReply}
															strokeWidth={orchestratorActive ? 2.5 : 2}
															surface="sidebar"
														/>
													</Button>
												</span>
											</TooltipTrigger>
												<TooltipContent>
													{isProjectProvisioning || isProjectRestarting
														? t("shell.restarting")
														: isSpawning
														? t("shell.spawning")
														: orchestrator
															? orchestratorReplyLabel(t("shell.orchestrator"), orchestratorReply, t)
															: t("shell.spawnOrchestratorLower")}
											</TooltipContent>
										</Tooltip>
									</>
								) : null}
								{workspace.shared || isStandalone ? null : (
									<>
										<DropdownMenu>
											<DropdownMenuTrigger asChild>
												<Button
													size="icon-xs"
													variant="quiet"
													aria-label={t("shell.projectActions", {
																name: nameWithHost,
													})}
													className={HOVER_ACTION_CLASS}
													type="button"
												>
													<MoreVertical aria-hidden="true" />
												</Button>
											</DropdownMenuTrigger>
											<DropdownMenuContent side="right" align="start" className="min-w-44">
												<MenuKitContext.Provider value={dropdownMenuKit}>
													<LazyMenuEntries build={buildMenuEntries} />
												</MenuKitContext.Provider>
											</DropdownMenuContent>
										</DropdownMenu>
									</>
								)}
							</div>
						</div>
					</div>
		<AnimatePresence initial={false}>
			{expanded && sessions.length > 0 && (
				<motion.div
					key="sessions"
					ref={projectSessionsRef}
					initial={
						animReady && (!suppressInitialExpandAnimation || hasInteractedWithDisclosure.current) ? { gridTemplateRows: "0fr" } : false
					}
					animate={{ gridTemplateRows: "1fr" }}
					exit={{ gridTemplateRows: "0fr" }}
					transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.14, ease: [0.25, 0.46, 0.45, 0.94] }}
					style={{ display: "grid" }}
					className="sidebar-expanded-chrome"
				>
					<div style={{ minHeight: 0, overflow: "hidden" }}>
					<motion.div
						initial={
							animReady && (!suppressInitialExpandAnimation || hasInteractedWithDisclosure.current)
								? { y: -12, opacity: 0 }
								: false
						}
						animate={{ y: 0, opacity: 1 }}
						exit={{ y: -12, opacity: 0 }}
						transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.14, ease: [0.25, 0.46, 0.45, 0.94] }}
					>
											<SessionReorderList
										dndId={sessionDndId(projectKey)}
										testId={`session-list-${projectKey}`}
												className={cn(
													"mx-0 ml-3.5 translate-x-0 gap-px border-l-0 px-0 pt-1",
													hiddenSessionCount > 0 ? "pb-px" : "pb-1",
												)}
												sessions={listedSessions}
												sessionIds={listedSessionIds}
												activeSessionId={selection.activeSessionId}
												plain={projectDragInProgress}
												onReorder={commitSessionOrder}
												onKilled={handleSessionKilled}
												onOpen={openSession}
											/>
											{hiddenSessionCount > 0 ? (
												<div className="pl-4">
													<ShowMoreRow
														className="px-3"
														expanded={showAllSessions}
														label={
															showAllSessions
																? t("shell.showLessAgents")
																: t("shell.showMoreAgents", { count: hiddenSessionCount })
														}
														onClick={toggleShowAllSessions}
													/>
												</div>
											) : null}
								</motion.div>
							</div>
							</motion.div>
						)}
					</AnimatePresence>
					<ConfirmDialog
						open={confirmOpen}
						onOpenChange={setConfirmOpen}
						title={isCloudProject ? t("shell.deleteCloudProjectTitle") : t("shell.removeProjectTitle")}
						description={
							<>
								<p className="text-sm font-medium text-foreground">
									{isCloudProject
										? t("shell.deleteCloudProjectLead", { name: workspace.name })
										: t("shell.removeProjectLead", { name: workspace.name })}
								</p>
								{isCloudProject ? null : <p className="mt-1 text-xs text-muted-foreground">{t("shell.removeProjectBody")}</p>}
								{openPullRequestCount > 0 ? (
									<p className="mt-2 text-xs font-medium text-error">
										{t("shell.removeProjectOpenPrWarning", { count: openPullRequestCount })}
									</p>
								) : null}
							</>
						}
						confirmLabel={isCloudProject ? t("shell.deleteCloudProjectConfirm") : t("shell.remove")}
						destructive
						onConfirm={handleConfirmRemove}
					/>
				</motion.li>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-44">
				<LazyMenuEntries build={buildMenuEntries} />
			</ContextMenuContent>
		</ContextMenu>
	);
}, projectItemPropsEqual);

function scratchpadListsSession(session: WorkspaceSession): boolean {
	return !isOrchestratorSession(session) && session.isTerminated !== true;
}

function scratchpadArchivesSession(session: WorkspaceSession): boolean {
	return !isOrchestratorSession(session) && isArchivedSession(session);
}

function scratchpadSummary(name: string): Pick<WorkspaceSummary, "id" | "name" | "kind" | "path"> {
	return { id: STANDALONE_WORKSPACE_ID, name, kind: STANDALONE_PROJECT_KIND, path: "" };
}

function ScratchpadSection({
	workspace,
	selection,
	isCollapsed,
	open,
	onToggle,
}: {
	workspace: WorkspaceSummary;
	selection: Selection;
	isCollapsed: boolean;
	open: boolean;
	onToggle: () => void;
}) {
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const visibleSessions = useMemo(
		() => sortedWorkerSessions(workspace.sessions).filter(scratchpadListsSession),
		[workspace.sessions],
	);
	const [sessionOrder, setSessionOrder] = useState<string[]>([]);
	const sessions = useMemo(
		() => applyOrder(visibleSessions, (session) => session.id, sessionOrder, "start"),
		[sessionOrder, visibleSessions],
	);
	const scratchpadKey = sessionUiKey(workspace.id, workspace.hostId);
	useEffect(() => {
		const sessionIds = sessions.map((session) => session.id);
		useSidebarOrderStore.getState().setSessionOrder(scratchpadKey, sessionIds);
		return () => useSidebarOrderStore.getState().clearSessionOrder(scratchpadKey);
	}, [scratchpadKey, sessions]);
	const {
		listed: listedSessions,
		hiddenCount: hiddenSessionCount,
		showAll,
		toggleShowAll,
	} = useShowMoreCap(sessions, SIDEBAR_SESSIONS_LIMIT, selection.activeSessionId, isCollapsed);
	const listedSessionIds = useStableArray(useMemo(() => listedSessions.map((session) => session.id), [listedSessions]));
	const archivedCount = useMemo(() => workspace.sessions.filter(scratchpadArchivesSession).length, [workspace.sessions]);
	const showArchivedRow = listedSessions.length === 0 && archivedCount > 0;
	const commitSessionOrder = useCallback(
		(next: string[] | null) => {
			if (!next) return;
			const listed = new Set(next);
			setSessionOrder([...next, ...sessions.filter((session) => !listed.has(session.id)).map((session) => session.id)]);
		},
		[sessions],
	);
	const scratchpadRouteOf = (sessionId: string) => {
		const session = workspace.sessions.find((candidate) => candidate.id === sessionId);
		return session?.cloud ? session.workspaceId : STANDALONE_WORKSPACE_ID;
	};
	const openSession = useLatestCallback((sessionId: string) => {
		recordManualWorkerOpen(sessionId);
		selection.goSession(scratchpadRouteOf(sessionId), sessionId);
	});
	const handleSessionKilled = useLatestCallback((killedSession: WorkspaceSession) => {
		if (selection.activeSessionId !== killedSession.id) return;
		const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id, sessions);
		if (nextRoute.target === "session") {
			selection.goSession(scratchpadRouteOf(nextRoute.sessionId), nextRoute.sessionId);
		} else {
			selection.goHome();
		}
	});

	return (
		<div
			className="sidebar-expanded-chrome mb-2 flex min-h-0 flex-col overflow-hidden group-data-[collapsible=icon]:hidden"
			data-scratchpad-section=""
		>
			<ContextMenu>
				<ContextMenuTrigger asChild>
					<div className="shrink-0">
						<SectionDisclosure
							label={workspace.name}
							open={open}
							onToggle={onToggle}
							className="group/scratchpad"
							trailing={
								<div className="relative inline-flex items-center">
									<span
										className={cn(
											"pointer-events-none absolute right-full top-0 flex h-full origin-center pr-1.5 scale-[0.8] items-center opacity-0",
											"transition-[scale] duration-normal ease-[var(--ease-out)]",
											"motion-reduce:transition-none",
											"group-has-[:focus-visible]/scratchpad:pointer-events-auto group-has-[:focus-visible]/scratchpad:scale-100 group-has-[:focus-visible]/scratchpad:opacity-100",
										)}
										data-scratchpad-archive-action=""
									>
										<Tooltip>
											<TooltipTrigger asChild>
												<span className="inline-flex">
													<Button
														size="icon-xs"
														variant="quiet"
														aria-label={t("shell.archivedSessions")}
														className={ROW_ACTION_BUTTON_CLASS}
														onClick={(event) => {
															event.stopPropagation();
															selection.goStandaloneBoard();
														}}
														type="button"
													>
														<Archive aria-hidden="true" />
													</Button>
												</span>
											</TooltipTrigger>
											<TooltipContent>{t("shell.archivedSessions")}</TooltipContent>
										</Tooltip>
									</span>
									<Tooltip>
										<TooltipTrigger asChild>
											<span className="inline-flex">
												<Button
													size="icon-xs"
													variant="quiet"
													aria-label={t("shell.openNewAgent")}
													className={ROW_ACTION_BUTTON_CLASS}
													onClick={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
													type="button"
												>
													<Plus aria-hidden="true" />
												</Button>
											</span>
										</TooltipTrigger>
										<TooltipContent>{t("shell.openNewAgent")}</TooltipContent>
									</Tooltip>
								</div>
							}
						/>
					</div>
				</ContextMenuTrigger>
				<ContextMenuContent className="min-w-44">
					<ContextMenuItem onSelect={() => requestNewTask(STANDALONE_WORKSPACE_ID)}>
						<Plus aria-hidden="true" />
						{t("shell.openNewAgent")}
					</ContextMenuItem>
					<ContextMenuItem onSelect={selection.goStandaloneBoard}>
						<Archive aria-hidden="true" />
						{t("shell.archivedSessions")}
					</ContextMenuItem>
				</ContextMenuContent>
			</ContextMenu>
			<AnimatedSectionBody open={open && (listedSessions.length > 0 || showArchivedRow)} className="min-h-0 flex-1">
				<SidebarSectionScroller
					className={`${SECTION_SCROLLER_CLASS} min-h-0 flex-1`}
					testId="sidebar-scratchpad-scroller"
					wrapperClassName="flex flex-col flex-1"
				>
					<SessionReorderList
						dndId={sessionDndId(STANDALONE_WORKSPACE_ID)}
						testId={`session-list-${STANDALONE_WORKSPACE_ID}`}
						className="mx-0 ml-0 translate-x-0 gap-0.5 border-l-0 px-0 py-0.5"
						sessions={listedSessions}
						sessionIds={listedSessionIds}
						activeSessionId={selection.activeSessionId}
						indented={false}
						cloudBadge
						onReorder={commitSessionOrder}
						onKilled={handleSessionKilled}
						onOpen={openSession}
					/>
				</SidebarSectionScroller>
				{!isCollapsed && hiddenSessionCount > 0 ? (
					<ShowMoreRow
						expanded={showAll}
						label={showAll ? t("shell.showLessAgents") : t("shell.showMoreAgents", { count: hiddenSessionCount })}
						onClick={toggleShowAll}
					/>
				) : null}
				{showArchivedRow ? (
					<button
						type="button"
						aria-label={t("shell.archivedSessionsAria", { count: archivedCount })}
						className={cn(SECTION_ROW_CLASS, NAV_ROW_HIGHLIGHT_HOST_CLASS, "mb-1 shrink-0 rounded-lg text-left text-muted-foreground")}
						data-scratchpad-archived-row=""
						onClick={selection.goStandaloneBoard}
					>
						<Archive aria-hidden="true" className="size-icon-sm shrink-0" />
						<span className="min-w-0 flex-1 truncate">{t("shell.archivedSessions")}</span>
						<span className="tabular-nums">{archivedCount}</span>
					</button>
				) : null}
			</AnimatedSectionBody>
		</div>
	);
}

const PinnedSessionRow = memo(function PinnedSessionRow({
	session,
	active,
	hostLabel,
	onKilled,
	onOpenSession,
}: {
	session: WorkspaceSession;
	active: boolean;
	hostLabel?: string;
	onKilled?: (session: WorkspaceSession) => void;
	onOpenSession: (session: WorkspaceSession) => void;
}) {
	const onOpen = useCallback(() => onOpenSession(session), [onOpenSession, session]);
	return <SessionRow session={session} active={active} hostLabel={hostLabel} indented={false} onKilled={onKilled} onOpen={onOpen} />;
});

const SortableSessionRow = memo(function SortableSessionRow({
	session,
	active,
	consumeDragClick,
	indented = true,
	layoutDependency,
	listIsDragging,
	dropTransitionDisabled,
	cloud,
	onKilled,
	onOpen,
}: {
	session: WorkspaceSession;
	active: boolean;
	consumeDragClick: (id: string) => boolean;
	cloud?: boolean;
	indented?: boolean;
	layoutDependency?: string;
	listIsDragging: boolean;
	dropTransitionDisabled: boolean;
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: (sessionId: string) => void;
}) {
	const { isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition } = useSortable({
		id: session.id,
	});
	const open = useCallback(() => {
		if (!consumeDragClick(session.id)) onOpen(session.id);
	}, [consumeDragClick, onOpen, session.id]);
	const reorder = useMemo(
		() => ({ isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition, dropTransitionDisabled }),
		[isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition, dropTransitionDisabled],
	);
	return (
		<SessionRow
			session={session}
			active={active}
			cloud={cloud}
			indented={indented}
			onKilled={onKilled}
			onOpen={open}
			layoutDependency={layoutDependency}
			listIsDragging={listIsDragging}
			reorder={reorder}
		/>
	);
});

function SessionReorderList({
	dndId,
	testId,
	className,
	sessions,
	sessionIds,
	activeSessionId,
	indented = true,
	plain = false,
	cloudBadge = false,
	onReorder,
	onKilled,
	onOpen,
}: {
	dndId: string;
	testId: string;
	className: string;
	sessions: WorkspaceSession[];
	sessionIds: string[];
	activeSessionId?: string;
	indented?: boolean;
	cloudBadge?: boolean;
	plain?: boolean;
	onReorder: (next: string[] | null) => void;
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: (sessionId: string) => void;
}) {
	const [layoutSettled, setLayoutSettled] = useState(false);
	useEffect(() => {
		const timer = window.setTimeout(() => setLayoutSettled(true), 500);
		return () => window.clearTimeout(timer);
	}, []);
	const orderKey = useMemo(() => sessionIds.join("\u0000"), [sessionIds]);
	const layoutDependency = layoutSettled ? orderKey : undefined;
	const sensors = useReorderSensors();
	const dragClickGuard = usePostDragClickGuard();
	const [listDragging, setListDragging] = useState(false);
	const [dropTransitionDisabledId, setDropTransitionDisabledId] = useState<string | null>(null);

	const onDragEnd = useCallback(({ active, over }: DragEndEvent) => {
		const sessionId = String(active.id);
		dragClickGuard.markDragEnded(sessionId);
		if (!over) {
			setListDragging(false);
			setDropTransitionDisabledId(null);
			return;
		}
		const next = reorderById(sessionIds, sessionId, String(over.id));
		flushSync(() => {
			onReorder(next);
			setListDragging(false);
			setDropTransitionDisabledId(sessionId);
		});
		requestAnimationFrame(() => setDropTransitionDisabledId(null));
	}, [dragClickGuard, onReorder, sessionIds]);

	const onDragCancel = useCallback(() => {
		setListDragging(false);
		setDropTransitionDisabledId(null);
	}, []);

	if (plain) {
		return (
			<SidebarMenuSub className={className} data-testid={testId}>
				{sessions.map((session) => (
					<SessionRow
						key={session.id}
						session={session}
						active={activeSessionId === session.id}
						cloud={cloudBadge && Boolean(session.cloud)}
						indented={indented}
						onKilled={onKilled}
						onOpen={() => onOpen(session.id)}
					/>
				))}
			</SidebarMenuSub>
		);
	}

	return (
		<DndContext
			collisionDetection={closestCenter}
			modifiers={[restrictToListBounds]}
			id={dndId}
			onDragStart={() => setListDragging(true)}
			onDragCancel={onDragCancel}
			onDragEnd={onDragEnd}
			sensors={sensors}
		>
			<SortableContext items={sessionIds} strategy={verticalListSortingStrategy}>
				<SidebarMenuSub className={className} data-testid={testId}>
					{sessions.map((session) => (
						<SortableSessionRow
							key={session.id}
							session={session}
							active={activeSessionId === session.id}
							consumeDragClick={dragClickGuard.consumeClick}
							cloud={cloudBadge && Boolean(session.cloud)}
							indented={indented}
							layoutDependency={layoutDependency}
							listIsDragging={listDragging}
							dropTransitionDisabled={dropTransitionDisabledId === session.id}
							onKilled={onKilled}
							onOpen={onOpen}
						/>
					))}
				</SidebarMenuSub>
			</SortableContext>
		</DndContext>
	);
}

type SessionReorder = Pick<SortableRow, "isDragging" | "listeners" | "setActivatorNodeRef" | "setNodeRef" | "transform" | "transition"> & {
	dropTransitionDisabled: boolean;
};

const SessionRow = memo(function SessionRow({
	session,
	active,
	hostLabel,
	cloud = false,
	indented = true,
	layoutDependency,
	listIsDragging = false,
	onKilled,
	onOpen,
	reorder,
}: {
	session: WorkspaceSession;
	active: boolean;
	hostLabel?: string;
	cloud?: boolean;
	indented?: boolean;
	layoutDependency?: string;
	listIsDragging?: boolean;
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: () => void;
	reorder?: SessionReorder;
}) {
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	useGrabbingCursor(Boolean(reorder?.isDragging));
	const indicator = getSessionRowIndicator(session);
	const working = indicator === "working";
	const markRead = useSessionReadStore((state) => state.markRead);
	useEffect(() => {
		if (!active) return;
		const updated = Date.parse(session.updatedAt);
		if (Number.isFinite(updated)) markRead(sessionReadKey(session), updated);
	}, [active, markRead, session]);
	const shimmer = working && !active;
	const marker = working ? null : indicator;
	const queryClient = useQueryClient();
	const refreshWorkspaces = useCallback(
		() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) }),
		[queryClient, session.hostId],
	);
	const rename = useSessionRename(session, refreshWorkspaces);
	const [contextMenuOpen, setContextMenuOpen] = useState(false);
	const preview = useSessionPreviewHover(
		Boolean(reorder?.isDragging || listIsDragging || rename.isEditing || contextMenuOpen),
	);
	const setSortableNode = reorder?.setNodeRef;
	const setPreviewRow = preview.rowRef;
	const rowNodeRef = useCallback(
		(node: HTMLElement | null) => {
			setSortableNode?.(node);
			setPreviewRow(node);
		},
		[setPreviewRow, setSortableNode],
	);
	const lastTouchAtRef = useRef(0);
	const suppressTouchOpenRef = useRef(false);
	const hoverTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const canPrefetchChat = session.mode === "chat" && !session.cloud && !active && !listIsDragging && !reorder?.isDragging;
	const canPrefetch = canPrefetchChat && !session.hostId;
	useEffect(() => () => clearTimeout(hoverTimerRef.current), [canPrefetch]);
	const resumeTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	useEffect(() => () => clearTimeout(resumeTimerRef.current), [canPrefetch]);
	const prefetchConversation = () => {
		if (!canPrefetchChat || (!session.hostId && !hasTrustedApiBaseUrl()) || queryClient.getQueryData(conversationQueryKey(session.id, session.hostId))) return;
		void queryClient.prefetchInfiniteQuery(conversationQueryOptions(session.id, session.hostId));
	};
	const warmUpAgent = () => {
		if (!canPrefetch || session.needsResume || !hasTrustedApiBaseUrl() || !canResumeAgent(session)) return;
		void resumeAgentOnOpen(session.id)
			.catch(() => {})
			.finally(() => {
				void refreshWorkspaces();
				void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id) });
			});
	};
	const renamable = !session.cloud?.shared;
	const beginRename = useCallback(() => {
		if (!renamable) return;
		setContextMenuOpen(false);
		rename.begin();
	}, [rename.begin, renamable]);
	const [archiveOpen, setArchiveOpen] = useState(false);

	if (rename.isEditing) {
		return (
			<SidebarMenuSubItem className={cn(indented && "pl-0.5")}>
				<div
					className={cn(
						"group/nav-row relative flex h-8 w-full items-center gap-1.5 rounded-lg py-0 pl-1.5 pr-1",
						active && "text-foreground",
					)}
					data-session-row=""
				>
					<NavRowHighlight active={active} />
					<SessionStatusDot indicator={indicator} session={session} />
					<input
						aria-label={t("shell.renameSession", { title: session.title })}
						autoFocus
						className={cn(
							"relative z-[1] h-full min-w-0 flex-1 appearance-none border-0 bg-transparent! p-0 text-sm text-foreground outline-none ring-0 focus:outline-none focus:ring-0",
							(session.lastInteractionAt ?? session.lastUserMessageAt) && "pr-[36px]",
						)}
						data-session-inline-editor=""
						maxLength={MAX_SESSION_DISPLAY_NAME_LEN}
						onBlur={() => void rename.commit()}
						onChange={(e) => rename.setDraft(e.target.value)}
						onFocus={(e) => e.currentTarget.select()}
						onKeyDown={(e) => {
							if (e.key === "Enter") {
								e.preventDefault();
								e.currentTarget.blur();
							} else if (e.key === "Escape") {
								e.preventDefault();
								rename.cancel();
							}
						}}
						value={rename.draft}
					/>
					<SessionMessageAge session={session} />
				</div>
			</SidebarMenuSubItem>
		);
	}

	return (
		<ContextMenu onOpenChange={setContextMenuOpen}>
			<HoverCard closeDelay={0} onOpenChange={(open) => !open && preview.hide()} open={preview.open} openDelay={0}>
			<ContextMenuTrigger asChild>
				<HoverCardTrigger asChild>
				<SidebarMenuSubItem
					className={cn(indented && "pl-0.5", reorder?.isDragging && "z-chrome cursor-grabbing opacity-60")}
					data-dragging={reorder?.isDragging ? "true" : undefined}
					ref={rowNodeRef}
					style={reorder ? sortableRowStyle(reorder) : undefined}
					{...preview.triggerProps}
				>
			<motion.div
				layout={layoutDependency !== undefined && !listIsDragging ? "y" : false}
				layoutDependency={layoutDependency}
				transition={prefersReducedMotion ? { duration: 0 } : { layout: { type: "spring", stiffness: 520, damping: 42, mass: 0.55 } }}
			>
				<div
					className={cn(
						"group/session-row group/nav-row relative flex h-8 w-full items-center rounded-lg",
						"hover:text-foreground",
						active && "text-foreground",
					)}
					data-session-row=""
					data-dragging={reorder?.isDragging ? "true" : undefined}
					data-preview-open={preview.open ? "" : undefined}
				>
					<NavRowHighlight active={active} disabled={Boolean(reorder?.isDragging)} />
					<div className={cn("relative z-[1] flex min-w-0 flex-1", reorder?.isDragging && "cursor-grabbing")}>
						<Pressable
							aria-current={active ? "page" : undefined}
							data-testid={session.hostId ? "remote-session-row" : undefined}
							aria-keyshortcuts="F2"
							aria-label={t("shell.openSession", { title: [session.title, hostLabel, cloud ? t("shell.cloudProjectBadge") : undefined].filter(Boolean).join(" · ") })}
							className={cn(
								"flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-lg py-0 pl-1.5 text-left text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-sidebar-ring",
								(session.lastInteractionAt ?? session.lastUserMessageAt) ? "pr-[36px]" : "pr-2.5",
								!reorder?.isDragging &&
									"group-hover/session-row:pr-sidebar-project-actions group-has-[:focus-visible]/session-row:pr-sidebar-project-actions",
								reorder && "cursor-grab active:cursor-grabbing",
								reorder?.isDragging && "!cursor-grabbing",
							)}
							{...(reorder?.listeners ?? {})}
							onMouseEnter={() => {
								if (!canPrefetchChat) return;
								hoverTimerRef.current = setTimeout(prefetchConversation, 100);
								if (canPrefetch) resumeTimerRef.current = setTimeout(warmUpAgent, AGENT_WARM_UP_HOVER_MS);
							}}
							onMouseLeave={() => {
								clearTimeout(hoverTimerRef.current);
								clearTimeout(resumeTimerRef.current);
							}}
							onFocus={prefetchConversation}
							onClick={(event) => {
								if (event.detail > 1) return;
								if (suppressTouchOpenRef.current) {
									suppressTouchOpenRef.current = false;
									return;
								}
								onOpen();
							}}
							onKeyDown={(event) => {
								reorder?.listeners?.onKeyDown?.(event);
								if (event.key !== "F2") return;
								event.preventDefault();
								beginRename();
							}}
							onDoubleClick={(event) => {
								event.preventDefault();
								event.stopPropagation();
								beginRename();
							}}
							ref={reorder?.setActivatorNodeRef}
							type="button"
						>
							<SessionStatusDot indicator={indicator} session={session} />
							<span className="flex min-w-0 flex-1 items-center gap-1.5">
								<span
									className={cn(
										"min-w-0 flex-1 truncate",
										active || marker === "attention"
											? "text-foreground"
											: "text-muted-foreground group-hover/session-row:text-foreground",
										shimmer && "chat-working-shimmer",
									)}
									data-session-name=""
									onPointerUp={(event) => {
										if (event.pointerType !== "touch") return;
										const now = Date.now();
										if (now - lastTouchAtRef.current <= 500) {
											suppressTouchOpenRef.current = true;
										beginRename();
										}
										lastTouchAtRef.current = now;
									}}
								>
									{session.title}
								</span>
								{hostLabel ? <Badge variant="outline" className="h-4 min-w-0 max-w-1/2 shrink justify-start px-1.5 text-2xs"><span className="truncate">{hostLabel}</span></Badge> : null}
								{cloud ? <CloudMark /> : null}
							</span>
						</Pressable>
					</div>
					<SessionActions
						archiveOpen={archiveOpen}
						isDragging={Boolean(reorder?.isDragging)}
						onArchiveOpenChange={setArchiveOpen}
						onKilled={onKilled}
						session={session}
					/>
				</div>
			</motion.div>
				</SidebarMenuSubItem>
				</HoverCardTrigger>
			</ContextMenuTrigger>
			<HoverCardContent
				align="center"
				animation={preview.animation}
				className="w-[17rem] border-0 bg-transparent p-0"
				collisionPadding={8}
				side="right"
				sideOffset={12}
				{...preview.contentProps}
			>
				<SessionPreviewCardAdapter session={session} />
			</HoverCardContent>
			</HoverCard>
			<ContextMenuContent className="min-w-44">
				<SessionContextMenuItems onArchive={session.cloud?.shared ? undefined : () => setArchiveOpen(true)} onRename={beginRename} session={session} />
			</ContextMenuContent>
		</ContextMenu>
	);
});

const SessionMessageAge = memo(function SessionMessageAge({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const at = session.lastInteractionAt ?? session.lastUserMessageAt;
	if (!at) return null;

	return (
		<time
			className="absolute inset-y-0 right-2 z-[1] flex min-w-0 shrink-0 items-center whitespace-nowrap font-sans text-micro tabular-nums text-passive opacity-100 group-has-[:focus-visible]/session-row:opacity-0"
			data-session-message-age=""
			dateTime={at}
			title={t("shell.lastMessageAt", { time: formatTimeCompact(at) })}
		>
			{formatTimeTerse(at)}
		</time>
	);
});

const SessionActions = memo(function SessionActions({
	session,
	isDragging,
	archiveOpen,
	onArchiveOpenChange,
	onKilled,
}: {
	session: WorkspaceSession;
	isDragging: boolean;
	archiveOpen: boolean;
	onArchiveOpenChange: (open: boolean) => void;
	onKilled?: (session: WorkspaceSession) => void;
}) {
	const { t } = useTranslation();
	const { mutate: pinSession } = usePinSession();
	const { mutate: unpinSession } = useUnpinSession();
	const onKilledRef = useRef(onKilled);
	onKilledRef.current = onKilled;
	const { mutate: terminateSession, isPending: isKilling } = useTerminateSession({
		onOptimistic: (killed) => {
			onKilledRef.current?.(killed);
		},
	});

	const handleArchive = (event: React.MouseEvent) => {
		event.stopPropagation();
		onArchiveOpenChange(true);
	};

	const confirmArchive = ({ deleteArtifacts }: SessionArchiveChoice) => {
		onArchiveOpenChange(false);
		terminateSession({ ...session, deleteArtifacts });
	};

	return (
		<div
			className="pointer-events-none absolute inset-y-0 right-0 z-chrome"
			data-session-actions=""
			onPointerDown={(event) => event.stopPropagation()}
		>
			<div
				className={cn(
					ROW_ACTIONS_CLASS,
					"origin-center scale-[0.8] opacity-0",
					"transition-[scale] duration-normal ease-[var(--ease-out)]",
					"motion-reduce:transition-none",
					!isDragging &&
						"group-has-[:focus-visible]/session-row:pointer-events-auto group-has-[:focus-visible]/session-row:scale-100 group-has-[:focus-visible]/session-row:opacity-100",
				)}
				data-session-action-buttons=""
			>
				{session.cloud?.shared ? null : <Button
							size="icon-xs"
							variant="quiet"
							aria-label={session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")}
							className={SESSION_ACTION_CLASS}
							onClick={(event) => {
								event.stopPropagation();
								session.isPinned ? unpinSession(session) : pinSession(session);
							}}
							type="button"
						>
							<Pin aria-hidden="true" />
						</Button>}
				{session.cloud?.shared ? null : (
					<SessionArchiveDialog
						onConfirm={confirmArchive}
						onOpenChange={onArchiveOpenChange}
						open={archiveOpen}
						session={session}
						trigger={
							<Tooltip>
								<TooltipTrigger asChild>
									<span className="inline-flex">
										<Button
											size="icon-xs"
											variant="quiet"
											aria-label={t("shell.archiveSession")}
											className={SESSION_ACTION_CLASS}
											disabled={isKilling}
											onClick={handleArchive}
											type="button"
										>
											<Archive aria-hidden="true" />
										</Button>
									</span>
								</TooltipTrigger>
								<TooltipContent side="top">{t("shell.archiveSession")}</TooltipContent>
							</Tooltip>
						}
					/>
				)}
			</div>
			<SessionMessageAge session={session} />
		</div>
	);
});

function CloudSignInRow({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { configured, status, signIn } = useCloudSession();
	const { available: localAuthAvailable } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);
	const onSignIn = () => (localAuthAvailable ? openLocalSignIn() : signIn());
	if (!configured || status !== "unauthenticated") return null;

	return (
		<Pressable
			aria-label={t("shell.signIn")}
			className={FOOTER_NAV_BUTTON_CLASS}
			onClick={onSignIn}
			tabIndex={tabIndex}
			type="button"
		>
			<NavRowHighlight />
			<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2 [&_svg]:size-icon-md [&_svg]:shrink-0">
				<LogIn aria-hidden="true" />
				<span className="truncate tracking-tight">{t("shell.signIn")}</span>
			</span>
		</Pressable>
	);
}

function CloudSignInRailButton({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { configured, status, signIn } = useCloudSession();
	const { available: localAuthAvailable } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);
	const onSignIn = () => (localAuthAvailable ? openLocalSignIn() : signIn());
	if (!configured || status !== "unauthenticated") return null;

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Pressable
					aria-label={t("shell.signIn")}
					className={FOOTER_RAIL_BUTTON_CLASS}
					onClick={onSignIn}
					tabIndex={tabIndex}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] grid place-items-center [&_svg]:size-icon-base">
						<LogIn aria-hidden="true" />
					</span>
				</Pressable>
			</TooltipTrigger>
			<TooltipContent side="right">{t("shell.signIn")}</TooltipContent>
		</Tooltip>
	);
}

function FooterAccountRow({
	onConnectMobile,
	onOpenSettings,
	tabIndex,
}: {
	onConnectMobile: () => void;
	onOpenSettings: () => void;
	tabIndex: number;
}) {
	const { t } = useTranslation();
	const { configured } = useCloudSession();

	return (
		<div className={FOOTER_BOTTOM_ROW_CLASS}>
			<div className="flex min-w-0 flex-1">
				{configured ? (
					<>
						<CloudSignInRow tabIndex={tabIndex} />
						<CloudAccountRow tabIndex={tabIndex} />
					</>
				) : (
					<Pressable
						aria-label={t("shell.settings")}
						className={FOOTER_NAV_BUTTON_CLASS}
						onClick={onOpenSettings}
						tabIndex={tabIndex}
						type="button"
					>
						<NavRowHighlight />
						<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2 [&_svg]:size-icon-md [&_svg]:shrink-0">
							<Settings aria-hidden="true" />
							<span className="truncate tracking-tight">{t("shell.settings")}</span>
						</span>
					</Pressable>
				)}
			</div>
			<FooterMobileMenu onConnectMobile={onConnectMobile} tabIndex={tabIndex} />
			{configured ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Pressable
							aria-label={t("shell.settings")}
							className={FOOTER_ICON_BUTTON_CLASS}
							onClick={onOpenSettings}
							tabIndex={tabIndex}
							type="button"
						>
							<NavRowHighlight />
							<span className="relative z-[1] grid place-items-center">
								<Settings aria-hidden="true" />
							</span>
						</Pressable>
					</TooltipTrigger>
					<TooltipContent shortcut="open-settings">{t("shell.settings")}</TooltipContent>
				</Tooltip>
			) : null}
		</div>
	);
}

function FooterMobileMenu({ onConnectMobile, tabIndex }: { onConnectMobile: () => void; tabIndex: number }) {
	const { t } = useTranslation();
	const stay = useFooterStayAwake();
	const attention = awakeNeedsAttention(stay);
	const label = isMac ? t("settings.mobile") : t("settings.connectMobile");
	const icon = (
		<span className="relative z-[1] grid place-items-center">
			<Smartphone aria-hidden="true" />
			{attention ? (
				<TriangleAlert
					aria-hidden="true"
					className="absolute -right-1 -top-1 size-2.5! fill-background text-error"
					data-testid="awake-inactive-indicator"
					strokeWidth={2.5}
				/>
			) : null}
		</span>
	);

	if (!isMac) {
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<Pressable
						aria-label={label}
						className={FOOTER_ICON_BUTTON_CLASS}
						onClick={onConnectMobile}
						tabIndex={tabIndex}
						type="button"
					>
						<NavRowHighlight />
						{icon}
					</Pressable>
				</TooltipTrigger>
				<TooltipContent>{label}</TooltipContent>
			</Tooltip>
		);
	}

	return (
		<DropdownMenu>
			<Tooltip>
				<TooltipTrigger asChild>
					<DropdownMenuTrigger asChild>
						<Pressable
							aria-label={label}
							className={FOOTER_ICON_BUTTON_CLASS}
							data-testid="footer-mobile-menu"
							tabIndex={tabIndex}
							type="button"
						>
							<NavRowHighlight />
							{icon}
						</Pressable>
					</DropdownMenuTrigger>
				</TooltipTrigger>
				<TooltipContent>{label}</TooltipContent>
			</Tooltip>
			<DropdownMenuContent side="top" align="end" sideOffset={8} collisionPadding={8} className="w-56">
				<DropdownMenuItem onSelect={onConnectMobile}>
					<Smartphone aria-hidden="true" />
					{t("settings.connectMobile")}
				</DropdownMenuItem>
				<DropdownMenuSeparator />
				<AwakeMenuItem stay={stay} />
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function CloudAccountMenuContent({ side }: { side: "top" | "right" }) {
	const { t } = useTranslation();
	const { session, signOut } = useCloudSession();
	const billing = useCloudBilling();
	const plan = billing.offering.selfServeBilling ? paidPlanBadge(billing.data) : null;
	const name = accountName(session?.user);
	const email = session?.user.email;
	return (
		<DropdownMenuContent side={side} align={side === "top" ? "start" : "end"} sideOffset={8} collisionPadding={8} className="w-56">
			<div className="flex items-center gap-2.5 px-2 py-1.5">
				<AccountAvatar size="lg" user={session?.user} />
				<div className="flex min-w-0 flex-1 flex-col leading-tight">
					<span className="flex min-w-0 items-center gap-1.5 text-sm font-medium text-foreground">
						<span className="break-words">{name}</span>
						{plan ? (
							<span className="shrink-0 rounded-full bg-[var(--color-status-working)] px-1.5 text-[length:var(--font-size-xs)] font-medium leading-4 text-white">
								{plan}
							</span>
						) : null}
					</span>
					{email && email !== name ? (
						<span className="break-all text-[length:var(--font-size-xs)] text-muted-foreground">{email}</span>
					) : null}
				</div>
			</div>
			<DropdownMenuSeparator />
			<DropdownMenuItem onSelect={() => void signOut()} variant="destructive">
				<LogOut aria-hidden="true" />
				{t("shell.signOut")}
			</DropdownMenuItem>
		</DropdownMenuContent>
	);
}

function CloudAccountRow({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { configured, session, status } = useCloudSession();
	if (!configured || status !== "authenticated") return null;
	const email = session?.user.email;

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Pressable
					aria-label={t("shell.signedInAs", {
						email: email ?? "Orchestrator Cloud",
					})}
					className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "flex h-9 w-full min-w-0 items-center rounded-lg px-1.5 text-left")}
					tabIndex={tabIndex}
					title={email}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2">
						<AccountAvatar size="lg" user={session?.user} />
						<span className="min-w-0 truncate text-sm font-medium tracking-tight text-foreground">
							{accountShortName(session?.user)}
						</span>
					</span>
				</Pressable>
			</DropdownMenuTrigger>
			<CloudAccountMenuContent side="top" />
		</DropdownMenu>
	);
}

function accountShortName(user: { displayName?: string; email?: string } | undefined): string {
	const display = user?.displayName?.trim();
	if (display && display !== user?.email) return display;
	const local = user?.email?.split("@")[0];
	return local || "Orchestrator Cloud";
}

function accountName(user: { displayName?: string; email?: string } | undefined): string {
	const display = user?.displayName?.trim();
	if (display && display !== user?.email) return display;
	return user?.email ?? "Orchestrator Cloud";
}

function AccountAvatar({
	size = "md",
	user,
}: {
	size?: "md" | "lg";
	user: { displayName?: string; email?: string; avatarUrl?: string } | undefined;
}) {
	const [broken, setBroken] = useState(false);
	const sizeClass = size === "lg" ? "size-6" : "size-icon-md";
	if (user?.avatarUrl && !broken) {
		return (
			<img
				src={user.avatarUrl}
				alt=""
				aria-hidden="true"
				referrerPolicy="no-referrer"
				className={cn(sizeClass, "shrink-0 rounded-full object-cover")}
				onError={() => setBroken(true)}
			/>
		);
	}
	const source = accountName(user);
	const initials = source.includes("@")
		? source.charAt(0).toUpperCase()
		: source.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part.charAt(0).toUpperCase()).join("");
	if (!initials) {
		return size === "lg" ? (
			<span aria-hidden="true" className="grid size-6 shrink-0 place-items-center rounded-full bg-[var(--color-bg-tertiary)] text-settings-label [&_svg]:size-icon-md">
				<User />
			</span>
		) : <User aria-hidden="true" />;
	}
	return (
		<span aria-hidden="true" className={cn(sizeClass, "grid shrink-0 place-items-center rounded-full bg-[var(--color-bg-tertiary)] text-[length:var(--font-size-xs)] font-medium leading-none text-settings-label")}>
			{initials}
		</span>
	);
}

function CloudAccountRailButton({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { configured, session, status } = useCloudSession();
	if (!configured || status !== "authenticated") return null;
	const label = t("shell.signedInAs", {
		email: session?.user.email ?? "Orchestrator Cloud",
	});

	return (
		<DropdownMenu>
			<Tooltip>
				<TooltipTrigger asChild>
					<DropdownMenuTrigger asChild>
						<Pressable
							aria-label={label}
							className={FOOTER_RAIL_BUTTON_CLASS}
							tabIndex={tabIndex}
							type="button"
						>
							<NavRowHighlight />
							<span className="relative z-[1] grid place-items-center [&_svg]:size-icon-base">
								<AccountAvatar user={session?.user} />
							</span>
						</Pressable>
					</DropdownMenuTrigger>
				</TooltipTrigger>
				<TooltipContent side="right">{label}</TooltipContent>
			</Tooltip>
			<CloudAccountMenuContent side="right" />
		</DropdownMenu>
	);
}

function ShowMoreRow({
	label,
	expanded,
	onClick,
	className,
}: {
	label: string;
	expanded: boolean;
	onClick: () => void;
	className?: string;
}) {
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	return (
		<AnimatePresence initial={false} mode="wait">
			<motion.button
				key={expanded ? "show-less" : "show-more"}
				aria-label={label}
				className={cn(
					SECTION_ROW_CLASS,
					NAV_ROW_HIGHLIGHT_HOST_CLASS,
					"mb-1 shrink-0 rounded-lg text-left text-muted-foreground",
					className,
				)}
				initial={{ opacity: 0, y: 4 }}
				animate={{ opacity: 1, y: 0 }}
				exit={{ opacity: 0, y: -4 }}
				onClick={onClick}
				transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.14, ease: [0.25, 0.46, 0.45, 0.94] }}
				type="button"
			>
				<NavRowHighlight />
				<span className="relative z-[1] truncate">{t(expanded ? "shell.showLess" : "shell.showMore")}</span>
			</motion.button>
		</AnimatePresence>
	);
}

function SectionDisclosure({
	icon,
	label,
	open = true,
	onToggle,
	className,
	trailing,
	collapsible = true,
}: {
	icon?: ReactNode;
	label: string;
	open?: boolean;
	onToggle?: () => void;
	className?: string;
	trailing?: ReactNode;
	collapsible?: boolean;
}) {
	const labelRow = (
		<>
			{icon}
			<span className="truncate">{label}</span>
			{collapsible ? (
				<ChevronRight
					aria-hidden="true"
					className={cn("size-3.5! shrink-0 transition-transform duration-150", open && "rotate-90")}
				/>
			) : null}
		</>
	);

	if (!collapsible) {
		return (
			<div className={cn(SECTION_ROW_CLASS, trailing && "pr-1", className)}>
				<div className="flex min-w-0 flex-1 items-center gap-2">
					{labelRow}
				</div>
				{trailing}
			</div>
		);
	}

	if (trailing) {
		return (
			<div
				className={cn(
					SECTION_ROW_CLASS,
					NAV_ROW_HIGHLIGHT_HOST_CLASS,
					"rounded-lg pr-1",
					className,
				)}
			>
				<NavRowHighlight />
				<Pressable
					aria-expanded={open}
					aria-label={label}
					className="relative z-[1] flex min-w-0 flex-1 self-stretch items-center gap-2 text-left"
					onClick={onToggle}
					type="button"
				>
					{labelRow}
				</Pressable>
				<span className="relative z-[1] shrink-0">{trailing}</span>
			</div>
		);
	}

	return (
		<Pressable
			aria-expanded={open}
			aria-label={label}
			className={cn(
				SECTION_ROW_CLASS,
				NAV_ROW_HIGHLIGHT_HOST_CLASS,
				"rounded-lg text-left",
				className,
			)}
			onClick={onToggle}
			type="button"
		>
			<NavRowHighlight />
			<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2">
				{labelRow}
			</span>
		</Pressable>
	);
}

function SidebarTopNavRow({
	active = false,
	disabled,
	icon,
	label,
	onClick,
	tooltip,
	trailing,
}: {
	active?: boolean;
	disabled?: boolean;
	icon: ReactNode;
	label: string;
	onClick: () => void;
	tooltip?: string;
	trailing?: string;
}) {
	return (
		<SidebarMenuButton
			aria-label={label}
			className={cn(
				NAV_ROW_CLASS,
				SIDEBAR_ROW_CLASS,
				NAV_ROW_HIGHLIGHT_HOST_CLASS,
				"transition-none",
				"group-data-[collapsible=icon]:size-control-board! group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:rounded-lg group-data-[collapsible=icon]:p-0!",
			)}
			aria-current={active ? "page" : undefined}
			disabled={disabled}
			isActive={active}
			onClick={onClick}
			tooltip={tooltip}
		>
			<NavRowHighlight active={active} />
			<span className="relative z-[1] inline-flex shrink-0 items-center justify-center">{icon}</span>
			<span className="sidebar-expanded-chrome relative z-[1] min-w-0 flex-1 truncate text-left group-data-[collapsible=icon]:hidden">
				{label}
			</span>
			{trailing ? (
				<span className="sidebar-expanded-chrome relative z-[1] ml-auto shrink-0 font-sans text-caption text-passive group-data-[collapsible=icon]:hidden">
					{trailing}
				</span>
			) : null}
		</SidebarMenuButton>
	);
}

export function SettingsSidebarNav({ layer }: { layer: NonNullable<ReturnType<typeof useSettingsPage>> }) {
	const { t } = useTranslation();
	return (
		<SidebarContent className="scrollbar-none gap-0 px-2">
			<SidebarGroup className="p-0">
				<SidebarGroupContent>
					<SidebarMenu aria-label={t("settings.navSectionsAria")} className="gap-0.5" role="navigation">
						{layer.navItems.map(({ id, label, icon: Icon, active, disabled, onSelect }) => (
							<SidebarMenuItem key={id}>
								<SidebarTopNavRow active={active} disabled={disabled} icon={<Icon aria-hidden="true" />} label={label} onClick={onSelect} tooltip={label} />
							</SidebarMenuItem>
						))}
					</SidebarMenu>
				</SidebarGroupContent>
				<SettingsSaveStatus />
			</SidebarGroup>
		</SidebarContent>
	);
}

function SidebarSearchButton({ onOpen }: { onOpen: () => void }) {
	const { t } = useTranslation();
	const { state } = useSidebar();
	const isCollapsed = state === "collapsed";
	const overrides = useKeybindingsStore((store) => store.overrides);
	const paletteBinding = effectiveShortcutBindings("command-palette", isMac, overrides)[0];
	const commandPaletteShortcutLabel = paletteBinding
		? shortcutBindingKeys(paletteBinding, isMac).join(isMac ? " " : "+")
		: "Unassigned";
	return (
		<SidebarMenuItem className="group-data-[collapsible=icon]:mb-0">
			<SidebarTopNavRow
				icon={<Search aria-hidden="true" />}
				label={t("shell.search")}
				onClick={() => {
					queueMicrotask(onOpen);
				}}
				tooltip={isCollapsed ? t("shell.search") : undefined}
				trailing={commandPaletteShortcutLabel}
			/>
		</SidebarMenuItem>
	);
}

function CreateProjectButton({
	existingProjectPaths,
	remoteHosts,
	onCreateRemoteProject,
	onInitializeRemoteProject,
	onCloneProject,
	onCreateProject,
	onInitializeProject,
	onOpenExistingProject,
}: Pick<SidebarProps, "onCloneProject" | "onCreateProject" | "onInitializeProject" | "onCreateRemoteProject" | "onInitializeRemoteProject" | "remoteHosts"> & {
	existingProjectPaths: readonly string[];
	onOpenExistingProject: (path: string) => void | Promise<void>;
}) {
	const { t } = useTranslation();
	const createProjectNonce = useUiStore((state) => state.createProjectNonce);
	const folderDropRequest = useUiStore((state) => state.folderDropRequest);
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const [hostId, setHostId] = useState<string>();
	const host = remoteHosts?.find((candidate) => candidate.hostId === hostId);
	return (
		<CreateProjectFlow
			droppedPath={folderDropRequest}
			existingProjectPaths={existingProjectPaths}
			remoteHosts={remoteHosts}
			hostId={hostId}
			hostLabel={host?.label ?? hostId}
			connected={!hostId || host?.status === "connected"}
			onSelectHost={setHostId}
			onDismiss={() => setHostId(undefined)}
			mode="choose"
			onCloneProject={onCloneProject}
			onCreateProject={async (input) => {
				if (hostId) {
					await onCreateRemoteProject(hostId, input);
					setHostId(undefined);
				} else await onCreateProject(input);
			}}
			onCreateStandaloneAgent={() => requestNewTask(STANDALONE_WORKSPACE_ID, hostId)}
			onInitializeProject={(path) => hostId ? onInitializeRemoteProject(hostId, path) : onInitializeProject(path)}
			onOpenExistingProject={onOpenExistingProject}
			openSignal={createProjectNonce}
		>
			{({ disabled, choosePath, label }) => (
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex">
							<Button
								size="icon-xs"
								variant="quiet"
								aria-label={t("shell.newProject")}
								className={ROW_ACTION_BUTTON_CLASS}
								disabled={disabled}
								onClick={choosePath}
								type="button"
							>
									<Plus aria-hidden="true" />
							</Button>
						</span>
					</TooltipTrigger>
					<TooltipContent>{label}</TooltipContent>
				</Tooltip>
			)}
		</CreateProjectFlow>
	);
}

function CreateProjectListItem() {
	const { t } = useTranslation();
	const requestCreateProject = useUiStore((state) => state.requestCreateProject);
	return (
		<SidebarMenuItem className="mb-px group-data-[collapsible=icon]:mb-0">
			<Tooltip>
				<TooltipTrigger asChild>
					<Button
						aria-label={t("shell.newProject")}
						className="h-control-board w-full rounded-lg"
						onClick={() => requestCreateProject()}
						size="none"
						type="button"
						variant="quiet"
					>
						<Plus className="size-icon-sm" aria-hidden="true" />
					</Button>
				</TooltipTrigger>
				<TooltipContent side="right">{t("shell.newProject")}</TooltipContent>
			</Tooltip>
		</SidebarMenuItem>
	);
}
