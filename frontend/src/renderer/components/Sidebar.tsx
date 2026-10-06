import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import {
	DndContext,
	PointerSensor,
	closestCenter,
	useSensor,
	useSensors,
	type Modifier,
	type DragEndEvent,
} from "@dnd-kit/core";
import { SortableContext, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
	AlertTriangle,
	Archive,
	CalendarClock,
	ChevronRight,
	Download,
	Folder,
	FolderOpen,
	LogIn,
	LogOut,
	MoreVertical,
	PanelLeft,
	Pencil,
	Pin,
	PinOff,
	Plus,
	RefreshCw,
	Search,
	Settings,
	Smartphone,
	Trash2,
	User,
	X,
} from "lucide-react";
import {
	useCallback,
	useEffect,
	useId,
	useLayoutEffect,
	memo,
	useMemo,
	useRef,
	useState,
	type ComponentProps,
	type CSSProperties,
	type DragEvent as ReactDragEvent,
	type MouseEvent,
	type ReactNode,
	type RefObject,
} from "react";
import { flushSync } from "react-dom";
import {
	CollapsibleBody,
	createSidebarDisclosureStore,
	useProjectExpanded,
	useSectionOpen,
	type SidebarDisclosureStore,
	type SidebarSection,
} from "./sidebarDisclosure";
import type { UpdateStatus } from "../../main/update-settings";
import { parseNightlyVersion } from "../lib/build-channel";
import {
	hasConfiguredOrchestratorAgent,
	newestActiveOrchestrator,
	openPRs,
	type WorkspaceSession,
	type WorkspaceSummary,
	sortedWorkerSessions,
	resolveNextNavigationAfterSessionKill,
	workerSessions,
	CLOUD_PROJECT_KIND,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
} from "../types/workspace";
import { getSessionStatusDotView } from "../lib/session-presentation";
import { deriveSessionAgentSwitchPresentation } from "../lib/agent-switch-presentation";
import { aoBridge } from "../lib/bridge";
import { hasTrustedApiBaseUrl } from "../lib/api-client";
import { useCommandPaletteEnabled } from "../hooks/useCommandPaletteEnabled";
import { useCanResumeAgent } from "../hooks/useCanResumeAgent";
import { cloudSessionsQueryKey, workspaceQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { conversationQueryKey, conversationQueryOptions } from "../hooks/useConversation";
import { usePinSession, useUnpinSession } from "../hooks/usePinSession";
import { spawnCloudOrchestrator } from "../lib/cloud-orchestrator";
import { resumeOrchestrator, spawnOrchestrator } from "../lib/spawn-orchestrator";
import { formatTimeCompact, formatTimeTerse } from "../lib/format-time";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { useResizable } from "../hooks/useResizable";
import { useCloudGate } from "../hooks/useCloudGate";
import { useCloudLocalAuth } from "../hooks/useCloudLocalAuth";
import { useLocalSignInDialogStore } from "../stores/local-signin-dialog-store";
import { useShellMaybe } from "../lib/shell-context";
import { useSidebarUpdateDismissal } from "../hooks/useSidebarUpdateDismissal";
import { useUpdateStatus } from "../hooks/useUpdateStatus";
import { MAX_SESSION_DISPLAY_NAME_LEN, useSessionRename } from "../hooks/useSessionRename";
import { effectiveShortcutBindings, shortcutBindingKeys } from "../../shared/shortcuts";
import {
	ContextMenu,
	ContextMenuContent,
	ContextMenuItem,
	ContextMenuTrigger,
} from "./ui/context-menu";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
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
import { LazyTooltip, Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { OrchestratorIcon } from "./icons";
import { Badge } from "./ui/badge";
import { cn } from "../lib/utils";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import { useUiStore } from "../stores/ui-store";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { ConfirmDialog } from "./ConfirmDialog";
import { SessionArchiveDialog } from "./SessionArchiveDialog";
import { CreateProjectFlow, type CloneProjectInput, type CreateProjectInput } from "./CreateProjectFlow";
import { ResizeHandle } from "./ResizeHandle";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { useCloudSession } from "../lib/cloud-session";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { sessionUiKey } from "../lib/hosts";
import { RemoteHostsSection } from "./RemoteHostsSection";

// macOS paints framed chrome: the fixed TitlebarNav cluster carries the
// sidebar toggle + history arrows above this surface. Windows hangs the sidebar
// under its custom titlebar.
const isMac = isMacPlatform();
const brandInTitlebar = isMac || isLinuxPlatform();
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

// One row-action layout shared by project rows, session rows, and the section
// header "+": 24px square buttons, 14px icons, a 6px gap, and a 4px right inset,
// vertically centered in the row, so every action's right edge lines up. Never
// painted: `.sidebar-icon-action` also opts out of the sidebar focus fill in
// styles.css. Hover/reveal stays instant (no transitions here).
const ROW_ACTIONS_CLASS = "absolute inset-y-0 right-1 flex items-center gap-0.5";
const ROW_ACTION_BUTTON_CLASS =
	"sidebar-icon-action grid size-6 shrink-0 place-items-center rounded-md !bg-transparent text-passive hover:!bg-interactive-hover focus:!bg-transparent focus-visible:!bg-interactive-hover active:!bg-interactive-hover data-[state=open]:!bg-interactive-hover hover:text-foreground focus-visible:text-foreground disabled:pointer-events-none disabled:opacity-50 data-[state=open]:text-foreground [&_svg]:size-icon-md";
const HOVER_ACTION_CLASS = ROW_ACTION_BUTTON_CLASS;
const SESSION_ACTION_CLASS = ROW_ACTION_BUTTON_CLASS;

// Shared nav-row chrome (Codex-style): inset pill, 14px type, no accent bar.
// Plain fill stays for non-interactive status rows; interactive rows use
// {@link NavRowHighlight} via {@link NAV_ROW_HIGHLIGHT_HOST_CLASS}.
const NAV_ROW_CLASS =
	"h-9 gap-2.5 rounded-lg px-2.5 text-sm font-medium text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground active:bg-interactive-hover active:text-foreground data-[active=true]:bg-interactive-active data-[active=true]:font-medium data-[active=true]:text-foreground";

/** Expanded footer action row: growing highlight behind icon + label. */
const FOOTER_NAV_BUTTON_CLASS = cn(
	NAV_ROW_CLASS,
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"flex h-9 w-full items-center text-left transition-none",
);

/** Collapsed footer icon-rail control: same growing highlight in the square. */
const FOOTER_RAIL_BUTTON_CLASS = cn(
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"grid size-control-board place-items-center rounded-lg text-muted-foreground [&_svg]:size-icon-base",
);

// Top-of-sidebar rows (Search, Automations) and project rows: one 32px row with
// a 10px inset, 8px icon gap, and 14px icons so edges match the section headers
// and session rows.
const SIDEBAR_ROW_CLASS = "h-8 gap-2 [&_svg]:size-icon-md";

// Search + Pinned/Projects section chrome: same type, icon, and row size.
const SECTION_ROW_CLASS =
	"flex h-8 w-full min-w-0 items-center gap-2 rounded-md px-2.5 text-sm font-medium text-muted-foreground [&_svg]:size-icon-md [&_svg]:shrink-0";

// Mirrors the daemon's display-name cap (maxDisplayNameLen) and the spawn
// `--name` flag, so inline edits never round-trip a value the API would reject.

// Reorder drags start from the row's primary click surface. The 4px activation
// distance keeps a plain navigation/disclosure click from starting a drag;
// nested action buttons remain outside that activator surface.
const REORDER_ACTIVATION_DISTANCE = 4;
// A transparent 1x1 image replaces the browser's default drag ghost, so the
// dragged row stays put at reduced opacity instead of trailing under the cursor.
const EMPTY_DRAG_IMAGE = typeof Image === "undefined" ? null : new Image();
if (EMPTY_DRAG_IMAGE) EMPTY_DRAG_IMAGE.src = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";

/** Stable drag-context id per project's session list. */
export const sessionDndId = (projectId: string) => `sidebar-sessions-${projectId}`;

// Module-level: useSensor memoizes on the options object's identity, and a new
// sensor descriptor changes dnd-kit's context value, re-rendering every row.
const REORDER_POINTER_SENSOR_OPTIONS = {
	activationConstraint: { distance: REORDER_ACTIVATION_DISTANCE },
};

function useReorderSensors() {
	return useSensors(useSensor(PointerSensor, REORDER_POINTER_SENSOR_OPTIONS));
}

// Browsers dispatch a click after pointerup even when dnd-kit has just completed
// a drag. Suppress only that same-turn synthetic click; if no click follows,
// clear the guard before the next user interaction.
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

/** Session sorting stays vertical and never inherits dnd-kit's scale correction. */
function sortableRowStyle({ transform, transition, isDragging, dropTransitionDisabled }: Pick<SortableRow, "transform" | "transition" | "isDragging"> & { dropTransitionDisabled?: boolean }): CSSProperties {
	return {
		transform: transform ? CSS.Transform.toString({ ...transform, x: 0, scaleX: 1, scaleY: 1 }) : undefined,
		// The active row must clear its drag transform immediately on drop; its
		// siblings retain dnd-kit's smooth displacement while the pointer moves.
		transition: isDragging || dropTransitionDisabled ? "none" : (transition ?? "transform 180ms cubic-bezier(0.22, 1, 0.36, 1)"),
	};
}

// Session drags use their owning list as the visual boundary.
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

// Module-level so DndContext sees one array identity across renders.
const SESSION_LIST_MODIFIERS: Modifier[] = [restrictToListBounds];

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

function applyOrder<T>(items: readonly T[], idOf: (item: T) => string, order: readonly string[], unplaced: "start" | "end"): T[] {
	if (order.length === 0) return [...items];
	const byId = new Map(items.map((item) => [idOf(item), item]));
	const placed = order.flatMap((id) => {
		const item = byId.get(id);
		return item ? [item] : [];
	});
	const placedIds = new Set(order);
	const rest = items.filter((item) => !placedIds.has(idOf(item)));
	return unplaced === "start" ? [...rest, ...placed] : [...placed, ...rest];
}

function useGrabbingCursor(active: boolean) {
	useEffect(() => {
		if (!active) return;
		document.documentElement.classList.add("sidebar-reordering");
		return () => document.documentElement.classList.remove("sidebar-reordering");
	}, [active]);
}

export const SIDEBAR_DEFAULT_WIDTH = 240;
/** Floor/ceiling for sidebar resize — pass the same values to useResizable AND ResizeHandle. */
export const SIDEBAR_MIN_WIDTH = 200;
export const SIDEBAR_MAX_WIDTH = 420;
const SIDEBAR_BRAND_TRAILING_GAP = 12;
/** Narrowest the sidebar may get: just wide enough to show the whole brand
 *  label. Measured, because the label's left edge moves with window zoom (the
 *  traffic-light reserve is divided by the zoom factor). Falls back to
 *  SIDEBAR_MIN_WIDTH when no brand is rendered (collapsed rail, tests). */
function sidebarMinWidth(): number {
	const brand = Array.from(document.querySelectorAll<HTMLElement>("[data-sidebar-brand]")).find(
		(el) => el.getBoundingClientRect().width > 0,
	);
	if (!brand) return SIDEBAR_MIN_WIDTH;
	// Measure the label's natural width (its scrollWidth), not its painted width:
	// the titlebar clips the label to the sidebar, so the painted width would
	// shrink the floor to whatever the sidebar already is. Without a separate
	// label (the Windows header), the whole brand element is the label.
	const label = brand.querySelector<HTMLElement>("[data-brand-label]");
	// +1px of slack: scrollWidth is rounded, and a fractional label width would
	// otherwise clip the last letter at exactly the floor.
	const fit = label
		? Math.ceil(label.getBoundingClientRect().left + label.scrollWidth + SIDEBAR_BRAND_TRAILING_GAP + 1)
		: Math.ceil(brand.getBoundingClientRect().left + brand.scrollWidth + SIDEBAR_BRAND_TRAILING_GAP);
	return Math.min(SIDEBAR_MAX_WIDTH, fit);
}
/** Initial item count shown in expanded sections; Show more/less toggles the remainder.
 *  Collapsed icon rail always shows the full list so projects stay reachable. */
const SIDEBAR_INITIAL_SECTION_LIMIT = 10;
/** Initial agent count listed under each expanded project before its own Show more. */
const SIDEBAR_PROJECT_SESSION_LIMIT = 6;
/** Section scrollers fill the height their flex parent leaves them and scroll inside it. */
const SECTION_SCROLLER_CLASS =
	"scrollbar-none overflow-y-auto overflow-x-hidden overscroll-contain group-data-[collapsible=icon]:overflow-visible";

/** Returns the previous array while the ids are unchanged, so dnd-kit's
 *  SortableContext (whose value is keyed on array identity) does not re-render
 *  every sortable row when only one session's fields changed. */
function useStableIds(ids: string[]): string[] {
	const ref = useRef(ids);
	if (ref.current !== ids && (ref.current.length !== ids.length || ref.current.some((id, index) => id !== ids[index]))) {
		ref.current = ids;
	}
	return ref.current;
}

/** Rows that just appeared (Show more) or are on their way out (Show less) get
 *  a short CSS fade for this long. */
const SHOW_MORE_WAVE_MS = 180;

type ShowMoreWave = { kind: "enter" | "leave"; ids: ReadonlySet<string>; until: number };

/** Caps a sidebar list at `limit` behind Show more/less. The cap lifts on its
 *  own when the active item sits past it, until the user collapses it again.
 *  `listed` is what to render: while Show less is fading the tail out it still
 *  includes those rows (`wave.kind === "leave"`); `settled` never does, so use it
 *  for drag-and-drop ids. */
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
	const settled = useMemo(
		() => (isCollapsed || showAll || items.length <= limit ? items : items.slice(0, limit)),
		[isCollapsed, items, limit, showAll],
	);
	// Derive the fade from the flip itself (render-phase update), so the tail rows
	// mount with their enter class already applied rather than flashing in first.
	const [wave, setWave] = useState<ShowMoreWave | null>(null);
	const [lastShowAll, setLastShowAll] = useState(showAll);
	if (lastShowAll !== showAll) {
		setLastShowAll(showAll);
		const tail = items.slice(limit);
		setWave(
			tail.length === 0 || isCollapsed || prefersReducedMotion()
				? null
				: { kind: showAll ? "enter" : "leave", ids: new Set(tail.map((item) => item.id)), until: Date.now() + SHOW_MORE_WAVE_MS },
		);
	}
	// Only the fade-out needs a re-render when it ends (to unmount the tail). The
	// fade-in expires by timestamp (see wavePhase), so a finished Show more costs
	// no extra render.
	useEffect(() => {
		if (wave?.kind !== "leave") return;
		const timer = setTimeout(() => setWave(null), SHOW_MORE_WAVE_MS);
		return () => clearTimeout(timer);
	}, [wave]);
	const listed = useMemo(
		() => (wave?.kind === "leave" ? items.filter((item, index) => index < limit || wave.ids.has(item.id)) : settled),
		[items, limit, settled, wave],
	);
	const toggleShowAll = useCallback(() => {
		const next = !showAll;
		setShowAll(next);
		setShowAllDismissed(!next);
	}, [showAll]);
	return { listed, settled, wave, hiddenCount: Math.max(0, items.length - limit), showAll, toggleShowAll };
}

function prefersReducedMotion(): boolean {
	return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/** The CSS fade a row should wear right now. An expired fade-in reads as none, so
 *  the class drops off on the next unrelated render (after its animation ended,
 *  so it never replays when a drag-and-drop reorder moves the node). */
function wavePhase(wave: ShowMoreWave | null, id: string): "enter" | "leave" | undefined {
	if (!wave?.ids.has(id)) return undefined;
	if (wave.kind === "enter" && Date.now() >= wave.until) return undefined;
	return wave.kind;
}

/** Scratchpad's total section cap, or none in the collapsed icon rail. Projects
 *  take whatever height remains (flex-1 + min-h-0), so the two sections share the
 *  column by flex layout alone and can never sum past it. */
function scratchpadSectionStyle(isCollapsed: boolean): CSSProperties | undefined {
	if (isCollapsed) return undefined;
	return { maxHeight: "calc(50cqh - var(--space-2))" };
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
	const [scrollEdges, setScrollEdges] = useState({ top: false, bottom: false });
	const updateScrollEdges = useCallback(() => {
		const scroller = scrollerRef.current;
		if (!scroller) return;
		const overflow = scroller.scrollHeight - scroller.clientHeight;
		const next = {
			top: overflow > 1 && scroller.scrollTop > 1,
			bottom: overflow > 1 && scroller.scrollTop < overflow - 1,
		};
		setScrollEdges((current) => (current.top === next.top && current.bottom === next.bottom ? current : next));
	}, []);

	useLayoutEffect(() => {
		const scroller = scrollerRef.current;
		if (!scroller) return;
		updateScrollEdges();
		scroller.addEventListener("scroll", updateScrollEdges, { passive: true });
		const resizeObserver = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(updateScrollEdges);
		resizeObserver?.observe(scroller);
		if (scroller.firstElementChild) resizeObserver?.observe(scroller.firstElementChild);
		const mutationObserver =
			typeof MutationObserver === "undefined"
				? undefined
				: new MutationObserver(() => {
					if (scroller.firstElementChild) resizeObserver?.observe(scroller.firstElementChild);
					updateScrollEdges();
				});
		mutationObserver?.observe(scroller, { childList: true, subtree: true });
		return () => {
			scroller.removeEventListener("scroll", updateScrollEdges);
			resizeObserver?.disconnect();
			mutationObserver?.disconnect();
		};
	}, [updateScrollEdges]);

	return (
		<div className={`relative min-h-0 ${wrapperClassName ?? ""}`}>
			<div ref={scrollerRef} className={className} data-testid={testId} style={style}>
				{children}
			</div>
			{scrollEdges.top ? <div aria-hidden="true" className="sidebar-section-scroll-fade sidebar-section-scroll-fade--top" /> : null}
			{scrollEdges.bottom ? <div aria-hidden="true" className="sidebar-section-scroll-fade sidebar-section-scroll-fade--bottom" /> : null}
		</div>
	);
}

/** Section header bound to the disclosure store: toggling re-renders this header
 *  (and its body) only, never the Sidebar. */
function StoreSectionHeader({
	store,
	section,
	...props
}: Omit<ComponentProps<typeof SectionDisclosure>, "open" | "onToggle"> & {
	store: SidebarDisclosureStore;
	section: SidebarSection;
}) {
	const open = useSectionOpen(store, section);
	return <SectionDisclosure {...props} open={open} onToggle={store.sectionToggles[section]} />;
}

/** Section body bound to the disclosure store. `children` is created by the
 *  section's owner, so its identity survives a toggle and the rows inside are not
 *  re-rendered when the section opens or closes. */
function StoreSectionBody({
	store,
	section,
	forceOpen = false,
	hasContent = true,
	children,
	className,
}: {
	store: SidebarDisclosureStore;
	section: SidebarSection;
	/** The collapsed icon rail always shows its list. */
	forceOpen?: boolean;
	hasContent?: boolean;
	children: ReactNode;
	className?: string;
}) {
	const open = useSectionOpen(store, section);
	return (
		<CollapsibleBody open={hasContent && (open || forceOpen)} className={className} innerClassName="flex flex-col">
			{children}
		</CollapsibleBody>
	);
}

type SidebarProps = {
	/** Hide the sidebar's right edge stroke on the welcome board inset chrome. */
	hideEdgeBorder?: boolean;
	underTopbar?: boolean;
	/** Chrome height to clear when underTopbar is set. Defaults to --size-toolbar. */
	topbarOffset?: "toolbar" | "titlebar" | "trafficLights" | "session";
	workspaceError?: string;
	workspaces: WorkspaceSummary[];
	remoteHosts?: RemoteHost[];
	onCreateRemoteProject: (hostId: string, input: CreateProjectInput) => Promise<void>;
	onInitializeRemoteProject: (hostId: string, path: string) => Promise<void>;
	onOpenRemoteProject?: (hostId: string, projectId: string) => void;
	onOpenRemoteOrchestrator?: (hostId: string, projectId: string) => void;
	onConfigureRemoteProject?: (hostId: string, projectId: string) => void;
	onRemoveRemoteProject?: (hostId: string, projectId: string) => Promise<void>;
	onRetryRemoteHosts?: () => void;
	remoteWorkspaces?: WorkspaceSummary[];
	remoteFailedHostIds?: string[];
	onCloneProject: (input: CloneProjectInput) => Promise<void>;
	onCreateProject: (input: CreateProjectInput) => Promise<void>;
	onInitializeProject: (path: string) => Promise<void>;
	onRemoveProject: (projectId: string) => Promise<void>;
	/** Fixed shell chrome that also consumes the live sidebar width. */
	resizeAuxiliaryTargetRef?: RefObject<HTMLElement | null>;
};

// Selection state comes from the URL: which project/session is active is the
// route params, and clicks navigate rather than mutate a store.
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
	const goStandaloneBoard = useCallback(() => void navigate({ to: "/sessions" }), [navigate]);
	const goGlobalSettings = useCallback(() => openGlobalSettings(), [openGlobalSettings]);
	const goConnectMobile = useCallback(() => openGlobalSettings("mobile"), [openGlobalSettings]);
	const goSettings = useCallback((projectId: string) => openProjectSettings(projectId), [openProjectSettings]);
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
	// Route-independent actions: every callback is stable, so rows that only
	// need to navigate do not re-render when the route changes.
	const nav = useMemo(
		() => ({ goHome, goProject, goSession, goSettings }),
		[goHome, goProject, goSession, goSettings],
	);
	return useMemo(() => ({
		nav,
		isHome: pathname === "/",
		isAutomations: pathname === "/automations",
		activeRemoteHostId: params.hostId,
		activeRemoteProjectId: params.hostId ? params.projectId : undefined,
		activeRemoteSessionId: params.hostId ? params.sessionId : undefined,
		activeProjectId: params.hostId ? undefined : params.projectId,
		activeSessionId: params.hostId ? undefined : params.sessionId,
		goHome,
		goAutomations,
		goStandaloneBoard,
		// Settings is a modal — open it in place so the current page (session
		// terminal, board, etc.) stays underneath.
		goGlobalSettings,
		goConnectMobile,
		goSettings,
		goProject,
		goSession,
	}), [nav, goAutomations, goConnectMobile, goGlobalSettings, goHome, goProject, goSession, goSettings, goStandaloneBoard, params.hostId, params.projectId, params.sessionId, pathname]);
}

// Colour tracks the session's board section, preserving SCM state while the
// agent runs; motion stays on raw agent activity. A no-PR idle session turns
// blue when it starts working. See getSessionStatusDotView for the lane mapping.
function SessionStatusDot({ session }: { session: WorkspaceSession }) {
	const dot = getSessionStatusDotView(session);
	return (
		<span
			aria-hidden="true"
			className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1.5"
		>
			<span
				className={cn(
					"size-2 rounded-full",
					dot.className,
					dot.breathe && "animate-status-pulse",
				)}
				data-session-status={session.status}
			/>
		</span>
	);
}

export {
	resolveNextNavigationAfterSessionKill,
	type NextSessionNavigation,
} from "../types/workspace";

// Built on shadcn's sidebar primitives (components/ui/sidebar): the provider in
// _shell owns the persistent open state. Collapsed sidebars move fully off-canvas.
export function Sidebar({
	hideEdgeBorder = false,
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
	onRemoveRemoteProject = async () => undefined,
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
	const [expandedChromeVisible, setExpandedChromeVisible] = useState(!isCollapsed);
	// One IPC subscription for both footer variants of the restart-to-update prompt.
	const updateStatus = useUpdateStatus();
	const availableUpdateVersion = updateStatus.state === "available" ? updateStatus.version : undefined;
	const updateDismissal = useSidebarUpdateDismissal(availableUpdateVersion);
	const openUpdateInstallPrompt = useUiStore((state) => state.openUpdateInstallPrompt);
	// Daemon status for the smoke suite's sr-only mirror in the footer. Null when
	// rendered outside the shell (unit tests) — the mirror simply doesn't render.
	const daemonStatus = useShellMaybe()?.daemonStatus ?? null;
	const commandPaletteEnabled = useCommandPaletteEnabled();
	const setCommandPaletteOpen = useUiStore((s) => s.setCommandPaletteOpen);
	// Which project lists the active session, so only that project re-renders
	// when the route moves between sessions.
	const activeSessionOwnerId = useMemo(
		() => selection.activeSessionId
			? workspaces.find((workspace) => workspace.sessions.some((session) => session.id === selection.activeSessionId))?.id
			: undefined,
		[selection.activeSessionId, workspaces],
	);
	const openCommandPalette = useCallback(() => setCommandPaletteOpen(true), [setCommandPaletteOpen]);
	const existingProjectPaths = useMemo(
		() => workspaces
			.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND)
			.map((workspace) => workspace.path)
			.filter((path): path is string => Boolean(path)),
		[workspaces],
	);
	const openExistingProject = useCallback(
		(path: string) => {
			const workspace = workspaces.find(
				(candidate) => candidate.kind !== STANDALONE_PROJECT_KIND && candidate.path === path,
			);
			if (workspace) selection.goProject(workspace.id);
		},
		[selection, workspaces],
	);
	useLayoutEffect(() => {
		// Offcanvas: the panel slides off-screen on collapse — no need to hide content.
		// Reveal immediately on expand so there's no fade-in delay.
		if (!isCollapsed) {
			setExpandedChromeVisible(true);
		}
	}, [isCollapsed]);

	// Project expansion and section open/close live in a store outside React
	// state: each project row and section subscribes to its own flag, so a toggle
	// re-renders that row or section and never the Sidebar. An empty/missing
	// persisted set intentionally means all projects start collapsed.
	const [disclosure] = useState(() =>
		createSidebarDisclosureStore(selection.activeSessionId ? selection.activeProjectId : undefined),
	);

	// agent-orchestrator's sidebar resize: drag the right edge (200-420px,
	// persisted), double-click to reset to 240px. Drives --ao-sidebar-w on :root,
	// only to the two layout consumers and fixed titlebar strip, rather than
	// :root. Dragging clamps
	// at SIDEBAR_MIN_WIDTH — collapsing stays on the explicit toggle (⌘B /
	// titlebar button), never on a drag.
	const resizeScopeRef = useRef<HTMLDivElement>(null);
	const getResizeTargets = useCallback(() => {
		const scope = resizeScopeRef.current;
		return [
			scope?.querySelector<HTMLElement>('[data-slot="sidebar-gap"]') ?? null,
			scope?.querySelector<HTMLElement>('[data-slot="sidebar-container"]') ?? null,
			resizeAuxiliaryTargetRef?.current ?? null,
		];
	}, [resizeAuxiliaryTargetRef]);
	// Stable getter — ResizeHandle keeps callbacks in refs; an inline arrow would
	// rebuild observers on every Sidebar render (daemon ticks / activity).
	const getSidebarBorderElement = useCallback(
		() =>
			resizeScopeRef.current?.querySelector<HTMLElement>('[data-slot="sidebar-container"]') ?? null,
		[],
	);
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

	const [projectOrder, setProjectOrder] = useState<string[]>([]);
	const orderedWorkspaces = useMemo(
		() => applyOrder(workspaces, (workspace) => workspace.id, projectOrder, "end"),
		[projectOrder, workspaces],
	);
	// The ad hoc group is a bucket for projectless sessions, not a project: it
	// gets its own Scratchpad section below the project list rather than a
	// project row appended to the end of it.
	const projectWorkspaces = useMemo(
		() => orderedWorkspaces.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND),
		[orderedWorkspaces],
	);
	const standaloneWorkspace = useMemo(
		() => workspaces.find((workspace) => workspace.kind === STANDALONE_PROJECT_KIND),
		[workspaces],
	);
	const {
		listed: visibleWorkspaces,
		wave: projectWave,
		hiddenCount: hiddenProjectCount,
		showAll: showAllProjects,
		toggleShowAll: toggleShowAllProjects,
	} = useShowMoreCap(projectWorkspaces, SIDEBAR_INITIAL_SECTION_LIMIT, selection.activeProjectId, isCollapsed);
	const hasProjectContent = projectWorkspaces.length > 0 || remoteHosts.length > 0;
	const projectIds = useMemo(
		() => projectWorkspaces.map((workspace) => workspace.id),
		[projectWorkspaces],
	);
	// Latest ids/callbacks live in refs so row handlers keep one identity across
	// daemon ticks (a new workspaces array would otherwise re-render every row).
	const projectIdsRef = useRef(projectIds);
	projectIdsRef.current = projectIds;
	const removeProjectRef = useRef(onRemoveProject);
	removeProjectRef.current = onRemoveProject;
	const removeProject = useCallback((projectId: string) => removeProjectRef.current(projectId), []);
	const projectDragClickGuard = usePostDragClickGuard();
	const [draggingProjectId, setDraggingProjectId] = useState<string | null>(null);
	// Keep the active id and drop target in refs so dragover can decide without a
	// full-list re-render while dragging: only the dragged row re-renders (via
	// `isDragged`), and the drop line updates through dedicated React state.
	const draggingProjectIdRef = useRef<string | null>(null);
	const projectDropTargetRef = useRef<{ overId: string; placement: ProjectDropPlacement } | null>(null);
	useGrabbingCursor(draggingProjectId !== null);
	// The drop line is a single element placed at the boundary's gap centre, so
	// "after A" and "before B" resolve to the same spot (no shift across the
	// boundary). Its top animates so the line slides between projects.
	const [dropLine, setDropLine] = useState<{ top: number; visible: boolean }>({ top: 0, visible: false });

	const clearProjectDropIndicator = useCallback(() => {
		projectDropTargetRef.current = null;
		setDropLine((previous) => ({ ...previous, visible: false }));
	}, []);

	const handleProjectDragStart = useCallback((event: ReactDragEvent<HTMLElement>, projectId: string) => {
		if (!projectIdsRef.current.includes(projectId)) return;
		event.dataTransfer.effectAllowed = "move";
		// Some engines refuse to begin a drag unless the transfer carries data.
		event.dataTransfer.setData("text/plain", projectId);
		if (EMPTY_DRAG_IMAGE) event.dataTransfer.setDragImage(EMPTY_DRAG_IMAGE, 0, 0);
		draggingProjectIdRef.current = projectId;
		projectDropTargetRef.current = null;
		setDraggingProjectId(projectId);
	}, []);

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
		if (reorderAtProjectBoundary(projectIdsRef.current, activeId, overId, placement) === null) {
			clearProjectDropIndicator();
			return;
		}
		// Only invite the drop once this is a real reorder target.
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
	}, [clearProjectDropIndicator]);

	const handleProjectDrop = useCallback((event: ReactDragEvent<HTMLElement>) => {
		const activeId = draggingProjectIdRef.current;
		const target = projectDropTargetRef.current;
		if (!activeId || !target) return;
		event.preventDefault();
		const next = reorderAtProjectBoundary(projectIdsRef.current, activeId, target.overId, target.placement);
		if (next) setProjectOrder(next);
		handleProjectDragEnd();
	}, [handleProjectDragEnd]);

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

	const openPinnedSession = useCallback(
		(target: WorkspaceSession) => {
			if (target.kind === "worker") recordManualWorkerOpen(target.id, target.hostId);
			if (target.hostId) void remoteNavigate(sessionNavigateTarget(target.workspaceId, target.id, target.hostId));
			else selection.nav.goSession(target.workspaceId, target.id);
		},
		[remoteNavigate, selection.nav],
	);

	const handlePinnedSessionKilled = useCallback(
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
			} else {
				selection.goProject(killedSession.workspaceId);
			}
		},
		[onOpenRemoteProject, remoteNavigate, remoteWorkspaces, selection, workspaces],
	);

	return (
		// Pinned sidebars start below shell chrome.
		<SidebarRoot
			collapsible="offcanvas"
			resizeScopeRef={resizeScopeRef}
			data-expanded-chrome={expandedChromeVisible ? "visible" : "hidden"}
			data-topbar-offset={underTopbar ? topbarOffset : undefined}
			className={cn(
				"sidebar-focusless",
				// The container keeps a 1px right border, so its content box is 1px
				// narrower on the right. Pad the left by the same 1px so row gutters
				// match on both sides.
				"pl-px",
				hideEdgeBorder ? "border-transparent" : "border-r-0 group-data-[side=left]:border-r-0",
				// Prefer top/bottom over h-svh/inset-y so titlebar offset (`top-(--sidebar-chrome-offset)`)
				// clears chrome without fighting a second height constraint.
				!underTopbar
					? "top-0 bottom-0"
					: "top-(--sidebar-chrome-offset) bottom-0 h-auto!",
			)}
		>
			<SidebarHeader className="gap-0 p-0 px-3 pt-2 group-data-[collapsible=icon]:px-1.5 group-data-[collapsible=icon]:pt-2">
				{/*
				 * Brand → home. Design contracts (do not regress):
				 * - Click navigates home; do NOT add hover/focus *fill* (styles.css
				 *   opts `[data-sidebar-brand]` out of `.sidebar-focusless` wash).
				 * - Keyboard focus uses the dedicated outline rule in styles.css —
				 *   never `focus-visible:outline-none` (global kill would leave it blind).
				 * - No separate "home" affordance on the mark — the whole brand is the control.
				 */}
				<button
					aria-label={t("shell.goHome")}
					className={cn(
						"group/brand flex w-full shrink-0 items-center gap-1.5 rounded-md px-0.5 text-left",
						// macOS/Linux show the brand in TitlebarNav instead.
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
						Orchestrator.inc
					</span>
				</button>
				<Tooltip>
					<TooltipTrigger asChild>
						<button
							aria-label={isCollapsed ? t("shell.expandSidebar") : t("shell.collapseSidebar")}
							className="hidden size-control-board place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground group-data-[collapsible=icon]:grid [&_svg]:size-icon-base"
							onClick={toggleSidebar}
							type="button"
						>
							<PanelLeft aria-hidden="true" />
						</button>
					</TooltipTrigger>
					<TooltipContent side="right">
						{isCollapsed ? t("shell.expandSidebar") : t("shell.collapseSidebar")}
					</TooltipContent>
				</Tooltip>
			</SidebarHeader>

			{/* Keep Search + section chrome fixed above the scrollable sidebar content. */}
			<div className="flex shrink-0 flex-col gap-0 px-2 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
				{commandPaletteEnabled ? (
					<SidebarGroup className="p-0 pb-0.5 group-data-[collapsible=icon]:pb-1">
						<SidebarGroupContent>
							<SidebarMenu className="gap-0.5 group-data-[collapsible=icon]:gap-1">
								<SidebarSearchButton onOpen={openCommandPalette} />
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
				</SidebarMenu>

				{/* Pinned — collapsible; hidden when empty. */}
				{pinnedSessions.length > 0 && (
					<div className="sidebar-expanded-chrome flex shrink-0 flex-col group-data-[collapsible=icon]:hidden">
							<StoreSectionHeader
								store={disclosure}
								section="pinned"
								label={t("shell.pinned")}
								className="mb-1"
							/>
							<StoreSectionBody store={disclosure} section="pinned">
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
							</StoreSectionBody>
						</div>
					)}

				{/* Projects — collapsible; the "+" stays mounted through the empty
				    state so it keeps owning the ⌘N create flow. */}
				<div className="sidebar-expanded-chrome flex shrink-0 pb-0.5 group-data-[collapsible=icon]:hidden">
							<StoreSectionHeader
							store={disclosure}
							section="projects"
							label={t("shell.projects")}
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
			</div>

			<SidebarContent className="scrollbar-none gap-0 px-2 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
				<SidebarGroup className="min-h-0 flex-1 p-0">
					{/* Tree (project-sidebar__tree) */}
					<SidebarGroupContent
						className="sidebar-sections-container flex min-h-0 flex-1 flex-col"
					>
						{workspaceError ? (
							<div className="sidebar-expanded-chrome px-2.5 py-3 group-data-[collapsible=icon]:hidden">
								<p className="text-sm text-foreground">{t("shell.couldNotLoadProjects")}</p>
								<p className="mt-1 text-caption text-passive">{workspaceError}</p>
							</div>
						) : null}
						{hasProjectContent ? (
								<StoreSectionBody store={disclosure} section="projects" forceOpen={isCollapsed} className="min-h-0 flex-initial group-data-[collapsible=icon]:flex-none">
								<SidebarSectionScroller
									className={`${SECTION_SCROLLER_CLASS} min-h-0 flex-1`}
									testId="sidebar-projects-scroller"
									wrapperClassName="flex flex-col flex-1"
								>
									<SidebarMenu className="relative gap-0.5 rounded-lg group-data-[collapsible=icon]:gap-1 group-data-[collapsible=icon]:rounded-none">
											{!workspaceError && visibleWorkspaces.map((workspace) => (
												<ProjectItem
													key={workspace.id}
													workspace={workspace}
													disclosure={disclosure}
													phase={wavePhase(projectWave, workspace.id)}
													nav={selection.nav}
													isActiveProject={selection.activeProjectId === workspace.id}
													activeSessionId={activeSessionOwnerId === workspace.id ? selection.activeSessionId : undefined}
													isDragged={draggingProjectId === workspace.id}
													projectDragInProgress={draggingProjectId !== null}
													consumeDragClick={projectDragClickGuard.consumeClick}
													onRemoveProject={removeProject}
													onProjectDragStart={handleProjectDragStart}
													onProjectDragEnd={handleProjectDragEnd}
													onProjectDragOver={handleProjectDragOver}
													onProjectDrop={handleProjectDrop}
												/>
											))}
										<RemoteHostsSection
											hosts={remoteHosts}
											workspaces={remoteWorkspaces}
											failedHostIds={remoteFailedHostIds}
											renderProject={(host, workspace) => {
												const hostId = host.hostId;
												const projectKey = sessionUiKey(workspace.id, hostId);
												const hostActive = selection.activeRemoteHostId === hostId;
												const scopedNav: SelectionNav = {
													...selection.nav,
													goProject: (projectId) => { onOpenRemoteProject(hostId, projectId); return undefined; },
													goSession: (projectId, sessionId) => { void remoteNavigate(sessionNavigateTarget(projectId, sessionId, hostId)); },
													goSettings: (projectId) => onConfigureRemoteProject(hostId, projectId),
												};
												return <ProjectItem
													key={projectKey}
													workspace={workspace}
													hostLabel={host.label}
													disclosure={disclosure}
													remote
													nav={scopedNav}
													isActiveProject={hostActive && selection.activeRemoteProjectId === workspace.id}
													activeSessionId={hostActive && selection.activeRemoteProjectId === workspace.id ? selection.activeRemoteSessionId : undefined}
													isDragged={false}
													projectDragInProgress={false}
													consumeDragClick={() => false}
													onRemoveProject={(projectId) => onRemoveRemoteProject(hostId, projectId)}
													onOpenOrchestrator={() => onOpenRemoteOrchestrator(hostId, workspace.id)}
													onProjectDragStart={() => undefined}
													onProjectDragEnd={() => undefined}
													onProjectDragOver={() => undefined}
													onProjectDrop={() => undefined}
												/>;
											}}
											onRetry={onRetryRemoteHosts}
										/>
										{isCollapsed && <CreateProjectListItem />}
										<div
											aria-hidden="true"
											data-project-drop-line=""
											className="pointer-events-none absolute inset-x-0 z-[70] h-px rounded-full bg-foreground transition-opacity duration-100"
											style={{ top: dropLine.top, opacity: dropLine.visible ? 1 : 0 }}
										/>
									</SidebarMenu>
								</SidebarSectionScroller>
								{!isCollapsed && hiddenProjectCount > 0 ? (
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
								</StoreSectionBody>
							) : null}
						{!workspaceError && standaloneWorkspace ? (
							<ScratchpadSection
								workspace={standaloneWorkspace}
								nav={selection.nav}
								activeSessionId={activeSessionOwnerId === STANDALONE_WORKSPACE_ID ? selection.activeSessionId : undefined}
								onOpenArchive={selection.goStandaloneBoard}
								isCollapsed={isCollapsed}
								disclosure={disclosure}
							/>
						) : null}
					</SidebarGroupContent>
				</SidebarGroup>
			</SidebarContent>

			{/* Footer — Settings opens the global settings page directly.
			    Footer rows share NAV_ROW height so Settings, Connect mobile,
			    and account actions line up. Bottom spacing stays inside the
			    footer so there is no empty strip beneath the final action. */}
			<SidebarFooter
				className="relative mt-auto gap-0 overflow-hidden border-t border-border-strong px-2 !py-2 transition-[padding] duration-200 ease-linear group-data-[collapsible=icon]:min-h-20 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:border-t-0 group-data-[collapsible=icon]:overflow-visible group-data-[collapsible=icon]:px-1.5 group-data-[collapsible=icon]:!pb-2 group-data-[collapsible=icon]:!pt-1.5"
			>
				{/* Always-present daemon status mirror for the smoke suite: no visible
				    daemon-state copy is guaranteed to be mounted elsewhere. */}
				{daemonStatus && (
					<span aria-hidden="true" className="sr-only" data-testid="daemon-status" data-state={daemonStatus.state}>
						daemon {daemonStatus.state}
					</span>
				)}
				<div
					aria-hidden={isCollapsed || undefined}
					hidden={isCollapsed}
					className="sidebar-expanded-chrome relative flex w-full min-w-46.5 flex-col gap-0.5"
				>
					<UpdateStatusRow
						availableDismissed={updateDismissal.dismissed}
						onDismissAvailable={updateDismissal.dismiss}
						status={updateStatus}
						tabIndex={isCollapsed ? -1 : 0}
					/>
					<CloudSignInRow tabIndex={isCollapsed ? -1 : 0} />
					<CloudAccountRow tabIndex={isCollapsed ? -1 : 0} />
					<UpdateInstallSlide
						availableDismissed={updateDismissal.dismissed}
						onRequestInstall={openUpdateInstallPrompt}
						status={updateStatus}
						tabIndex={isCollapsed ? -1 : 0}
					/>
					<button
						aria-label={t("settings.connectMobile")}
						className={FOOTER_NAV_BUTTON_CLASS}
						onClick={() => selection.goConnectMobile()}
						tabIndex={isCollapsed ? -1 : 0}
						type="button"
					>
						<NavRowHighlight />
						<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0">
							<Smartphone aria-hidden="true" />
							<span className="tracking-tight">{t("settings.connectMobile")}</span>
						</span>
					</button>
					<button
						aria-label={t("shell.settings")}
						className={FOOTER_NAV_BUTTON_CLASS}
						onClick={() => selection.goGlobalSettings()}
						tabIndex={isCollapsed ? -1 : 0}
						type="button"
					>
						<NavRowHighlight />
						<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0">
							<Settings aria-hidden="true" />
							<span className="tracking-tight">{t("shell.settings")}</span>
						</span>
					</button>
				</div>
				<div
					aria-hidden={!isCollapsed || undefined}
					className="pointer-events-none absolute inset-x-1.5 bottom-0 top-auto flex min-h-row-md flex-col items-center justify-end gap-1 opacity-0 transition-opacity duration-150 ease-out group-data-[collapsible=icon]:pointer-events-auto group-data-[collapsible=icon]:!bottom-2 group-data-[collapsible=icon]:opacity-100"
				>
					<UpdateStatusRail
						availableDismissed={updateDismissal.dismissed}
						onRequestInstall={openUpdateInstallPrompt}
						status={updateStatus}
						tabIndex={isCollapsed ? 0 : -1}
					/>
					<CloudSignInRailButton tabIndex={isCollapsed ? 0 : -1} />
					<CloudAccountRailButton tabIndex={isCollapsed ? 0 : -1} />
					<Tooltip>
						<TooltipTrigger asChild>
							<button
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
							</button>
						</TooltipTrigger>
						<TooltipContent side="right">{t("settings.connectMobile")}</TooltipContent>
					</Tooltip>
					<Tooltip>
						<TooltipTrigger asChild>
							<button
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
							</button>
						</TooltipTrigger>
						<TooltipContent side="right">{t("shell.settings")}</TooltipContent>
					</Tooltip>
				</div>
			</SidebarFooter>

			{/* Grip follows the painted sidebar-container edge; useResizable owns clamp. */}
			<ResizeHandle
				className="group-data-[state=collapsed]:hidden"
				getBorderElement={getSidebarBorderElement}
				getObserveElements={getResizeTargets}
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
}

type Selection = ReturnType<typeof useSelection>;
type SelectionNav = Selection["nav"];

type ProjectItemProps = {
	workspace: WorkspaceSummary;
	hostLabel?: string;
	onOpenOrchestrator?: () => void;
	/** Expansion lives in this store; the row subscribes to its own flag. */
	disclosure: SidebarDisclosureStore;
	/** Remote projects keep their (default-expanded) flag apart from local ones. */
	remote?: boolean;
	/** Show more/less fade for a project row entering or leaving the capped list. */
	phase?: "enter" | "leave";
	nav: SelectionNav;
	/** This project is the route's active project. */
	isActiveProject: boolean;
	/** The active session id, only when it belongs to this project. */
	activeSessionId?: string;
	isDragged: boolean;
	projectDragInProgress: boolean;
	consumeDragClick: (id: string) => boolean;
	onRemoveProject: (projectId: string) => Promise<void>;
	onProjectDragStart: (event: ReactDragEvent<HTMLElement>, projectId: string) => void;
	onProjectDragEnd: () => void;
	onProjectDragOver: (event: ReactDragEvent<HTMLElement>, overId: string) => void;
	onProjectDrop: (event: ReactDragEvent<HTMLElement>) => void;
};

const ProjectItem = memo(function ProjectItem({
	workspace,
	hostLabel,
	onOpenOrchestrator,
	disclosure,
	remote = false,
	phase,
	nav,
	isActiveProject,
	activeSessionId,
	isDragged,
	projectDragInProgress,
	consumeDragClick,
	onRemoveProject,
	onProjectDragStart,
	onProjectDragEnd,
	onProjectDragOver,
	onProjectDrop,
}: ProjectItemProps) {
	const { t } = useTranslation();
	const nameWithHost = hostLabel ? `${workspace.name} · ${hostLabel}` : workspace.name;
	const isStandalone = workspace.id === STANDALONE_WORKSPACE_ID;
	const activeProjectMatches = isActiveProject;
	const dashboardActive = activeProjectMatches && !activeSessionId;
	const orchestratorActive =
		activeProjectMatches &&
		workspace.sessions.some(
			(session) => session.id === activeSessionId && session.kind === "orchestrator",
		);
	const projectActive = dashboardActive || orchestratorActive;
	const queryClient = useQueryClient();
	const [removeError, setRemoveError] = useState<string | null>(null);
	const [isRemoving, setIsRemoving] = useState(false);
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [isSpawning, setIsSpawning] = useState(false);
	const projectKey = sessionUiKey(workspace.id, workspace.hostId);
	const expanded = useProjectExpanded(disclosure, workspace.id, projectKey, remote);
	const isProjectProvisioning = useUiStore((state) => state.provisioningProjectIds.has(projectKey));
	const isProjectRestarting = useUiStore((state) => state.restartingProjectIds.has(projectKey));
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const projectIsDragging = isDragged;
	// The project's live orchestrator (if any) backs the hover Orchestrator
	// button: navigate to it when present, otherwise spawn one first.
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	const canResumeOrchestrator = useCanResumeAgent(orchestrator, workspace.hostId);
	const toggleDisclosure = () => {
		if (remote) disclosure.toggleRemote(projectKey);
		else disclosure.toggleProject(workspace.id);
	};

	// Mirrors ShellTopbar's launcher: attach to the running orchestrator, or
	// spawn one via the daemon and follow it once the workspace refetches.
	// Expand a collapsed project so opening the orchestrator also reveals its
	// session list — otherwise the tree stays shut while you're inside it.
	const openOrchestrator = async () => {
		if (isProjectProvisioning || isProjectRestarting) return;
		if (!expanded) toggleDisclosure();
		if (onOpenOrchestrator) {
			onOpenOrchestrator();
			return;
		}
		if (orchestrator) {
			// Mirrors useProjectOrchestratorAction; both launchers must stay in step.
			if (canResumeOrchestrator && workspace.kind !== "cloud") {
				setIsSpawning(true);
				try {
					await resumeOrchestrator(orchestrator.id);
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
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
			nav.goSession(workspace.id, orchestrator.id);
			return;
		}
		// A cloud project has no local orchestrator-agent config, so the settings
		// fallback below would dead-end it. Spawn the orchestrator as a cloud
		// session in its own sandbox instead.
		if (workspace.kind === "cloud") {
			setIsSpawning(true);
			try {
				const sessionId = await spawnCloudOrchestrator(queryClient, workspace.id);
				await queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
				nav.goSession(workspace.id, sessionId);
			} catch (err) {
				console.error("Failed to spawn cloud orchestrator:", err);
			} finally {
				setIsSpawning(false);
			}
			return;
		}
		if (!hasConfiguredOrchestratorAgent(workspace)) {
			nav.goSettings(workspace.id);
			return;
		}
		setIsSpawning(true);
		try {
			const sessionId = await spawnOrchestrator(workspace.id, "sidebar");
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			nav.goSession(workspace.id, sessionId);
		} catch (err) {
			console.error("Failed to spawn orchestrator:", err);
		} finally {
			setIsSpawning(false);
		}
	};

	// Expanded + already on the project board → collapse. Expanded + on a
	// session (orchestrator or worker) → board. Collapsed → expand + board.
	// Do not treat orchestratorActive like the board: the project row is the
	// one-click path back from the orchestrator button.
	const onProjectClick = () => {
		if (consumeDragClick(workspace.id)) return;
		if (isStandalone) {
			toggleDisclosure();
			return;
		}
		if (!expanded) {
			toggleDisclosure();
			nav.goProject(workspace.id);
		} else if (dashboardActive) {
			toggleDisclosure();
		} else {
			nav.goProject(workspace.id);
		}
	};

	// Folder icon always toggles disclosure, even when another project is
	// selected — without this, collapsing a non-active project required a
	// select click then a second click (felt like a double-click).
	const onFolderClick = (event: MouseEvent) => {
		event.stopPropagation();
		if (consumeDragClick(workspace.id)) return;
		toggleDisclosure();
	};


	const removeProject = () => {
		setRemoveError(null);
		setConfirmOpen(true);
	};
	const openPullRequestCount = new Set(
		workspace.sessions.flatMap((session) => openPRs(session).map((pr) => pr.url)),
	).size;

	const handleConfirmRemove = async () => {
		setConfirmOpen(false);
		setIsRemoving(true);
		// Teardown can take a while when a project owns several sessions. Leave
		// the confirmation immediately and move to the route that remains valid
		// after removal while the sidebar keeps progress/error feedback visible.
		nav.goHome();
		try {
			await onRemoveProject(workspace.id);
		} catch (err) {
			const message = err instanceof Error ? err.message : t("shell.couldNotRemoveProject");
			setRemoveError(message);
		} finally {
			setIsRemoving(false);
		}
	};

	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<li
					className={cn(
						"group/menu-item relative group-data-[collapsible=icon]:mb-0",
						phase === "enter" && "sidebar-row-enter",
						phase === "leave" && "sidebar-row-leave",
						projectIsDragging && "opacity-50",
					)}
					data-dragging={projectIsDragging ? "true" : undefined}
					data-project-drop-target=""
					data-project-id={workspace.id}
					data-remote-project-row={workspace.hostId ? "" : undefined}
					data-host-id={workspace.hostId}
					data-sidebar="menu-item"
					data-slot="sidebar-menu-item"
					onDragOver={(event) => onProjectDragOver(event, workspace.id)}
					onDrop={onProjectDrop}
				>
					<div
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
								{/* project-sidebar__proj-row */}
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
										// Same 32px row, gap, and icon size as Search/Automations and the
										// Projects header so icons and labels share one left edge.
										SIDEBAR_ROW_CLASS,
										!workspace.hostId && "cursor-grab active:cursor-grabbing",
										"pr-sidebar-project-actions",
										"transition-none",
										projectIsDragging && "!cursor-grabbing",
										projectDragInProgress && "hover:text-muted-foreground active:text-muted-foreground",
										"group-data-[collapsible=icon]:size-control-board! group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:rounded-lg group-data-[collapsible=icon]:p-0! group-data-[collapsible=icon]:font-semibold",
									)}
								>
									<NavRowHighlight active={projectActive} disabled={projectIsDragging} />
									{/* Expanded sidebar: visual folder/chevron icon (decorative — toggle button is a sibling).
		    size-icon-md matches the Projects section row; an 18px centered box was
		    optically indenting these icons relative to the header. */}
									<span
										aria-hidden="true"
										className="relative z-[1] inline-flex size-icon-md shrink-0 translate-y-px items-center justify-center group-data-[collapsible=icon]:hidden"
										data-expanded={expanded ? "" : undefined}
										data-project-folder-visual=""
									>
										{/* 1.2 — contextual icon swap: scale 0.8↔1 (animated); opacity snaps for hide.
										    Hover paint lives in styles.css (fine pointer only). */}
										<span
											className="inline-flex size-icon-md items-center justify-center transition-[scale] duration-normal ease-[var(--ease-out)] motion-reduce:transition-none"
											data-project-folder-icon=""
										>
											{expanded ? <FolderOpen strokeWidth={1.75} /> : <Folder strokeWidth={1.75} />}
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
											<ChevronRight strokeWidth={1.75} />
										</span>
									</span>
									{/* Collapsed icon rail: folder icon */}
									<span
										aria-hidden="true"
										className="relative z-[1] hidden size-8 items-center justify-center group-data-[collapsible=icon]:inline-flex"
									>
										{expanded ? <FolderOpen className="size-5" strokeWidth={1.75} /> : <Folder className="size-5" strokeWidth={1.75} />}
									</span>
									<span
										className="sidebar-expanded-chrome relative z-[1] min-w-0 flex-1 translate-y-px truncate group-data-[collapsible=icon]:hidden"
										data-project-label=""
									>
										{workspace.name}
									</span>
									{hostLabel && <Badge variant="outline" className="sidebar-expanded-chrome relative z-[1] h-4 shrink-0 px-1.5 text-2xs group-data-[collapsible=icon]:hidden">{hostLabel}</Badge>}
									{workspace.kind === "cloud" && (
										<Badge
											variant="outline"
											className="sidebar-expanded-chrome relative z-[1] h-4 shrink-0 px-1.5 text-2xs group-data-[collapsible=icon]:hidden"
										>
											{t("shell.cloudProjectBadge")}
										</Badge>
									)}
								</SidebarMenuButton>
								{/* Folder disclosure toggle: sibling of the nav button, absolutely positioned over
	    the icon area so it intercepts clicks there without nesting buttons. */}
								<button
									aria-label={t("shell.toggleProject", {
										name: nameWithHost,
									})}
									aria-expanded={expanded}
									className="absolute inset-y-0 left-0 z-10 w-9 cursor-pointer bg-transparent group-data-[collapsible=icon]:hidden"
									data-project-folder=""
									onClick={onFolderClick}
									type="button"
								/>
							</div>
							{/* Per-project actions: orchestrator and kebab menu. Outside the row's
		navigation surface so their own presses stay independent. */}
						{!isStandalone && <div
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
								<LazyTooltip
									content={
										isProjectProvisioning || isProjectRestarting
											? t("shell.restarting")
											: isSpawning
											? t("shell.spawning")
											: orchestrator
												? t("shell.orchestrator")
												: t("shell.spawnOrchestratorLower")
									}
								>
											<button
												aria-current={orchestratorActive ? "page" : undefined}
												aria-label={
													orchestrator
														? t("shell.openProjectOrchestrator", {
														name: nameWithHost,
															})
														: t("shell.spawnProjectOrchestrator", {
														name: nameWithHost,
															})
												}
													className={cn(HOVER_ACTION_CLASS, orchestratorActive && "text-foreground")}
													disabled={isSpawning || isProjectProvisioning || isProjectRestarting}
												onClick={() => void openOrchestrator()}
												type="button"
											>
												<OrchestratorIcon aria-hidden="true" strokeWidth={orchestratorActive ? 2.5 : 2} />
											</button>
								</LazyTooltip>
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<button
											aria-label={t("shell.projectActions", {
														name: nameWithHost,
											})}
											className={HOVER_ACTION_CLASS}
											type="button"
										>
											<MoreVertical aria-hidden="true" />
										</button>
									</DropdownMenuTrigger>
									<DropdownMenuContent side="right" align="start" className="min-w-44">
										<DropdownMenuItem disabled={isProjectRestarting} onSelect={() => requestNewTask(workspace.id, workspace.hostId)}>
											<Plus aria-hidden="true" />
											{t("shell.newTask")}
										</DropdownMenuItem>
										<DropdownMenuItem onSelect={() => nav.goSettings(workspace.id)}>
											<Settings aria-hidden="true" />
											{t("shell.projectSettings")}
										</DropdownMenuItem>
										<DropdownMenuItem
											className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
											disabled={isRemoving}
											onSelect={() => void removeProject()}
										>
											<Trash2 aria-hidden="true" />
											{t("shell.removeProjectTitle")}
										</DropdownMenuItem>
									</DropdownMenuContent>
								</DropdownMenu>
							</div>}
						</div>
						{/* end outer relative */}
					</div>
					{isRemoving ? (
						<div className="sidebar-expanded-chrome px-5 py-1 text-2xs text-muted-foreground" role="status">
							{t("shell.removingNamed", { name: workspace.name })}
						</div>
					) : removeError ? (
						<div className="sidebar-expanded-chrome px-5 py-1 text-2xs text-destructive" role="alert">
							{removeError}
						</div>
					) : null}
					{/* project-sidebar__sessions: indented under the project parent so worker
          sessions read as children without adding a persistent guide rail. */}
					<ProjectSessionList
						workspace={workspace}
						projectKey={projectKey}
						disclosure={disclosure}
						remote={remote}
						nav={nav}
						activeSessionId={activeSessionId}
						projectDragInProgress={projectDragInProgress}
					/>
					<ConfirmDialog
						open={confirmOpen}
						onOpenChange={setConfirmOpen}
						title={t("shell.removeProjectTitle")}
						description={
							<>
								<p className="text-sm font-medium text-foreground">{t("shell.removeProjectLead", { name: workspace.name })}</p>
								<p className="mt-1 text-xs text-muted-foreground">
									{workspace.kind === CLOUD_PROJECT_KIND
										? t("shell.removeCloudProjectBody")
										: t("shell.removeProjectBody")}
								</p>
								{openPullRequestCount > 0 ? (
									<p className="mt-2 text-xs font-medium text-error">
										{t("shell.removeProjectOpenPrWarning", { count: openPullRequestCount })}
									</p>
								) : null}
							</>
						}
						confirmLabel={t("shell.remove")}
						destructive
						onConfirm={handleConfirmRemove}
					/>
				</li>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-44">
				<ContextMenuItem disabled={isProjectRestarting} onSelect={() => requestNewTask(workspace.id, workspace.hostId)}>
					<Plus aria-hidden="true" />
					{t("shell.newTask")}
				</ContextMenuItem>
				{!isStandalone && <ContextMenuItem onSelect={() => nav.goSettings(workspace.id)}>
					<Settings aria-hidden="true" />
					{t("shell.projectSettings")}
				</ContextMenuItem>}
				{!isStandalone && <ContextMenuItem
					className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
					disabled={isRemoving}
					onSelect={() => void removeProject()}
				>
					<Trash2 aria-hidden="true" />
					{t("shell.removeProjectTitle")}
				</ContextMenuItem>}
			</ContextMenuContent>
		</ContextMenu>
	);
});

/** A project's session list. It owns the list's own state (order, Show more) and
 *  subscribes to the project's expansion flag itself, so opening or closing a
 *  project re-renders only the collapsible shell: the rows, built once per data
 *  change, stay mounted and untouched after the first open. */
const ProjectSessionList = memo(function ProjectSessionList({
	workspace,
	projectKey,
	disclosure,
	remote,
	nav,
	activeSessionId,
	projectDragInProgress,
}: {
	workspace: WorkspaceSummary;
	projectKey: string;
	disclosure: SidebarDisclosureStore;
	remote: boolean;
	nav: SelectionNav;
	activeSessionId?: string;
	projectDragInProgress: boolean;
}) {
	const { t } = useTranslation();
	const expanded = useProjectExpanded(disclosure, workspace.id, projectKey, remote);
	// Keep completed PR sessions reachable while their runtime still exists.
	// Only termination removes a worker from the sidebar; archived sessions stay
	// reachable through SessionsBoard.
	const visibleSessions = useMemo(
		() => sortedWorkerSessions(workspace.sessions).filter((session) => session.isTerminated !== true),
		[workspace.sessions],
	);
	const [sessionOrder, setSessionOrder] = useState<string[]>([]);
	const sessions = useMemo(
		() => applyOrder(visibleSessions, (session) => session.id, sessionOrder, "start"),
		[sessionOrder, visibleSessions],
	);
	const {
		listed: listedSessions,
		settled: settledSessions,
		wave,
		hiddenCount: hiddenSessionCount,
		showAll: showAllSessions,
		toggleShowAll: toggleShowAllSessions,
	} = useShowMoreCap(sessions, SIDEBAR_PROJECT_SESSION_LIMIT, activeSessionId);
	const listedSessionIds = useStableIds(useMemo(() => settledSessions.map((session) => session.id), [settledSessions]));
	const commitSessionOrder = useCallback(
		(next: string[] | null) => {
			if (!next) return;
			// Only the listed slice is draggable, so keep the still-hidden tail
			// behind it rather than letting applyOrder float it to the front.
			const listed = new Set(next);
			setSessionOrder([...next, ...sessions.filter((session) => !listed.has(session.id)).map((session) => session.id)]);
		},
		[sessions],
	);
	const openSession = useCallback((sessionId: string) => {
		recordManualWorkerOpen(sessionId);
		nav.goSession(workspace.id, sessionId);
	}, [nav, workspace.id]);
	// Latest-value ref keeps the callback stable across daemon ticks so rows
	// holding it are not re-rendered by it.
	const killContextRef = useRef({ activeSessionId, sessions, workspace });
	killContextRef.current = { activeSessionId, sessions, workspace };
	const handleSessionKilled = useCallback(
		(killedSession: WorkspaceSession) => {
			const { activeSessionId: activeId, sessions: current, workspace: ws } = killContextRef.current;
			if (activeId !== killedSession.id) return;
			const nextRoute = resolveNextNavigationAfterSessionKill(ws, killedSession.id, current);
			if (nextRoute.target === "session") {
				nav.goSession(ws.id, nextRoute.sessionId);
			} else if (ws.id === STANDALONE_WORKSPACE_ID) {
				nav.goHome();
			} else {
				nav.goProject(ws.id);
			}
		},
		[nav],
	);
	// Memoized so a pure expand/collapse (which changes only `expanded`) hands the
	// shell the same element and React skips the rows.
	const body = useMemo(
		() => (
			<>
				<SessionReorderList
					dndId={sessionDndId(projectKey)}
					testId={`session-list-${projectKey}`}
					className={cn(
						"mx-0 ml-3.5 translate-x-0 gap-px border-l-0 px-0 pt-1",
						hiddenSessionCount > 0 ? "pb-px" : "pb-1",
					)}
					sessions={listedSessions}
					sessionIds={listedSessionIds}
					wave={wave}
					activeSessionId={activeSessionId}
					plain={projectDragInProgress}
					onReorder={commitSessionOrder}
					onKilled={handleSessionKilled}
					onOpen={openSession}
				/>
				{hiddenSessionCount > 0 ? (
					// Indented to the session list so its label starts on the status-dot column.
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
			</>
		),
		[
			activeSessionId,
			commitSessionOrder,
			handleSessionKilled,
			hiddenSessionCount,
			listedSessionIds,
			listedSessions,
			openSession,
			projectDragInProgress,
			projectKey,
			showAllSessions,
			t,
			toggleShowAllSessions,
			wave,
		],
	);
	return (
		<CollapsibleBody open={expanded && sessions.length > 0} variant="project" className="sidebar-expanded-chrome">
			{body}
		</CollapsibleBody>
	);
});

/** Projectless ("ad hoc") agents. Their own section under Projects — same
 *  header chrome, own capped scroller, own Show more — instead of a project row
 *  appended to the project list. */
const ScratchpadSection = memo(function ScratchpadSection({
	workspace,
	nav,
	activeSessionId,
	onOpenArchive,
	isCollapsed,
	disclosure,
}: {
	workspace: WorkspaceSummary;
	nav: SelectionNav;
	/** The active session id, only when it belongs to the Scratchpad. */
	activeSessionId?: string;
	onOpenArchive: () => void;
	isCollapsed: boolean;
	disclosure: SidebarDisclosureStore;
}) {
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	// Mirrors the project tree: only termination removes an agent from the
	// sidebar, so a completed PR session stays reachable.
	const visibleSessions = useMemo(
		() => sortedWorkerSessions(workspace.sessions).filter((session) => session.isTerminated !== true),
		[workspace.sessions],
	);
	const [sessionOrder, setSessionOrder] = useState<string[]>([]);
	const sessions = useMemo(
		() => applyOrder(visibleSessions, (session) => session.id, sessionOrder, "start"),
		[sessionOrder, visibleSessions],
	);
	const {
		listed: listedSessions,
		settled: settledSessions,
		wave,
		hiddenCount: hiddenSessionCount,
		showAll,
		toggleShowAll,
	} = useShowMoreCap(sessions, SIDEBAR_INITIAL_SECTION_LIMIT, activeSessionId, isCollapsed);
	const listedSessionIds = useStableIds(useMemo(() => settledSessions.map((session) => session.id), [settledSessions]));
	const commitSessionOrder = useCallback(
		(next: string[] | null) => {
			if (!next) return;
			// Only the listed slice is draggable, so keep the still-hidden tail
			// behind it rather than letting applyOrder float it to the front.
			const listed = new Set(next);
			setSessionOrder([...next, ...sessions.filter((session) => !listed.has(session.id)).map((session) => session.id)]);
		},
		[sessions],
	);
	const openSession = useCallback(
		(sessionId: string) => {
			recordManualWorkerOpen(sessionId);
			nav.goSession(STANDALONE_WORKSPACE_ID, sessionId);
		},
		[nav],
	);
	const killContextRef = useRef({ activeSessionId, sessions, workspace });
	killContextRef.current = { activeSessionId, sessions, workspace };
	const handleSessionKilled = useCallback(
		(killedSession: WorkspaceSession) => {
			const { activeSessionId: activeId, sessions: current, workspace: ws } = killContextRef.current;
			if (activeId !== killedSession.id) return;
			const nextRoute = resolveNextNavigationAfterSessionKill(ws, killedSession.id, current);
			// An ad hoc agent has no project board to fall back to.
			if (nextRoute.target === "session") {
				nav.goSession(STANDALONE_WORKSPACE_ID, nextRoute.sessionId);
			} else {
				nav.goHome();
			}
		},
		[nav],
	);

	return (
		<div
			className="sidebar-expanded-chrome mb-2 flex min-h-0 shrink-0 flex-col overflow-hidden group-data-[collapsible=icon]:hidden"
			data-scratchpad-section=""
			style={scratchpadSectionStyle(isCollapsed)}
		>
			<StoreSectionHeader
				store={disclosure}
				section="scratchpad"
				label={workspace.name}
				className="group/scratchpad mt-1"
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
							<LazyTooltip content={t("shell.archivedSessions")}>
								<button
									aria-label={t("shell.archivedSessions")}
									className={ROW_ACTION_BUTTON_CLASS}
									onClick={(event) => {
										event.stopPropagation();
										onOpenArchive();
									}}
									type="button"
								>
									<Archive aria-hidden="true" />
								</button>
							</LazyTooltip>
						</span>
						<LazyTooltip content={t("shell.openNewAgent")}>
							<button
								aria-label={t("shell.openNewAgent")}
								className={ROW_ACTION_BUTTON_CLASS}
								onClick={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
								type="button"
							>
								<Plus aria-hidden="true" />
							</button>
						</LazyTooltip>
					</div>
				}
			/>
			<StoreSectionBody store={disclosure} section="scratchpad" hasContent={listedSessions.length > 0} className="min-h-0 flex-1">
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
						activeSessionId={activeSessionId}
						wave={wave}
						indented={false}
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
			</StoreSectionBody>
		</div>
	);
});

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

// A session row inside its project's drag context. The Pinned section renders
// plain SessionRows instead: that list is ordered by pin time, not by hand.
const SortableSessionRow = memo(function SortableSessionRow({
	session,
	active,
	consumeDragClick,
	indented = true,
	listIsDragging,
	phase,
	dropTransitionDisabled,
	onKilled,
	onOpen,
}: {
	session: WorkspaceSession;
	active: boolean;
	consumeDragClick: (id: string) => boolean;
	indented?: boolean;
	phase?: "enter" | "leave";
	listIsDragging: boolean;
	dropTransitionDisabled: boolean;
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: (sessionId: string) => void;
}) {
	const { isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition } = useSortable({
		id: session.id,
	});
	const reorder = useMemo(
		() => ({ isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition, dropTransitionDisabled }),
		[dropTransitionDisabled, isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition],
	);
	const handleOpen = useCallback(() => {
		if (!consumeDragClick(session.id)) onOpen(session.id);
	}, [consumeDragClick, onOpen, session.id]);
	return (
		<SessionRow
			session={session}
			active={active}
			indented={indented}
			onKilled={onKilled}
			onOpen={handleOpen}
			listIsDragging={listIsDragging}
			phase={phase}
			reorder={reorder}
		/>
	);
});

/** The reorderable session list shared by a project's tree and the Scratchpad
 *  section. It owns the drag context; the committed order stays with the caller
 *  so each list keeps its own persistence and slicing rules. */
function SessionReorderList({
	dndId,
	testId,
	className,
	sessions,
	sessionIds,
	wave = null,
	activeSessionId,
	indented = true,
	plain = false,
	onReorder,
	onKilled,
	onOpen,
}: {
	dndId: string;
	testId: string;
	className: string;
	sessions: WorkspaceSession[];
	/** Ids that take part in drag-and-drop (excludes rows fading out). */
	sessionIds: string[];
	/** Show more/less fade for the tail rows. */
	wave?: ShowMoreWave | null;
	activeSessionId?: string;
	indented?: boolean;
	/** While a project is being dragged, leave the session lists as plain rows:
	 *  otherwise every expanded project's DnD context measures its sortable
	 *  descendants on drop. */
	plain?: boolean;
	onReorder: (next: string[] | null) => void;
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: (sessionId: string) => void;
}) {
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
			if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
			return;
		}
		// reorderById rejects any id that is not in THIS list, so a stray
		// cross-list drop leaves both lists' orders untouched.
		const next = reorderById(sessionIds, sessionId, String(over.id));
		// Commit the destination DOM order before dnd-kit removes its live transform.
		// Otherwise the row briefly snaps back to its derived (usually top) position,
		// then Motion animates it forward to the persisted destination.
		flushSync(() => {
			onReorder(next);
			setListDragging(false);
			setDropTransitionDisabledId(sessionId);
		});
		requestAnimationFrame(() => setDropTransitionDisabledId(null));
		if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
	}, [dragClickGuard, onReorder, sessionIds]);

	const onDragStart = useCallback(() => setListDragging(true), []);

	const onDragCancel = useCallback(() => {
		setListDragging(false);
		setDropTransitionDisabledId(null);
		if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
	}, []);

	if (plain) {
		return (
			<SidebarMenuSub className={className} data-testid={testId}>
				{sessions.map((session) => (
					<SessionRow
						key={session.id}
						session={session}
						active={activeSessionId === session.id}
						indented={indented}
						phase={wavePhase(wave, session.id)}
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
			modifiers={SESSION_LIST_MODIFIERS}
			id={dndId}
			onDragStart={onDragStart}
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
							indented={indented}
							listIsDragging={listDragging}
							phase={wavePhase(wave, session.id)}
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

type SessionRowProps = {
	session: WorkspaceSession;
	active: boolean;
	hostLabel?: string;
	indented?: boolean;
	listIsDragging?: boolean;
	/** Show more/less fade (CSS only, for the 180ms after the toggle). */
	phase?: "enter" | "leave";
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: () => void;
	/** Present only for rows inside a reorderable project list. */
	reorder?: SessionReorder;
};

/** dnd-kit hands every sortable row a fresh `listeners` object (and re-derives
 *  `transition`) on each of its context updates, which defeats a plain memo and
 *  re-rendered every row for one session's tick. Listeners only close over the
 *  row id and the fixed sensor activators, and a transition only matters when
 *  the transform changes (which re-renders), so neither identity gates a render. */
function sessionRowPropsEqual(prev: SessionRowProps, next: SessionRowProps): boolean {
	for (const key of Object.keys(next) as (keyof SessionRowProps)[]) {
		if (key === "reorder") continue;
		if (prev[key] !== next[key]) return false;
	}
	const a = prev.reorder;
	const b = next.reorder;
	if (!a || !b) return a === b;
	return (
		a.isDragging === b.isDragging &&
		a.dropTransitionDisabled === b.dropTransitionDisabled &&
		a.setNodeRef === b.setNodeRef &&
		a.setActivatorNodeRef === b.setActivatorNodeRef &&
		a.transform?.x === b.transform?.x &&
		a.transform?.y === b.transform?.y &&
		a.transform?.scaleX === b.transform?.scaleX &&
		a.transform?.scaleY === b.transform?.scaleY
	);
}

// One worker-session row. Reads as a link by default; double-click/double-tap
// on the name or F2 flips the label into an inline input (Enter/blur saves,
// Escape cancels) that persists through the daemon rename endpoint.
const SessionRow = memo(function SessionRow({
	session,
	active,
	hostLabel,
	indented = true,
	listIsDragging = false,
	phase,
	onKilled,
	onOpen,
	reorder,
}: SessionRowProps) {
	const { t } = useTranslation();
	useGrabbingCursor(Boolean(reorder?.isDragging));
	const switchPresentation = deriveSessionAgentSwitchPresentation(session);
	const switchLabel = switchPresentation
		? t(switchPresentation.compactLabelKey, switchPresentation.values)
		: undefined;
	const switchStatusId = useId();
	const describedBy = switchLabel ? switchStatusId : undefined;
	const queryClient = useQueryClient();
	const refreshWorkspaces = useCallback(
		() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) }),
		[queryClient, session.hostId],
	);
	const rename = useSessionRename(session, refreshWorkspaces);
	const lastTouchAtRef = useRef(0);
	const suppressTouchOpenRef = useRef(false);
	const hoverTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const canPrefetch = session.mode === "chat" && !session.cloud && !session.hostId && !active && !listIsDragging && !reorder?.isDragging;
	useEffect(() => () => clearTimeout(hoverTimerRef.current), [canPrefetch]);
	const prefetchConversation = () => {
		if (!canPrefetch || !hasTrustedApiBaseUrl() || queryClient.getQueryData(conversationQueryKey(session.id))) return;
		void queryClient.prefetchInfiniteQuery(conversationQueryOptions(session.id));
	};
	const beginRename = useCallback(() => {
		rename.begin();
	}, [rename.begin]);

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
					<SessionStatusDot session={session} />
					<input
						aria-label={t("shell.renameSession", { title: session.title })}
						autoFocus
						className={cn(
							"relative z-[1] h-full min-w-0 flex-1 appearance-none border-0 bg-transparent! p-0 text-sm text-foreground outline-none ring-0 focus:outline-none focus:ring-0",
							session.lastUserMessageAt && "pr-[36px]",
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
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<SidebarMenuSubItem
					className={cn(
						indented && "pl-0.5",
						phase === "enter" && "sidebar-row-enter",
						phase === "leave" && "sidebar-row-leave",
						reorder?.isDragging && "z-chrome cursor-grabbing opacity-60",
					)}
					data-dragging={reorder?.isDragging ? "true" : undefined}
					ref={reorder?.setNodeRef}
					style={reorder ? sortableRowStyle(reorder) : undefined}
				>
			<div>
				<div
					className={cn(
						"group/session-row group/nav-row relative flex h-8 w-full items-center rounded-lg",
						"hover:text-foreground",
						active && "text-foreground",
					)}
					data-session-row=""
					data-dragging={reorder?.isDragging ? "true" : undefined}
				>
					<NavRowHighlight active={active} disabled={Boolean(reorder?.isDragging)} />
					<div className={cn("relative z-[1] flex min-w-0 flex-1", reorder?.isDragging && "cursor-grabbing")}>
						<button
							aria-current={active ? "page" : undefined}
							data-testid={session.hostId ? "remote-session-row" : undefined}
							aria-describedby={describedBy}
							aria-keyshortcuts="F2"
							aria-label={t("shell.openSession", { title: hostLabel ? `${session.title} · ${hostLabel}` : session.title })}
							className={cn(
								"flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-lg py-0 pl-1.5 text-left text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-sidebar-ring",
								session.lastUserMessageAt ? "pr-[36px]" : "pr-2.5",
								!reorder?.isDragging &&
									"group-hover/session-row:pr-sidebar-project-actions group-has-[:focus-visible]/session-row:pr-sidebar-project-actions",
								reorder && "cursor-grab active:cursor-grabbing",
								reorder?.isDragging && "!cursor-grabbing",
							)}
							{...(reorder?.listeners ?? {})}
							onMouseEnter={() => {
								if (canPrefetch) hoverTimerRef.current = setTimeout(prefetchConversation, 100);
							}}
							onMouseLeave={() => clearTimeout(hoverTimerRef.current)}
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
							<SessionStatusDot session={session} />
							<span className="flex min-w-0 flex-1 items-center gap-1.5">
								<span
									className={cn(
										"min-w-0 flex-1 truncate",
										active ? "text-foreground" : "text-muted-foreground group-hover/session-row:text-foreground",
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
								{hostLabel ? <Badge variant="outline" className="h-4 shrink-0 px-1.5 text-2xs">{hostLabel}</Badge> : null}
								{switchLabel ? (
									<span id={switchStatusId} className="max-w-28 shrink-0 truncate text-2xs text-muted-foreground">
										{switchLabel}
									</span>
								) : null}
							</span>
						</button>
					</div>
					{/* The timestamp is stable at the right edge. Pin and kill use label
					    space while idle, then reveal without changing the row footprint. */}
					<SessionActions
						isDragging={Boolean(reorder?.isDragging)}
						onKilled={onKilled}
						session={session}
					/>
				</div>
			</div>
				</SidebarMenuSubItem>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-44">
				<ContextMenuItem aria-label={t("shell.renameSession", { title: session.title })} onSelect={beginRename}>
					<Pencil aria-hidden="true" />
					{t("shell.rename")}
				</ContextMenuItem>
			</ContextMenuContent>
		</ContextMenu>
	);
}, sessionRowPropsEqual);

const SessionMessageAge = memo(function SessionMessageAge({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	if (!session.lastUserMessageAt) return null;

	return (
		<time
			className="absolute inset-y-0 right-2 z-[1] flex min-w-0 shrink-0 items-center whitespace-nowrap font-sans text-micro tabular-nums text-passive opacity-100 group-has-[:focus-visible]/session-row:opacity-0"
			data-session-message-age=""
			dateTime={session.lastUserMessageAt}
			title={t("shell.lastMessageAt", { time: formatTimeCompact(session.lastUserMessageAt) })}
		>
			{formatTimeTerse(session.lastUserMessageAt)}
		</time>
	);
});

const SessionActions = memo(function SessionActions({
	session,
	isDragging,
	onKilled,
}: {
	session: WorkspaceSession;
	isDragging: boolean;
	onKilled?: (session: WorkspaceSession) => void;
}) {
	const { t } = useTranslation();
	const { mutate: pinSession } = usePinSession();
	const { mutate: unpinSession } = useUnpinSession();
	const [confirmOpen, setConfirmOpen] = useState(false);
	// Optimistic: navigate + drop the row as soon as kill starts (onMutate),
	// not after the daemon round-trip.
	const onKilledRef = useRef(onKilled);
	onKilledRef.current = onKilled;
	const { mutate: terminateSession, isPending: isKilling } = useTerminateSession({
		onOptimistic: (killed) => {
			onKilledRef.current?.(killed);
		},
	});

	// The row used to archive on the bare click while the session page asked
	// first; both surfaces now open the same confirm before anything moves.
	const handleArchive = (event: React.MouseEvent) => {
		event.stopPropagation();
		setConfirmOpen(true);
	};

	const confirmArchive = () => {
		setConfirmOpen(false);
		terminateSession(session);
	};

	return (
		<div
			className="pointer-events-none absolute inset-y-0 right-0 z-chrome"
			data-session-actions=""
			onPointerDown={(event) => event.stopPropagation()}
		>
			<div
				className={cn(
					/* 1.3 — pin/kill: scale 0.8↔1 from center (not origin-right — that reads as a slide) */
					ROW_ACTIONS_CLASS,
					"origin-center scale-[0.8] opacity-0",
					"transition-[scale] duration-normal ease-[var(--ease-out)]",
					"motion-reduce:transition-none",
					!isDragging &&
						"group-has-[:focus-visible]/session-row:pointer-events-auto group-has-[:focus-visible]/session-row:scale-100 group-has-[:focus-visible]/session-row:opacity-100",
				)}
				data-session-action-buttons=""
			>
				<LazyTooltip content={session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")} side="top">
						<button
							aria-label={session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")}
							className={cn(
								SESSION_ACTION_CLASS,
								"focus-visible:text-foreground",
								session.isPinned && "text-foreground",
							)}
							onClick={(event) => {
								event.stopPropagation();
								session.isPinned ? unpinSession(session) : pinSession(session);
							}}
							type="button"
						>
							{session.isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
						</button>
				</LazyTooltip>
				<LazyTooltip content={t("shell.archiveSession")} side="top">
							<SessionArchiveDialog
								onConfirm={confirmArchive}
								onOpenChange={setConfirmOpen}
								open={confirmOpen}
								session={session}
								trigger={
									<button
										aria-label={t("shell.archiveSession")}
										className={cn(SESSION_ACTION_CLASS, "focus-visible:text-foreground")}
										disabled={isKilling}
										onClick={handleArchive}
										type="button"
									>
										<Archive aria-hidden="true" />
									</button>
								}
							/>
				</LazyTooltip>
			</div>
			<SessionMessageAge session={session} />
		</div>
	);
});

// CloudSignInRow: the entry point that starts the WorkOS sign-in flow. Shown
// only when the cloud offering is enabled (entitled client + flag + control
// plane), WorkOS is configured, and no one is signed in yet.
function CloudSignInRow({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { cloudEnabled } = useCloudGate();
	const { configured, status, signIn } = useCloudSession();
	// Dev + loopback CP: open the local email/password dialog instead of WorkOS.
	const { available: localAuthAvailable } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);
	const onSignIn = () => (localAuthAvailable ? openLocalSignIn() : signIn());
	if (!configured || !cloudEnabled || status !== "unauthenticated") return null;

	return (
		<button
			aria-label={t("shell.signInToAOCloud")}
			className={FOOTER_NAV_BUTTON_CLASS}
			onClick={onSignIn}
			tabIndex={tabIndex}
			type="button"
		>
			<NavRowHighlight />
			<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0">
				<LogIn aria-hidden="true" />
				<span className="tracking-tight">{t("shell.signInToAOCloud")}</span>
			</span>
		</button>
	);
}

// Icon-rail variant for the collapsed sidebar.
function CloudSignInRailButton({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { cloudEnabled } = useCloudGate();
	const { configured, status, signIn } = useCloudSession();
	// Dev + loopback CP: open the local email/password dialog instead of WorkOS.
	const { available: localAuthAvailable } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);
	const onSignIn = () => (localAuthAvailable ? openLocalSignIn() : signIn());
	if (!configured || !cloudEnabled || status !== "unauthenticated") return null;

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={t("shell.signInToAOCloud")}
					className={FOOTER_RAIL_BUTTON_CLASS}
					onClick={onSignIn}
					tabIndex={tabIndex}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] grid place-items-center [&_svg]:size-icon-base">
						<LogIn aria-hidden="true" />
					</span>
				</button>
			</TooltipTrigger>
			<TooltipContent side="right">{t("shell.signInToAOCloud")}</TooltipContent>
		</Tooltip>
	);
}

// CloudAccountRow: shown above the Settings button for an existing cloud
// session (the signed-in state). The sign-in entry point is CloudSignInRow.
function CloudAccountRow({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { cloudEnabled } = useCloudGate();
	const { configured, session, status, signOut } = useCloudSession();
	if (!configured || !cloudEnabled || status !== "authenticated") return null;

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button
					aria-label={t("shell.signedInAs", {
						email: session?.user.email ?? "AO Cloud",
					})}
					className={FOOTER_NAV_BUTTON_CLASS}
					tabIndex={tabIndex}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0">
						<User aria-hidden="true" />
						<span className="min-w-0 flex-1 truncate tracking-tight">
							{session?.user.email ?? "AO Cloud"}
						</span>
					</span>
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent side="top" align="start" className="min-w-44">
				<DropdownMenuItem
					className="text-destructive focus:text-destructive [&_svg]:text-destructive"
					onSelect={() => void signOut()}
				>
					<LogOut aria-hidden="true" />
					{t("shell.signOut")}
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

// Icon-rail variant for collapsed sidebar.
function CloudAccountRailButton({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { cloudEnabled } = useCloudGate();
	const { configured, session, status, signOut } = useCloudSession();
	if (!configured || !cloudEnabled || status !== "authenticated") return null;

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={t("shell.signedInAs", {
						email: session?.user.email ?? "AO Cloud",
					})}
					className={FOOTER_RAIL_BUTTON_CLASS}
					onClick={() => void signOut()}
					tabIndex={tabIndex}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] grid place-items-center [&_svg]:size-icon-base">
						<User aria-hidden="true" />
					</span>
				</button>
			</TooltipTrigger>
			<TooltipContent side="right">
				{t("shell.signOutWithEmail", {
					email: session?.user.email ?? "AO Cloud",
				})}
			</TooltipContent>
		</Tooltip>
	);
}

/**
 * What the sidebar should act on, derived from the live update status.
 *
 * `status.state` alone is not enough. It cycles through checking → available →
 * not-available on every background check while a staged build sits untouched,
 * which blinked the restart row out of existence every 15 minutes on nightly.
 * `status.staged` is stamped on every status by the main process for exactly
 * this reason, so the staged build is read from there rather than from `state`.
 */
type SidebarUpdateAction =
	| { kind: "downloading"; percent: number }
	| { kind: "download"; version?: string }
	| { kind: "install"; version?: string; escalated: boolean }
	| { kind: "retry" }
	| null;

function sidebarUpdateAction(status: UpdateStatus, availableDismissed: boolean): SidebarUpdateAction {
	if (status.state === "downloading") {
		return { kind: "downloading", percent: Math.min(100, Math.max(0, status.percent ?? 0)) };
	}
	// `staged` is the stamp the main process puts on every status; the
	// `downloaded` fallback keeps this correct for any status that predates it
	// or arrives from a source that does not stamp.
	const staged =
		status.staged ??
		(status.state === "downloaded"
			? {
					version: status.version,
					stagedAt: status.stagedAt ?? 0,
					escalated: status.escalated === true,
				}
			: undefined);
	// Something newer than what is already staged still deserves the download
	// action; the main process reports a re-discovered staged build as
	// "downloaded", so an "available" here is genuinely a different version.
	if (status.state === "available" && !availableDismissed && status.version !== staged?.version) {
		return { kind: "download", version: status.version };
	}
	if (staged) return { kind: "install", version: staged.version, escalated: staged.escalated };
	// Ranked below a staged build on purpose: an update ready to install is more
	// actionable than "checks are failing". Only when there is nothing better to
	// show does the failure take the row — it used to render nothing at all,
	// which reads as "up to date" rather than "checks are not getting through".
	if (status.checksFailing === true) return { kind: "retry" };
	return null;
}

/**
 * Sidebar label for a build. A raw nightly string truncates to noise and two
 * consecutive nightlies differ only in trailing digits, so nightlies render as
 * base version plus build date instead.
 */
function updateVersionLabel(
	version: string | undefined,
	variant: "available" | "ready",
	t: TFunction,
	locale: string,
): string | null {
	if (!version) return null;
	const nightly = parseNightlyVersion(version);
	if (nightly) {
		return t("shell.nightlyBuild", {
			version: nightly.base,
			date: new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(nightly.builtAt),
		});
	}
	return t(variant === "ready" ? "shell.versionReady" : "shell.versionAvailable", { version });
}

/** Plain version number for the install cue — base for nightlies, no channel/date. */
function installVersionNumber(version: string | undefined): string | null {
	if (!version) return null;
	return parseNightlyVersion(version)?.base ?? version;
}

// UpdateStatusRow makes download progress visible in the footer. A staged build
// ready to install renders as UpdateInstallSlide above Connect mobile / Settings.
function UpdateStatusRow({
	availableDismissed,
	onDismissAvailable,
	status,
	tabIndex,
}: {
	availableDismissed: boolean;
	onDismissAvailable: () => void;
	status: UpdateStatus;
	tabIndex: number;
}) {
	const { t, i18n } = useTranslation();
	const locale = i18n.resolvedLanguage ?? i18n.language;
	const action = sidebarUpdateAction(status, availableDismissed);
	if (action === null || action.kind === "install") return null;

	if (action.kind === "download") {
		const versionLabel = updateVersionLabel(action.version, "available", t, locale);
		// A manual check leaves autoDownload off, so without this the row would
		// announce an update and offer nothing to act on.
		return (
			<div className="flex w-full items-center gap-1" data-testid="sidebar-update-available">
				<button
					aria-label={
						action.version
							? t("shell.downloadUpdateVersion", { version: action.version })
							: t("shell.downloadUpdate")
					}
					className={cn(FOOTER_NAV_BUTTON_CLASS, "min-w-0 flex-1")}
					onClick={() => void aoBridge.updates.download()}
					tabIndex={tabIndex}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0">
						<Download aria-hidden="true" className="size-icon-lg shrink-0" />
						<span className="min-w-0 flex-1">
							<span className="block truncate tracking-tight">{t("shell.updateAvailable")}</span>
							{versionLabel && (
								<span className="block truncate text-caption font-normal text-passive">{versionLabel}</span>
							)}
						</span>
					</span>
				</button>
				{action.version && (
					<button
						aria-label={t("shell.dismissUpdateVersion", { version: action.version })}
						className="grid size-8 shrink-0 place-items-center text-muted-foreground hover:text-foreground"
						onClick={onDismissAvailable}
						tabIndex={tabIndex}
						type="button"
					>
						<X aria-hidden="true" className="size-icon-base" />
					</button>
				)}
			</div>
		);
	}

	if (action.kind === "downloading") {
		return (
			<div
				aria-live="polite"
				className={cn(NAV_ROW_CLASS, "flex w-full items-center text-left [&_svg]:size-icon-md [&_svg]:shrink-0")}
				data-testid="sidebar-update-downloading"
				role="status"
			>
				<Download aria-hidden="true" className="size-icon-lg shrink-0" />
				<span className="min-w-0 flex-1 truncate tabular-nums">
					{t("settings.updates.downloading", { percent: action.percent })}
				</span>
			</div>
		);
	}

	return (
		<button
			aria-label={t("shell.retryUpdateCheck")}
			className="flex w-full items-center gap-2.5 rounded-lg border border-warning/35 bg-warning/12 p-2.5 text-left text-control font-medium text-warning hover:bg-warning/18 [&_svg]:text-warning"
			data-testid="sidebar-update-failed"
			onClick={() => void aoBridge.updates.check()}
			tabIndex={tabIndex}
			type="button"
		>
			<AlertTriangle aria-hidden="true" className="size-icon-lg shrink-0" />
			<span className="min-w-0 flex-1">
				<span className="block truncate tracking-tight">{t("shell.updateCheckFailed")}</span>
				<span className="block truncate text-caption font-normal text-warning">
					{t("shell.retryUpdateCheck")}
				</span>
			</span>
		</button>
	);
}

/**
 * Alert-style install cue above Connect mobile / Settings. Muted fill so it
 * reads apart from nav rows; shows the version number only (no Nightly/date).
 */
function UpdateInstallSlide({
	availableDismissed,
	onRequestInstall,
	status,
	tabIndex,
}: {
	availableDismissed: boolean;
	onRequestInstall: () => void;
	status: UpdateStatus;
	tabIndex: number;
}) {
	const { t } = useTranslation();
	const action = sidebarUpdateAction(status, availableDismissed);
	if (action?.kind !== "install") return null;

	const versionNumber = installVersionNumber(action.version);
	return (
		<button
			aria-label={
				versionNumber
					? t("shell.restartInstallUpdateVersion", { version: versionNumber })
					: t("shell.restartInstallUpdate")
			}
			className={cn(
				"mb-1 flex h-9 w-full items-center gap-2.5 rounded-lg bg-muted px-3 text-left text-sm font-normal text-foreground",
				"hover:bg-interactive-hover",
			)}
			data-testid="sidebar-update-ready"
			onClick={onRequestInstall}
			tabIndex={tabIndex}
			type="button"
		>
			<RefreshCw aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />
			<span className="min-w-0 flex-1 truncate tracking-tight">
				{t("shell.restartToUpdate")}
				{versionNumber ? (
					<>
						{" "}
						<span className="text-muted-foreground">{versionNumber}</span>
					</>
				) : null}
			</span>
		</button>
	);
}

// Icon-rail variant of UpdateStatusRow. An available build downloads on click
// and a staged one installs; an in-flight download is informational.
function UpdateStatusRail({
	availableDismissed,
	onRequestInstall,
	status,
	tabIndex,
}: {
	availableDismissed: boolean;
	/** Opens the restart confirmation; installing outright would quit the app. */
	onRequestInstall: () => void;
	status: UpdateStatus;
	tabIndex: number;
}) {
	const { t } = useTranslation();
	const action = sidebarUpdateAction(status, availableDismissed);
	if (action === null) return null;

	if (action.kind === "download") {
		const label = t("settings.updates.available", { version: action.version ? ` (v${action.version})` : "" });
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label={
							action.version
								? t("shell.downloadUpdateVersion", { version: action.version })
								: t("shell.downloadUpdate")
						}
						className={cn(FOOTER_RAIL_BUTTON_CLASS, "size-9 text-passive [&_svg]:size-4")}
						onClick={() => void aoBridge.updates.download()}
						tabIndex={tabIndex}
						type="button"
					>
						<NavRowHighlight />
						<span className="relative z-[1] grid place-items-center [&_svg]:size-4">
							<Download aria-hidden="true" />
						</span>
					</button>
				</TooltipTrigger>
				<TooltipContent side="right">{label}</TooltipContent>
			</Tooltip>
		);
	}

	if (action.kind === "downloading") {
		const label = t("settings.updates.downloading", { percent: action.percent });
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<span
						aria-label={label}
						aria-live="polite"
						className="grid size-9 place-items-center rounded-lg text-passive [&_svg]:size-4"
						role="status"
					>
						<Download aria-hidden="true" />
					</span>
				</TooltipTrigger>
				<TooltipContent side="right">{label}</TooltipContent>
			</Tooltip>
		);
	}

	if (action.kind === "retry") {
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label={t("shell.retryUpdateCheck")}
						className="grid size-9 place-items-center rounded-lg bg-warning/12 text-warning hover:bg-warning/18 [&_svg]:size-4"
						onClick={() => void aoBridge.updates.check()}
						tabIndex={tabIndex}
						type="button"
					>
						<AlertTriangle aria-hidden="true" />
					</button>
				</TooltipTrigger>
				<TooltipContent side="right">
					{t("shell.updateCheckFailed")} · {t("shell.retryUpdateCheck")}
				</TooltipContent>
			</Tooltip>
		);
	}

	const versionNumber = installVersionNumber(action.version);
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={
						versionNumber
							? t("shell.restartInstallUpdateVersion", { version: versionNumber })
							: t("shell.restartInstallUpdate")
					}
					className="grid size-9 place-items-center rounded-lg bg-muted text-muted-foreground hover:bg-interactive-hover hover:text-foreground [&_svg]:size-4"
					onClick={onRequestInstall}
					tabIndex={tabIndex}
					type="button"
				>
					<RefreshCw aria-hidden="true" />
				</button>
			</TooltipTrigger>
			<TooltipContent side="right">
				{t("shell.restartToUpdate")}
				{versionNumber ? ` ${versionNumber}` : ""}
			</TooltipContent>
		</Tooltip>
	);
}

/** Releases a section's initial cap. Sits below its section's scroller so the
 *  cap can never hide the control that lifts it. */
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
	return (
		<button
			aria-label={label}
			className={cn(
				SECTION_ROW_CLASS,
				NAV_ROW_HIGHLIGHT_HOST_CLASS,
				"mb-1 shrink-0 rounded-lg text-left text-muted-foreground",
				className,
			)}
			onClick={onClick}
			type="button"
		>
			<NavRowHighlight />
			<span key={expanded ? "less" : "more"} className="sidebar-label-swap relative z-[1] truncate">
				{t(expanded ? "shell.showLess" : "shell.showMore")}
			</span>
		</button>
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
	/** Optional trailing control (e.g. Projects "+") — its own button, not the row. */
	trailing?: ReactNode;
	/** When false, render a static label row with no chevron, toggle, or hover fill. */
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
					strokeWidth={2}
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
				<button
					aria-expanded={open}
					aria-label={label}
					className="relative z-[1] flex min-w-0 flex-1 self-stretch items-center gap-2 text-left"
					onClick={onToggle}
					type="button"
				>
					{labelRow}
				</button>
				<span className="relative z-[1] shrink-0">{trailing}</span>
			</div>
		);
	}

	return (
		<button
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
		</button>
	);
}

/** Search + Automations share this one row so their hover/focus/active treatment
 * (NavRowHighlight pill, foreground text, instant) cannot drift apart. */
function SidebarTopNavRow({
	active = false,
	icon,
	label,
	onClick,
	tooltip,
	trailing,
}: {
	active?: boolean;
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
				icon={<Search strokeWidth={1.75} aria-hidden="true" />}
				label={t("shell.search")}
				onClick={() => {
					// Open on the microtask after this click rather than inside it: mounting
					// the palette dialog while this button's tooltip layer is still tearing
					// down from the same pointer sequence dismissed it immediately. The
					// "defers opening" test pins the deferral so it is not dropped as noise.
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
	// Single CreateProjectFlow owner for the sidebar: the header "+" stays mounted
	// (CSS-hidden when collapsed) so it can own
	// openSignal for ⌘N on every shell route. The collapsed rail button below
	// reuses this flow via requestCreateProject().
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
							<button
								aria-label={t("shell.newProject")}
								className={ROW_ACTION_BUTTON_CLASS}
								disabled={disabled}
								onClick={choosePath}
								type="button"
							>
									<Plus aria-hidden="true" />
							</button>
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
					<button
						aria-label={t("shell.newProject")}
						className="grid h-control-board w-full place-items-center rounded-lg text-passive transition-colors hover:bg-interactive-hover hover:text-muted-foreground"
						onClick={() => requestCreateProject()}
						type="button"
					>
						<Plus className="size-icon-sm" aria-hidden="true" />
					</button>
				</TooltipTrigger>
				<TooltipContent side="right">{t("shell.newProject")}</TooltipContent>
			</Tooltip>
		</SidebarMenuItem>
	);
}
