import { useTranslation } from "react-i18next";
import { Button } from "../components/ui/button";
import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { ChevronDown, ChevronUp, Loader2, Search, X } from "lucide-react";
import { useAppShortcut, isSettingsPageOpen } from "./useAppShortcut";
import { useHarnessUpdateNoticeClearance } from "./useHarnessUpdateNoticeClearance";
import { toSnapshot, mergeConversationPages } from "./useConversation";
import { clientForSessionHost } from "../lib/host-clients";
import { apiErrorMessage } from "../lib/api-client";
import { findTextRanges, isEmptyFindQuery, occurrenceKey, renderedFindBlocks, findRange, type FindOccurrence } from "../lib/chat-find";
import type { ConversationSnapshot } from "../types/conversation";

type FindPage = { conversationId: string; transcriptRevision: string; occurrences: FindOccurrence[]; total: number; nextCursor?: string; previousCursor?: string; lastCursor?: string; offset?: number; initialIndex?: number };
type SearchState = { phase: "idle" | "searching" | "ready" | "error"; hits: FindOccurrence[]; total: number; error?: string; page?: FindPage; updating?: boolean };
const initial: SearchState = { phase: "idle", hits: [], total: 0 };
const MODAL = "[role='dialog'], [role='alertdialog'], [role='menu'], [aria-modal='true'], [data-keybinding-recording]";
/** A streaming reply changes the transcript constantly; re-search it at this pace. */
const STREAM_REFRESH_MS = 2000;
const OTHER_SURFACE = ".xterm, .terminal-surface, .monaco-editor, .cm-editor, iframe, webview";

function isFindModalOpen(): boolean {
	return Array.from(document.querySelectorAll<HTMLElement>(MODAL)).some((element) =>
		element.dataset.state !== "closed" && !element.closest("[aria-hidden='true']") && element.getClientRects().length > 0 && getComputedStyle(element).visibility !== "hidden");
}

export function useChatFind({ source, root, viewport, hostId, unavailable = false, sideChat = false, pauseFollow, onScrollWritten, reveal }: {
	source: ConversationSnapshot;
	root: RefObject<HTMLDivElement | null>;
	viewport: RefObject<HTMLDivElement | null>;
	hostId?: string;
	unavailable?: boolean;
	sideChat?: boolean;
	pauseFollow: () => void;
	onScrollWritten?: (top: number) => void;
	reveal: (hit: FindOccurrence) => void;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false), [query, setQuery] = useState("");
	const [engaged, setEngaged] = useState(false);
	const engagedRef = useRef(false), barRef = useRef<HTMLDivElement>(null);
	const [composing, setComposing] = useState(false), [retry, setRetry] = useState(0);
	const [state, setState] = useState<SearchState>(initial);
	const stateRef = useRef(state); stateRef.current = state;
	const commit = (value: SearchState) => { stateRef.current = value; setState(value); };
	const [active, setActive] = useState<FindOccurrence>();
	const [windowPage, setWindowPage] = useState<ConversationSnapshot>();
	const windowPageRef = useRef(windowPage);
	windowPageRef.current = windowPage;
	const [announcement, setAnnouncement] = useState("");
	const input = useRef<HTMLInputElement>(null), previousFocus = useRef<HTMLElement | null>(null);
	const sourceRef = useRef(source), activeRef = useRef(active), revealRef = useRef(reveal), pauseRef = useRef(pauseFollow);
	const queryRef = useRef(query), hostRef = useRef(hostId);
	// Where openWith asked the first search to start, instead of the first match
	// on screen: a session search opens its result's message, and find must
	// start at that match rather than wherever the transcript was.
	const pendingAnchor = useRef<string | undefined>(undefined);
	queryRef.current = query; hostRef.current = hostId;
	const writtenRef = useRef(onScrollWritten);
	writtenRef.current = onScrollWritten;
	sourceRef.current = source; activeRef.current = active; revealRef.current = reveal; pauseRef.current = pauseFollow;
	const generation = useRef(0), navigation = useRef(0), windowAbort = useRef<AbortController | null>(null);
	const requestedScroll = useRef<string | undefined>(undefined);
	const lifetime = useRef(0), pagingGeneration = useRef(0), paging = useRef(false);
	const pagingAbort = useRef<AbortController | null>(null), moveQueue = useRef<Promise<void>>(Promise.resolve());
	const searchAbort = useRef<AbortController | null>(null);
	const stopHighlights = useRef<() => void>(() => {});
	const ownedHighlights = useRef<{ all: unknown; active: unknown } | undefined>(undefined);
	const readiness = useRef<Promise<boolean>>(Promise.resolve(true));
	const ready = useRef<((success: boolean) => void) | undefined>(undefined);
	const dirty = useRef(true), streamDirty = useRef(false), lastSearchAt = useRef(0);
	const finishRefresh = useCallback((success: boolean) => { ready.current?.(success); ready.current = undefined; }, []);
	const prepareRefresh = useCallback(() => {
		finishRefresh(false);
		readiness.current = new Promise<boolean>((resolve) => { ready.current = resolve; });
		dirty.current = true;
	}, [finishRefresh]);
	const cancelWork = useCallback(() => {
		// Cancel synchronously on blur; React's effect cleanup can run later.
		generation.current++; navigation.current++; lifetime.current++; pagingGeneration.current++;
		searchAbort.current?.abort(); pagingAbort.current?.abort(); windowAbort.current?.abort();
		paging.current = false; moveQueue.current = Promise.resolve(); requestedScroll.current = undefined;
		stopHighlights.current(); finishRefresh(false);
	}, [finishRefresh]);
	const deactivate = useCallback(() => {
		engagedRef.current = false; cancelWork(); setEngaged(false);
		setState((current) => current.updating ? { ...current, updating: false } : current);
	}, [cancelWork]);
	const activate = useCallback(() => {
		if (engagedRef.current) return;
		engagedRef.current = true; prepareRefresh(); setEngaged(true);
	}, [prepareRefresh]);
	const activateControl = useCallback((event: { target: EventTarget | null }) => {
		if (event.target instanceof Element && event.target.closest("input, button")) activate();
	}, [activate]);
	const clearHighlights = useCallback(() => {
		const registry = (CSS as unknown as { highlights?: Map<string, unknown> }).highlights;
		if (ownedHighlights.current && registry) {
			if (registry.get("chat-find-matches") === ownedHighlights.current.all) registry.delete("chat-find-matches");
			if (registry.get("chat-find-active") === ownedHighlights.current.active) registry.delete("chat-find-active");
		}
		ownedHighlights.current = undefined;
	}, []);
	const identity = `${hostId ?? "local"}:${source.conversationId}`;
	const branch = source.activeBranchId ?? "";
	const previousIdentity = useRef(identity), lastQuery = useRef("");
	const previousBranch = useRef(branch);
	const close = useCallback((restore = true) => {
		deactivate(); clearHighlights();
		setOpen(false); setState(initial); setActive(undefined); setAnnouncement("");
		if (restore) {
			const target = previousFocus.current;
			if (target?.isConnected) target.focus({ preventScroll: true });
			else viewport.current?.focus({ preventScroll: true });
		}
	}, [viewport, deactivate, clearHighlights]);
	useEffect(() => {
		if (previousIdentity.current !== identity) {
			previousIdentity.current = identity;
			close(false); setQuery(""); setWindowPage(undefined); lastQuery.current = "";
		} else if (previousBranch.current !== branch) {
			cancelWork(); clearHighlights(); prepareRefresh();
			setWindowPage(undefined); setActive(undefined); setState(initial); setAnnouncement("");
		}
		previousBranch.current = branch;
	}, [identity, branch, close, cancelWork, clearHighlights, prepareRefresh]);
	const canHandle = useCallback(() => {
		const element = root.current;
		if (!element || !element.getClientRects().length || isSettingsPageOpen()) return false;
		if (isFindModalOpen()) return false;
		const focused = document.activeElement;
		if (focused instanceof Element && focused.closest(OTHER_SURFACE)) return false;
		const focusedChat = focused instanceof Element ? focused.closest("[data-chat-find-scope], [data-chat-find-surface]") : null;
		if (focusedChat) return focusedChat === element || focusedChat === element.closest("[data-chat-find-scope]");
		if (focused instanceof Element && focused.closest("input, textarea, [contenteditable='true'], [role='textbox']")) return false;
		// Prefer the primary selected transcript when chrome holds focus.
		return Array.from(document.querySelectorAll<HTMLElement>("[data-chat-find-surface]")).find((candidate) => candidate.getClientRects().length > 0) === element;
	}, [root]);
	const show = useCallback(() => {
		if (!open) {
			previousFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
			document.dispatchEvent(new CustomEvent("chat-find-open", { detail: root.current }));
		}
		setOpen(true); activate();
		requestAnimationFrame(() => { input.current?.focus(); input.current?.select(); });
	}, [open, root, activate]);
	useAppShortcut("find-in-chat", show, true, canHandle);
	// Opens find already searching for value, its first match at or after
	// anchorMessageId (a session search result opening its session).
	const openWith = useCallback((value: string, anchorMessageId?: string) => {
		cancelWork(); prepareRefresh();
		// A new search from the anchor even when value is the current query:
		// otherwise the search would keep its active match instead.
		pendingAnchor.current = anchorMessageId; activeRef.current = undefined; lastQuery.current = "";
		setQuery(value); setAnnouncement(""); setActive(undefined);
		setState({ ...initial, phase: isEmptyFindQuery(value) ? "idle" : "searching" });
		show();
	}, [cancelWork, prepareRefresh, show]);
	useEffect(() => {
		const otherOpened = (event: Event) => { if ((event as CustomEvent).detail !== root.current) close(false); };
		document.addEventListener("chat-find-open", otherOpened);
		return () => document.removeEventListener("chat-find-open", otherOpened);
	}, [close, root]);

	const go = useCallback(async (hit: FindOccurrence, scroll = true) => {
		if (!engagedRef.current) return;
		const token = ++navigation.current;
		windowAbort.current?.abort();
		const controller = new AbortController(); windowAbort.current = controller;
		setActive(hit); activeRef.current = hit;
		if (scroll) { pauseRef.current(); requestedScroll.current = occurrenceKey(hit); }
		const snapshot = sourceRef.current;
		const navigationQuery = queryRef.current;
		const priorPage = windowPageRef.current;
		const windowItems = priorPage?.conversationId === snapshot.conversationId && priorPage.activeBranchId === snapshot.activeBranchId ? priorPage.items : [];
		const loaded = [...snapshot.items, ...windowItems].some((item) => item.kind === "message" && item.id === hit.messageId && (item.revision === hit.revision || item.streaming));
		if (!loaded || !scroll && Boolean(priorPage)) {
			try {
				const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/conversations/{conversationId}/find-window", {
					params: { path: { conversationId: snapshot.conversationId }, query: { sequence: hit.sequence } }, signal: controller.signal,
				});
				if (!engagedRef.current || controller.signal.aborted || token !== navigation.current || queryRef.current !== navigationQuery || hostRef.current !== hostId || sourceRef.current.conversationId !== snapshot.conversationId || sourceRef.current.activeBranchId !== snapshot.activeBranchId) return;
				if (error || !data) throw error ?? new Error(t("chat.find.historyUnavailable"));
				const page = toSnapshot(data);
				if (page.conversationId !== snapshot.conversationId || page.activeBranchId !== snapshot.activeBranchId || !page.items.some((item) => item.kind === "message" && item.id === hit.messageId && item.revision === hit.revision)) throw new Error(t("chat.find.resultChanged"));
				setWindowPage(page);
			} catch (error) {
				if (!controller.signal.aborted && token === navigation.current) setState((current) => ({ ...current, phase: "error", error: apiErrorMessage(error) }));
				return;
			}
		}
		if (engagedRef.current && scroll && token === navigation.current) revealRef.current(hit);
	}, [hostId, t]);

	// Load only the occurrence page around the reader or active hit. A common
	// query can match millions of times; counts must not imply downloading them.
	useEffect(() => {
		if (source.items.some((item) => item.kind === "message" && item.streaming)) streamDirty.current = true;
		else dirty.current = true;
	}, [source]);
	useEffect(() => {
		if (!open || !engaged || composing) return;
		const controller = new AbortController();
		searchAbort.current = controller;
		let stopped = false, running = false;
		const run = async () => {
			const streamDue = streamDirty.current && performance.now() - lastSearchAt.current >= STREAM_REFRESH_MS;
			if (!engagedRef.current || stopped || running || paging.current || !dirty.current && !streamDue) return;
			dirty.current = false; streamDirty.current = false; lastSearchAt.current = performance.now();
			if (isEmptyFindQuery(query)) { commit(initial); setActive(undefined); finishRefresh(true); return; }
			if (unavailable) { commit({ ...initial, phase: "error", error: t("chat.find.transportUnavailable") }); finishRefresh(false); return; }
			if (!sourceRef.current.conversationId && !sourceRef.current.items.length) { commit({ ...initial, phase: "ready" }); setActive(undefined); finishRefresh(true); return; }
			if (Array.from(query).length > 512) { commit({ ...initial, phase: "error", error: t("chat.find.queryTooLong") }); finishRefresh(false); return; }
			running = true;
			const token = ++generation.current;
			const current = stateRef.current;
			commit(current.phase === "ready" && lastQuery.current === query ? { ...current, updating: true } : { ...current, phase: "searching", error: undefined });
			try {
				const snapshot = sourceRef.current;
				const changedQuery = lastQuery.current !== query;
				const prior = changedQuery ? undefined : activeRef.current;
				let anchorMessageId = prior?.messageId, anchorBlock = prior?.block ?? 0, anchorOffset = prior?.start ?? 0;
				if (!prior && pendingAnchor.current) {
					anchorMessageId = pendingAnchor.current;
					pendingAnchor.current = undefined;
				} else if (!prior) {
					const top = viewport.current?.getBoundingClientRect().top ?? 0;
					outer: for (const element of root.current?.querySelectorAll<HTMLElement>("[data-find-message-id]") ?? []) {
						if (element.getBoundingClientRect().bottom < top) continue;
						anchorMessageId ??= element.dataset.findMessageId;
						const body = element.querySelector<HTMLElement>("[data-chat-message-body]");
						if (!body) continue;
						const blocks = renderedFindBlocks(body);
						for (let block = 0; block < blocks.length; block++) for (const range of findTextRanges(blocks[block].text, query)) {
							const rect = findRange(blocks[block].nodes, range.start, range.end)?.getBoundingClientRect();
							if (rect && rect.bottom >= top) { anchorMessageId = element.dataset.findMessageId; anchorBlock = block; anchorOffset = range.start; break outer; }
						}
					}
				}
				const result = await clientForSessionHost(hostId).GET("/api/v1/conversations/{conversationId}/find", {
					params: { path: { conversationId: snapshot.conversationId }, query: { query, anchorMessageId, anchorBlock, anchorOffset, anchorSequence: prior?.sequence, fallbackToPrevious: Boolean(prior) } }, signal: controller.signal,
				});
				if (!engagedRef.current || controller.signal.aborted || stopped || token !== generation.current || queryRef.current !== query || hostRef.current !== hostId || sourceRef.current.conversationId !== snapshot.conversationId || sourceRef.current.activeBranchId !== snapshot.activeBranchId) return;
				if (result.error || !result.data) throw result.error ?? new Error(t("chat.find.searchUnavailable"));
				const page: FindPage = result.data;
				const hits = page.occurrences;
				const next = prior && hits.find((hit) => occurrenceKey(hit) === occurrenceKey(prior)) || hits[(page.initialIndex ?? page.offset ?? 0) - (page.offset ?? 0)] || hits[0];
				if (!changedQuery && prior && (!next || occurrenceKey(next) !== occurrenceKey(prior))) setAnnouncement(t("chat.find.activeChanged"));
				lastQuery.current = query;
				commit({ phase: "ready", hits, total: page.total, page });
				if (windowPageRef.current && prior && !hits.some((hit) => hit.messageId === prior.messageId)) setWindowPage(undefined);
				if (next && (changedQuery || !prior || occurrenceKey(next) !== occurrenceKey(prior))) await go(next);
				else {
					setActive(next);
					if (windowPageRef.current && next && !requestedScroll.current) await go(next, false);
				}
				if (!stopped && engagedRef.current && token === generation.current) finishRefresh(true);
			} catch (error) {
				if (!stopped && engagedRef.current && !controller.signal.aborted && token === generation.current) {
					commit({ ...stateRef.current, phase: "error", updating: false, error: apiErrorMessage(error) || t("chat.find.searchFailed") }); finishRefresh(false);
				}
			} finally { running = false; }
		};
		dirty.current = true;
		const timer = window.setInterval(() => void run(), 150);
		return () => { stopped = true; controller.abort(); window.clearInterval(timer); };
	}, [open, engaged, composing, query, identity, branch, retry, hostId, go, root, viewport, unavailable, finishRefresh, t]);

	const move = (direction: number) => {
		activate();
		const refreshed = readiness.current;
		const epoch = lifetime.current;
		moveQueue.current = moveQueue.current.catch(() => {}).then(async () => {
			if (!await refreshed || !engagedRef.current || epoch !== lifetime.current) return;
			const current = stateRef.current;
			if (current.phase !== "ready" || !current.total || !current.page) return;
			let page = current.page;
			const offset = page.offset ?? 0;
			const localIndex = activeRef.current ? current.hits.findIndex((hit) => occurrenceKey(hit) === occurrenceKey(activeRef.current!)) : 0;
			const desired = (offset + Math.max(0, localIndex) + direction + current.total) % current.total;
			if (desired < offset || desired >= offset + current.hits.length) {
				const token = ++pagingGeneration.current;
				const controller = new AbortController(); pagingAbort.current = controller; paging.current = true;
				const cursor = desired === 0 ? undefined : desired === current.total - 1 ? page.lastCursor : direction > 0 ? page.nextCursor : page.previousCursor;
				commit({ ...current, updating: true });
				try {
					const result = await clientForSessionHost(hostId).GET("/api/v1/conversations/{conversationId}/find", {
						params: { path: { conversationId: sourceRef.current.conversationId }, query: { query: queryRef.current, cursor } }, signal: controller.signal,
					});
					if (epoch !== lifetime.current || controller.signal.aborted) return;
					if (result.response.status === 409 || result.data && result.data.transcriptRevision !== page.transcriptRevision) { prepareRefresh(); setRetry((value) => value + 1); setAnnouncement(t("chat.find.resultsChanged")); return; }
					if (result.error || !result.data) throw result.error ?? new Error(t("chat.find.nextFailed"));
					page = result.data;
					commit({ phase: "ready", hits: page.occurrences, total: page.total, page });
				} catch (error) {
					if (!controller.signal.aborted && epoch === lifetime.current) commit({ ...stateRef.current, phase: "error", updating: false, error: apiErrorMessage(error) });
					return;
				} finally { if (token === pagingGeneration.current) paging.current = false; }
			}
			if (epoch !== lifetime.current) return;
			const hit = page.occurrences[desired - (page.offset ?? 0)];
			if (!hit) return;
			setAnnouncement(desired === 0 && direction > 0 ? t("chat.find.wrappedFirst") : desired === current.total - 1 && direction < 0 ? t("chat.find.wrappedLast") : "");
			await go(hit); input.current?.focus({ preventScroll: true });
		});
	};

	useEffect(() => {
		if (!open) return;
		const visibility = () => { if (document.hidden) deactivate(); };
		window.addEventListener("blur", deactivate); document.addEventListener("visibilitychange", visibility);
		return () => { window.removeEventListener("blur", deactivate); document.removeEventListener("visibilitychange", visibility); };
	}, [open, deactivate]);
	useEffect(() => clearHighlights, [open, identity, branch, query, clearHighlights]);
	useEffect(() => {
		if (!open) return;
		const surface = root.current?.closest("[data-chat-find-scope]") ?? root.current;
		if (!surface) return;
		const escape = (event: KeyboardEvent) => {
			if (event.key === "Escape" && !event.isComposing && !composing && !isFindModalOpen()) { event.preventDefault(); event.stopPropagation(); close(); }
		};
		surface.addEventListener("keydown", escape as EventListener, true);
		return () => surface.removeEventListener("keydown", escape as EventListener, true);
	}, [open, close, composing, root]);

	useEffect(() => {
		const surface = root.current;
		if (!open || !engaged || !surface || isEmptyFindQuery(query)) return;
		const registry = (CSS as unknown as { highlights?: Map<string, unknown> }).highlights;
		const HighlightClass = (globalThis as unknown as { Highlight?: new () => { add: (range: Range) => void } }).Highlight;
		let timer = 0;
		const matchedMessages = new Set([...source.items, ...(windowPage?.items ?? [])].filter((item) => item.kind === "message" && item.origin !== "automation" && item.origin !== "daemon" && !item.continuation).map((item) => item.id));
		const paint = () => {
			if (!engagedRef.current || dirty.current || stateRef.current.phase !== "ready" || stateRef.current.updating) return;
			const ranges: Range[] = [], selected: Range[] = [];
			for (const element of surface.querySelectorAll<HTMLElement>("[data-find-message-id]")) {
				if (!matchedMessages.has(element.dataset.findMessageId ?? "")) continue;
				const body = element.querySelector<HTMLElement>("[data-chat-message-body]");
				if (!body) continue;
				renderedFindBlocks(body).forEach((block, index) => {
					for (const hit of findTextRanges(block.text, query)) {
						const range = findRange(block.nodes, hit.start, hit.end);
						if (!range) continue;
						ranges.push(range);
						if (active && active.messageId === element.dataset.findMessageId && active.block === index && active.start === hit.start && active.end === hit.end) selected.push(range);
					}
				});
			}
			if (HighlightClass && registry) {
				const all = new HighlightClass(), current = new HighlightClass();
				for (const range of ranges) all.add(range);
				for (const range of selected) current.add(range);
				ownedHighlights.current = { all, active: current };
				registry.set("chat-find-matches", all); registry.set("chat-find-active", current);
			}
			if (active && selected[0] && requestedScroll.current === occurrenceKey(active)) {
				const code = selected[0].startContainer.parentElement?.closest("pre");
				if (code) {
					const match = selected[0].getBoundingClientRect(), bounds = code.getBoundingClientRect();
					if (match.left < bounds.left || match.right > bounds.right) code.scrollLeft += match.left + match.width / 2 - bounds.left - bounds.width / 2;
				}
				const rect = selected[0].getBoundingClientRect(), scroll = viewport.current;
				// A long user prompt folds to ten lines; open it when the match is under the fold.
				const fold = selected[0].startContainer.parentElement?.closest<HTMLElement>(".chat-human-fold[data-folded]");
				if (fold?.id && rect.bottom > fold.getBoundingClientRect().bottom) surface.querySelector<HTMLElement>(`[aria-controls="${CSS.escape(fold.id)}"]`)?.click();
				if (scroll) { const bounds = scroll.getBoundingClientRect(); scroll.scrollTo({ top: scroll.scrollTop + rect.top + rect.height / 2 - bounds.top - bounds.height / 2, behavior: "auto" }); writtenRef.current?.(scroll.scrollTop); }
				requestedScroll.current = undefined;
			}
		};
		const schedule = () => { if (engagedRef.current && !timer) timer = window.setTimeout(() => { timer = 0; paint(); }, 150); };
		paint();
		const observer = new MutationObserver(schedule); observer.observe(surface, { subtree: true, childList: true, characterData: true });
		const resize = new ResizeObserver(schedule); if (viewport.current) resize.observe(viewport.current);
		const timeout = window.setTimeout(() => {
			if (engagedRef.current && active && requestedScroll.current === occurrenceKey(active)) {
				requestedScroll.current = undefined;
				setState((current) => ({ ...current, phase: "error", error: t("chat.find.notRevealed") }));
			}
		}, 3000);
		// Pausing retains the existing browser ranges, without rescanning or copying text.
		const disconnect = () => { observer.disconnect(); resize.disconnect(); window.clearTimeout(timer); window.clearTimeout(timeout); };
		stopHighlights.current = disconnect;
		return disconnect;
	}, [open, engaged, query, active, state.page, windowPage, root, viewport, t]);
	useEffect(() => () => { engagedRef.current = false; cancelWork(); clearHighlights(); }, [cancelWork, clearHighlights]);

	// Closing find leaves the reader in place; the jumped-to page goes when they return to the latest.
	const dropWindow = useCallback(() => setWindowPage(undefined), []);
	// Shows the history around one sequence without opening find: a search match
	// deep in a long chat loads its page in one request rather than paging back
	// through everything newer. Resolves once the page is shown or could not be.
	const showWindowAt = useCallback(async (sequence: number) => {
		windowAbort.current?.abort();
		const controller = new AbortController(); windowAbort.current = controller;
		const snapshot = sourceRef.current;
		try {
			const { data } = await clientForSessionHost(hostId).GET("/api/v1/conversations/{conversationId}/find-window", {
				params: { path: { conversationId: snapshot.conversationId }, query: { sequence } }, signal: controller.signal,
			});
			if (!data || controller.signal.aborted || sourceRef.current.conversationId !== snapshot.conversationId || sourceRef.current.activeBranchId !== snapshot.activeBranchId) return;
			const page = toSnapshot(data);
			if (page.conversationId === snapshot.conversationId && page.activeBranchId === snapshot.activeBranchId) setWindowPage(page);
		} catch {
			// The caller falls back to the turn the match belongs to.
		}
	}, [hostId]);
	let snapshot = windowPage && windowPage.conversationId === source.conversationId && windowPage.activeBranchId === source.activeBranchId
		? mergeConversationPages([source, windowPage]) ?? source : source;
	if (sideChat && snapshot !== source) {
		let previous = -Infinity;
		snapshot = { ...snapshot, items: [...snapshot.items].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.sequence - b.sequence).map((item) => {
			previous = Math.max(Date.parse(item.createdAt), previous + 0.001);
			return { ...item, sequence: previous };
		}) };
	}
	const activeIndex = useMemo(() => active ? (state.page?.offset ?? 0) + state.hits.findIndex((hit) => occurrenceKey(hit) === occurrenceKey(active)) + 1 : 0, [active, state.page, state.hits]);
	// The update notice shares the top-right corner; step left of it, as the session switch does.
	const noticeClearance = useHarnessUpdateNoticeClearance(open ? root.current : null);
	const newestWindowSequence = windowPage?.items.at(-1)?.sequence, firstSourceSequence = source.items[0]?.sequence;
	const gapBeforeSequence = !sideChat && source.hasMoreBefore && newestWindowSequence !== undefined && firstSourceSequence !== undefined && newestWindowSequence < firstSourceSequence ? firstSourceSequence : undefined;
	const bar = open ? <div ref={barRef} role="search" style={noticeClearance ? { right: noticeClearance, maxWidth: `calc(100% - 12px - ${noticeClearance}px)` } : undefined} onFocusCapture={activate} onPointerDownCapture={activateControl} onClickCapture={activateControl} onKeyDownCapture={activate} onBlurCapture={(event) => {
		if (event.relatedTarget instanceof Node) { if (!event.currentTarget.contains(event.relatedTarget)) deactivate(); }
		else queueMicrotask(() => { if (!barRef.current?.contains(document.activeElement)) deactivate(); });
	}} aria-label={t("shortcut.find-in-chat")} aria-busy={engaged && (state.updating || state.phase === "searching")} className="z-overlay absolute top-3 right-3 flex w-[320px] max-w-[calc(100%-24px)] shrink-0 items-center gap-1 rounded-lg border border-(--color-border-overlay) bg-overlay px-3 py-2 text-sm leading-5 shadow-(--shadow-popover) focus-within:ring-1 focus-within:ring-ring/40">
		<Search aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
		<input ref={input} type="search" aria-label={t("shortcut.find-in-chat")} placeholder={t("shortcut.find-in-chat")} value={query} className="min-w-0 flex-1 bg-transparent px-1 outline-none [&::-webkit-search-cancel-button]:hidden" onChange={(event) => {
			const value = event.target.value;
			cancelWork(); activate(); prepareRefresh();
			setQuery(value); setAnnouncement(""); setActive(undefined); setState({ ...initial, phase: isEmptyFindQuery(value) ? "idle" : "searching" });
		}} onCompositionStart={() => { cancelWork(); setComposing(true); }} onCompositionEnd={() => { prepareRefresh(); setComposing(false); }} onKeyDown={(event) => {
			if (event.nativeEvent.isComposing || composing) return;
			if (event.key === "Enter") { event.preventDefault(); event.stopPropagation(); move(event.shiftKey ? -1 : 1); }
		}} />
		<span role="status" aria-live="polite" className={`inline-flex min-h-4 min-w-16 max-w-24 shrink-0 items-center ${state.phase === "searching" ? "justify-center" : "justify-end"} overflow-hidden px-2 whitespace-nowrap text-muted-foreground tabular-nums`}>
			{state.phase === "searching" ? engaged ? <><Loader2 aria-hidden="true" className="size-4 shrink-0 animate-spin text-muted-foreground motion-reduce:animate-none" /><span className="sr-only">{t("command.searching")}</span></> : null : state.phase === "error" ? t("chat.find.incomplete") : isEmptyFindQuery(query) ? "" : state.total ? <><span aria-hidden="true">{activeIndex}/{state.total}</span><span className="sr-only">{t("browser.find.matchCount", { active: activeIndex, total: state.total })}</span></> : state.phase === "ready" ? t("browser.find.noResults") : ""}
		</span>
		<div className="flex shrink-0 items-center gap-1 border-l border-border pl-2">
			<Button type="button" variant="quiet" size="icon-sm" aria-label={t("browser.find.previous")} disabled={state.phase !== "ready" || !state.total} onClick={() => move(-1)} className="focus-visible:ring-1 focus-visible:ring-ring"><ChevronUp className="size-3.5" /></Button>
			<Button type="button" variant="quiet" size="icon-sm" aria-label={t("browser.find.next")} disabled={state.phase !== "ready" || !state.total} onClick={() => move(1)} className="focus-visible:ring-1 focus-visible:ring-ring"><ChevronDown className="size-3.5" /></Button>
			<Button type="button" variant="quiet" size="icon-sm" aria-label={t("browser.find.close")} onClick={() => close()} className="focus-visible:ring-1 focus-visible:ring-ring"><X className="size-3.5" /></Button>
		</div>
		{state.error ? <span role="alert" className="absolute top-full right-0 mt-2 w-full rounded-lg border border-border bg-surface p-2 shadow-lg">{state.error} <button type="button" className="underline" onClick={() => { prepareRefresh(); setRetry((value) => value + 1); }}>{t("notify.retry")}</button></span> : null}
		<span className="sr-only" role="status" aria-live="polite">{announcement}</span>
	</div> : null;
	return { snapshot, bar, active, open, engaged, gapBeforeSequence, dropWindow, showWindowAt, openWith };
}
