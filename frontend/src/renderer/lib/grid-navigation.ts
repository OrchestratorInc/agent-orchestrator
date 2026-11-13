/** The keys that move a selection through a grid of cards. */
export type GridNavigationKey = "ArrowLeft" | "ArrowRight" | "ArrowUp" | "ArrowDown" | "Home" | "End" | "PageUp" | "PageDown";

const GRID_KEYS = new Set<string>(["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "PageUp", "PageDown"]);

export function isGridNavigationKey(key: string): key is GridNavigationKey {
	return GRID_KEYS.has(key);
}

/**
 * Where a key moves the selection in a grid filled row by row, `columns` wide,
 * with `count` items in all (mounted or not). Left and Right step one item
 * within the row, stopping at its ends rather than wrapping; Up and Down keep the column and stop at the first and last rows, and Down
 * into a short last row lands on its last item; PageUp and PageDown move
 * `pageRows` rows the same way, then to the first or last item. -1 when empty.
 */
export function gridNavigationTarget(key: GridNavigationKey, index: number, count: number, columns: number, pageRows: number): number {
	if (count <= 0) return -1;
	const last = count - 1;
	const cols = Math.max(1, Math.floor(columns));
	const rows = Math.max(1, Math.floor(pageRows));
	const current = Math.min(Math.max(index, 0), last);
	const row = Math.floor(current / cols);
	const column = current % cols;
	const lastRow = Math.floor(last / cols);
	switch (key) {
		case "ArrowLeft":
			return column === 0 ? current : current - 1;
		case "ArrowRight":
			return column === cols - 1 ? current : Math.min(last, current + 1);
		case "ArrowUp":
			return row === 0 ? current : current - cols;
		case "ArrowDown":
			return row === lastRow ? current : Math.min(last, current + cols);
		case "Home":
			return 0;
		case "End":
			return last;
		case "PageUp":
			return row === 0 ? 0 : Math.max(0, row - rows) * cols + column;
		case "PageDown":
			return row === lastRow ? last : Math.min(last, Math.min(lastRow, row + rows) * cols + column);
	}
}

/**
 * Columns in a CSS grid as laid out now: the resolved track list of a
 * responsive `repeat(auto-fill, …)` template, one px length per column. 1 when
 * it cannot be read (no layout, as in a test without one).
 */
export function measureGridColumns(grid: HTMLElement | null): number {
	if (!grid) return 1;
	const template = getComputedStyle(grid).getPropertyValue("grid-template-columns").trim();
	if (!template) return 1;
	const tracks = template.split(/\s+/);
	const columns = tracks.filter((track) => /^\d+(\.\d+)?px$/.test(track)).length;
	return columns > 0 && columns === tracks.length ? columns : 1;
}

/**
 * Whole rows of `grid` that fit in `viewport`'s height, counted from the row
 * holding item `fromIndex` (included) downwards (`direction` 1, for PageDown)
 * or upwards (-1, for PageUp), by the rows' laid-out positions (rows can differ
 * in height). At least 1; 1 when there is no layout to read.
 */
export function measureVisibleRows(viewport: HTMLElement | null, grid: HTMLElement | null, fromIndex: number, columns: number, direction: 1 | -1 = 1): number {
	const items = grid?.children;
	if (!viewport || !items || items.length === 0) return 1;
	const cols = Math.max(1, Math.floor(columns));
	const height = viewport.clientHeight;
	// A row's top is its first item's; its bottom, its tallest item's.
	const rowBounds = (row: number) => {
		const start = row * cols;
		const top = items[start]?.getBoundingClientRect().top ?? 0;
		let bottom = top;
		for (let index = start; index < Math.min(start + cols, items.length); index += 1) {
			bottom = Math.max(bottom, items[index]?.getBoundingClientRect().bottom ?? 0);
		}
		return { bottom, top };
	};
	const firstRow = Math.floor(Math.min(Math.max(fromIndex, 0), items.length - 1) / cols);
	const lastRow = Math.floor((items.length - 1) / cols);
	const anchor = rowBounds(firstRow);
	let rows = 0;
	for (let row = firstRow; row >= 0 && row <= lastRow; row += direction) {
		const bounds = rowBounds(row);
		if (!(bounds.bottom > bounds.top)) break;
		const span = direction > 0 ? bounds.bottom - anchor.top : anchor.bottom - bounds.top;
		if (span > height) break;
		rows += 1;
	}
	return Math.max(1, rows);
}

/**
 * Bounds a move in a windowed grid: it may mount at most `maxMount` items past
 * the `mountedCount` already mounted, so End in a huge list cannot mount every
 * item in one update. Past that bound it stops on the furthest item it may
 * mount, and pressing again goes on from there.
 */
export function boundedGridTarget(target: number, mountedCount: number, maxMount: number): number {
	if (target < 0) return target;
	return Math.min(target, Math.max(0, mountedCount) + Math.max(1, Math.floor(maxMount)) - 1);
}

/**
 * How many items to mount for a move onto `target`: the target, then a
 * `prefetch` of items past it, all within the same `maxMount` budget past the
 * `mountedCount` already mounted (the target itself always counts).
 */
export function mountLimitForTarget(target: number, mountedCount: number, prefetch: number, maxMount: number): number {
	const reach = target + 1;
	return Math.max(reach, Math.min(reach + Math.max(0, prefetch), Math.max(0, mountedCount) + Math.max(1, maxMount)));
}
