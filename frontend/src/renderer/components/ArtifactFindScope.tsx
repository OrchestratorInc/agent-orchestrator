import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { FindBar } from "./FindBar";
import { useAppShortcut, isSettingsPageOpen } from "../hooks/useAppShortcut";
import { artifactFindOwner, claimArtifactFind, nativeArtifactFindFocused, registerArtifactFindRoot } from "../lib/artifact-find-focus";
import { createArtifactFindEngine, foldExceptions, readArtifactFindResult, type ArtifactFindAdapter, type ArtifactFindMessage, type ArtifactFindResult } from "../lib/artifact-find";
import { currentShortcutBindings, useKeybindingsStore } from "../stores/keybindings-store";
import { isEmptyFindQuery } from "../lib/chat-find";
import { AoError } from "../../../../packages/shared/telemetry/ao-error";

type RegisterAdapter = (root: HTMLElement, adapter: ArtifactFindAdapter) => () => void;
const AdapterContext = createContext<RegisterAdapter | undefined>(undefined);
export const useArtifactFindRegistration = () => useContext(AdapterContext);

function visible(element: Element): boolean {
	return !element.closest("[hidden], [aria-hidden='true'], [data-state='closed']") && getComputedStyle(element).display !== "none";
}

/** One reader's search state outlives its file and rendered/source switches. */
export function ArtifactFindScope({ children, identity, enabled = true, className = "relative flex h-full min-h-0 min-w-0 flex-col" }: {
	children: ReactNode;
	identity: string;
	enabled?: boolean;
	className?: string;
}) {
	const { t } = useTranslation();
	const root = useRef<HTMLDivElement>(null), input = useRef<HTMLInputElement>(null);
	const [open, setOpen] = useState(false), [query, setQuery] = useState("");
	const [revision, setRevision] = useState(0), [active, setActive] = useState(0);
	const [state, setState] = useState<{ phase: "idle" | "searching" | "ready" | "error"; total: number; warning?: "partial" | "unavailable" | "noText" | "failed" }>({ phase: "idle", total: 0 });
	const result = useRef<ArtifactFindResult | undefined>(undefined);
	const activeRange = useRef<ReturnType<ReturnType<typeof createArtifactFindEngine>["anchor"]>>(undefined);
	const adapters = useRef(new Map<HTMLElement, ArtifactFindAdapter>());
	const engine = useMemo(createArtifactFindEngine, []);
	const previousFocus = useRef<HTMLElement | null>(null), requestedScroll = useRef(false);
	const composing = useRef(false), generation = useRef(0), priorIdentity = useRef(identity), priorQuery = useRef(query);
	const pending = useRef<(() => void) | undefined>(undefined);
	const cancel = useCallback(() => {
		generation.current++;
		pending.current?.(); pending.current = undefined;
		result.current?.clear(); result.current = undefined;
		engine.clear();
	}, [engine]);
	const current = useRef({ open, query, active, identity }); current.current = { open, query, active, identity };
	const invalidate = useCallback(() => setRevision((value) => value + 1), []);
	const register = useCallback<RegisterAdapter>((element, adapter) => {
		adapters.current.set(element, adapter); invalidate();
		return () => { adapters.current.delete(element); invalidate(); };
	}, [invalidate]);
	const close = useCallback(() => {
		cancel();
		activeRange.current = undefined; setActive(0);
		setOpen(false); setState({ phase: "idle", total: 0 });
		if (previousFocus.current?.isConnected && visible(previousFocus.current) && previousFocus.current.getClientRects().length > 0 && !previousFocus.current.closest("[inert]")) previousFocus.current.focus({ preventScroll: true });
		else if (root.current) { root.current.tabIndex = -1; root.current.focus({ preventScroll: true }); }
	}, [setOpen, cancel]);
	const show = useCallback(() => {
		if (!root.current || isSettingsPageOpen() || nativeArtifactFindFocused() || Array.from(root.current.querySelectorAll("[data-editing='true'], .cm-editor")).some((node) => visible(node) && !node.closest(".hidden"))) return;
		if (!current.current.open && document.activeElement instanceof HTMLElement) previousFocus.current = document.activeElement;
		claimArtifactFind(root.current);
		// Suspend another reader's highlights without resetting its retained query.
		document.dispatchEvent(new CustomEvent("artifact-find-open", { detail: root.current }));
		setOpen(true); requestedScroll.current = true; invalidate();
		requestAnimationFrame(() => { input.current?.focus(); input.current?.select(); });
	}, [invalidate, setOpen]);
	useAppShortcut("find-in-chat", show, enabled, () => Boolean(root.current && artifactFindOwner() === root.current && !isSettingsPageOpen()));
	useLayoutEffect(() => {
		cancel();
		if (!enabled || !root.current) return;
		const unregister = registerArtifactFindRoot(root.current);
		return () => { cancel(); unregister(); };
	}, [enabled, identity, cancel]);
	const ownsEscape = useCallback((event: KeyboardEvent) => enabled && open && event.key === "Escape" && !event.defaultPrevented && !event.isComposing && !composing.current && artifactFindOwner() === root.current &&
		!Array.from(document.querySelectorAll("[role='menu'][data-state='open']")).some((menu) => visible(menu) && menu.getClientRects().length > 0), [enabled, open]);
	useEffect(() => {
		if (!enabled || !open) return;
		const escape = (event: KeyboardEvent) => {
			if (!ownsEscape(event)) return;
			event.preventDefault(); event.stopImmediatePropagation(); close();
		};
		window.addEventListener("keydown", escape, true);
		return () => window.removeEventListener("keydown", escape, true);
	}, [enabled, open, close, ownsEscape]);
	useEffect(() => {
		if (!enabled) return;
		const suspend = (event: Event) => {
			if ((event as CustomEvent).detail !== root.current) { cancel(); setOpen(false); }
		};
		document.addEventListener("artifact-find-open", suspend);
		document.addEventListener("chat-find-open", suspend);
		return () => { document.removeEventListener("artifact-find-open", suspend); document.removeEventListener("chat-find-open", suspend); };
	}, [enabled, setOpen, cancel]);
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
		const loaded = () => { cancel(); configure(); invalidate(); };
		element.addEventListener("load", loaded, true);
		window.addEventListener("message", message);
		const observer = new MutationObserver(configure); observer.observe(element, { childList: true, subtree: true });
		const unsubscribe = useKeybindingsStore.subscribe(configure);
		configure();
		return () => {
			observer.disconnect(); unsubscribe(); element.removeEventListener("load", loaded, true); window.removeEventListener("message", message);
			for (const frame of element.querySelectorAll("iframe")) {
				frame.contentWindow?.postMessage({ jsonrpc: "2.0", method: "ao/find-configure", params: { exceptions: foldExceptions, bindings: [], open: false } } satisfies ArtifactFindMessage, "*");
				frame.contentWindow?.postMessage({ jsonrpc: "2.0", method: "ao/find", params: { generation: ++generation.current, query: "", active: 0, reveal: false } } satisfies ArtifactFindMessage, "*");
			}
		};
	}, [enabled, open, show, close, invalidate, cancel]);
	useEffect(() => {
		const element = root.current;
		if (!enabled || !open || !element) return;
		return engine.observe(element, () => { cancel(); invalidate(); });
	}, [enabled, open, invalidate, cancel, engine]);
	useEffect(() => {
		const element = root.current;
		if (!enabled || !open || !element || composing.current) return;
		if (Array.from(element.querySelectorAll("[data-editing='true']")).some((node) => visible(node) && !node.closest(".hidden"))) { cancel(); setOpen(false); return; }
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
						if (event.data?.jsonrpc === "2.0" && event.data.params?.generation === token) {
							if (event.data.method === "ao/find-started") { window.clearTimeout(timeout); return; }
							if (event.data.method === "ao/find-error") { reject(new AoError("ARTIFACT_FIND_FRAME_FAILED", "The artifact frame could not complete the search")); return; }
						}
						const data = readArtifactFindResult(event.data, token);
						if (!data) return;
						window.clearTimeout(timeout);
						resolve({ total: data.total, active: data.active, paint: (selected, reveal) => { if (!stop.signal.aborted && token === generation.current) send(selected, reveal); }, clear: () => frame.contentWindow?.postMessage({ jsonrpc: "2.0", method: "ao/find-cancel", params: { generation: token } } satisfies ArtifactFindMessage, "*") });
					};
					const timeout = window.setTimeout(() => reject(new AoError("ARTIFACT_FIND_FRAME_TIMEOUT", "The artifact frame did not acknowledge the search")), 5000);
					dispose = () => { window.clearTimeout(timeout); window.removeEventListener("message", onResult); frame.contentWindow?.postMessage({ jsonrpc: "2.0", method: "ao/find-cancel", params: { generation: token } } satisfies ArtifactFindMessage, "*"); reject(new DOMException("Cancelled", "AbortError")); };
					window.addEventListener("message", onResult); send(index, requestedScroll.current);
				});
			}
			const contents = Array.from(element!.querySelectorAll<HTMLElement>("[data-artifact-find-content]")).filter((node) => visible(node) && !node.closest(".hidden") && !node.parentElement?.closest("[data-artifact-find-content]"));
			const warning = contents.find((node) => node.dataset.artifactFindUnavailable)?.dataset.artifactFindUnavailable as "partial" | "unavailable" | undefined;
			const ranges: Awaited<ReturnType<typeof engine.search>> = [];
			// Append one by one: spreading a large result into push() overflows the stack.
			for (const node of contents) for (const match of await engine.search(node, query, stop.signal)) ranges.push(match);
			const retained = changed ? -1 : engine.retained(ranges, activeRange.current);
			return { total: ranges.length, active: retained >= 0 ? retained : undefined, warning: warning ?? (contents.length ? undefined : "unavailable"), paint: (selected, reveal) => {
				if (stop.signal.aborted || token !== generation.current) return;
				const range = ranges[selected];
				activeRange.current = engine.anchor(range);
				engine.paint(ranges, selected, document); if (reveal) engine.reveal(range);
			}, clear: engine.clear };
		}
		const release = () => { stop.abort(); dispose(); };
		pending.current = release;
		void search().then((found) => {
			if (stop.signal.aborted || token !== generation.current) return;
			result.current = found;
			const selected = Math.min(found.active ?? index, Math.max(0, found.total - 1));
			setActive(selected); setState({ phase: "ready", total: found.total, warning: found.warning });
			found.paint(selected, requestedScroll.current); requestedScroll.current = false;
		}).catch((error: unknown) => {
			if (!stop.signal.aborted && token === generation.current) setState({ phase: "error", total: 0, warning: error instanceof Error && error.name === "AbortError" ? undefined : "failed" });
		});
		return () => { release(); if (pending.current === release) pending.current = undefined; };
	}, [enabled, open, query, identity, revision, engine, setOpen, cancel]);
	const move = (direction: number) => {
		if (!result.current?.total || state.phase !== "ready") return;
		const selected = (active + direction + state.total) % state.total;
		setActive(selected); result.current.paint(selected, true);
	};
	useEffect(() => {
		if (!enabled) {
			cancel();
			activeRange.current = undefined; composing.current = false; setActive(0); setState({ phase: "idle", total: 0 });
		}
	}, [enabled, cancel]);
	const warning = state.warning ? t(`artifacts.find.${state.warning}`) : undefined;
	return <AdapterContext.Provider value={enabled ? register : undefined}>
		<div ref={root} className={className} data-artifact-find-scope={enabled ? "" : undefined}
			onKeyDownCapture={(event) => {
				if (!event.defaultPrevented && ownsEscape(event.nativeEvent)) { event.preventDefault(); event.stopPropagation(); close(); }
			}}>
			{children}
			{enabled && open ? <FindBar label={t("artifacts.find.label")} inputRef={input} aria-busy={state.phase === "searching"}
				inputProps={{ value: query, onChange: (event) => { cancel(); requestedScroll.current = true; setQuery(event.target.value); },
					onCompositionStart: () => { composing.current = true; cancel(); }, onCompositionEnd: () => { composing.current = false; invalidate(); },
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
