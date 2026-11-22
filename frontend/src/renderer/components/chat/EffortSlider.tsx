import { useEffect, useRef, useState, type PointerEvent } from "react";
import { cn } from "../../lib/utils";
import { OptionMenuItem } from "../ui/option-menu";

const THUMB_PX = 18;

/** Where a level's centre sits on the track: inset by half a thumb at each end, so the thumb stays inside. */
function centre(percent: number): string {
	return `calc(${percent}% + ${(THUMB_PX / 2) * (1 - percent / 50)}px)`;
}

/**
 * The reasoning-effort setting as a stepped slider: one dot per level, filled up to the chosen one.
 * It is a menu row, so the menu's arrow keys reach it like any other; Left and Right change the level.
 */
export function EffortSlider({
	label,
	valueLabel,
	choices,
	value,
	disabled,
	ultraValue,
	onCommit,
}: {
	label: string;
	/** What the right edge reads for the level shown, or for "no level chosen". */
	valueLabel: (choice: { value: string; label: string } | undefined) => string;
	choices: Array<{ value: string; label: string }>;
	/** The chosen level; unset when the agent has not been given one. */
	value?: string;
	disabled?: boolean;
	/** The level drawn with the animated top-of-scale treatment, if any. */
	ultraValue?: (value: string) => boolean;
	onCommit: (value: string) => void;
}) {
	const chosen = choices.findIndex((choice) => choice.value === value);
	// A drag previews a level without sending it: each commit updates or restarts the agent.
	const [draft, setDraft] = useState<number | undefined>();
	useEffect(() => setDraft(undefined), [value]);
	const trackRef = useRef<HTMLDivElement>(null);
	const index = draft ?? Math.max(chosen, 0);
	const shown = draft !== undefined || chosen >= 0 ? choices[index] : undefined;
	const last = Math.max(choices.length - 1, 1);
	const percent = (index / last) * 100;

	const commit = (next: number) => {
		const choice = choices[next];
		if (choice && choice.value !== value) onCommit(choice.value);
		else setDraft(undefined);
	};
	const levelAt = (event: PointerEvent) => {
		const rect = trackRef.current?.getBoundingClientRect();
		if (!rect || rect.width <= THUMB_PX) return index;
		const along = (event.clientX - rect.left - THUMB_PX / 2) / (rect.width - THUMB_PX);
		return Math.round(Math.min(Math.max(along, 0), 1) * last);
	};

	return (
		<OptionMenuItem
			disabled={disabled}
			// The row stays open on a click or Enter: the level is set on the track or with Left and Right.
			onSelect={(event) => event.preventDefault()}
			onKeyDown={(event) => {
				if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
				event.preventDefault();
				event.stopPropagation();
				commit(Math.min(Math.max(index + (event.key === "ArrowRight" ? 1 : -1), 0), choices.length - 1));
			}}
			className="flex-col items-stretch gap-1 text-xs text-foreground"
		>
			<span className="flex items-baseline justify-between gap-3">
				<span>{label}</span>
				<span className={cn(shown && ultraValue?.(shown.value) ? "ultra-text" : "text-muted-foreground")}>{valueLabel(shown)}</span>
			</span>
			{/* The hit area is the thumb's height; the bar drawn inside it is thin. */}
			<div
				ref={trackRef}
				aria-hidden="true"
				className={cn("relative touch-none", disabled && "opacity-50")}
				style={{ height: THUMB_PX }}
				onPointerDown={(event) => {
					if (disabled || event.button !== 0) return;
					event.currentTarget.setPointerCapture(event.pointerId);
					setDraft(levelAt(event));
				}}
				onPointerMove={(event) => {
					if (event.currentTarget.hasPointerCapture(event.pointerId)) setDraft(levelAt(event));
				}}
				onPointerUp={(event) => {
					if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
					event.currentTarget.releasePointerCapture(event.pointerId);
					commit(levelAt(event));
				}}
				onPointerCancel={() => setDraft(undefined)}
			>
				<div className="absolute inset-x-0 top-1/2 h-2 -translate-y-1/2 overflow-hidden rounded-full bg-interactive-active">
					<div
						className="absolute inset-0 bg-muted-foreground/60"
						style={{ clipPath: `inset(0 calc(100% - ${centre(percent)}) 0 0)` }}
					/>
				</div>
				{choices.map((choice, step) => (
					<span
						key={choice.value}
						className={cn("absolute top-1/2 size-1 -translate-x-1/2 -translate-y-1/2 rounded-full", step <= index ? "bg-background/70" : "bg-foreground/20")}
						style={{ left: centre((step / last) * 100) }}
					/>
				))}
				<span
					className={cn(
						"absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full border border-black/10 bg-white shadow-sm motion-safe:transition-[left] motion-safe:duration-100",
						chosen < 0 && draft === undefined && "opacity-70",
					)}
					style={{ left: centre(percent), width: THUMB_PX, height: THUMB_PX }}
				/>
			</div>
		</OptionMenuItem>
	);
}
