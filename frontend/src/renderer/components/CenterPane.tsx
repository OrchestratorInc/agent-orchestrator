import { isSettingsPageOpen, onDedicatedShortcut } from "../hooks/useAppShortcut";
import { motion, Reorder, useDragControls, useMotionValue } from "motion/react";
import {
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type PointerEvent,
	type ReactNode,
	type WheelEvent as ReactWheelEvent,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useTabListTabStop } from "../hooks/useTabListTabStop";
import { useTabScrollEdges } from "../hooks/useTabScrollEdges";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useHostConnection } from "../hooks/useHostConnection";
import { useSidebarChromeClearanceRef } from "../hooks/useSidebarChromeGeometry";
import { MAX_SESSION_DISPLAY_NAME_LEN, useSessionRename } from "../hooks/useSessionRename";
import { useTruncatedText } from "../hooks/useTruncatedText";
import type { ShellTerminal } from "../hooks/useShellTerminals";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../lib/terminal-font-size";
import { createTerminalMux, muxUrlFromApiBase } from "../lib/terminal-mux";
import { getAgentActivityView } from "../lib/session-presentation";
import { agentLabel } from "../lib/agent-options";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { aoBridge } from "../lib/bridge";
import { handleTerminalTabListKeyDown } from "../lib/terminal-tabs";
import { cn } from "../lib/utils";
import { sidebarOccupiesLayout, useUiStore, type Theme } from "../stores/ui-store";
import type { TerminalTarget } from "../types/terminal";
import { isOrchestratorSession, sessionIsActive, type WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { ShellTerminalTab } from "./ShellTerminalTab";
import { TerminalTabFrame, terminalTabRenameInputClassName } from "./TerminalTabFrame";
import { TerminalPane } from "./TerminalPane";
import { SessionTopbarPortal } from "./SessionTopbarPortal";
import { SessionTabsTopbar } from "./PageTopbar";
import { beginPaneTabDrag } from "../lib/pane-drag";
import { SESSION_TAB_KEY } from "../lib/pane-split";
import { useSplitCloseFocused, useSplitHiddenTabKeys } from "./split-pane-context";
import { TabContextMenu, TabMenuSpecContext, type TabMenuSpec } from "./TabContextMenu";

type CenterPaneProps = {
	session?: WorkspaceSession;
	hostId?: string;
	terminalGeneration?: string;
	theme: Theme;
	daemonReady: boolean;
	terminalTarget?: TerminalTarget;
	reviewerTerminal?: { handleId: string; harness: string };
	onSelectReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	reviewerChat?: { reviewId: string; harness: string };
	reviewerChatSelected?: boolean;
	reviewerChatContent?: ReactNode;
	onSelectReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	/** Standalone shells to render as tabs beside the session's own pane. */
	shellTerminals?: ShellTerminal[];
	onSelectSessionTerminal?: () => void;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	/** Workspace-level controls (e.g. shell topbar) rendered beside the tab strip. */
	topbarActions?: ReactNode;
	/** Agent-session actions (interface switch, handoff) on the primary session tab. */
	sessionTabAction?: ReactNode;
	/** Keep the primary tab's action showing while an interface switch runs. */
	sessionTabActionPinned?: boolean;
	/** Pinned beside the tab strip, before the workspace topbar actions. */
	tabStripAction?: ReactNode;
	workspaceTabs?: CenterPaneWorkspaceTab[];
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	/** Session-owned order shared with the Chat surface. */
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	/** Stop forwarding the agent pane's keystrokes while its controller drains. */
	agentInputDisabled?: boolean;
	/** The agent terminal is coming up after an interface switch. */
	terminalBooting?: boolean;
	/** An account move is restarting the worker agent; see TerminalPane. */
	terminalSwitchingLabel?: string;
	/** Reports attachment of the session's own agent terminal. */
	onSessionTerminalAttached?: (attached: boolean) => void;
};

export type CenterPaneWorkspaceTab = {
	key: string;
	content: ReactNode;
	onSelect: () => void;
	onClose?: () => void;
	/** The tab's menu; absent, the tab draws its own (a split group's tab). */
	menu?: TabMenuSpec;
};

type AuxiliaryTab =
	| { key: string; kind: "reviewer"; terminal: NonNullable<CenterPaneProps["reviewerTerminal"]> }
	| { key: string; kind: "reviewer-chat"; terminal: NonNullable<CenterPaneProps["reviewerChat"]> }
	| { key: string; kind: "shell"; terminal: ShellTerminal }
	| { key: string; kind: "workspace"; tab: CenterPaneWorkspaceTab };

const WHEEL_ZOOM_THRESHOLD = 80;
const WHEEL_ZOOM_RESET_MS = 250;
const isMac = isMacPlatform();
const isLinux = isLinuxPlatform();

const PANE_DRAG_IGNORE = "[data-terminal-tab-action],input,a";

// The session's own tab does not reorder, but it can still be dragged out of
// the strip into a split.
export function startSessionTabPaneDrag(event: PointerEvent<HTMLElement>) {
	if ((event.target as HTMLElement).closest(PANE_DRAG_IGNORE)) return;
	beginPaneTabDrag(event, SESSION_TAB_KEY, event.currentTarget);
}

// The strip's slot for the session's own tab. It drags out like any tab, and
// it is left out while the tab is shown in a split group's tab.
export function SessionTabSlot({ children }: { children: ReactNode }) {
	if (useSplitHiddenTabKeys().has(SESSION_TAB_KEY)) return null;
	return (
		<div className="flex shrink-0 self-stretch" data-terminal-tab-key={SESSION_TAB_KEY} onPointerDown={startSessionTabPaneDrag}>
			{children}
		</div>
	);
}

// A reorderable strip tab. It stays inside its group, so it never covers the
// pinned session tab or the toolbar, and keeps its hover look while dragged.
// Pulled out of the strip vertically, it hands over to the split-pane drag.
export function DraggableWorkspaceTab({ children, value }: { children: ReactNode; value: string }) {
	const dragControls = useDragControls();
	const itemRef = useRef<HTMLDivElement>(null);
	const x = useMotionValue(0);
	const [dragging, setDragging] = useState(false);
	const dragRef = useRef<{ pointerX: number; grabOffset: number; scroller: HTMLElement | null } | null>(null);
	const startDrag = (event: PointerEvent<HTMLDivElement>) => {
		// A middle-click closes the tab and a right-click opens its menu; neither drags.
		if (event.button !== 0 || (event.target as HTMLElement).closest(PANE_DRAG_IGNORE)) return;
		dragControls.start(event);
		const item = itemRef.current;
		if (item) beginPaneTabDrag(event, value, item, settleInSlot);
	};
	// While dragging, the tab's x is derived from the pointer and the group's
	// current box, not from framer's pointer delta: Reorder's edge auto-scroll
	// and each reorder move the layout, and framer only catches x up a frame
	// later. Recomputing on every write, scroll and commit keeps the tab under
	// the pointer and inside the group, so it never covers the session tab.
	const followPointer = useCallback(() => {
		const drag = dragRef.current;
		const item = itemRef.current;
		const group = item?.parentElement;
		if (!drag || !item || !group) return;
		const offset = item.offsetLeft - group.offsetLeft;
		const layoutLeft = group.getBoundingClientRect().left + offset;
		const max = group.offsetWidth - item.offsetWidth - offset;
		const next = Math.min(max, Math.max(-offset, drag.pointerX - drag.grabOffset - layoutLeft));
		if (Math.abs(x.get() - next) > 0.01) x.set(next);
	}, [x]);
	useEffect(() => x.on("change", followPointer), [x, followPointer]);
	// Lifting the tab out into a split hands its motion to the drag ghost. The
	// tab goes straight back to its slot: letting the reorder drag animate home
	// while followPointer still tracked the pointer could strand it a few
	// pixels off, leaving a gap in the strip.
	const settleInSlot = useCallback(() => {
		dragRef.current?.scroller?.removeEventListener("scroll", followPointer);
		dragRef.current = null;
		dragControls.cancel();
		x.stop();
		x.set(0);
		setDragging(false);
	}, [dragControls, followPointer, x]);
	// A tab removed mid-drag gets no onDragEnd; drop its scroll listener anyway.
	useEffect(() => () => dragRef.current?.scroller?.removeEventListener("scroll", followPointer), [followPointer]);
	useLayoutEffect(followPointer);

	return (
		<Reorder.Item
			as="div"
			// The opaque background keeps a dragged tab from showing the tabs it passes over.
			className="flex shrink-0 self-stretch touch-pan-y bg-background"
			data-dragging={dragging || undefined}
			data-terminal-tab-key={value}
			drag="x"
			dragControls={dragControls}
			dragListener={false}
			onDrag={(event) => {
				if (!dragRef.current) return;
				dragRef.current.pointerX = (event as globalThis.PointerEvent).clientX;
				followPointer();
			}}
			onDragEnd={() => {
				dragRef.current?.scroller?.removeEventListener("scroll", followPointer);
				dragRef.current = null;
				setDragging(false);
			}}
			onDragStart={(event) => {
				const item = itemRef.current;
				if (!item) return;
				const pointerX = (event as globalThis.PointerEvent).clientX;
				const scroller = item.closest<HTMLElement>("[data-tab-scroll-region]");
				dragRef.current = { pointerX, grabOffset: pointerX - item.getBoundingClientRect().left, scroller };
				scroller?.addEventListener("scroll", followPointer);
				setDragging(true);
			}}
			onPointerDown={startDrag}
			ref={itemRef}
			style={{ x }}
			// Reorder needs layout measurement, but tabs should snap into place
			// when a sibling opens or closes rather than slide.
			transition={{ layout: { duration: 0 } }}
			value={value}
		>
			{children}
		</Reorder.Item>
	);
}

export function CenterPane({
	session,
	hostId,
	terminalGeneration,
	theme,
	daemonReady,
	terminalTarget,
	reviewerTerminal,
	reviewerChat,
	reviewerChatSelected = false,
	reviewerChatContent,
	onSelectReviewerTerminal,
	onSelectReviewerChat,
	shellTerminals = [],
	onSelectSessionTerminal,
	onSelectShellTerminal,
	onCloseShellTerminal,
	onRenameShellTerminal,
	topbarActions,
	sessionTabAction,
	sessionTabActionPinned = false,
	tabStripAction,
	workspaceTabs,
	workspaceTabActions,
	workspaceActiveTabKey,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	agentInputDisabled = false,
	terminalBooting = false,
	terminalSwitchingLabel,
	onSessionTerminalAttached,
}: CenterPaneProps) {
	const { t } = useTranslation();
	const paneRef = useRef<HTMLDivElement | null>(null);
	const tabListRef = useTabListTabStop<HTMLDivElement>();
	const wheelZoomRemainderRef = useRef(0);
	const lastWheelZoomAtRef = useRef(0);
	const [fontSize, setFontSize] = useState(initialTerminalFontSize);
	const [isFullscreen, setIsFullscreen] = useState(false);
	const [terminalBounds, setTerminalBounds] = useState({ leftInset: 0, rightInset: 0, width: 0 });
	const [tabOrderBySession, setTabOrderBySession] = useState<Record<string, string[]>>({});
	const queryClient = useQueryClient();
	const refreshWorkspaces = useCallback(
		() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
		[hostId, queryClient],
	);
	const { baseUrl: remoteBase } = useHostConnection(hostId);
	const remoteCreateMux = useMemo(
		() => remoteBase ? () => createTerminalMux(muxUrlFromApiBase(remoteBase)) : undefined,
		[remoteBase],
	);
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const sessionId = session?.id;
	const splitHiddenTabKeys = useSplitHiddenTabKeys();
	const closeFocusedSplitPane = useSplitCloseFocused();
	const auxiliaryTabs = useMemo<AuxiliaryTab[]>(
		() => [
			...(reviewerTerminal
				? [
						{
							key: `reviewer:${reviewerTerminal.handleId}`,
							kind: "reviewer" as const,
							terminal: reviewerTerminal,
						},
					]
				: []),
			...(!reviewerTerminal && reviewerChat
				? [{ key: `reviewer-chat:${reviewerChat.reviewId}`, kind: "reviewer-chat" as const, terminal: reviewerChat }]
				: []),
			...shellTerminals.map((terminal) => ({ key: terminal.handleId, kind: "shell" as const, terminal })),
			...(workspaceTabs ?? []).map((tab) => ({ key: tab.key, kind: "workspace" as const, tab })),
		].filter((tab) => !splitHiddenTabKeys.has(tab.key)),
		[reviewerChat, reviewerTerminal, shellTerminals, splitHiddenTabKeys, workspaceTabs],
	);
	const availableAuxiliaryKeys = useMemo(() => auxiliaryTabs.map((tab) => tab.key), [auxiliaryTabs]);
	const stripShellHandleIds = useMemo(() => shellTerminals.map((terminal) => terminal.handleId), [shellTerminals]);
	const orderedAuxiliaryTabs = useMemo(() => {
		const preferred = auxiliaryTabOrder ?? (sessionId ? (tabOrderBySession[sessionId] ?? []) : []);
		const byKey = new Map(auxiliaryTabs.map((tab) => [tab.key, tab]));
		const ordered = preferred.flatMap((key) => {
			const tab = byKey.get(key);
			if (!tab) return [];
			byKey.delete(key);
			return [tab];
		});
		return [...ordered, ...byKey.values()];
	}, [auxiliaryTabOrder, auxiliaryTabs, sessionId, tabOrderBySession]);
	const tabOverflowWatch = `${sessionId ?? ""}|${availableAuxiliaryKeys.join("|")}`;
	const {
		scrollRef: tabsOverflowRef,
		scrollToEnd: scrollTabsToEnd,
	} = useTabScrollEdges([isFullscreen, tabOverflowWatch]);
	const previousTabCountRef = useRef(availableAuxiliaryKeys.length);
	const target = terminalTarget ?? { kind: "worker" };
	const activeWorkspaceTab = workspaceActiveTabKey
		? workspaceTabs?.find((tab) => tab.key === workspaceActiveTabKey)
		: undefined;
	const workerInputDisabled = target.kind === "worker" && agentInputDisabled;
	const sessionTabLabel = session
		? isOrchestratorSession(session)
			? t("shell.orchestrator")
			: session.title
		: t("terminal.noSession");
	const activeTerminalLabel =
		reviewerChatSelected && reviewerChat
			? `${t("terminal.reviewer")} · ${reviewerChat.harness}`
			: target.kind === "shell"
			? (shellTerminals.find((shell) => shell.handleId === target.handleId)?.title ?? target.title)
			: target.kind === "reviewer"
				? `${t("terminal.reviewer")} · ${target.harness}`
				: (session?.title ?? sessionTabLabel);
	const reorderAuxiliaryTabs = useCallback(
		(nextKeys: string[]) => {
			if (!sessionId) return;
			const available = new Set(availableAuxiliaryKeys);
			const next = nextKeys.filter((key, index) => available.has(key) && nextKeys.indexOf(key) === index);
			for (const key of availableAuxiliaryKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (onAuxiliaryTabOrderChange) onAuxiliaryTabOrderChange(next);
			else setTabOrderBySession((current) => ({ ...current, [sessionId]: next }));
		},
		[availableAuxiliaryKeys, onAuxiliaryTabOrderChange, sessionId],
	);
	const selectAdjacentTab = useCallback(
		(direction: -1 | 1) => {
			const activeKey =
				reviewerChatSelected && reviewerChat
					? `reviewer-chat:${reviewerChat.reviewId}`
					: workspaceActiveTabKey ?? (target.kind === "shell"
						? target.handleId
						: target.kind === "reviewer"
							? `reviewer:${target.handleId}`
							: "worker");
			const tabKeys = ["worker", ...orderedAuxiliaryTabs.map((tab) => tab.key)];
			const activeIndex = Math.max(0, tabKeys.indexOf(activeKey));
			const nextIndex = (activeIndex + direction + tabKeys.length) % tabKeys.length;
			if (nextIndex === 0) {
				onSelectSessionTerminal?.();
				return;
			}
			const nextTab = orderedAuxiliaryTabs[nextIndex - 1];
			if (nextTab?.kind === "reviewer") onSelectReviewerTerminal?.(nextTab.terminal);
			if (nextTab?.kind === "reviewer-chat") onSelectReviewerChat?.(nextTab.terminal);
			if (nextTab?.kind === "shell") onSelectShellTerminal?.(nextTab.terminal.handleId);
			if (nextTab?.kind === "workspace") nextTab.tab.onSelect();
		},
		[
			onSelectReviewerChat,
			onSelectReviewerTerminal,
			onSelectSessionTerminal,
			onSelectShellTerminal,
			orderedAuxiliaryTabs,
			reviewerChat,
			reviewerChatSelected,
			target,
			workspaceActiveTabKey,
		],
	);

	useEffect(() => {
		if (!sessionId) return;
		if (auxiliaryTabOrder) {
			const available = new Set(availableAuxiliaryKeys);
			const keys = auxiliaryTabOrder.filter((key) => available.has(key));
			for (const key of availableAuxiliaryKeys) {
				if (!keys.includes(key)) keys.push(key);
			}
			if (!keys.every((key, index) => key === auxiliaryTabOrder[index]) || keys.length !== auxiliaryTabOrder.length) {
				onAuxiliaryTabOrderChange?.(keys);
			}
			return;
		}
		setTabOrderBySession((current) => {
			const currentOrder = current[sessionId] ?? [];
			const available = new Set(availableAuxiliaryKeys);
			const keys = currentOrder.filter((key) => available.has(key));
			for (const key of availableAuxiliaryKeys) {
				if (!keys.includes(key)) keys.push(key);
			}
			if (keys.length === currentOrder.length && keys.every((key, index) => key === currentOrder[index])) return current;
			if (keys.length === 0) {
				const { [sessionId]: _removed, ...rest } = current;
				return rest;
			}
			return { ...current, [sessionId]: keys };
		});
	}, [auxiliaryTabOrder, availableAuxiliaryKeys, onAuxiliaryTabOrderChange, sessionId]);

	useEffect(() => {
		const handleFullscreenChange = () => setIsFullscreen(document.fullscreenElement === paneRef.current);
		document.addEventListener("fullscreenchange", handleFullscreenChange);
		return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
	}, []);

	useEffect(
		() =>
			onDedicatedShortcut("close-shell-terminal", (handler) => aoBridge.app.onCloseShellTerminalShortcut(handler), () => {
				if (closeFocusedSplitPane) closeFocusedSplitPane();
				else if (activeWorkspaceTab?.onClose) activeWorkspaceTab.onClose();
				else if (target.kind === "shell") onCloseShellTerminal?.(target.handleId);
			}),
		[activeWorkspaceTab, closeFocusedSplitPane, target, onCloseShellTerminal],
	);

	useEffect(() => {
		const disposePrevious = onDedicatedShortcut("previous-tab", (handler) => aoBridge.app.onPreviousTabShortcut(handler), () => {
			if (!isSettingsPageOpen()) selectAdjacentTab(-1);
		});
		const disposeNext = onDedicatedShortcut("next-tab", (handler) => aoBridge.app.onNextTabShortcut(handler), () => {
			if (!isSettingsPageOpen()) selectAdjacentTab(1);
		});
		return () => {
			disposePrevious();
			disposeNext();
		};
	}, [selectAdjacentTab]);

	useEffect(() => {
		aoBridge.app.setCloseShellTerminalShortcutEnabled(
			Boolean(closeFocusedSplitPane) || Boolean(activeWorkspaceTab?.onClose) || (target.kind === "shell" && Boolean(onCloseShellTerminal)),
		);
		return () => aoBridge.app.setCloseShellTerminalShortcutEnabled(false);
	}, [activeWorkspaceTab, closeFocusedSplitPane, target.kind, onCloseShellTerminal]);

	useEffect(() => {
		if (availableAuxiliaryKeys.length > previousTabCountRef.current) scrollTabsToEnd();
		previousTabCountRef.current = availableAuxiliaryKeys.length;
	}, [availableAuxiliaryKeys.length, scrollTabsToEnd]);

	const activeAuxiliaryTabKey =
		reviewerChatSelected && reviewerChat
			? `reviewer-chat:${reviewerChat.reviewId}`
			: workspaceActiveTabKey ?? (target.kind === "shell"
				? target.handleId
				: target.kind === "reviewer"
					? `reviewer:${target.handleId}`
					: undefined);
	// Sorted, so reordering by drag does not scroll the strip under the pointer.
	const auxiliaryTabSet = orderedAuxiliaryTabs.map((tab) => tab.key).sort().join("|");
	// Reveal the active tab only when the selection or tab set changes. Keyed on
	// strings, not the tab objects, which are rebuilt on every render and would
	// otherwise yank a manually scrolled strip back to the active tab.
	useEffect(() => {
		const activeKey = activeAuxiliaryTabKey;
		if (!activeKey) return;
		const scrollRegion = tabsOverflowRef.current;
		if (!scrollRegion) return;
		const activeTab = Array.from(
			scrollRegion.querySelectorAll<HTMLElement>("[data-terminal-tab-key]"),
		).find((element) => element.dataset.terminalTabKey === activeKey);
		if (!activeTab) return;
		const scrollRect = scrollRegion.getBoundingClientRect();
		const tabRect = activeTab.getBoundingClientRect();
		let nextScrollLeft = scrollRegion.scrollLeft;
		if (tabRect.left < scrollRect.left) nextScrollLeft -= scrollRect.left - tabRect.left;
		if (tabRect.right > scrollRect.right) nextScrollLeft += tabRect.right - scrollRect.right;
		if (nextScrollLeft === scrollRegion.scrollLeft) return;
		scrollRegion.scrollTo({ behavior: "smooth", left: Math.max(0, nextScrollLeft) });
	}, [activeAuxiliaryTabKey, auxiliaryTabSet]);

	useEffect(() => {
		const pane = paneRef.current;
		if (!pane) return;
		const workspaceSurface = pane.closest<HTMLElement>(".center-panel-surface");
		const measure = () => {
			const paneRect = pane.getBoundingClientRect();
			// leftInset/rightInset are kept for the terminal region width calculation
			// but no longer used for viewport-alignment padding (topbar is inside the surface).
			const workspaceRect = workspaceSurface?.getBoundingClientRect() ?? paneRect;
			const next = {
				leftInset: workspaceRect.left,
				rightInset: Math.max(0, window.innerWidth - workspaceRect.right),
				width: paneRect.width,
			};
			setTerminalBounds((current) =>
				current.leftInset === next.leftInset && current.rightInset === next.rightInset && current.width === next.width
					? current
					: next,
			);
		};
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(pane);
		if (workspaceSurface) observer.observe(workspaceSurface);
		return () => observer.disconnect();
	}, []);

	const updateFontSize = useCallback((delta: number) => {
		setFontSize((current) => {
			const next = clampTerminalFontSize(current + delta);
			window.localStorage?.setItem(terminalFontSizeStorageKey, String(next));
			return next;
		});
	}, []);

	const toggleFullscreen = useCallback(async () => {
		const pane = paneRef.current;
		if (!pane) return;
		try {
			if (document.fullscreenElement === pane) {
				await document.exitFullscreen();
				return;
			}
			await pane.requestFullscreen();
		} catch (error) {
			console.warn("Unable to toggle terminal fullscreen", error);
		}
	}, []);

	const handleWheelZoom = useCallback(
		(event: ReactWheelEvent<HTMLDivElement>) => {
			if (!event.ctrlKey && !event.metaKey) return;
			event.preventDefault();
			event.stopPropagation();

			if (event.timeStamp - lastWheelZoomAtRef.current > WHEEL_ZOOM_RESET_MS) {
				wheelZoomRemainderRef.current = 0;
			}
			lastWheelZoomAtRef.current = event.timeStamp;
			wheelZoomRemainderRef.current += event.deltaY;

			const steps = Math.floor(Math.abs(wheelZoomRemainderRef.current) / WHEEL_ZOOM_THRESHOLD);
			if (steps === 0) return;

			const direction = wheelZoomRemainderRef.current > 0 ? -1 : 1;
			updateFontSize(direction * steps);
			wheelZoomRemainderRef.current -= Math.sign(wheelZoomRemainderRef.current) * steps * WHEEL_ZOOM_THRESHOLD;
		},
		[updateFontSize],
	);

	const clearanceRef = useSidebarChromeClearanceRef<HTMLDivElement>(!isFullscreen && isMac);
	const terminalTopbar = (
		<SessionTabsTopbar
			fullscreen={isFullscreen}
			actions={topbarActions}
			clearanceClassName={cn(
				!isFullscreen && isMac && "session-topbar-titlebar-clearance-mac",
				!isFullscreen && !isSidebarOpen && isLinux && "session-topbar-titlebar-clearance-linux",
			)}
			clearanceRef={clearanceRef}
			tabsTrailing={tabStripAction}
			hideActions={isFullscreen}
			regionWidth={terminalBounds.width > 0 ? terminalBounds.width : "100%"}
			tabs={
					<div
							aria-label={t("terminal.tabsAria")}
							className="flex h-full min-w-0 items-stretch"
							onKeyDown={handleTerminalTabListKeyDown}
							ref={tabListRef}
							role="tablist"
						>
							<div className="relative min-w-0 flex-1 self-stretch overflow-hidden">
								<motion.div
									ref={tabsOverflowRef}
									className="session-tab-scroll-region edge-fade edge-fade-x scrollbar-none flex h-full min-w-flex-min min-w-0 items-stretch overflow-x-auto"
									data-tab-scroll-region
									layoutScroll
								>
								{/* The macOS header is a window drag region outside its controls; the
								    whole tab row opts out so separators and tab edges still take
								    wheel scrolling and clicks. */}
								<div className="flex w-max items-stretch [-webkit-app-region:no-drag]">
									{/* The owning session scrolls with its auxiliary terminals, but remains fixed in order. */}
									<SessionTabSlot>
										{session ? (
											<SessionPaneTab
												isActive={target.kind === "worker" && !workspaceActiveTabKey && !reviewerChatSelected}
												label={sessionTabLabel}
												onSelect={onSelectSessionTerminal}
												onRenamed={refreshWorkspaces}
												session={session}
												tabAction={sessionTabAction}
												tabActionPinned={sessionTabActionPinned}
											/>
										) : (
											<SessionPaneTab isActive={target.kind === "worker"} label={sessionTabLabel} />
										)}
									</SessionTabSlot>
									<Reorder.Group
										as="div"
										axis="x"
										className="flex items-stretch self-stretch"
										onReorder={reorderAuxiliaryTabs}
										values={orderedAuxiliaryTabs.map((tab) => tab.key)}
									>
										{orderedAuxiliaryTabs.map((tab) => (
											<DraggableWorkspaceTab key={tab.key} value={tab.key}>
												{tab.kind === "reviewer" || tab.kind === "reviewer-chat" ? (
													<SessionPaneTab
														icon={
															<AgentAvatar
																provider={tab.terminal.harness}
																className="size-icon-sm!"
																decorative
															/>
														}
																		isActive={tab.kind === "reviewer" ? target.kind === "reviewer" && !workspaceActiveTabKey : reviewerChatSelected && !workspaceActiveTabKey}
														label={t("terminal.reviewer")}
														onSelect={() =>
															tab.kind === "reviewer"
																? onSelectReviewerTerminal?.(tab.terminal)
																: onSelectReviewerChat?.(tab.terminal)
														}
														tabKey={tab.key}
														tabMenu
														title={tab.terminal.harness}
													/>
												) : tab.kind === "shell" ? (
													<ShellTerminalTab
														appearance="connected"
														isActive={target.kind === "shell" && target.handleId === tab.terminal.handleId && !workspaceActiveTabKey}
														onClose={() => onCloseShellTerminal?.(tab.terminal.handleId)}
														onRename={
															onRenameShellTerminal
																? (title) => onRenameShellTerminal(tab.terminal.handleId, title)
																: undefined
														}
														onSelect={() => onSelectShellTerminal?.(tab.terminal.handleId)}
														shell={tab.terminal}
														stripShellHandleIds={stripShellHandleIds}
													/>
												) : (
													tab.tab.menu ? (
														<TabContextMenu spec={{ ...tab.tab.menu, closeShortcut: tab.key === workspaceActiveTabKey }}>
															{tab.tab.content}
														</TabContextMenu>
													) : tab.tab.content
												)}
											</DraggableWorkspaceTab>
										))}
									</Reorder.Group>
									{workspaceTabActions}
								</div>
							</motion.div>
							</div>
					</div>
			}
		/>
	);

	return (
		<div
			ref={paneRef}
			className="terminal-pane-frame flex h-full min-h-0 min-w-flex-min flex-col"
			onWheelCapture={handleWheelZoom}
		>
			{isFullscreen ? terminalTopbar : <SessionTopbarPortal>{terminalTopbar}</SessionTopbarPortal>}
			<div
				aria-label={t("terminal.panelAria", { title: activeTerminalLabel })}
				className="relative min-h-0 flex-1"
				role="tabpanel"
			>
				{reviewerChatSelected && reviewerChatContent ? reviewerChatContent : (
					<div
						className="h-full min-h-0"
						data-testid="terminal-interaction-surface"
						inert={workerInputDisabled ? true : undefined}
					>
						{hostId && !remoteBase ? null : <TerminalPane
							key={hostId ? `${hostId}:${remoteBase}` : undefined}
							createMux={hostId ? remoteCreateMux : undefined}
							daemonReady={hostId ? Boolean(remoteBase) : daemonReady}
							fontSize={fontSize}
						// A terminal you can type into should already hold the caret when you
						// open or switch to the session, the same way the chat composer does.
						// Worker input is off during an interface transition; every other target
						// is interactive as soon as it is on screen.
							focusRequested={target.kind !== "worker" || !workerInputDisabled}
							isFullscreen={isFullscreen}
							inputDisabled={workerInputDisabled}
							booting={terminalBooting && target.kind === "worker"}
							switchingLabel={target.kind === "worker" ? terminalSwitchingLabel : undefined}
							onChangeFontSize={updateFontSize}
							onTerminalContentReadyChange={target.kind === "worker" && session?.cloud ? onSessionTerminalAttached : undefined}
							onToggleFullscreen={toggleFullscreen}
							session={session}
							terminalGeneration={terminalGeneration}
							terminalTarget={target}
							theme={theme}
						/>}
					</div>
				)}
			</div>
		</div>
	);
}

// The interface switch arrives as a shared topbar control (28px button, or a
// 28px status with a cancel button). In the slot after the tab's title it is
// resized to match the close and menu controls: a 16px box, 13px glyph, 28px
// hit area. Like them it is a pure ghost: the quiet button's hover pill
// (its ::before) loses its fill, and ::before stays as the hit area.
const sessionTabActionClassName = cn(
	"grid place-items-center [&_svg]:size-icon-sm [&_button]:before:bg-transparent!",
	"[&_[data-interface-switch]]:relative [&_[data-interface-switch]]:size-icon-base [&_[data-interface-switch]]:rounded-sm [&_[data-interface-switch]]:after:absolute [&_[data-interface-switch]]:after:-inset-1.5 [&_[data-interface-switch]]:after:content-['']",
	"[&_[role=status]]:size-icon-base [&_[role=status]_button]:rounded-sm [&_[role=status]_button]:before:absolute [&_[role=status]_button]:before:-inset-1.5 [&_[role=status]_button]:before:content-['']",
);

type SessionPaneTabProps = {
	label: string;
	isActive: boolean;
	onSelect?: () => void;
	onRenamed?: () => void | Promise<void>;
	session?: WorkspaceSession;
	/** The agent shown without a session; its mark is the icon and its name joins the label. */
	provider?: string;
	icon?: ReactNode;
	title?: string;
	/** The control after the tab's title: the session's ⋮ menu, or a running interface switch's status. */
	tabAction?: ReactNode;
	/** Keep the action showing at rest (the session tab's ⋮ always shows). */
	tabActionPinned?: boolean;
	/** Give a tab with no session (a reviewer) the strip's right-click menu. */
	tabMenu?: boolean;
	/** The tab's strip key, for split actions in its menu. */
	tabKey?: string;
};

// The session, reviewer and no-session tabs of the strip. The full label only
// becomes the hover tooltip when the tab strip is crowded enough to truncate it.
export function SessionPaneTab({
	label,
	isActive,
	onSelect,
	onRenamed,
	session,
	provider = session?.provider,
	icon,
	title,
	tabAction,
	tabActionPinned = false,
	tabMenu = false,
	tabKey = SESSION_TAB_KEY,
}: SessionPaneTabProps) {
	const { t } = useTranslation();
	const { ref, isTruncated } = useTruncatedText<HTMLButtonElement>(label);
	const activityLabel = session ? getAgentActivityView(session.activity, t).label : undefined;
	const providerLabel = provider ? agentLabel(provider) : undefined;
	const tabIcon = provider ? <AgentAvatar className="size-icon-sm!" decorative provider={provider} /> : icon;
	// A session object supplies the tab presentation; refresh wiring explicitly
	// opts the owning surface into rename so shared preview/cloud tabs cannot
	// persist a title without updating their query cache. The project
	// orchestrator's tab shows its role, not a name, so it is never renamable.
	const renameSession = onRenamed && session && !isOrchestratorSession(session) ? session : undefined;
	const rename = useSessionRename(renameSession, onRenamed);
	const editingContent = renameSession && rename.isEditing ? (
		<input
			aria-label={t("shell.renameSession", { title: renameSession.title })}
			autoFocus
			className={terminalTabRenameInputClassName}
			maxLength={MAX_SESSION_DISPLAY_NAME_LEN}
			onBlur={() => void rename.commit()}
			onChange={(event) => rename.setDraft(event.target.value)}
			onFocus={(event) => event.currentTarget.select()}
			onKeyDown={(event) => {
				if (event.key === "Enter") {
					event.preventDefault();
					event.currentTarget.blur();
				} else if (event.key === "Escape") {
					event.preventDefault();
					rename.cancel();
				}
			}}
			value={rename.draft}
		/>
	) : undefined;
	const tabFrame = (
		<TerminalTabFrame
			// Renaming hides the ⋮ but keeps its slot, so the tab keeps its width.
			action={tabAction ? <span className={cn(sessionTabActionClassName, rename.isEditing && "invisible")} data-testid="session-tab-action">{tabAction}</span> : undefined}
			actionPosition="end"
			actionPinned={tabActionPinned}
			wide={session ? "session" : undefined}
			active={isActive}
			buttonProps={{
				"aria-current": isActive,
				"aria-keyshortcuts": renameSession ? "F2" : undefined,
				"aria-label": [label, providerLabel, activityLabel].filter(Boolean).join(" · "),
				"aria-selected": isActive,
				onClick: (event) => {
					if (event.detail > 1) return;
					onSelect?.();
				},
				onDoubleClick: renameSession
					? (event) => {
							event.preventDefault();
							rename.begin();
						}
					: undefined,
				onKeyDown: renameSession
					? (event) => {
							if (event.key !== "F2") return;
							event.preventDefault();
							rename.begin();
						}
					: undefined,
				role: "tab",
				tabIndex: isActive ? 0 : -1,
				title: title ?? (isTruncated ? label : t("terminal.sessionAria")),
				type: "button",
			}}
			buttonRef={ref}
			editingContent={editingContent}
			editingValue={rename.draft}
			icon={tabIcon}
			label={label}
		/>
	);
	if (rename.isEditing || (!session && !tabMenu)) return tabFrame;
	// Archive only where the ⇧⌘A confirmation is mounted (SessionTopbarActions):
	// an active local or remote worker. The orchestrator goes with its project.
	const menu: TabMenuSpec = session
		? {
				kind: "session",
				tabKey,
				session,
				onRename: renameSession ? rename.begin : undefined,
				canArchive: !isOrchestratorSession(session) && !session.cloud && sessionIsActive(session),
			}
		: { kind: "plain", tabKey };
	// The ⋮ button in the action slot opens this same spec.
	return (
		<TabMenuSpecContext.Provider value={menu}>
			<TabContextMenu spec={menu}>{tabFrame}</TabContextMenu>
		</TabMenuSpecContext.Provider>
	);
}
