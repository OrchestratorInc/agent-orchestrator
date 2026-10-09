import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import type { FileCodeReference } from "../../../shared/file-annotations";
import type { SelectedLineRange } from "@pierre/diffs";
import type { DiffRow } from "../../lib/diff-parser";

/** Code highlighted with the mouse, read back as the rows it covers. */
export type CodeSelection = {
	/** The Pierre surface (one per file) the selection is in. */
	host: HTMLElement;
	/** Every selected row, top to bottom, in diff-row form. */
	rows: DiffRow[];
	/** The selection as a Pierre range, for surfaces that resolve their own rows. */
	range: SelectedLineRange;
	/** Just past the end of the selected text on its last line, for the Ask button. */
	point: { x: number; y: number };
	/** True while the mouse button is still down: the selection is still being made. */
	dragging: boolean;
};

type ShadowRootWithSelection = ShadowRoot & { getSelection?: () => Selection | null };

const ROW_SELECTOR = "[data-line][data-line-type]";

function rowElement(node: Node | null): Element | null {
	const element = node instanceof Element ? node : node?.parentElement;
	return element?.closest(ROW_SELECTOR) ?? null;
}

// A split diff's old-side column numbers its context rows by the old file.
function inDeletionsColumn(element: Element): boolean {
	return element.closest("[data-deletions]") !== null;
}

function diffRow(element: Element): DiffRow {
	const line = Number(element.getAttribute("data-line"));
	const type = element.getAttribute("data-line-type") ?? "";
	const text = element.textContent ?? "";
	if (type === "change-addition") return { kind: "add", oldNo: null, newNo: line, text };
	if (type === "change-deletion") return { kind: "del", oldNo: line, newNo: null, text };
	// Context rows carry their column's number, and the other side's as data-alt-line.
	const alt = element.getAttribute("data-alt-line");
	const other = alt ? Number(alt) : line;
	return inDeletionsColumn(element)
		? { kind: "context", oldNo: line, newNo: other, text }
		: { kind: "context", oldNo: other, newNo: line, text };
}

function rowSide(element: Element): "deletions" | "additions" {
	return element.getAttribute("data-line-type") === "change-deletion" || inDeletionsColumn(element) ? "deletions" : "additions";
}

// The selection as a range between its anchor and focus, in document order.
// Built by hand: Electron 33's Chromium reports a shadow-root selection as
// collapsed (and its getRangeAt as the host) even while it holds text, but its
// anchor and focus points are right.
function selectionRange(selection: Selection): Range | null {
	const { anchorNode, anchorOffset, focusNode, focusOffset } = selection;
	if (!anchorNode || !focusNode) return null;
	const forward = anchorNode === focusNode
		? anchorOffset <= focusOffset
		: (anchorNode.compareDocumentPosition(focusNode) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
	const range = document.createRange();
	range.setStart(forward ? anchorNode : focusNode, forward ? anchorOffset : focusOffset);
	range.setEnd(forward ? focusNode : anchorNode, forward ? focusOffset : anchorOffset);
	return range;
}

/** The highlighted lines as a chat reference: old-side diff lines keep old numbers. */
export function codeReference(path: string, selection: CodeSelection, diff: boolean): FileCodeReference {
	return {
		path,
		side: diff ? (selection.range.side === "deletions" ? "old" : "new") : "file",
		line: selection.range.start,
		endLine: selection.range.end,
		lines: selection.rows.map(({ kind, oldNo, newNo, text }) => ({ kind: kind as "context" | "add" | "del", oldNo, newNo, text })),
	};
}

/** Reads the rows a selection covers inside one Pierre surface, or null. */
export function readCodeSelection(host: HTMLElement, dragging = false): CodeSelection | null {
	const root = host.shadowRoot as ShadowRootWithSelection | null;
	// Chromium (and so Electron) exposes a shadow root's own selection; the
	// document-level selection is retargeted to the host and loses the rows.
	const selection = root?.getSelection?.();
	const range = selection ? selectionRange(selection) : null;
	if (!root || !selection || !range || range.collapsed || !range.toString().trim()) return null;
	const anchor = rowElement(selection.anchorNode);
	const focus = rowElement(selection.focusNode);
	if (!anchor || !focus || !root.contains(anchor) || !root.contains(focus)) return null;
	// A split diff has one column per side; keep to the column the drag started in.
	const column = anchor.parentElement;
	const candidates = [...root.querySelectorAll(ROW_SELECTOR)].filter((row) => row.parentElement === column);
	const from = candidates.indexOf(anchor);
	const to = candidates.indexOf(focus);
	if (from < 0 || to < 0) return null;
	const elements = candidates.slice(Math.min(from, to), Math.max(from, to) + 1);
	// A drag that ends at the very start of a line selects none of it, as in an
	// editor: leave that line out.
	const end = elements[elements.length - 1];
	if (elements.length > 1 && end.contains(range.endContainer)) {
		const head = document.createRange();
		head.setStart(end, 0);
		head.setEnd(range.endContainer, range.endOffset);
		if (!head.toString()) elements.pop();
	}
	const first = elements[0];
	const last = elements[elements.length - 1];
	// The Ask button sits just past the selected text on the last selected line.
	const lastBox = last.getBoundingClientRect();
	const onLastLine = [...range.getClientRects()].filter((rect) => rect.width > 0 && rect.bottom > lastBox.top + 1 && rect.top < lastBox.bottom - 1);
	const edge = onLastLine.length ? Math.max(...onLastLine.map((rect) => rect.right)) : lastBox.left;
	return {
		host,
		rows: elements.map(diffRow),
		range: { start: Number(first.getAttribute("data-line")), side: rowSide(first), end: Number(last.getAttribute("data-line")), endSide: rowSide(last) },
		point: { x: edge, y: lastBox.top + lastBox.height / 2 },
		dragging,
	};
}

function scrollParent(element: Element): Element {
	for (let node = element.parentElement; node; node = node.parentElement) {
		if (/(auto|scroll)/.test(getComputedStyle(node).overflowY)) return node;
	}
	return document.documentElement;
}

function sameSelection(a: CodeSelection | null, b: CodeSelection | null): boolean {
	if (!a || !b) return a === b;
	return a.host === b.host && a.range.start === b.range.start && a.range.end === b.range.end && a.range.side === b.range.side
		&& a.range.endSide === b.range.endSide && Math.round(a.point.x) === Math.round(b.point.x) && Math.round(a.point.y) === Math.round(b.point.y)
		&& a.dragging === b.dragging;
}

/** What the Ask button needs from a code selection; useCodeSelection provides it. */
export type CodeSelectionSource = {
	get: () => CodeSelection | null;
	subscribe: (listener: () => void) => () => void;
};

/**
 * Tracks code highlighted with the mouse in a container's Pierre surfaces, the
 * way an editor offers "add to chat" for a selection. It never acts by itself:
 * `source` places the Ask button while a selection is on screen, and `ask` (the
 * button, or Cmd/Ctrl+L) hands the captured selection to `onAsk`, then clears
 * the selection. Escape clears it too.
 *
 * The selection lives in a small store only the button subscribes to:
 * re-rendering the surface would redraw Pierre's rows and drop the very
 * selection it shows.
 */
export function useCodeSelection(containerRef: RefObject<HTMLElement | null>, onAsk: (selection: CodeSelection) => void, enabled = true) {
	const [store] = useState(createSelectionStore);
	const selectionRef = useRef<CodeSelection | null>(null);
	const onAskRef = useRef(onAsk);
	onAskRef.current = onAsk;
	// Asks about the captured selection; the selection then clears, and the
	// button with it.
	const ask = useCallback((): boolean => {
		const current = selectionRef.current;
		if (!current) return false;
		onAskRef.current(current);
		clearSelection(current.host);
		return true;
	}, []);
	useEffect(() => {
		const publish = (next: CodeSelection | null) => {
			selectionRef.current = next;
			store.set(next);
		};
		if (!enabled) {
			publish(null);
			return;
		}
		// Looked up on every read: a view can mount behind a loading state and
		// render its container (and Pierre's surfaces) only later.
		const hosts = () => [...(containerRef.current?.querySelectorAll<HTMLElement>("diffs-container") ?? [])];
		let frame: number | null = null;
		// The button follows the selection while it is being dragged out, and
		// becomes clickable once the mouse button is released.
		let pressed = false;
		const redraws = new MutationObserver(() => schedule());
		const update = () => {
			frame = null;
			let next: CodeSelection | null = null;
			for (const host of hosts()) {
				next = readCodeSelection(host, pressed);
				if (next) break;
			}
			// A redraw that replaces the selected rows does not always announce a
			// selection change, so watch the surfaces' rows while one is shown.
			redraws.disconnect();
			if (next) for (const host of hosts()) if (host.shadowRoot) redraws.observe(host.shadowRoot, { childList: true, subtree: true });
			// Off screen (scrolled away), the button hides; the selection stays.
			if (next) {
				const view = scrollParent(next.host).getBoundingClientRect();
				if (next.point.y < Math.max(view.top, 0) || next.point.y > Math.min(view.bottom, window.innerHeight)) next = null;
			}
			publish(next);
		};
		const schedule = () => {
			if (frame === null) frame = requestAnimationFrame(update);
		};
		const onPointerDown = (event: PointerEvent) => {
			if (event.button === 0) pressed = true;
		};
		const onPointerUp = () => {
			pressed = false;
			schedule();
		};
		const onKeyDown = (event: KeyboardEvent) => {
			const current = selectionRef.current;
			if (!current) return;
			if (event.key === "Escape") {
				clearSelection(current.host);
				return;
			}
			// Cmd/Ctrl+L adds the selection to chat, as in Cursor.
			if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === "l" && ask()) {
				event.preventDefault();
				event.stopPropagation();
			}
		};
		document.addEventListener("selectionchange", schedule);
		document.addEventListener("pointerdown", onPointerDown, true);
		document.addEventListener("pointerup", onPointerUp, true);
		document.addEventListener("pointercancel", onPointerUp, true);
		document.addEventListener("scroll", schedule, true);
		document.addEventListener("keydown", onKeyDown, true);
		window.addEventListener("resize", schedule);
		schedule();
		return () => {
			document.removeEventListener("selectionchange", schedule);
			document.removeEventListener("pointerdown", onPointerDown, true);
			document.removeEventListener("pointerup", onPointerUp, true);
			document.removeEventListener("pointercancel", onPointerUp, true);
			document.removeEventListener("scroll", schedule, true);
			document.removeEventListener("keydown", onKeyDown, true);
			window.removeEventListener("resize", schedule);
			if (frame !== null) cancelAnimationFrame(frame);
			redraws.disconnect();
			publish(null);
		};
	}, [ask, containerRef, enabled, store]);
	return { source: store, ask };
}

function clearSelection(host: HTMLElement) {
	(host.shadowRoot as ShadowRootWithSelection | null)?.getSelection?.()?.removeAllRanges();
	window.getSelection()?.removeAllRanges();
}

function createSelectionStore(): CodeSelectionSource & { set: (next: CodeSelection | null) => void } {
	let current: CodeSelection | null = null;
	const listeners = new Set<() => void>();
	return {
		get: () => current,
		set: (next) => {
			if (sameSelection(current, next)) return;
			current = next;
			for (const listener of listeners) listener();
		},
		subscribe: (listener) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
	};
}
