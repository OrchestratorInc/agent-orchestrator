import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { FindBar } from "./FindBar";
import { useAppShortcut, isSettingsPageOpen } from "../hooks/useAppShortcut";
import { artifactFindOwner, claimArtifactFind, registerArtifactFindRoot } from "../lib/artifact-find-focus";
import { createArtifactFindEngine, foldExceptions, readArtifactFindResult, type ArtifactFindAdapter, type ArtifactFindMessage, type ArtifactFindResult } from "../lib/artifact-find";
import { currentShortcutBindings, useKeybindingsStore } from "../stores/keybindings-store";
import { isEmptyFindQuery } from "../lib/chat-find";

type RegisterAdapter = (root: HTMLElement, adapter: ArtifactFindAdapter) => () => void;
const AdapterContext = createContext<RegisterAdapter | undefined>(undefined);
export const useArtifactFindRegistration = () => useContext(AdapterContext);

function visible(element: Element): boolean {
	return !element.closest("[hidden], [aria-hidden='true'], [data-state='closed']") && getComputedStyle(element).display !== "none";
}

export type ArtifactFindState = { open: boolean; query: string };

/** One reader's search state outlives its file and rendered/source switches. */
export function ArtifactFindScope({ children, identity, enabled = true, findState, onFindStateChange, className = "relative flex h-full min-h-0 min-w-0 flex-col" }: {
	children: ReactNode;
	identity: string;
	enabled?: boolean;
	findState?: ArtifactFindState;
	onFindStateChange?: (state: ArtifactFindState) => void;
	className?: string;
}) {
	const { t } = useTranslation();
	const root = useRef<HTMLDivElement>(null), input = useRef<HTMLInputElement>(null);
	const [internalState, setInternalState] = useState<ArtifactFindState>({ open: false, query: "" });
	const { open, query } = findState ?? internalState;
	const stateRef = useRef(findState ?? internalState); stateRef.current = findState ?? internalState;
	const setOpen = useCallback((value: boolean) => { const next = { ...stateRef.current, open: value }; stateRef.current = next; (onFindStateChange ?? setInternalState)(next); }, [onFindStateChange]);
	const setQuery = useCallback((value: string) => { const next = { ...stateRef.current, query: value }; stateRef.current = next; (onFindStateChange ?? setInternalState)(next); }, [onFindStateChange]);
	const [revision, setRevision] = useState(0), [active, setActive] = useState(0);
	const [state, setState] = useState<{ phase: "idle" | "searching" | "ready" | "error"; total: number; warning?: "partial" | "unavailable" | "noText" | "failed" }>({ phase: "idle", total: 0 });
	const result = useRef<ArtifactFindResult | undefined>(undefined);
	const activeRange = useRef<ReturnType<ReturnType<typeof createArtifactFindEngine>["anchor"]>>(undefined);
	const adapters = useRef(new Map<HTMLElement, ArtifactFindAdapter>());
	const engine = useMemo(createArtifactFindEngine, []);
	const previousFocus = useRef<HTMLElement | null>(null), requestedScroll = useRef(false);
	const composing = useRef(false), generation = useRef(0), priorIdentity = useRef(identity), priorQuery = useRef(query);
	const current = useRef({ open, query, active, identity }); current.current = { open, query, active, identity };
	const invalidate = useCallback(() => setRevision((value) => value + 1), []);
	const register = useCallback<RegisterAdapter>((element, adapter) => {
		adapters.current.set(element, adapter); invalidate();
		return () => { adapters.current.delete(element); invalidate(); };
	}, [invalidate]);
	const close = useCallback(() => {
		generation.current++; result.current?.clear(); result.current = undefined;
		setOpen(false); setState({ phase: "idle", total: 0 });
		if (previousFocus.current?.isConnected) previousFocus.current.focus({ preventScroll: true });
		else if (root.current) { root.current.tabIndex = -1; root.current.focus({ preventScroll: true }); }
	}, [setOpen]);
	const show = useCallback(() => {
		if (!root.current || isSettingsPageOpen() || Array.from(root.current.querySelectorAll("[data-editing='true'], .cm-editor")).some((node) => visible(node) && !node.closest(".hidden"))) return;
		if (!current.current.open && document.activeElement instanceof HTMLElement) previousFocus.current = document.activeElement;
		claimArtifactFind(root.current);
		// Suspend another reader's highlights without resetting its retained query.
		document.dispatchEvent(new CustomEvent("artifact-find-open", { detail: root.current }));
		setOpen(true); requestedScroll.current = true; invalidate();
		requestAnimationFrame(() => { input.current?.focus(); input.current?.select(); });
	}, [invalidate, setOpen]);
	useAppShortcut("find-in-chat", show, enabled, () => Boolean(root.current && artifactFindOwner() === root.current && !isSettingsPageOpen()));
	useLayoutEffect(() => {
		if (!enabled || !root.current) return;
		return registerArtifactFindRoot(root.current);
	}, [enabled]);
	useEffect(() => {
		if (!enabled || !open) return;
		const escape = (event: KeyboardEvent) => {
			if (event.key !== "Escape" || event.isComposing || artifactFindOwner() !== root.current || document.querySelector("[role='menu'][data-state='open']")) return;
			event.preventDefault(); event.stopImmediatePropagation(); close();
		};
		window.addEventListener("keydown", escape, true);
		return () => window.removeEventListener("keydown", escape, true);
	}, [enabled, open, close]);
	useEffect(() => {
		if (!enabled) return;
		const suspend = (event: Event) => {
			if ((event as CustomEvent).detail !== root.current) { result.current?.clear(); setOpen(false); }
		};
		document.addEventListener("artifact-find-open", suspend);
		document.addEventListener("chat-find-open", suspend);
		return () => { document.removeEventListener("artifact-find-open", suspend); document.removeEventListener("chat-find-open", suspend); };
	}, [enabled, setOpen]);
	// Configure local frames before Find is pressed, including frames mounted later.
	useEffect(() => {
		const element = root.current;
		if (!enabled || !element) return;
		const configure = () => {
			for (const frame of element.querySelectorAll("iframe")) {
				frame.contentWindow?.postMessage({ jsonrpc: "2.0", method: "ao/find-configure", params: { exceptions: foldExceptions, bindings: currentShortcutBindings("find-in-chat"), open } } satisfies ArtifactFindMessage, "*");
			}
		};
		const message = (event: MessageEvent) => {
			const frame = Array.from(element.querySelectorAll("iframe")).find((candidate) => candidate.contentWindow === event.source && visible(candidate));
			if (!frame || event.data?.jsonrpc !== "2.0") return;
			if (event.data.method === "ao/find-open" && document.activeElement === frame && artifactFindOwner() === element) { claimArtifactFind(element); show(); }
			if (event.data.method === "ao/find-changed" && event.data.params?.generation === generation.current && current.current.open) invalidate();
			if (event.data.method === "ao/find-close" && current.current.open) close();
		};
		const loaded = () => { configure(); invalidate(); };
		element.addEventListener("load", loaded, true);
		window.addEventListener("message", message);
		const observer = new MutationObserver(configure); observer.observe(element, { childList: true, subtree: true });
		const unsubscribe = useKeybindingsStore.subscribe(configure);
		configure();
		return () => { observer.disconnect(); unsubscribe(); element.removeEventListener("load", loaded, true); window.removeEventListener("message", message); };
	}, [enabled, open, show, close, invalidate]);
	useEffect(() => {
		const element = root.current;
		if (!enabled || !open || !element) return;
		let timer = 0;
		const observers: MutationObserver[] = [];
		const watched = new WeakSet<Node>();
		const observe = (target: Node) => {
			if (watched.has(target)) return;
			watched.add(target);
			const observer = new MutationObserver((changes) => {
				if (changes.every((change) => (change.target instanceof Element ? change.target : change.target.parentElement)?.closest("[role='search'], style[data-artifact-find-style]"))) return;
				if (!timer) timer = window.setTimeout(() => { timer = 0; watchShadows(); invalidate(); }, 100);
			});
			observer.observe(target, { childList: true, characterData: true, subtree: true, attributes: true, attributeFilter: ["hidden", "aria-hidden", "class", "style", "data-editing", "data-artifact-find-unavailable"] });
			observers.push(observer);
		};
		const watchShadows = () => {
			observe(element);
			for (const host of element.querySelectorAll("diffs-container")) if (host.shadowRoot) observe(host.shadowRoot);
		};
		watchShadows();
		return () => { window.clearTimeout(timer); observers.forEach((observer) => observer.disconnect()); };
	}, [enabled, open, invalidate]);
	useEffect(() => {
		const element = root.current;
		if (!enabled || !open || !element || composing.current) return;
		if (Array.from(element.querySelectorAll("[data-editing='true']")).some((node) => visible(node) && !node.closest(".hidden"))) { result.current?.clear(); setOpen(false); return; }
		const token = ++generation.current, stop = new AbortController();
		const changed = priorIdentity.current !== identity || priorQuery.current !== query;
		priorIdentity.current = identity; priorQuery.current = query;
		const index = changed ? 0 : current.current.active;
		if (changed) { setActive(0); requestedScroll.current = true; }
		result.current?.clear(); result.current = undefined;
		if (isEmptyFindQuery(query)) { setState({ phase: "idle", total: 0 }); return; }
		setState({ phase: "searching", total: 0 });
		let dispose = () => {};
		async function search(): Promise<ArtifactFindResult> {
			const registered = Array.from(adapters.current).find(([node]) => visible(node) && element!.contains(node) && !node.closest(".hidden"));
			if (registered) return registered[1].search(query, stop.signal);
			const frame = Array.from(element!.querySelectorAll("iframe")).find((frame) => visible(frame) && !frame.closest(".hidden"));
			if (frame?.contentWindow) {
				return new Promise((resolve, reject) => {
					const send = (selected: number, reveal: boolean) => frame.contentWindow?.postMessage({ jsonrpc: "2.0", method: "ao/find", params: { generation: token, query, active: selected, reveal } } satisfies ArtifactFindMessage, "*");
					const onResult = (event: MessageEvent) => {
						if (event.source !== frame!.contentWindow) return;
						const data = readArtifactFindResult(event.data, token);
						if (!data) return;
						window.clearTimeout(timeout);
						resolve({ total: data.total, active: data.active, paint: send, clear: () => frame.contentWindow?.postMessage({ jsonrpc: "2.0", method: "ao/find", params: { generation: token, query: "", active: 0, reveal: false } }, "*") });
					};
					const timeout = window.setTimeout(() => reject(new Error("frame")), 5000);
					dispose = () => { window.clearTimeout(timeout); window.removeEventListener("message", onResult); reject(new DOMException("Cancelled", "AbortError")); };
					window.addEventListener("message", onResult); send(index, requestedScroll.current);
				});
			}
			const contents = Array.from(element!.querySelectorAll<HTMLElement>("[data-artifact-find-content]")).filter((node) => visible(node) && !node.closest(".hidden") && !node.parentElement?.closest("[data-artifact-find-content]"));
			const warning = contents.find((node) => node.dataset.artifactFindUnavailable)?.dataset.artifactFindUnavailable as "partial" | "unavailable" | undefined;
			const ranges = contents.flatMap((node) => engine.ranges(node, query));
			const retained = changed ? -1 : engine.retained(ranges, activeRange.current);
			return { total: ranges.length, active: retained >= 0 ? retained : undefined, warning: warning ?? (contents.length ? undefined : "unavailable"), paint: (selected, reveal) => {
				const range = ranges[selected];
				activeRange.current = engine.anchor(range);
				engine.paint(ranges, selected, document); if (reveal) engine.reveal(range);
			}, clear: engine.clear };
		}
		void search().then((found) => {
			if (stop.signal.aborted || token !== generation.current) return;
			result.current = found;
			const selected = Math.min(found.active ?? index, Math.max(0, found.total - 1));
			setActive(selected); setState({ phase: "ready", total: found.total, warning: found.warning });
			found.paint(selected, requestedScroll.current); requestedScroll.current = false;
		}).catch((error: unknown) => {
			if (!stop.signal.aborted && token === generation.current) setState({ phase: "error", total: 0, warning: error instanceof Error && error.name === "AbortError" ? undefined : "failed" });
		});
		return () => { stop.abort(); dispose(); };
	}, [enabled, open, query, identity, revision, engine, setOpen]);
	useEffect(() => () => { generation.current++; result.current?.clear(); engine.clear(); }, [engine]);
	const move = (direction: number) => {
		if (!result.current?.total || state.phase !== "ready") return;
		const selected = (active + direction + state.total) % state.total;
		setActive(selected); result.current.paint(selected, true);
	};
	useEffect(() => { if (!enabled) { result.current?.clear(); result.current = undefined; } }, [enabled]);
	const warning = state.warning ? t(`artifacts.find.${state.warning}`) : undefined;
	if (!enabled) return <AdapterContext.Provider value={undefined}>{children}</AdapterContext.Provider>;
	return <AdapterContext.Provider value={enabled ? register : undefined}>
		<div ref={root} className={className} data-artifact-find-scope={enabled ? "" : undefined}
			onKeyDownCapture={(event) => {
				if (event.key === "Escape" && open && artifactFindOwner() === root.current) { event.preventDefault(); event.stopPropagation(); close(); }
			}}>
			{children}
			{enabled && open ? <FindBar label={t("artifacts.find.label")} inputRef={input} aria-busy={state.phase === "searching"}
				inputProps={{ value: query, onChange: (event) => { generation.current++; requestedScroll.current = true; setQuery(event.target.value); },
					onCompositionStart: () => { composing.current = true; generation.current++; }, onCompositionEnd: () => { composing.current = false; invalidate(); },
					onKeyDown: (event) => { if (event.key === "Enter" && !event.nativeEvent.isComposing && !composing.current) { event.preventDefault(); event.stopPropagation(); move(event.shiftKey ? -1 : 1); } },
				}} disabled={state.phase !== "ready" || !state.total} onPrevious={() => move(-1)} onNext={() => move(1)} onClose={close}
				status={<span role="status" aria-live="polite" title={warning} className="inline-flex min-h-4 min-w-16 max-w-24 shrink-0 items-center justify-end overflow-hidden px-2 whitespace-nowrap text-muted-foreground tabular-nums">
					{state.phase === "searching" ? <><Loader2 aria-hidden="true" className="size-4 animate-spin" /><span className="sr-only">{t("command.searching")}</span></> : isEmptyFindQuery(query) ? "" : warning ? t("chat.find.incomplete") : state.total ? <><span aria-hidden="true">{active + 1}/{state.total}</span><span className="sr-only">{t("browser.find.matchCount", { active: active + 1, total: state.total })}</span></> : t("browser.find.noResults")}
				</span>}>
				{warning ? <span role="alert" className="absolute top-full right-0 mt-2 w-full rounded-lg border border-border bg-surface p-2 shadow-lg">{warning}</span> : null}
			</FindBar> : null}
		</div>
	</AdapterContext.Provider>;
}
