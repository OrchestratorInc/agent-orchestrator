import { normalizeCodeRowText } from "../../../shared/file-annotations";
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import type { SelectedLineRange } from "@pierre/diffs";
import type { FileAnnotationRange } from "../../../shared/chat-file-reference";

export function useFileAnnotationReveal(container: RefObject<HTMLElement | null>, range?: FileAnnotationRange, { ready = true, contentKey }: { ready?: boolean; contentKey?: unknown } = {}) {
	const [selectedLines, setSelectedLines] = useState<SelectedLineRange | null>(null);
	const [changed, setChanged] = useState(false);
	const consumed = useRef<number | undefined>(undefined);
	const dismissed = useRef<number | undefined>(undefined);
	const highlightedRows = useRef<HTMLElement[]>([]);
	const reveal = useCallback(() => {
		if (!ready || !range || dismissed.current === range.requestKey) return;
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
				if (element || alreadyRevealed) {
					setChanged(true);
					setSelectedLines(null);
					// A stale first render must not consume an unverified request.
				}
				return;
			}
			elements.push(element);
		}
		highlightedRows.current = elements;
		if (!elements.length || alreadyRevealed) return;
		const side = oldSide(expected[0]) ? "deletions" : "additions";
		const endSide = oldSide(expected[expected.length - 1]) ? "deletions" : "additions";
		const first = elements[0],
			last = elements[elements.length - 1];
		consumed.current = range.requestKey;
		setChanged(false);
		setSelectedLines({
			start: oldSide(expected[0]) ? expected[0].oldNo! : expected[0].newNo!,
			end: oldSide(expected[expected.length - 1]) ? expected[expected.length - 1].oldNo! : expected[expected.length - 1].newNo!,
			side,
			endSide,
		});
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
		highlightedRows.current = [];
		setChanged(false);
		setSelectedLines(null);
		const frame = requestAnimationFrame(() => latestReveal.current());
		const timeout = setTimeout(() => {
			if (range && dismissed.current !== range.requestKey && consumed.current !== range.requestKey) setChanged(true);
		}, 5000);
		return () => {
			cancelAnimationFrame(frame);
			clearTimeout(timeout);
		};
	}, [range?.requestKey]);
 useEffect(() => {
  if (!ready) return;
  const frame = requestAnimationFrame(() => latestReveal.current());
  return () => cancelAnimationFrame(frame);
 }, [ready, contentKey]);
	useEffect(() => {
		if (!range) return;
		const dismiss = () => {
			dismissed.current = range.requestKey;
			highlightedRows.current = [];
			setSelectedLines(null);
		};
		const onPointerDown = (event: PointerEvent) => {
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
	return { selectedLines: range && consumed.current === range.requestKey ? selectedLines : null, changed, reveal };
}
