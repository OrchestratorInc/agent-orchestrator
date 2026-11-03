import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { ChevronDown, ChevronUp, Search, X } from "lucide-react";
import { useAppShortcut, isSettingsPageOpen } from "./useAppShortcut";
import { toSnapshot, mergeConversationPages } from "./useConversation";
import { clientForSessionHost } from "../lib/host-clients";
import { apiErrorMessage } from "../lib/api-client";
import { findTextRanges, isEmptyFindQuery, occurrenceKey, renderedFindBlocks, findRange, type FindOccurrence } from "../lib/chat-find";
import type { ConversationSnapshot } from "../types/conversation";

type FindPage = { conversationId: string; transcriptRevision: string; occurrences: FindOccurrence[]; total: number; nextCursor?: string; previousCursor?: string; lastCursor?: string; offset?: number; initialIndex?: number };
type SearchState = { phase: "idle" | "searching" | "ready" | "error"; hits: FindOccurrence[]; total: number; error?: string; page?: FindPage; updating?: boolean };
const initial: SearchState = { phase: "idle", hits: [], total: 0 };
const MODAL = "[role='dialog'], [role='alertdialog'], [role='menu'], [aria-modal='true'], [data-keybinding-recording]";
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
	const [open, setOpen] = useState(false), [query, setQuery] = useState("");
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
	queryRef.current = query; hostRef.current = hostId;
	const writtenRef = useRef(onScrollWritten);
	writtenRef.current = onScrollWritten;
	sourceRef.current = source; activeRef.current = active; revealRef.current = reveal; pauseRef.current = pauseFollow;
	const generation = useRef(0), navigation = useRef(0), windowAbort = useRef<AbortController | null>(null);
	const requestedScroll = useRef<string | undefined>(undefined);
	const lifetime = useRef(0), pagingGeneration = useRef(0), paging = useRef(false);
	const pagingAbort = useRef<AbortController | null>(null), moveQueue = useRef<Promise<void>>(Promise.resolve());
	const identity = `${hostId ?? "local"}:${source.conversationId}`;
	const branch = source.activeBranchId ?? "";
	const previousIdentity = useRef(identity), lastQuery = useRef("");
	const previousBranch = useRef(branch);
	const close = useCallback((restore = true) => {
		generation.current++; navigation.current++; lifetime.current++; pagingGeneration.current++; pagingAbort.current?.abort(); paging.current = false; moveQueue.current = Promise.resolve(); windowAbort.current?.abort(); requestedScroll.current = undefined;
		setOpen(false); setState(initial); setActive(undefined); setAnnouncement("");
		if (restore) {
			const target = previousFocus.current;
			if (target?.isConnected) target.focus({ preventScroll: true });
			else viewport.current?.focus({ preventScroll: true });
		}
	}, [viewport]);
	useEffect(() => {
		if (previousIdentity.current !== identity) {
			previousIdentity.current = identity;
			close(false); setQuery(""); setWindowPage(undefined); lastQuery.current = "";
		} else if (previousBranch.current !== branch) {
			setWindowPage(undefined); setActive(undefined); setState(initial); setAnnouncement("Conversation branch changed. Updating matches.");
		}
		previousBranch.current = branch;
	}, [identity, branch, close]);
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
		setOpen(true);
		requestAnimationFrame(() => { input.current?.focus(); input.current?.select(); });
	}, [open, root]);
	useAppShortcut("find-in-chat", show, true, canHandle);
	useEffect(() => {
		const otherOpened = (event: Event) => { if ((event as CustomEvent).detail !== root.current) close(false); };
		document.addEventListener("chat-find-open", otherOpened);
		return () => document.removeEventListener("chat-find-open", otherOpened);
	}, [close, root]);

	const go = useCallback(async (hit: FindOccurrence, scroll = true) => {
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
				if (token !== navigation.current || queryRef.current !== navigationQuery || hostRef.current !== hostId || sourceRef.current.conversationId !== snapshot.conversationId || sourceRef.current.activeBranchId !== snapshot.activeBranchId) return;
				if (error || !data) throw error ?? new Error("History is unavailable.");
				const page = toSnapshot(data);
				if (page.conversationId !== snapshot.conversationId || page.activeBranchId !== snapshot.activeBranchId || !page.items.some((item) => item.kind === "message" && item.id === hit.messageId && item.revision === hit.revision)) throw new Error("This result changed. Retry the search.");
				setWindowPage(page);
			} catch (error) {
				if (!controller.signal.aborted && token === navigation.current) setState((current) => ({ ...current, phase: "error", error: apiErrorMessage(error) }));
				return;
			}
		}
		if (scroll && token === navigation.current) revealRef.current(hit);
	}, [hostId]);

	// Load only the occurrence page around the reader or active hit. A common
	// query can match millions of times; counts must not imply downloading them.
	const dirty = useRef(true);
	useEffect(() => { dirty.current = true; }, [source]);
	useEffect(() => {
		if (!open || composing) return;
		const controller = new AbortController();
		let stopped = false, running = false;
		const run = async () => {
			if (stopped || running || paging.current || !dirty.current) return;
			dirty.current = false;
			if (isEmptyFindQuery(query)) { commit(initial); setActive(undefined); return; }
			if (unavailable) { commit({ ...initial, phase: "error", error: "Full-history search is not available for this chat transport." }); return; }
			if (!sourceRef.current.conversationId && !sourceRef.current.items.length) { commit({ ...initial, phase: "ready" }); setActive(undefined); return; }
			if (Array.from(query).length > 512) { commit({ ...initial, phase: "error", error: "Search supports up to 512 characters." }); return; }
			running = true;
			const token = ++generation.current;
			const current = stateRef.current;
			commit(current.phase === "ready" && lastQuery.current === query ? { ...current, updating: true } : { ...current, phase: "searching", error: undefined });
			try {
				const snapshot = sourceRef.current;
				const changedQuery = lastQuery.current !== query;
				const prior = changedQuery ? undefined : activeRef.current;
				let anchorMessageId = prior?.messageId, anchorBlock = prior?.block ?? 0, anchorOffset = prior?.start ?? 0;
				if (!prior) {
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
				if (stopped || token !== generation.current || queryRef.current !== query || hostRef.current !== hostId || sourceRef.current.conversationId !== snapshot.conversationId || sourceRef.current.activeBranchId !== snapshot.activeBranchId) return;
				if (result.error || !result.data) throw result.error ?? new Error("Complete history search is unavailable for this chat.");
				const page: FindPage = result.data;
				const hits = page.occurrences;
				const next = prior && hits.find((hit) => occurrenceKey(hit) === occurrenceKey(prior)) || hits[(page.initialIndex ?? page.offset ?? 0) - (page.offset ?? 0)] || hits[0];
				if (!changedQuery && prior && (!next || occurrenceKey(next) !== occurrenceKey(prior))) setAnnouncement("The active match changed. Moved to the nearest available match.");
				lastQuery.current = query;
				commit({ phase: "ready", hits, total: page.total, page });
				if (windowPageRef.current && prior && !hits.some((hit) => hit.messageId === prior.messageId)) setWindowPage(undefined);
				if (next && (changedQuery || !prior || occurrenceKey(next) !== occurrenceKey(prior))) void go(next);
				else {
					setActive(next);
					if (windowPageRef.current && next && !requestedScroll.current) void go(next, false);
				}
			} catch (error) {
				if (!stopped && !controller.signal.aborted) commit({ ...stateRef.current, phase: "error", updating: false, error: apiErrorMessage(error) || "Could not search complete history. Retry." });
			} finally { running = false; }
		};
		dirty.current = true;
		const timer = window.setInterval(() => void run(), 150);
		return () => { stopped = true; controller.abort(); generation.current++; navigation.current++; lifetime.current++; pagingGeneration.current++; pagingAbort.current?.abort(); paging.current = false; moveQueue.current = Promise.resolve(); windowAbort.current?.abort(); requestedScroll.current = undefined; window.clearInterval(timer); };
	}, [open, composing, query, identity, branch, retry, hostId, go, root, viewport, unavailable]);

	const move = (direction: number) => {
		const epoch = lifetime.current;
		moveQueue.current = moveQueue.current.catch(() => {}).then(async () => {
			if (epoch !== lifetime.current) return;
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
					if (result.response.status === 409 || result.data && result.data.transcriptRevision !== page.transcriptRevision) { dirty.current = true; setRetry((value) => value + 1); setAnnouncement("Results changed. Updating matches."); return; }
					if (result.error || !result.data) throw result.error ?? new Error("Could not load the next matches.");
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
			setAnnouncement(desired === 0 && direction > 0 ? "Wrapped to the first match." : desired === current.total - 1 && direction < 0 ? "Wrapped to the last match." : "");
			await go(hit); input.current?.focus({ preventScroll: true });
		});
	};

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
		if (!open || !surface || isEmptyFindQuery(query)) return;
		const registry = (CSS as unknown as { highlights?: Map<string, unknown> }).highlights;
		const HighlightClass = (globalThis as unknown as { Highlight?: new () => { add: (range: Range) => void } }).Highlight;
		let timer = 0;
		let owned: unknown, ownedActive: unknown;
		const matchedMessages = new Set([...source.items, ...(windowPage?.items ?? [])].filter((item) => item.kind === "message" && item.origin !== "automation" && item.origin !== "daemon" && !item.continuation).map((item) => item.id));
		const paint = () => {
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
				owned = all; ownedActive = current;
				registry.set("chat-find-matches", owned); registry.set("chat-find-active", ownedActive);
			}
			if (active && selected[0] && requestedScroll.current === occurrenceKey(active)) {
				const code = selected[0].startContainer.parentElement?.closest("pre");
				if (code) {
					const match = selected[0].getBoundingClientRect(), bounds = code.getBoundingClientRect();
					if (match.left < bounds.left || match.right > bounds.right) code.scrollLeft += match.left + match.width / 2 - bounds.left - bounds.width / 2;
				}
				const rect = selected[0].getBoundingClientRect(), scroll = viewport.current;
				if (scroll) { const bounds = scroll.getBoundingClientRect(); scroll.scrollTo({ top: scroll.scrollTop + rect.top + rect.height / 2 - bounds.top - bounds.height / 2, behavior: "auto" }); writtenRef.current?.(scroll.scrollTop); }
				requestedScroll.current = undefined;
			}
		};
		const schedule = () => { if (!timer) timer = window.setTimeout(() => { timer = 0; paint(); }, 150); };
		paint();
		const observer = new MutationObserver(schedule); observer.observe(surface, { subtree: true, childList: true, characterData: true });
		const resize = new ResizeObserver(schedule); if (viewport.current) resize.observe(viewport.current);
		const timeout = window.setTimeout(() => {
			if (active && requestedScroll.current === occurrenceKey(active)) {
				requestedScroll.current = undefined;
				setState((current) => ({ ...current, phase: "error", error: "This match could not be revealed. Retry the search." }));
			}
		}, 3000);
		return () => {
			observer.disconnect(); resize.disconnect(); window.clearTimeout(timer); window.clearTimeout(timeout);
			if (registry && registry.get("chat-find-matches") === owned) registry.delete("chat-find-matches");
			if (registry && registry.get("chat-find-active") === ownedActive) registry.delete("chat-find-active");
		};
	}, [open, query, active, source.items, windowPage, root, viewport]);
	useEffect(() => () => { windowAbort.current?.abort(); pagingAbort.current?.abort(); navigation.current++; generation.current++; lifetime.current++; }, []);

	let snapshot = windowPage && windowPage.conversationId === source.conversationId && windowPage.activeBranchId === source.activeBranchId
		? mergeConversationPages([source, windowPage]) ?? source : source;
	if (sideChat && snapshot !== source) {
		let previous = -Infinity;
		snapshot = { ...snapshot, items: [...snapshot.items].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.sequence - b.sequence).map((item) => {
			previous = Math.max(Date.parse(item.createdAt), previous + 0.001);
			return { ...item, sequence: previous };
		}) };
	}
	const activeIndex = active ? (state.page?.offset ?? 0) + state.hits.findIndex((hit) => occurrenceKey(hit) === occurrenceKey(active)) + 1 : 0;
	const bar = open ? <div role="search" aria-label="Find in chat" aria-busy={state.updating || state.phase === "searching"} className="relative z-20 flex shrink-0 flex-wrap items-center gap-1 border-b border-border bg-surface px-3 py-2 text-xs">
		<Search aria-hidden="true" className="size-4" />
		<input ref={input} type="search" aria-label="Find in chat" placeholder="Find in chat" value={query} className="min-w-20 flex-1 bg-transparent px-2 py-1 outline-none focus-visible:ring-1 focus-visible:ring-ring" onChange={(event) => {
			const value = event.target.value;
			setQuery(value); setAnnouncement(""); setActive(undefined); setState({ ...initial, phase: isEmptyFindQuery(value) ? "idle" : "searching" });
		}} onCompositionStart={() => setComposing(true)} onCompositionEnd={() => setComposing(false)} onKeyDown={(event) => {
			if (event.nativeEvent.isComposing || composing) return;
			if (event.key === "Enter") { event.preventDefault(); event.stopPropagation(); move(event.shiftKey ? -1 : 1); }
		}} />
		<span role="status" aria-live="polite">{state.phase === "searching" ? state.total ? `Loading matches… (${state.total} total)` : "Searching…" : state.phase === "error" ? "Search incomplete" : isEmptyFindQuery(query) ? "" : state.total ? `${activeIndex} of ${state.total}${state.updating ? " (updating)" : ""}` : state.phase === "ready" ? "No results" : ""}</span>
		<button type="button" aria-label="Previous match" disabled={state.phase !== "ready" || !state.total} onClick={() => move(-1)} className="rounded p-1 disabled:opacity-40"><ChevronUp className="size-4" /></button>
		<button type="button" aria-label="Next match" disabled={state.phase !== "ready" || !state.total} onClick={() => move(1)} className="rounded p-1 disabled:opacity-40"><ChevronDown className="size-4" /></button>
		<button type="button" aria-label="Close find in chat" onClick={() => close()} className="rounded p-1"><X className="size-4" /></button>
		{state.error ? <span role="alert" className="basis-full">{state.error} <button type="button" className="underline" onClick={() => setRetry((value) => value + 1)}>Retry</button></span> : null}
		<span className="sr-only" role="status" aria-live="polite">{announcement}</span>
	</div> : <button type="button" aria-label="Find in chat" title="Find in chat" className="absolute right-10 top-1 z-20 rounded bg-surface p-1 text-muted-foreground hover:text-foreground" onMouseDown={(event) => event.preventDefault()} onClick={show}><Search aria-hidden="true" className="size-4" /></button>;
	return { snapshot, bar, active, open };
}
