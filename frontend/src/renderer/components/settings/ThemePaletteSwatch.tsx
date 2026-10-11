import type { ThemePalette } from "../../lib/theme-palettes";

/**
 * A theme's palette drawn on its own canvas: accent, then status hues. Colors
 * come from the palette, not tokens, because each swatch previews a theme that
 * usually isn't the active one. `compact` keeps three hues for the trigger.
 */
export function ThemePaletteSwatch({ palette, compact = false }: { palette: ThemePalette; compact?: boolean }) {
	const hues = compact
		? [palette.accent, palette.green, palette.red]
		: [palette.accent, palette.red, palette.orange, palette.yellow, palette.green, palette.blue, palette.purple];
	return (
		<span
			aria-hidden="true"
			className="inline-flex h-4.5 shrink-0 items-center gap-[3px] rounded-[4px] px-1 ring-1 ring-border"
			style={{ backgroundColor: palette.bg }}
		>
			{hues.map((color, index) => (
				<span key={index} className="size-2 rounded-full" style={{ backgroundColor: color }} />
			))}
		</span>
	);
}
