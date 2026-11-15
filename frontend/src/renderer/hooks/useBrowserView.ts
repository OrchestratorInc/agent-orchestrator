import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
	BrowserAgentActivityState,
	BrowserDevToolsPlacement,
	BrowserDevToolsState,
	BrowserFindState,
	BrowserNavState,
	BrowserRect,
	BrowserTabState,
	BrowserTabsState,
} from "../../main/browser-view-host";
import type {
	BrowserAnnotationActionInput,
	BrowserAnnotationCancelPayload,
	BrowserAnnotationStatePayload,
	BrowserAnnotationSubmitPayload,
} from "../../shared/browser-annotations";
import type { BrowserProfileViewState } from "../../shared/browser-profiles";
import { aoBridge } from "../lib/bridge";
import { subscribeBrowserOverlaySync } from "../lib/browser-overlay-sync";

export type { BrowserNavState };

export type ClosedBrowserTab = {
	id: string;
	title: string;
	url: string;
	favicon?: string;
};

const MAX_CLOSED_TABS = 5;

function isBlankTabUrl(url: string): boolean {
	return !url || url === "about:blank";
}

function sameBrowserURL(left: string, right: string): boolean {
	try {
		const normalize = (value: string) => {
			const parsed = new URL(value);
			parsed.hostname = parsed.hostname.replace(/^www\./i, "");
			parsed.hash = "";
			return parsed.href;
		};
		return normalize(left) === normalize(right);
	} catch {
		return left === right;
	}
}

function sameBrowserOrigin(left: string, right: string): boolean {
	try {
		return new URL(left).origin === new URL(right).origin;
	} catch {
		return false;
	}
}

type UseBrowserViewOptions = {
	sessionId: string;
	origin?: { hostId: string; sessionId: string; proxyBase: string };
	cloudAgentBrowser?: { baseUrl: string; orgId: string; sessionId: string };
	active: boolean;
	poppedOut: boolean;
	terminated?: boolean;
	sessionEnded?: boolean;
	previewUrl?: string;
	previewRevision?: number;
};

export type BrowserViewModel = {
	viewId: string;
	navState: BrowserNavState;
	slotRef: (node: HTMLDivElement | null) => void;
	navigate: (url: string) => Promise<void>;
	goBack: () => Promise<void>;
	goForward: () => Promise<void>;
	reload: () => Promise<void>;
	stop: () => Promise<void>;
	findState: BrowserFindState;
	findOpenRequest: number;
	findInPage: (query: string, forward: boolean, newSession: boolean) => Promise<void>;
	stopFindInPage: (focusPage?: boolean) => Promise<void>;
	tabs: BrowserTabState[];
	activeTabId: string;
	tabNotice: string;
	selectTab: (tabId: string) => Promise<void>;
	closeTab: (tabId: string) => Promise<void>;
	openTab: (url?: string) => Promise<void>;
	openLink: (url: string) => Promise<void>;
	reorderTabs: (orderedIds: string[]) => void;
	closedTabs: ClosedBrowserTab[];
	reopenClosedTab: (tabId?: string) => Promise<void>;
	devtoolsState: BrowserDevToolsState;
	profileState: BrowserProfileViewState;
	openDevTools: () => Promise<void>;
	closeDevTools: () => Promise<void>;
	setDevToolsPlacement: (placement: BrowserDevToolsPlacement) => Promise<void>;
	agentBrowserActive: boolean;
	agentBrowserActivity: ObservedBrowserAgentActivity | null;
	destroy: () => void;
	annotationMode: boolean;
	annotationState?: Pick<BrowserAnnotationStatePayload, "count" | "screenshotCount" | "hasDraft">;
	setAnnotationMode: (enabled: boolean) => Promise<void>;
	annotationAction?: (action: BrowserAnnotationActionInput["action"]) => Promise<void>;
};

const EMPTY_NAV_STATE: BrowserNavState = {
	viewId: "",
	url: "",
	title: "",
	canGoBack: false,
	canGoForward: false,
	isLoading: false,
};

const EMPTY_TABS_STATE: BrowserTabsState = {
	viewId: "",
	activeTabId: "",
	tabs: [],
};

const EMPTY_TABS: BrowserTabState[] = [];
const EMPTY_CLOSED_TABS: ClosedBrowserTab[] = [];

const EMPTY_DEVTOOLS_STATE: BrowserDevToolsState = {
	viewId: "",
	open: false,
	activeTabId: "",
	placement: "undocked",
};

const EMPTY_FIND_STATE: BrowserFindState = {
	viewId: "",
	tabId: "",
	query: "",
	activeMatchOrdinal: 0,
	matches: 0,
	finalUpdate: true,
};

const EMPTY_PROFILE_STATE: BrowserProfileViewState = {
	viewId: "",
	profileId: null,
	temporary: true,
};

type PreviewTrigger = { revision: number | null; target: string; origin: string; sessionEnded: boolean };

const consumedPreviewTriggers = new Map<string, PreviewTrigger>();

export function resetConsumedPreviewTriggersForTest(): void {
	consumedPreviewTriggers.clear();
}

const closedTabsBySession = new Map<string, ClosedBrowserTab[]>();

type CachedNativeView = { viewId: string; navState: BrowserNavState; tabsState: BrowserTabsState };
const nativeViewBySession = new Map<string, CachedNativeView>();

export function resetNativeViewCacheForTest(): void {
	nativeViewBySession.clear();
}

function refreshCachedNativeView(viewId: string, update: Partial<Omit<CachedNativeView, "viewId">>): void {
	for (const [sessionId, cached] of nativeViewBySession) {
		if (cached.viewId === viewId) nativeViewBySession.set(sessionId, { ...cached, ...update });
	}
}

export function resetClosedTabsForTest(): void {
	closedTabsBySession.clear();
}

const HIDDEN_RECT: BrowserRect = { x: 0, y: 0, width: 0, height: 0 };

let nextLayoutRevision = Math.floor(globalThis.performance?.timeOrigin ?? Date.now()) * 1_000;

function sameRect(a: BrowserRect, b: BrowserRect): boolean {
	return a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
}

function scrollCanMoveSlot(target: EventTarget | null, slot: Element | null): boolean {
	if (!slot || !(target instanceof Node)) return true;
	if (target.nodeType === Node.DOCUMENT_NODE) return true;
	return target.contains(slot);
}

function claimLayoutRevision(): number {
	nextLayoutRevision += 1;
	return nextLayoutRevision;
}

let browserHoleOwner: string | null = null;

function publishBrowserHole(viewId: string, rect: BrowserRect | null): void {
	const root = document.documentElement;
	if (rect && root.dataset.windowTranslucent !== "true") rect = null;
	if (!rect) {
		if (browserHoleOwner !== viewId) return;
		browserHoleOwner = null;
		delete root.dataset.browserHole;
		return;
	}
	browserHoleOwner = viewId;
	root.dataset.browserHole = "true";
	const shell = document.querySelector<HTMLElement>(".app-shell-root");
	if (!shell) return;
	shell.style.setProperty("--browser-hole-x", `${rect.x}px`);
	shell.style.setProperty("--browser-hole-y", `${rect.y}px`);
	shell.style.setProperty("--browser-hole-w", `${rect.width}px`);
	shell.style.setProperty("--browser-hole-h", `${rect.height}px`);
}

const RESIZE_HANDLE_RESERVE_PX = 6;

function visibleSlotRect(node: HTMLElement): BrowserRect {
	const rect = node.getBoundingClientRect();
	let { left, top, right, bottom } = rect;
	const panel = node.closest<HTMLElement>("[data-panel]");
	if (panel) {
		const bounds = panel.getBoundingClientRect();
		left = Math.max(left, bounds.left + RESIZE_HANDLE_RESERVE_PX);
		top = Math.max(top, bounds.top);
		right = Math.min(right, bounds.right);
		bottom = Math.min(bottom, bounds.bottom);
	}
	const split = node.closest<HTMLElement>(".session-split");
	if (split) {
		const bounds = split.getBoundingClientRect();
		left = Math.max(left, bounds.left);
		top = Math.max(top, bounds.top);
		if (panel?.dataset.slot !== "inspector-container") right = Math.min(right, bounds.right);
		bottom = Math.min(bottom, bounds.bottom);
	}
	return {
		x: left,
		y: top,
		width: Math.max(0, right - left),
		height: Math.max(0, bottom - top),
	};
}

function hiddenByFullscreen(node: HTMLElement): boolean {
	const fullscreen = document.fullscreenElement;
	return Boolean(fullscreen) && !fullscreen!.contains(node);
}

export type ObservedBrowserAgentActivity = BrowserAgentActivityState & { startObserved: boolean };

export function useBrowserView({
	sessionId,
	origin,
	cloudAgentBrowser,
	active,
	poppedOut,
	terminated,
	sessionEnded,
	previewUrl,
	previewRevision,
}: UseBrowserViewOptions): BrowserViewModel {
	const lifecycleRef = useRef({ sessionId, terminated });
	lifecycleRef.current = { sessionId, terminated };
	const [viewId, setViewId] = useState("");
	const [navState, setNavState] = useState<BrowserNavState>(EMPTY_NAV_STATE);
	const [annotationMode, setAnnotationModeState] = useState(false);
	const [annotationState, setAnnotationState] = useState({ count: 0, screenshotCount: 0, hasDraft: false });
	const [tabsState, setTabsState] = useState<BrowserTabsState>(EMPTY_TABS_STATE);
	const [tabOrder, setTabOrder] = useState<string[]>([]);
	const [devtoolsState, setDevtoolsState] = useState<BrowserDevToolsState>(EMPTY_DEVTOOLS_STATE);
	const [findState, setFindState] = useState<BrowserFindState>(EMPTY_FIND_STATE);
	const [findOpenRequest, setFindOpenRequest] = useState(0);
	const [profileState, setProfileState] = useState<BrowserProfileViewState>(EMPTY_PROFILE_STATE);
	const [tabNotice, setTabNotice] = useState("");
	const [closedTabs, setClosedTabs] = useState<ClosedBrowserTab[]>([]);
	const [agentBrowserActive, setAgentBrowserActive] = useState(false);
	const [agentBrowserActivity, setAgentBrowserActivity] = useState<ObservedBrowserAgentActivity | null>(null);
	const observedCommandStartsRef = useRef(new Set<string>());
	const [stateSessionId, setStateSessionId] = useState(sessionId);
	const slotNodeRef = useRef<HTMLDivElement | null>(null);
	const viewIdRef = useRef("");
	const annotationModeRef = useRef(false);
	const activeRef = useRef(active);
	const poppedOutRef = useRef(poppedOut);
	const frameRef = useRef<number | null>(null);
	const appliedLayoutRevisionRef = useRef(0);
	const settleTimerRef = useRef<number | null>(null);
	const lastSentBoundsRef = useRef<{ viewId: string; rect: BrowserRect; visible: boolean } | null>(null);
	const scrollOnlyMeasureRef = useRef(false);
	const observerRef = useRef<ResizeObserver | null>(null);
	const previewTriggerRef = useRef<PreviewTrigger | null>(null);
	const tabNoticeTimerRef = useRef<number | null>(null);
	const tabsStateRef = useRef(tabsState);
	const hasNativeBrowser = Boolean(window.ao?.browser);

	useEffect(() => {
		activeRef.current = active;
	}, [active]);

	useEffect(() => {
		tabsStateRef.current = tabsState;
	}, [tabsState]);

	useEffect(() => {
		annotationModeRef.current = annotationMode;
	}, [annotationMode]);

	const showTabNotice = useCallback((message: string) => {
		setTabNotice(message);
		if (tabNoticeTimerRef.current !== null) window.clearTimeout(tabNoticeTimerRef.current);
		tabNoticeTimerRef.current = window.setTimeout(() => {
			tabNoticeTimerRef.current = null;
			setTabNotice("");
		}, 3_000);
	}, []);

	const updateClosedTabs = useCallback(
		(updater: (current: ClosedBrowserTab[]) => ClosedBrowserTab[]) => {
			const current = closedTabsBySession.get(sessionId) ?? [];
			const next = updater(current);
			closedTabsBySession.set(sessionId, next);
			setClosedTabs(next);
		},
		[sessionId],
	);

	const sendHiddenBounds = useCallback((id = viewIdRef.current) => {
		if (!id) return;
		publishBrowserHole(id, null);
		lastSentBoundsRef.current = { viewId: id, rect: HIDDEN_RECT, visible: false };
		window.ao?.browser.setBounds({
			viewId: id,
			revision: claimLayoutRevision(),
			rect: HIDDEN_RECT,
			visible: false,
		});
	}, []);

	const measureAndSend = useCallback((onlyIfChanged = false) => {
		if (frameRef.current !== null) {
			if (window.cancelAnimationFrame) window.cancelAnimationFrame(frameRef.current);
			window.clearTimeout(frameRef.current);
		}
		frameRef.current = null;
		const id = viewIdRef.current;
		const node = slotNodeRef.current;
		if (!id) return;
		if (!activeRef.current || !node || !node.isConnected || hiddenByFullscreen(node)) {
			const last = lastSentBoundsRef.current;
			if (onlyIfChanged && last?.viewId === id && !last.visible) return;
			sendHiddenBounds(id);
			return;
		}
		const rect = visibleSlotRect(node);
		const visible = rect.width > 0 && rect.height > 0;
		const last = lastSentBoundsRef.current;
		if (onlyIfChanged && last?.viewId === id && last.visible === visible && sameRect(last.rect, rect)) return;
		const payload = { viewId: id, revision: claimLayoutRevision(), rect, visible };
		lastSentBoundsRef.current = { viewId: id, rect, visible };
		publishBrowserHole(id, payload.visible ? rect : null);
		window.ao?.browser.setBounds(payload);
	}, [sendHiddenBounds]);

	const cancelScheduledMeasure = useCallback(() => {
		if (frameRef.current === null) return;
		if (window.cancelAnimationFrame) {
			window.cancelAnimationFrame(frameRef.current);
		}
		window.clearTimeout(frameRef.current);
		frameRef.current = null;
	}, []);

	const scheduleMeasure = useCallback(() => {
		scrollOnlyMeasureRef.current = false;
		if (frameRef.current !== null) return;
		frameRef.current = window.requestAnimationFrame
			? window.requestAnimationFrame(() => measureAndSend(scrollOnlyMeasureRef.current))
			: window.setTimeout(() => measureAndSend(scrollOnlyMeasureRef.current), 16);
	}, [measureAndSend]);

	const scheduleScrollMeasure = useCallback((event: Event) => {
		if (!scrollCanMoveSlot(event.target, slotNodeRef.current)) return;
		if (frameRef.current !== null) return;
		scrollOnlyMeasureRef.current = true;
		frameRef.current = window.requestAnimationFrame
			? window.requestAnimationFrame(() => measureAndSend(scrollOnlyMeasureRef.current))
			: window.setTimeout(() => measureAndSend(scrollOnlyMeasureRef.current), 16);
	}, [measureAndSend]);

	const scheduleSettleMeasure = useCallback(() => {
		scheduleMeasure();
		if (settleTimerRef.current !== null) window.clearTimeout(settleTimerRef.current);
		settleTimerRef.current = window.setTimeout(() => {
			settleTimerRef.current = null;
			measureAndSend();
		}, 280);
	}, [measureAndSend, scheduleMeasure]);

	const slotRef = useCallback(
		(node: HTMLDivElement | null) => {
			observerRef.current?.disconnect();
			slotNodeRef.current = node;
			if (!node) {
				sendHiddenBounds();
				return;
			}
			const observer = new ResizeObserver(scheduleMeasure);
			observer.observe(node);
			const column = node.closest("[data-panel]");
			if (column) observer.observe(column);
			const gap = node.closest(".session-split")?.querySelector("[data-slot='inspector-gap']");
			if (gap) observer.observe(gap);
			observerRef.current = observer;
			measureAndSend();
			scheduleMeasure();
		},
		[measureAndSend, scheduleMeasure, sendHiddenBounds],
	);

	useEffect(() => {
		return window.ao?.browser.onBoundsApplied((result) => {
			if (result.viewId !== viewIdRef.current || result.revision <= appliedLayoutRevisionRef.current) return;
			appliedLayoutRevisionRef.current = result.revision;
		});
	}, []);

	useEffect(() => {
		let disposed = false;
		previewTriggerRef.current = hasNativeBrowser ? (consumedPreviewTriggers.get(sessionId) ?? null) : null;
		const cached = hasNativeBrowser && !terminated ? nativeViewBySession.get(sessionId) : undefined;
		let ownViewId = cached?.viewId ?? "";
		if (cached) viewIdRef.current = cached.viewId;
		setStateSessionId(sessionId);
		setViewId(cached?.viewId ?? "");
		setNavState(cached?.navState ?? EMPTY_NAV_STATE);
		setTabsState(cached?.tabsState ?? EMPTY_TABS_STATE);
		setTabOrder([]);
		setDevtoolsState(EMPTY_DEVTOOLS_STATE);
		setFindState(EMPTY_FIND_STATE);
		setFindOpenRequest(0);
		setProfileState(EMPTY_PROFILE_STATE);
		setTabNotice("");
		setClosedTabs(closedTabsBySession.get(sessionId) ?? []);
		setAgentBrowserActive(false);
		setAgentBrowserActivity(null);
		observedCommandStartsRef.current.clear();
		if (tabNoticeTimerRef.current !== null) {
			window.clearTimeout(tabNoticeTimerRef.current);
			tabNoticeTimerRef.current = null;
		}
		if (terminated) {
			consumedPreviewTriggers.delete(sessionId);
			closedTabsBySession.delete(sessionId);
			nativeViewBySession.delete(sessionId);
			return;
		}
		if (!hasNativeBrowser) {
			const state = {
				...EMPTY_NAV_STATE,
				viewId: `preview-${sessionId}`,
				url: "",
				title: "",
			};
			viewIdRef.current = state.viewId;
			setViewId(state.viewId);
			setNavState(state);
			setDevtoolsState((current) => ({
				...current,
				viewId: state.viewId,
				activeTabId: "",
			}));
			setProfileState({
				viewId: state.viewId,
				profileId: null,
				temporary: true,
			});
			return () => {
				disposed = true;
				viewIdRef.current = "";
			};
		}
		window.ao?.browser.ensure(sessionId, false).then((state) => {
			if (disposed) {
				if (lifecycleRef.current.sessionId === sessionId && lifecycleRef.current.terminated) {
					window.ao?.browser.destroy(state.viewId);
				}
				return;
			}
			viewIdRef.current = state.viewId;
			ownViewId = state.viewId;
			setViewId(state.viewId);
			setNavState(state);
			void window.ao?.browser
				.getProfile(state.viewId)
				.then((profile) => {
					if (!disposed && viewIdRef.current === profile.viewId) setProfileState(profile);
				})
				.catch(() => undefined);
			void window.ao?.browser
				.getTabs(state.viewId)
				.then((tabs) => {
					if (!disposed && viewIdRef.current === tabs.viewId) setTabsState(tabs);
				})
				.catch(() => undefined);
			void window.ao?.browser
				.getFindState(state.viewId)
				.then((find) => {
					if (!disposed && viewIdRef.current === find.viewId) setFindState(find);
				})
				.catch(() => undefined);
			scheduleSettleMeasure();
		});
		return () => {
			disposed = true;
			const id = ownViewId;
			if (id) {
				if (annotationModeRef.current) {
					void window.ao?.browser.setAnnotationMode({
						viewId: id,
						enabled: false,
					});
					setAnnotationModeState(false);
				}
				if (lifecycleRef.current.sessionId === sessionId && lifecycleRef.current.terminated) {
					window.ao?.browser.destroy(id);
				} else if (viewIdRef.current === id) {
					sendHiddenBounds(id);
				}
			}
			if (viewIdRef.current === id) viewIdRef.current = "";
		};
	}, [
		sessionEnded,
		terminated,
		hasNativeBrowser,
		scheduleSettleMeasure,
		sendHiddenBounds,
		sessionId,
	]);

	const shownSessionRef = useRef(sessionId);
	useLayoutEffect(() => {
		if (shownSessionRef.current === sessionId) return;
		shownSessionRef.current = sessionId;
		const cached = hasNativeBrowser && !terminated ? nativeViewBySession.get(sessionId) : undefined;
		if (!cached) return;
		const previous = viewIdRef.current;
		viewIdRef.current = cached.viewId;
		activeRef.current = active;
		let frame: number | null = window.requestAnimationFrame(() => {
			frame = null;
			if (viewIdRef.current === cached.viewId) measureAndSend();
			if (previous && previous !== viewIdRef.current) sendHiddenBounds(previous);
		});
		return () => {
			if (frame === null) return;
			window.cancelAnimationFrame(frame);
			if (previous && previous !== cached.viewId) sendHiddenBounds(previous);
		};
	}, [active, hasNativeBrowser, measureAndSend, sendHiddenBounds, sessionId, terminated]);

	useEffect(() => {
		if (!hasNativeBrowser || stateSessionId !== sessionId || !viewId || terminated) return;
		nativeViewBySession.set(sessionId, { viewId, navState, tabsState });
	}, [hasNativeBrowser, navState, sessionId, stateSessionId, tabsState, terminated, viewId]);

	useEffect(() => {
		return window.ao?.browser.onNavState((state) => {
			if (state.viewId !== viewIdRef.current) {
				refreshCachedNativeView(state.viewId, { navState: state });
				return;
			}
			setNavState(state);
		});
	}, []);

	useEffect(() => {
		return window.ao?.browser.onFindState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setFindState(state);
		});
	}, []);

	useEffect(() => {
		return window.ao?.browser.onFindOpen((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setFindState(state);
			setFindOpenRequest((current) => current + 1);
		});
	}, []);

	useEffect(() => {
		return window.ao?.browser.onTabsState((state) => {
			if (state.viewId !== viewIdRef.current) {
				refreshCachedNativeView(state.viewId, { tabsState: state });
				return;
			}
			setTabsState(state);
			const change = state.change;
			if (change?.kind === "popup") {
				showTabNotice("Opened new tab");
				return;
			}
			if (change?.kind !== "closed" || !change.tab || isBlankTabUrl(change.tab.url)) return;
			const { id, title, url, favicon } = change.tab;
			updateClosedTabs((current) => [
				{ id, title, url, favicon },
				...current.filter((tab) => tab.id !== id),
			].slice(0, MAX_CLOSED_TABS));
		});
	}, [showTabNotice, updateClosedTabs]);

	useEffect(() => {
		const incomingIds = tabsState.tabs.map((tab) => tab.id);
		setTabOrder((prev) => {
			const kept = prev.filter((id) => incomingIds.includes(id));
			const added = incomingIds.filter((id) => !kept.includes(id));
			return kept.length === prev.length && added.length === 0 ? prev : [...kept, ...added];
		});
	}, [tabsState.tabs]);

	const tabs = useMemo(() => {
		const byId = new Map(tabsState.tabs.map((tab) => [tab.id, tab]));
		return tabOrder.map((id) => byId.get(id)).filter((tab): tab is BrowserTabState => Boolean(tab));
	}, [tabOrder, tabsState.tabs]);

	const reorderTabs = useCallback((orderedIds: string[]) => setTabOrder(orderedIds), []);

	useEffect(() => {
		return window.ao?.browser.onDevToolsState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setDevtoolsState(state);
		});
	}, []);

	useEffect(() => {
		return window.ao?.browser.onProfileState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setProfileState(state);
		});
	}, []);

	useEffect(() => {
		return window.ao?.browser.onAgentActivity((state) => {
			if (state.viewId !== viewIdRef.current) return;
			const starts = observedCommandStartsRef.current;
			let startObserved = false;
			if (state.commandId) {
				if (state.phase === "started") starts.add(state.commandId);
				startObserved = starts.has(state.commandId);
				if (state.phase === "finished") starts.delete(state.commandId);
				if (starts.size > 64) starts.delete(starts.values().next().value!);
			}
			setAgentBrowserActive(state.active);
			setAgentBrowserActivity({ ...state, startObserved });
		});
	}, []);

	useEffect(
		() => () => {
			if (tabNoticeTimerRef.current !== null) window.clearTimeout(tabNoticeTimerRef.current);
		},
		[],
	);

	useLayoutEffect(() => {
		if (active) {
			scheduleSettleMeasure();
		} else {
			sendHiddenBounds();
		}
	}, [active, navState.url, poppedOut, scheduleSettleMeasure, sendHiddenBounds]);

	useEffect(() => {
		if (poppedOutRef.current === poppedOut) return;
		poppedOutRef.current = poppedOut;
		if (!hasNativeBrowser || !activeRef.current) {
			scheduleSettleMeasure();
			return;
		}
		measureAndSend();
		window.setTimeout(() => {
			measureAndSend();
		}, 0);
		scheduleSettleMeasure();
	}, [hasNativeBrowser, measureAndSend, poppedOut, scheduleSettleMeasure]);

	useEffect(() => {
		if (!hasNativeBrowser) return;
		let isResizing = document.body.classList.contains("is-resizing-x");
		const updateResize = () => {
			const wasResizing = isResizing;
			isResizing = document.body.classList.contains("is-resizing-x");
			if (wasResizing !== isResizing) scheduleSettleMeasure();
		};
		const unsubscribeOverlay = subscribeBrowserOverlaySync();
		updateResize();
		const resizeObserver = new MutationObserver(updateResize);
		resizeObserver.observe(document.body, {
			attributes: true,
			attributeFilter: ["class"],
		});
		return () => {
			unsubscribeOverlay();
			resizeObserver.disconnect();
		};
	}, [hasNativeBrowser, scheduleSettleMeasure]);

	useEffect(() => {
		if (!hasNativeBrowser) return;
		const running = new Set<string>();
		let frame: number | null = null;
		let deadline = 0;
		const isInspectorSlide = (event: TransitionEvent) => {
			const slot = event.target instanceof Element ? event.target.getAttribute("data-slot") : null;
			return (slot === "inspector-gap" || slot === "inspector-container") &&
				(event.propertyName === "width" || event.propertyName === "transform");
		};
		const step = () => {
			frame = null;
			if (!activeRef.current) return;
			measureAndSend();
			if (running.size > 0 && performance.now() < deadline) frame = window.requestAnimationFrame(step);
		};
		const onRun = (event: TransitionEvent) => {
			if (!isInspectorSlide(event) || !activeRef.current) return;
			running.add(`${(event.target as Element).getAttribute("data-slot")}:${event.propertyName}`);
			deadline = performance.now() + 1_000;
			if (frame === null) frame = window.requestAnimationFrame(step);
		};
		const onEnd = (event: TransitionEvent) => {
			if (!isInspectorSlide(event)) return;
			running.delete(`${(event.target as Element).getAttribute("data-slot")}:${event.propertyName}`);
			if (running.size > 0) return;
			if (frame !== null) {
				window.cancelAnimationFrame(frame);
				frame = null;
			}
			if (activeRef.current) measureAndSend();
		};
		document.addEventListener("transitionrun", onRun);
		document.addEventListener("transitionend", onEnd);
		document.addEventListener("transitioncancel", onEnd);
		return () => {
			document.removeEventListener("transitionrun", onRun);
			document.removeEventListener("transitionend", onEnd);
			document.removeEventListener("transitioncancel", onEnd);
			if (frame !== null) window.cancelAnimationFrame(frame);
		};
	}, [hasNativeBrowser, measureAndSend]);

	useEffect(() => {
		const handle = () => scheduleMeasure();
		const handleFullscreenChange = () => scheduleSettleMeasure();
		window.addEventListener("resize", handle);
		window.addEventListener("scroll", scheduleScrollMeasure, true);
		document.addEventListener("fullscreenchange", handleFullscreenChange);
		return () => {
			window.removeEventListener("resize", handle);
			window.removeEventListener("scroll", scheduleScrollMeasure, true);
			document.removeEventListener("fullscreenchange", handleFullscreenChange);
			observerRef.current?.disconnect();
			cancelScheduledMeasure();
			if (settleTimerRef.current !== null) window.clearTimeout(settleTimerRef.current);
		};
	}, [cancelScheduledMeasure, scheduleMeasure, scheduleScrollMeasure, scheduleSettleMeasure]);

	const withView = useCallback(async (fn: (id: string) => Promise<BrowserNavState | void>) => {
		const id = viewIdRef.current;
		if (!id) return;
		try {
			const next = await fn(id);
			if (next) setNavState(next);
		} catch {
		}
	}, []);

	const setAnnotationMode = useCallback(
		async (enabled: boolean) => {
			const id = viewIdRef.current;
			if (!id || !hasNativeBrowser) {
				setAnnotationModeState(false);
				return;
			}
			const styles = getComputedStyle(document.documentElement);
			await window.ao!.browser.setAnnotationMode({
				viewId: id,
				enabled,
				theme: {
					background: styles.getPropertyValue("--background").trim(),
					foreground: styles.getPropertyValue("--foreground").trim(),
					muted: styles.getPropertyValue("--muted").trim(),
					mutedForeground: styles.getPropertyValue("--muted-foreground").trim(),
					border: styles.getPropertyValue("--border").trim(),
					accent: styles.getPropertyValue("--primary").trim(),
					accentForeground: styles.getPropertyValue("--primary-foreground").trim(),
					destructive: styles.getPropertyValue("--destructive").trim(),
				},
			});
			setAnnotationModeState(enabled);
		},
		[hasNativeBrowser],
	);

	const annotationAction = useCallback(
		async (action: BrowserAnnotationActionInput["action"]) => {
			const id = viewIdRef.current;
			if (!id || !hasNativeBrowser) return;
			await window.ao!.browser.annotationAction({ viewId: id, action });
		},
		[hasNativeBrowser],
	);

	const selectTab = useCallback(
		async (tabId: string) => {
			const viewId = viewIdRef.current;
			if (!viewId || !hasNativeBrowser) return;
			try {
				const state = await window.ao!.browser.selectTab({ viewId, tabId });
				if (viewIdRef.current === state.viewId) setTabsState(state);
			} catch {
				const state = await window.ao!.browser.getTabs(viewId).catch(() => undefined);
				if (state && viewIdRef.current === state.viewId) setTabsState(state);
			}
		},
		[hasNativeBrowser],
	);

	const closeTab = useCallback(
		async (tabId: string) => {
			const viewId = viewIdRef.current;
			if (!viewId || !hasNativeBrowser) return;
			const closing = tabsStateRef.current.tabs.find((tab) => tab.id === tabId);
			try {
				const state = await window.ao!.browser.closeTab({ viewId, tabId });
				if (viewIdRef.current !== state.viewId) return;
				setTabsState(state);
				const stillOpen = state.tabs.some((tab) => tab.id === tabId);
				if (closing && !stillOpen && !closing.remote && !isBlankTabUrl(closing.url)) {
					const { id, title, url, favicon } = closing;
					updateClosedTabs((current) => [{ id, title, url, favicon }, ...current.filter((t) => t.id !== id)].slice(0, MAX_CLOSED_TABS));
				}
			} catch {
				showTabNotice("Couldn't close that tab");
				window.ao?.browser
					.getTabs(viewId)
					.then((state) => {
						if (viewIdRef.current === state.viewId) setTabsState(state);
					})
					.catch(() => undefined);
			}
		},
		[hasNativeBrowser, showTabNotice, updateClosedTabs],
	);

	const openTab = useCallback(
		async (url?: string) => {
			if (!hasNativeBrowser) return;
			let viewId = viewIdRef.current;
			if (!viewId) {
				const ensured = await window.ao!.browser.ensure(sessionId, false);
				viewId = ensured.viewId;
				viewIdRef.current = viewId;
				setViewId(viewId);
				setNavState(ensured);
			}
			const state = await window.ao!.browser.openTab({ viewId, url });
			if (viewIdRef.current === state.viewId) setTabsState(state);
		},
		[hasNativeBrowser, sessionId],
	);
	const openLink = useCallback(
		async (url: string) => {
			if (!hasNativeBrowser) return;
			let id = viewIdRef.current;
			if (!id) {
				const ensured = await window.ao!.browser.ensure(sessionId, false);
				id = ensured.viewId;
				viewIdRef.current = id;
				setViewId(id);
				setNavState(ensured);
			}
			let tabs = tabsStateRef.current.tabs;
			try {
				const next = await window.ao!.browser.getTabs(id);
				tabs = next.tabs;
				if (viewIdRef.current === id) setTabsState(next);
			} catch {
			}
			const existingTab = tabs.find((tab) => !tab.remote && !isBlankTabUrl(tab.url) && sameBrowserURL(tab.url, url));
			if (existingTab) {
				await selectTab(existingTab.id);
				return;
			}
			const activeTab = tabs.find((tab) => tab.active);
			if (activeTab && !activeTab.remote && isBlankTabUrl(activeTab.url)) {
				const state = await window.ao!.browser.navigate({ viewId: id, url });
				if (viewIdRef.current === state.viewId) setNavState(state);
				return;
			}
			await openTab(url);
		},
		[hasNativeBrowser, openTab, selectTab, sessionId],
	);

	const reopenClosedTab = useCallback(
		async (tabId?: string) => {
			const current = closedTabsBySession.get(sessionId) ?? [];
			const entry = tabId ? current.find((tab) => tab.id === tabId) : current[0];
			if (!entry) return;
			updateClosedTabs((tabs) => tabs.filter((tab) => tab.id !== entry.id));
			try {
				await openTab(entry.url);
			} catch {
				updateClosedTabs((tabs) => [entry, ...tabs.filter((tab) => tab.id !== entry.id)].slice(0, MAX_CLOSED_TABS));
				showTabNotice("Couldn't reopen that tab");
			}
		},
		[openTab, sessionId, showTabNotice, updateClosedTabs],
	);

	const runDevtools = useCallback(
		async (operation: "open" | "close" | "setPlacement", placement?: BrowserDevToolsPlacement) => {
			const id = viewIdRef.current;
			if (!id || !hasNativeBrowser) return;
			try {
				const next = await window.ao!.browser.devtools({
					viewId: id,
					operation,
					placement,
				});
				if (viewIdRef.current === next.viewId) setDevtoolsState(next);
			} catch {
			}
		},
		[hasNativeBrowser],
	);

	useEffect(() => {
		const handleDone = (payload: BrowserAnnotationSubmitPayload | BrowserAnnotationCancelPayload) => {
			if (payload.viewId !== viewIdRef.current) return;
			setAnnotationModeState(false);
		};
		const offSubmit = window.ao?.browser.onAnnotationSubmit(handleDone);
		const offCancel = window.ao?.browser.onAnnotationCancel(handleDone);
		return () => {
			offSubmit?.();
			offCancel?.();
		};
	}, []);

	useEffect(() => {
		const offState = window.ao?.browser.onAnnotationState((payload) => {
			if (payload.viewId !== viewIdRef.current) return;
			setAnnotationState({
				count: payload.count,
				screenshotCount: payload.screenshotCount,
				hasDraft: payload.hasDraft,
			});
		});
		return () => offState?.();
	}, []);

	useEffect(() => {
		if (navState.url || !annotationModeRef.current) return;
		void setAnnotationMode(false);
	}, [navState.url, setAnnotationMode]);

	const navigate = useCallback(
		(url: string) => {
			if (!hasNativeBrowser) {
				const normalized = url.trim();
				setNavState((current) => ({
					...current,
					url: normalized,
					title: normalized ? "Orchestrator preview" : "",
					isLoading: false,
				}));
				return Promise.resolve();
			}
			return withView((id) => window.ao!.browser.navigate({ viewId: id, url }));
		},
		[hasNativeBrowser, withView],
	);

	const showPreview = useCallback(
		async (url: string) => {
			const id = viewIdRef.current;
			const tabs = hasNativeBrowser && id ? (await window.ao!.browser.getTabs(id).catch(() => null))?.tabs : undefined;
			if (!tabs?.find((tab) => tab.active)?.remote) return navigate(url);
			const preview = /^https:\/\/ao-preview-[0-9a-f]{32}\.localhost(?::\d+)?\//;
			const own = tabs.find((tab) => !tab.remote && (sameBrowserOrigin(tab.url, url) || preview.test(tab.url)));
			if (!own) return openTab(url);
			await selectTab(own.id);
			return navigate(url);
		},
		[hasNativeBrowser, navigate, openTab, selectTab],
	);

	useEffect(() => {
		const agentBrowserUrl = origin
			? () => aoBridge.remotes.agentBrowserUrl(origin.hostId, origin.sessionId)
			: cloudAgentBrowser
				? () => aoBridge.cloudCp.agentBrowserUrl(cloudAgentBrowser.baseUrl, cloudAgentBrowser.orgId, cloudAgentBrowser.sessionId)
				: undefined;
		if (!hasNativeBrowser || !agentBrowserUrl || !viewId || viewIdRef.current !== viewId || terminated) return;
		let cancelled = false;
		let retry = 0;
		const again = (attempt: number) => {
			if (!cancelled) retry = window.setTimeout(() => ask(attempt + 1), Math.min(2_000 * 2 ** attempt, 15_000));
		};
		const ask = (attempt: number) => {
			void agentBrowserUrl().then(async (url) => {
				if (cancelled || viewIdRef.current !== viewId) return;
				if (!url) return again(attempt);
				const state = await window.ao!.browser.openTab({ viewId, url });
				if (viewIdRef.current === viewId) setTabsState(state);
			}).catch((error) => {
				if (attempt === 0) console.warn("Unable to open the agent browser", error);
				again(attempt);
			});
		};
		ask(0);
		return () => {
			cancelled = true;
			window.clearTimeout(retry);
		};
	}, [hasNativeBrowser, origin?.hostId, origin?.sessionId, origin?.proxyBase, cloudAgentBrowser?.baseUrl, cloudAgentBrowser?.orgId, cloudAgentBrowser?.sessionId, terminated, viewId]);

	const clear = useCallback(() => {
		if (!hasNativeBrowser) {
			setNavState((current) => ({
				...current,
				url: "",
				title: "",
				isLoading: false,
			}));
			return Promise.resolve();
		}
		return withView((id) => window.ao!.browser.clear(id));
	}, [hasNativeBrowser, withView]);

	const findInPage = useCallback(
		(query: string, forward: boolean, newSession: boolean) => {
			if (!hasNativeBrowser) {
				setFindState((current) => ({ ...current, query, activeMatchOrdinal: 0, matches: 0, finalUpdate: true }));
				return Promise.resolve();
			}
			return withView(async (id) => {
				const state = await window.ao!.browser.findInPage({ viewId: id, query, forward, newSession });
				if (viewIdRef.current === state.viewId) setFindState(state);
			});
		},
		[hasNativeBrowser, withView],
	);

	const stopFindInPage = useCallback(
		(focusPage = false) => {
			if (!hasNativeBrowser) {
				setFindState(EMPTY_FIND_STATE);
				return Promise.resolve();
			}
			return withView(async (id) => {
				const state = await window.ao!.browser.stopFindInPage({ viewId: id, focusPage });
				if (viewIdRef.current === state.viewId) setFindState(state);
			});
		},
		[hasNativeBrowser, withView],
	);

	useEffect(() => {
		if (!origin || (!terminated && previewUrl?.trim())) return;
		void aoBridge.remotes.previewUrl(origin.hostId, origin.sessionId, "").catch(() => {
		});
		return undefined;
	}, [origin?.hostId, origin?.sessionId, origin?.proxyBase, previewUrl, terminated]);

	useEffect(() => {
		if (!viewId || viewIdRef.current !== viewId || terminated) return;
		const target = previewUrl?.trim() ?? "";
		const source = origin ? `${origin.hostId}:${origin.sessionId}:${origin.proxyBase}` : "local";
		const revision = typeof previewRevision === "number" ? previewRevision : null;
		const previous = previewTriggerRef.current;
		const sameLifecycle = previous?.sessionEnded === Boolean(sessionEnded);
		if (sameLifecycle && previous?.revision === revision && previous.target === target && previous.origin === source) return;
		if (sameLifecycle && revision !== null && previous?.revision === revision && previous.origin === source) return;
		const consumed: PreviewTrigger = { revision, target, origin: source, sessionEnded: Boolean(sessionEnded) };
		previewTriggerRef.current = consumed;
		if (hasNativeBrowser) consumedPreviewTriggers.set(sessionId, consumed);
		if (target) {
			let cancelled = false;
			const resolved = origin
				? aoBridge.remotes.previewUrl(origin.hostId, origin.sessionId, target)
				: Promise.resolve(target);
			void resolved.then((url) => {
				return cancelled ? undefined : showPreview(url);
			}).catch((error) => console.warn("Unable to open browser preview", error));
			return () => { cancelled = true; };
		} else if ((revision !== null && revision > 0) || previous?.target) {
			void clear();
		}
		return undefined;
	}, [clear, hasNativeBrowser, origin?.hostId, origin?.sessionId, origin?.proxyBase, previewRevision, previewUrl, sessionEnded, sessionId, showPreview, terminated, viewId]);

	const destroy = useCallback(() => {
		const id = viewIdRef.current;
		if (!id) return;
		if (annotationModeRef.current) {
			void window.ao?.browser.setAnnotationMode({ viewId: id, enabled: false });
			setAnnotationModeState(false);
		}
		sendHiddenBounds(id);
		window.ao?.browser.destroy(id);
		viewIdRef.current = "";
		setViewId("");
		setNavState(EMPTY_NAV_STATE);
		setTabsState(EMPTY_TABS_STATE);
		setFindState(EMPTY_FIND_STATE);
		setClosedTabs([]);
	}, [sendHiddenBounds]);

	const stateBelongsToSession = stateSessionId === sessionId;
	const switchingTo = !stateBelongsToSession && hasNativeBrowser && !terminated ? nativeViewBySession.get(sessionId) : undefined;

	const modelViewId = stateBelongsToSession ? viewId : (switchingTo?.viewId ?? "");
	const modelNavState = stateBelongsToSession ? navState : (switchingTo?.navState ?? EMPTY_NAV_STATE);
	const modelFindState = stateBelongsToSession ? findState : EMPTY_FIND_STATE;
	const modelFindOpenRequest = stateBelongsToSession ? findOpenRequest : 0;
	const modelTabs = stateBelongsToSession ? tabs : (switchingTo?.tabsState.tabs ?? EMPTY_TABS);
	const modelActiveTabId = stateBelongsToSession ? tabsState.activeTabId : (switchingTo?.tabsState.activeTabId ?? "");
	const modelTabNotice = stateBelongsToSession ? tabNotice : "";
	const modelClosedTabs = stateBelongsToSession ? closedTabs : EMPTY_CLOSED_TABS;
	const modelDevtoolsState = stateBelongsToSession ? devtoolsState : EMPTY_DEVTOOLS_STATE;
	const modelProfileState = stateBelongsToSession ? profileState : EMPTY_PROFILE_STATE;
	const modelAgentBrowserActive = stateBelongsToSession && agentBrowserActive;
	const modelAgentBrowserActivity = stateBelongsToSession ? agentBrowserActivity : null;
	const goBack = useCallback(() => (hasNativeBrowser ? withView((id) => window.ao!.browser.goBack(id)) : Promise.resolve()), [hasNativeBrowser, withView]);
	const goForward = useCallback(() => (hasNativeBrowser ? withView((id) => window.ao!.browser.goForward(id)) : Promise.resolve()), [hasNativeBrowser, withView]);
	const reload = useCallback(() => (hasNativeBrowser ? withView((id) => window.ao!.browser.reload(id)) : Promise.resolve()), [hasNativeBrowser, withView]);
	const stop = useCallback(() => (hasNativeBrowser ? withView((id) => window.ao!.browser.stop(id)) : Promise.resolve()), [hasNativeBrowser, withView]);
	const openDevTools = useCallback(() => runDevtools("open"), [runDevtools]);
	const closeDevTools = useCallback(() => runDevtools("close"), [runDevtools]);
	const setDevToolsPlacement = useCallback((placement: BrowserDevToolsPlacement) => runDevtools("setPlacement", placement), [runDevtools]);

	return useMemo<BrowserViewModel>(
		() => ({
			viewId: modelViewId,
			navState: modelNavState,
			slotRef,
			navigate,
			goBack,
			goForward,
			reload,
			stop,
			findState: modelFindState,
			findOpenRequest: modelFindOpenRequest,
			findInPage,
			stopFindInPage,
			tabs: modelTabs,
			activeTabId: modelActiveTabId,
			tabNotice: modelTabNotice,
			selectTab,
			closeTab,
			openTab,
			openLink,
			reorderTabs,
			closedTabs: modelClosedTabs,
			reopenClosedTab,
			devtoolsState: modelDevtoolsState,
			profileState: modelProfileState,
			openDevTools,
			closeDevTools,
			setDevToolsPlacement,
			agentBrowserActive: modelAgentBrowserActive,
			agentBrowserActivity: modelAgentBrowserActivity,
			destroy,
			annotationMode,
			annotationState,
			setAnnotationMode,
			annotationAction,
		}),
		[
			modelViewId, modelNavState, slotRef, navigate, goBack, goForward, reload, stop, modelFindState,
			modelFindOpenRequest, findInPage, stopFindInPage, modelTabs, modelActiveTabId, modelTabNotice, selectTab,
			closeTab, openTab, openLink, reorderTabs, modelClosedTabs, reopenClosedTab, modelDevtoolsState,
			modelProfileState, openDevTools, closeDevTools, setDevToolsPlacement, modelAgentBrowserActive,
			modelAgentBrowserActivity, destroy, annotationMode, annotationState, setAnnotationMode, annotationAction,
		],
	);
}
