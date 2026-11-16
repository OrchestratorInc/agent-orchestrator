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

/**
 * Rows built above and below the screen. The built rows move only once the
 * screen comes within a row of their edge, so a scroll changes the list every
 * few rows rather than at every row: each change lays the page out again.
 */
const OVERSCAN_ROWS = 4;
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

/** The rows to build for the scroll position: `current` while it still covers the screen with a row to spare. */
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
	// The rows on screen, two either side included. Set only when a row comes or goes, never on
	// every scroll event: a re-render of the list per scroll frame would cost more than it saves.
	const [rows, setRows] = useState<{ first: number; last: number }>();

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
		// The list is coming back (the gallery after the viewer closes) and not attached yet: build
		// what the first render would, rather than every card, until it is measured again.
		if (!scroller) return { start: 0, end: Math.min(count, FIRST_RENDER_CARDS), before: 0, after: 0 };
		// Right after a new layout the scroll listener has not read the rows yet.
		const visible = rows ?? rowsOnScreen(layout, scroller);
		return gridWindow(count, layout, visible.first, visible.last);
	})();
	return { ...range, listRef: setList };
}

/** A grid item that stands for the rows not built, so the list keeps its full height. */
export function GridSpacer({ height }: { height: number }) {
	if (height <= 0) return null;
	const style: CSSProperties = { gridColumn: "1 / -1", height };
	return <li aria-hidden="true" data-grid-spacer="" role="presentation" style={style} />;
}
