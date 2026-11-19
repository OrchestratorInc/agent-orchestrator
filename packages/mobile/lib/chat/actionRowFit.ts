/**
 * Widths for a row of decisions that must share one line, or undefined while
 * they fit at their own widths (or before every width is known).
 *
 * Max-min fair: each decision gets at most an equal share of what's left, the
 * shortest first, so a short label keeps its own width and hands back what it
 * doesn't need, and no long label can take the width the others need.
 */
export function fairWidths(natural: readonly (number | undefined)[], available: number): number[] | undefined {
	if (!(available > 0) || natural.some((width) => width === undefined)) return undefined;
	const widths = natural as readonly number[];
	if (widths.reduce((sum, width) => sum + width, 0) <= available) return undefined;
	const out = new Array<number>(widths.length);
	let remaining = available;
	let left = widths.length;
	for (const index of widths.map((_, i) => i).sort((a, b) => widths[a] - widths[b])) {
		const width = Math.min(widths[index], remaining / left);
		out[index] = Math.floor(width);
		remaining -= width;
		left -= 1;
	}
	return out;
}
