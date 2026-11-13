import { useVirtualizer } from "@tanstack/react-virtual";
import { useLayoutEffect, useState, type CSSProperties } from "react";

/** The grid as laid out: how many cards a row holds and the distance from one row's top to the next. */
export type GridLayout = {
	columns: number;
	/** Row height plus the gap below it. */
	pitch: number;
	gap: number;
	/** Where the grid's first row starts in the scroll element's content. */
	offset: number;
	/** The scroll element's visible height. */
	viewport: number;
};

/** Rows kept above and below the screen, so a scroll never shows a blank row. */
const OVERSCAN_ROWS = 2;
/** Cards built before the grid has been measured: more than a screen holds, far fewer than a long list. */
export const FIRST_RENDER_CARDS = 24;

/**
 * Which cards to build, and the space the rest take: the cards from
 * `start` to `end`, a spacer of `before` pixels above them and `after`
 * below. Pure, so the arithmetic is testable without a layout engine.
 */
export function gridWindow(count: number, layout: Pick<GridLayout, "columns" | "pitch" | "gap">, firstRow: number, lastRow: number) {
	const rows = Math.ceil(count / layout.columns);
	if (rows === 0) return { start: 0, end: 0, before: 0, after: 0 };
	const first = Math.max(0, Math.min(firstRow, rows - 1));
	const last = Math.max(first, Math.min(lastRow, rows - 1));
	// A spacer is a grid item too, so the gap after it is part of the space it stands for.
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

function sameLayout(a: GridLayout, b: GridLayout): boolean {
	return a.columns === b.columns && a.gap === b.gap && a.viewport === b.viewport
		&& Math.abs(a.pitch - b.pitch) < 0.5 && Math.abs(a.offset - b.offset) < 0.5;
}

/**
 * Builds only the rows of a card grid that are on screen or near it; a
 * spacer above and below stands for the rest, so the list keeps its height
 * and the scrollbar its place. The grid keeps its CSS: its columns and card
 * size are read from the layout, so breakpoints keep working, and every card
 * must be as tall as the first. The first render builds the first screen or
 * so, and the grid is measured before it is painted. Where there is no
 * layout to measure (a test environment) every card is built.
 */
export function useVirtualGridRows(count: number, scrollElement?: HTMLElement | null) {
	const [list, setList] = useState<HTMLElement | null>(null);
	const scroller = scrollElement === undefined ? list : scrollElement;
	// undefined until measured; null once measuring found no layout.
	const [layout, setLayout] = useState<GridLayout | null>();
	const rows = layout ? Math.ceil(count / layout.columns) : 0;
	const virtualizer = useVirtualizer({
		count: rows,
		getScrollElement: () => scroller,
		estimateSize: () => layout?.pitch ?? 1,
		overscan: OVERSCAN_ROWS,
		scrollMargin: layout?.offset ?? 0,
		enabled: Boolean(layout),
	});

	useLayoutEffect(() => {
		if (!list || !scroller) return;
		const update = () => {
			const next = measure(list, scroller);
			setLayout((current) => (!next ? current ?? null : current && sameLayout(current, next) ? current : next));
		};
		update();
		// The list resizes when its width (and so its columns and card height) or its card count changes.
		const observer = new ResizeObserver(update);
		observer.observe(list);
		observer.observe(scroller);
		return () => observer.disconnect();
	}, [list, scroller, count]);

	// Every row has the same height, so a new pitch re-measures them all at once.
	useLayoutEffect(() => {
		if (layout) virtualizer.measure();
	}, [layout, virtualizer]);

	const range = (() => {
		if (layout === undefined) return { start: 0, end: Math.min(count, FIRST_RENDER_CARDS), before: 0, after: 0 };
		if (layout === null) return { start: 0, end: count, before: 0, after: 0 };
		const items = virtualizer.getVirtualItems();
		if (items.length > 0) return gridWindow(count, layout, items[0].index, items[items.length - 1].index);
		// The virtualizer reads the scroll position after its first render; until then, the rows on screen now.
		const top = Math.max(0, (scroller?.scrollTop ?? 0) - layout.offset);
		return gridWindow(count, layout, Math.floor(top / layout.pitch) - OVERSCAN_ROWS, Math.ceil((top + layout.viewport) / layout.pitch) + OVERSCAN_ROWS);
	})();
	return { ...range, listRef: setList };
}

/** A grid item that stands for the rows not built, so the list keeps its full height. */
export function GridSpacer({ height }: { height: number }) {
	if (height <= 0) return null;
	const style: CSSProperties = { gridColumn: "1 / -1", height };
	return <li aria-hidden="true" data-grid-spacer="" role="presentation" style={style} />;
}
