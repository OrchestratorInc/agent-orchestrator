import type { ThemePalette } from "../../lib/theme-palettes";

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
