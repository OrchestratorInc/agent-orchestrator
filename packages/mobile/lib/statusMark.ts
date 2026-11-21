import type { Theme } from "./theme";

/**
 * Fills for a status drawn as a shape (a worker row's square, its lattice)
 * rather than as words. A light theme's status hues are deepened so a label
 * reads on white (amber is #875900), which turns a filled mark muddy; a mark
 * needs no text contrast, so a light theme draws it in the bright hue instead.
 * A dark theme's hues are already bright and stay as they are.
 */
const LIGHT_FILLS = {
	amber: "#f5a524",
	green: "#12b76a",
	red: "#f04438",
	orange: "#f97316",
	purple: "#8b5cf6",
} as const;

export function statusMarkColor(t: Theme, color: string, scheme: "light" | "dark"): string {
	if (scheme === "dark") return color;
	for (const hue of Object.keys(LIGHT_FILLS) as (keyof typeof LIGHT_FILLS)[]) {
		if (color === t[hue]) return LIGHT_FILLS[hue];
	}
	return color;
}
