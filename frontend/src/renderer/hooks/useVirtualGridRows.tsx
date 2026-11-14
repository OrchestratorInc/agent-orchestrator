import { useLayoutEffect, useState, type CSSProperties } from "react";

export type GridLayout = {
	columns: number;
	pitch: number;
	gap: number;
	offset: number;
	viewport: number;
};

const OVERSCAN_ROWS = 4;
export const FIRST_RENDER_CARDS = 24;

export function gridWindow(count: number, layout: Pick<GridLayout, "columns" | "pitch" | "gap">, firstRow: number, lastRow: number) {
	const rows = Math.ceil(count / layout.columns);
	if (rows === 0) return { start: 0, end: 0, before: 0, after: 0 };
	const first = Math.max(0, Math.min(firstRow, rows - 1));
	const last = Math.max(first, Math.min(lastRow, rows - 1));
	const before = first > 0 ? first * layout.pitch - layout.gap : 0;
	const after = last < rows - 1 ? (rows - 1 - last) * layout.pitch - layout.gap : 0;
	return { start: first * layout.columns, end: Math.min(count, (last + 1) * layout.columns), before, after };
}

function measure(list: HTMLElement, scroller: HTMLElement): GridLayout | undefined {
	const style = getComputedStyle(list);
	const columns = style.gridTemplateColumns.split(" ").filter(Boolean).length || 1;
	const gap = Number.parseFloat(style.rowGap) || 0;
	const card = list.querySelector<HTMLElement>(":scope > li:not([data-grid-spacer])");
	const height = card?.getBoundingClientRect().height ?? 0;
	if (height <= 0 || scroller.clientHeight <= 0) return undefined;
	const padding = Number.parseFloat(style.paddingTop) || 0;
	const offset = list === scroller
		? padding
		: list.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop + padding;
	return { columns, pitch: height + gap, gap, offset, viewport: scroller.clientHeight };
}

function rowsOnScreen(layout: GridLayout, scroller: HTMLElement, current?: { first: number; last: number }) {
	const top = Math.max(0, scroller.scrollTop - layout.offset);
	const first = Math.floor(top / layout.pitch);
	const last = Math.ceil((top + layout.viewport) / layout.pitch);
	if (current && (current.first === 0 || first - current.first >= 1) && current.last - last >= 1) return current;
	return { first: Math.max(0, first - OVERSCAN_ROWS), last: last + OVERSCAN_ROWS };
}

function sameLayout(a: GridLayout, b: GridLayout): boolean {
	return a.columns === b.columns && a.gap === b.gap && a.viewport === b.viewport
		&& Math.abs(a.pitch - b.pitch) < 0.5 && Math.abs(a.offset - b.offset) < 0.5;
}

export function useVirtualGridRows(count: number, scrollElement?: HTMLElement | null) {
	const [list, setList] = useState<HTMLElement | null>(null);
	const scroller = scrollElement === undefined ? list : scrollElement;
	const [layout, setLayout] = useState<GridLayout | null>();
	const [rows, setRows] = useState<{ first: number; last: number }>();

	useLayoutEffect(() => {
		if (!list || !scroller) return;
		const update = () => {
			const next = measure(list, scroller);
			setLayout((current) => (!next ? current ?? null : current && sameLayout(current, next) ? current : next));
		};
		update();
		const observer = new ResizeObserver(update);
		observer.observe(list);
		observer.observe(scroller);
		return () => observer.disconnect();
	}, [list, scroller, count]);

	useLayoutEffect(() => {
		if (!layout || !scroller) return;
		const read = () => {
			setRows((current) => {
				const next = rowsOnScreen(layout, scroller, current);
				return current?.first === next.first && current.last === next.last ? current : next;
			});
		};
		read();
		let frame = 0;
		const onScroll = () => {
			if (frame) return;
			frame = requestAnimationFrame(() => {
				frame = 0;
				read();
			});
		};
		scroller.addEventListener("scroll", onScroll, { passive: true });
		return () => {
			scroller.removeEventListener("scroll", onScroll);
			cancelAnimationFrame(frame);
		};
	}, [layout, scroller]);

	const range = (() => {
		if (layout === undefined) return { start: 0, end: Math.min(count, FIRST_RENDER_CARDS), before: 0, after: 0 };
		if (layout === null) return { start: 0, end: count, before: 0, after: 0 };
		if (!scroller) return { start: 0, end: Math.min(count, FIRST_RENDER_CARDS), before: 0, after: 0 };
		const visible = rows ?? rowsOnScreen(layout, scroller);
		return gridWindow(count, layout, visible.first, visible.last);
	})();
	return { ...range, listRef: setList };
}

export function GridSpacer({ height }: { height: number }) {
	if (height <= 0) return null;
	const style: CSSProperties = { gridColumn: "1 / -1", height };
	return <li aria-hidden="true" data-grid-spacer="" role="presentation" style={style} />;
}
