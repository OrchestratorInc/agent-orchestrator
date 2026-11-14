import { normalizeCodeRowText } from "../../../shared/file-annotations";
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import type { FileCodeReference } from "../../../shared/file-annotations";
import type { SelectedLineRange } from "@pierre/diffs";
import type { DiffRow } from "../../lib/diff-parser";
import { selectionRangesEqual } from "../../lib/selection-toolbar";

export type CodeSelection = {
	host: HTMLElement;
	rows: Array<DiffRow & { side?: "old" | "new" }>;
	range: SelectedLineRange;
	point: { x: number; y: number };
	dragging: boolean;
	textRange?: Range;
	viewport?: Element;
	toolbarHeader?: Element;
	snapshot: SelectionSnapshot;
};

export type SelectionEnd = { column: Element; row: Element; rowIndex: number; offset: number; text: string; rowCount: number };

export type SelectionSnapshot = { anchor: SelectionEnd; focus: SelectionEnd; rows: SelectionRow[] };

type SelectionRow = { text: string; line: string | null; altLine: string | null; type: string | null; side: "deletions" | "additions" };

type ShadowRootWithSelection = ShadowRoot & { getSelection?: () => Selection | null };

const ROW_SELECTOR = "[data-line][data-line-type]";

function rowElement(node: Node | null): Element | null {
	const element = node instanceof Element ? node : node?.parentElement;
	return element?.closest(ROW_SELECTOR) ?? null;
}

function inDeletionsColumn(element: Element): boolean {
	return element.closest("[data-deletions]") !== null;
}

function diffRow(element: Element): DiffRow {
	const line = Number(element.getAttribute("data-line"));
	const type = element.getAttribute("data-line-type") ?? "";
	const text = normalizeCodeRowText(element.textContent ?? "");
	if (type === "change-addition") return { kind: "add", oldNo: null, newNo: line, text };
	if (type === "change-deletion") return { kind: "del", oldNo: line, newNo: null, text };
	const alt = element.getAttribute("data-alt-line");
	const other = alt ? Number(alt) : line;
	return inDeletionsColumn(element)
		? { kind: "context", oldNo: line, newNo: other, text }
		: { kind: "context", oldNo: other, newNo: line, text };
}

function rowSide(element: Element): "deletions" | "additions" {
	return element.getAttribute("data-line-type") === "change-deletion" || inDeletionsColumn(element) ? "deletions" : "additions";
}

function snapshotRow(element: Element): SelectionRow {
	return {
		text: normalizeCodeRowText(element.textContent ?? ""),
		line: element.getAttribute("data-line"),
		altLine: element.getAttribute("data-alt-line"),
		type: element.getAttribute("data-line-type"),
		side: rowSide(element),
	};
}

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

function textOffsetIn(row: Element, node: Node, offset: number): number {
	const preceding = document.createRange();
	preceding.setStart(row, 0);
	preceding.setEnd(node, offset);
	return preceding.toString().length;
}

function locateText(row: Element, offset: number): { node: Text; offset: number } | null {
	const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT);
	let last: Text | null = null;
	let remaining = offset;
	for (let current = walker.nextNode(); current; current = walker.nextNode()) {
		const text = current as Text;
		if (remaining <= text.length) return { node: text, offset: remaining };
		remaining -= text.length;
		last = text;
	}
	return last ? { node: last, offset: last.length } : null;
}

function selectionEnd(node: Node, offset: number): SelectionEnd | null {
	const row = rowElement(node);
	const column = row?.parentElement;
	if (!row || !column) return null;
	const rows = [...column.children].filter((child): child is Element => child instanceof Element && child.matches(ROW_SELECTOR));
	return { column, row, rowIndex: rows.indexOf(row), offset: textOffsetIn(row, node, offset), text: normalizeCodeRowText(row.textContent ?? ""), rowCount: rows.length };
}

export function restoreCodeSelection(host: HTMLElement, snapshot: SelectionSnapshot): boolean {
	if (snapshot.anchor.row.isConnected || snapshot.focus.row.isConnected) return false;
	if (snapshot.anchor.column !== snapshot.focus.column) return false;
	const root = host.shadowRoot as ShadowRootWithSelection | null;
	if (!root) return false;
	const place = (end: SelectionEnd) => {
		if (!end.column.isConnected) return null;
		const rows = [...end.column.children].filter((child): child is Element => child instanceof Element && child.matches(ROW_SELECTOR));
		if (rows.length !== end.rowCount) return null;
		const row = rows[end.rowIndex];
		if (!row || normalizeCodeRowText(row.textContent ?? "") !== end.text) return null;
		return locateText(row, end.offset);
	};
	const anchor = place(snapshot.anchor);
	const focus = place(snapshot.focus);
	if (!anchor || !focus) return false;
	const rows = [...snapshot.anchor.column.children].filter((child): child is Element => child instanceof Element && child.matches(ROW_SELECTOR));
	const first = Math.min(snapshot.anchor.rowIndex, snapshot.focus.rowIndex);
	const last = Math.max(snapshot.anchor.rowIndex, snapshot.focus.rowIndex);
	if (last - first + 1 !== snapshot.rows.length) return false;
	for (let index = first; index <= last; index++) {
		const row = rows[index];
		if (!row) return false;
		const next = snapshotRow(row);
		const captured = snapshot.rows[index - first];
		if (next.text !== captured.text || next.line !== captured.line || next.altLine !== captured.altLine
			|| next.type !== captured.type || next.side !== captured.side) return false;
	}
	root.getSelection?.()?.setBaseAndExtent(anchor.node, anchor.offset, focus.node, focus.offset);
	return true;
}

export function codeReference(path: string, selection: CodeSelection, diff: boolean): FileCodeReference {
	return {
		path,
		side: diff ? (selection.range.side === "deletions" ? "old" : "new") : "file",
		line: selection.range.start,
		endLine: selection.range.end,
		endSide: diff ? (selection.range.endSide === "deletions" ? "old" : "new") : "file",
		lines: selection.rows.map(({ kind, oldNo, newNo, text, side }) => ({ kind: kind as "context" | "add" | "del", oldNo, newNo, text, side: diff ? side : "file" })),
	};
}

export function codeSelectionRange(host: HTMLElement): Range | null {
	const selection = (host.shadowRoot as ShadowRootWithSelection | null)?.getSelection?.();
	const range = selection ? selectionRange(selection) : null;
	return range && !range.collapsed && host.shadowRoot?.contains(range.startContainer) && host.shadowRoot.contains(range.endContainer) ? range : null;
}

export function readCodeSelection(host: HTMLElement, dragging = false): CodeSelection | null {
	const root = host.shadowRoot as ShadowRootWithSelection | null;
	const selection = root?.getSelection?.();
	const range = selection ? selectionRange(selection) : null;
	if (!root || !selection || !range || range.collapsed || !range.toString().trim()) return null;
	const anchor = rowElement(selection.anchorNode);
	const focus = rowElement(selection.focusNode);
	if (!anchor || !focus || !root.contains(anchor) || !root.contains(focus)) return null;
	const column = anchor.parentElement;
	const candidates = [...root.querySelectorAll(ROW_SELECTOR)].filter((row) => row.parentElement === column);
	const from = candidates.indexOf(anchor);
	const to = candidates.indexOf(focus);
	if (from < 0 || to < 0) return null;
	const elements = candidates.slice(Math.min(from, to), Math.max(from, to) + 1);
	const snapshotRows = elements.map(snapshotRow);
	const end = elements[elements.length - 1];
	if (elements.length > 1 && end.contains(range.endContainer)) {
		const head = document.createRange();
		head.setStart(end, 0);
		head.setEnd(range.endContainer, range.endOffset);
		if (!head.toString()) elements.pop();
	}
	const first = elements[0];
	const last = elements[elements.length - 1];
	const lastBox = last.getBoundingClientRect();
	const onLastLine = [...range.getClientRects()].filter((rect) => rect.width > 0 && rect.bottom > lastBox.top + 1 && rect.top < lastBox.bottom - 1);
	const edge = onLastLine.length ? Math.max(...onLastLine.map((rect) => rect.right)) : lastBox.left;
	const viewport = scrollParent(host);
	return {
		host,
		rows: elements.map(element => ({ ...diffRow(element), side: rowSide(element) === "deletions" ? "old" : "new" })),
		range: { start: Number(first.getAttribute("data-line")), side: rowSide(first), end: Number(last.getAttribute("data-line")), endSide: rowSide(last) },
		point: { x: edge, y: lastBox.top + lastBox.height / 2 },
		dragging,
		textRange: range,
		viewport,
		toolbarHeader: host.querySelector("[data-selection-toolbar-header]") ?? host.closest("[data-selection-toolbar-scope]")?.querySelector("[data-selection-toolbar-header]") ?? undefined,
		snapshot: {
			anchor: selectionEnd(selection.anchorNode!, selection.anchorOffset)!,
			focus: selectionEnd(selection.focusNode!, selection.focusOffset)!,
			rows: snapshotRows,
		},
	};
}

function scrollParent(element: Element): Element {
	for (let node = element.parentElement; node; node = node.parentElement) {
		if (/(auto|scroll)/.test(getComputedStyle(node).overflowY)) return node;
	}
	return document.documentElement;
}

function sameRange(a?: Range, b?: Range): boolean {
	if (!a || !b) return a === b;
	return a.startContainer === b.startContainer && a.startOffset === b.startOffset
		&& a.endContainer === b.endContainer && a.endOffset === b.endOffset;
}

function sameSelection(a: CodeSelection | null, b: CodeSelection | null): boolean {
	if (!a || !b) return a === b;
	return a.host === b.host && a.range.start === b.range.start && a.range.end === b.range.end && a.range.side === b.range.side
		&& a.range.endSide === b.range.endSide && Math.round(a.point.x) === Math.round(b.point.x) && Math.round(a.point.y) === Math.round(b.point.y)
		&& a.dragging === b.dragging && sameRange(a.textRange, b.textRange);
}

export type CodeSelectionSource = {
	get: () => CodeSelection | null;
	subscribe: (listener: () => void) => () => void;
};

export function useCodeSelection(containerRef: RefObject<HTMLElement | null>, onAsk: (selection: CodeSelection) => boolean | void, enabled = true) {
	const [store] = useState(createSelectionStore);
	const selectionRef = useRef<CodeSelection | null>(null);
	const onAskRef = useRef(onAsk);
	onAskRef.current = onAsk;
	const ask = useCallback((): boolean => {
		const current = selectionRef.current;
		if (!current) return false;
		if (current.textRange && (!current.host.isConnected || !current.textRange.startContainer.isConnected || !current.textRange.endContainer.isConnected)) return false;
		if (current.textRange) {
			const liveSelection = (current.host.shadowRoot as ShadowRootWithSelection | null)?.getSelection?.();
			const liveRange = liveSelection ? selectionRange(liveSelection) : null;
			const toolbarFocused = document.activeElement?.closest("[data-selection-ask]");
			if (!selectionRangesEqual(liveRange ?? undefined, current.textRange) && !(toolbarFocused && (!liveRange || liveRange.collapsed))) return false;
		}
		if (onAskRef.current(current) === false) return false;
		clearSelection(current.host);
		selectionRef.current = null;
		store.set(null);
		return true;
	}, [store]);
	const dismiss = useCallback(() => {
		const current = selectionRef.current;
		if (current) clearSelection(current.host);
		selectionRef.current = null;
		store.set(null);
	}, [store]);
	useEffect(() => {
		const publish = (next: CodeSelection | null) => {
			selectionRef.current = next;
			store.set(next);
		};
		if (!enabled) {
			publish(null);
			return;
		}
		const hosts = () => [...(containerRef.current?.querySelectorAll<HTMLElement>("diffs-container") ?? [])];
		let frame: number | null = null;
		let pressed = false;
		let restoredFor: SelectionSnapshot | null = null;
		const redraws = new MutationObserver(() => schedule());
		const update = () => {
			frame = null;
			const current = selectionRef.current;
			let next: CodeSelection | null = null;
			for (const host of hosts()) {
				next = readCodeSelection(host, pressed);
				if (next) break;
			}
			redraws.disconnect();
			if (next || current) for (const host of hosts()) if (host.shadowRoot) redraws.observe(host.shadowRoot, { childList: true, subtree: true });
			if (!next && current && restoredFor !== current.snapshot && restoreCodeSelection(current.host, current.snapshot)) {
				restoredFor = current.snapshot;
				schedule();
				return;
			}
			restoredFor = null;
			publish(next);
		};
		const schedule = () => {
			if (frame === null) frame = requestAnimationFrame(update);
		};
		const onPointerDown = (event: PointerEvent) => {
			if (event.button !== 0) return;
			pressed = true;
			const current = selectionRef.current;
			if (current && !event.composedPath().includes(current.host)
				&& !(event.target instanceof Element && event.target.closest("[data-selection-ask]"))) dismiss();
		};
		const onPointerUp = () => {
			pressed = false;
			schedule();
		};
		document.addEventListener("selectionchange", schedule);
		document.addEventListener("pointerdown", onPointerDown, true);
		document.addEventListener("pointerup", onPointerUp, true);
		document.addEventListener("pointercancel", onPointerUp, true);
		schedule();
		return () => {
			document.removeEventListener("selectionchange", schedule);
			document.removeEventListener("pointerdown", onPointerDown, true);
			document.removeEventListener("pointerup", onPointerUp, true);
			document.removeEventListener("pointercancel", onPointerUp, true);
			if (frame !== null) cancelAnimationFrame(frame);
			redraws.disconnect();
			publish(null);
		};
	}, [ask, dismiss, containerRef, enabled, store]);
	return { source: store, ask, dismiss };
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
