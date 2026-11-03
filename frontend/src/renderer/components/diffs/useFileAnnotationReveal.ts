import { normalizeCodeRowText } from "../../../shared/file-annotations";
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import type { SelectedLineRange } from "@pierre/diffs";
import type { FileAnnotationRange } from "../../../shared/chat-file-reference";

export function useFileAnnotationReveal(container: RefObject<HTMLElement | null>, range?: FileAnnotationRange, { ready = true, contentKey }: { ready?: boolean; contentKey?: unknown } = {}) {
	const [selectedLines, setSelectedLines] = useState<SelectedLineRange | null>(null);
	const [changed, setChanged] = useState(false);
	const [unverified, setUnverified] = useState(false);
	const [dismissedRequest, setDismissedRequest] = useState<number>();
	const consumed = useRef<number | undefined>(undefined);
	const dismissed = useRef<number | undefined>(undefined);
	const highlightedRows = useRef<HTMLElement[]>([]);
	const currentRequest = useRef(range?.requestKey);
	currentRequest.current = range?.requestKey;
	const reveal = useCallback(() => {
		if (!ready || !range || currentRequest.current !== range.requestKey || dismissed.current === range.requestKey) return;
		const alreadyRevealed = consumed.current === range.requestKey;
		const roots = [...(container.current?.querySelectorAll("diffs-container") ?? [])].flatMap((host) => (host.shadowRoot ? [host.shadowRoot] : []));
		const expected = range.lines;
		const oldSide = (row: (typeof expected)[number]) =>
			row.side === "old" || (row.side !== "new" && (row.kind === "del" || (range.side === "old" && row.kind !== "add")));
		const oldRows = new Map<number, HTMLElement>();
		const newRows = new Map<number, HTMLElement>();
		for (const element of roots.flatMap((root) => [...root.querySelectorAll<HTMLElement>("[data-line][data-line-type]")])) {
			const deletion = element.dataset.lineType === "change-deletion" || Boolean(element.closest("[data-deletions]"));
			const number = Number(element.dataset.line);
			if (deletion) oldRows.set(number, element);
			else newRows.set(number, element);
			if (!deletion && element.dataset.lineType !== "change-addition") {
				const oldNumber = Number(element.dataset.altLine ?? element.dataset.line);
				if (!oldRows.has(oldNumber)) oldRows.set(oldNumber, element);
			}
		}
		const elements: HTMLElement[] = [];
		for (const row of expected) {
			const old = oldSide(row);
			const number = old ? row.oldNo : row.newNo;
			if (number == null) continue;
			const element = (old ? oldRows : newRows).get(number);
			if (!element || normalizeCodeRowText(element.textContent ?? "") !== row.text) {
				setChanged(Boolean(element));
				if (element) setUnverified(false);
				if (element || alreadyRevealed) {
					setSelectedLines(null);
					highlightedRows.current = [];
					// A stale first render must not consume an unverified request.
				}
				return;
			}
			elements.push(element);
		}
		if (!elements.length) return;
		// Pierre highlights an interval of render indexes, including both split
		// columns. Matching only the requested endpoints can include other code.
		const first = elements[0], last = elements[elements.length - 1];
		const root = first.getRootNode();
		const rows = [...(root as ShadowRoot).querySelectorAll<HTMLElement>("[data-line][data-line-type]")];
		const split = first.closest("[data-diff-type]")?.getAttribute("data-diff-type") === "split";
		const index = (row: HTMLElement) => {
			const indexes = row.dataset.lineIndex?.split(",").map(Number);
			return indexes ? indexes[split ? 1 : 0] ?? indexes[0] : rows.indexOf(row);
		};
		const start = index(first), end = index(last);
		const interval = rows.filter(row => index(row) >= start && index(row) <= end);
		const matchesCapturedRow = (element: HTMLElement) => {
			const kind = element.dataset.lineType === "change-deletion" ? "del" : element.dataset.lineType === "change-addition" ? "add" : "context";
			const old = kind === "del" || Boolean(element.closest("[data-deletions]"));
			return expected.some(row => row.kind === kind && (old ? row.oldNo : row.newNo) === Number(element.dataset.line) && normalizeCodeRowText(element.textContent ?? "") === row.text);
		};
		if (root !== last.getRootNode() || end < start || elements.some(row => index(row) < start || index(row) > end) || (!split && elements.some((row, i) => i > 0 && index(row) < index(elements[i - 1]))) || interval.some(row => !matchesCapturedRow(row))) {
			setChanged(true);
			setSelectedLines(null);
			highlightedRows.current = [];
			return;
		}
		if (new Set(interval.map(index)).size < end - start + 1) return; // Virtualized rows are not ready yet.
		highlightedRows.current = interval;
		const side = oldSide(expected[0]) ? "deletions" : "additions";
		const endSide = oldSide(expected[expected.length - 1]) ? "deletions" : "additions";
		const startLine = oldSide(expected[0]) ? expected[0].oldNo! : expected[0].newNo!;
		const endLine = oldSide(expected[expected.length - 1]) ? expected[expected.length - 1].oldNo! : expected[expected.length - 1].newNo!;
		setChanged(false);
		setUnverified(false);
		// Post-render verification can recover a redraw without starting another
		// render cycle when Pierre already has the same verified selection.
		setSelectedLines(current => current?.start === startLine && current.end === endLine && current.side === side && current.endSide === endSide
			? current
			: { start: startLine, end: endLine, side, endSide });
		if (alreadyRevealed) return;
		consumed.current = range.requestKey;
		let parent = container.current?.parentElement;
		while (parent && !/(auto|scroll)/.test(getComputedStyle(parent).overflowY)) parent = parent.parentElement;
		const view = parent?.getBoundingClientRect();
		const top = Math.max(0, view?.top ?? 0),
			bottom = Math.min(window.innerHeight, view?.bottom ?? window.innerHeight);
		if (first.getBoundingClientRect().top < top || last.getBoundingClientRect().bottom > bottom) first.scrollIntoView({ block: "center" });
	}, [container, range, ready]);
	const latestReveal = useRef(reveal);
	latestReveal.current = reveal;
	useEffect(() => {
		consumed.current = undefined;
		dismissed.current = undefined;
		setDismissedRequest(undefined);
		setUnverified(false);
		highlightedRows.current = [];
		setChanged(false);
		setSelectedLines(null);
		const frame = requestAnimationFrame(() => latestReveal.current());
		return () => cancelAnimationFrame(frame);
	}, [range?.requestKey]);
 useEffect(() => {
  if (!ready) return;
  const frame = requestAnimationFrame(() => latestReveal.current());
  return () => cancelAnimationFrame(frame);
 }, [ready, contentKey]);
	const verified = Boolean(range && selectedLines && consumed.current === range.requestKey);
	// Transport/hydration delays have no render deadline. Late rows can still
	// verify this same request after the advisory deadline, without polling.
	useEffect(() => {
		if (!range || !ready || verified || changed || dismissedRequest === range.requestKey) return;
		const key = range.requestKey;
		const timeout = setTimeout(() => {
			if (currentRequest.current === key && dismissed.current !== key) setUnverified(true);
		}, 5000);
		return () => clearTimeout(timeout);
	}, [range?.requestKey, ready, verified, changed, dismissedRequest]);
	useEffect(() => {
		if (!range) return;
		const dismiss = () => {
			dismissed.current = range.requestKey;
			setDismissedRequest(range.requestKey);
			highlightedRows.current = [];
			setSelectedLines(null);
		};
		const onPointerDown = (event: PointerEvent) => {
			// Retry continues this navigation; it is not an outside-click dismissal.
			if (event.composedPath().some(node => node instanceof HTMLElement && node.dataset.fileAnnotationRetry === String(range.requestKey))) return;
			// composedPath reaches the row inside Pierre's shadow root. Do not
			// interfere with selecting/copying text within the highlighted code.
			if (event.button === 0 && !highlightedRows.current.some(row => event.composedPath().includes(row))) dismiss();
		};
		const onKeyDown = (event: KeyboardEvent) => { if (event.key === "Escape") dismiss(); };
		document.addEventListener("pointerdown", onPointerDown, true);
		document.addEventListener("keydown", onKeyDown, true);
		return () => {
			document.removeEventListener("pointerdown", onPointerDown, true);
			document.removeEventListener("keydown", onKeyDown, true);
		};
	}, [range?.requestKey]);
	return { selectedLines: ready && range && consumed.current === range.requestKey ? selectedLines : null, changed: ready && changed, unverified: ready && unverified, loading: Boolean(range && dismissedRequest !== range.requestKey && (!ready || (!verified && !changed && !unverified))), reveal };
}
