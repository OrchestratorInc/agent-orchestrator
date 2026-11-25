import { useEffect, useRef, useState, type PointerEvent } from "react";
import { cn } from "../../lib/utils";
import { OptionMenuItem } from "../ui/option-menu";

// A small upright rounded square, as tall as the squared bar it sits on.
const THUMB_W = 14;
const THUMB_H = 22;
const TRACK_H = THUMB_H;
// How long a sent level is held on screen while the agent has not reported it.
const SENT_HOLD_MS = 2500;
// How far a press moves before it is a drag; a shorter one is a click, which eases to the nearest level.
const DRAG_SLOP_PX = 3;

/** Where a level's centre sits on the track: inset by half a thumb at each end, so the thumb stays inside. */
function centre(percent: number): string {
	return `calc(${percent}% + ${(THUMB_W / 2) * (1 - percent / 50)}px)`;
}

/**
 * The reasoning-effort setting as a stepped slider: a tick per level, filled up to the chosen one
 * in a lighter grey, or in the ultra colours at the top level.
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
	const last = Math.max(choices.length - 1, 1);
	// While dragging, how far along the track the pointer is (0 to 1): the thumb follows it freely,
	// and only the release picks a level. Nothing is sent before then: each send updates or restarts the agent.
	const [drag, setDrag] = useState<number | undefined>();
	// A level just sent stays on screen until the agent reports it, so the thumb does not jump back
	// meanwhile. If the agent never does (it refused), the reported level shows again shortly.
	const [sent, setSent] = useState<number | undefined>();
	useEffect(() => setSent(undefined), [value]);
	useEffect(() => {
		if (sent === undefined) return;
		const timer = window.setTimeout(() => setSent(undefined), SENT_HOLD_MS);
		return () => window.clearTimeout(timer);
	}, [sent]);
	const trackRef = useRef<HTMLDivElement>(null);
	// Where a press on the track started, until it moves far enough to be a drag.
	const pressX = useRef<number | undefined>(undefined);
	const index = drag !== undefined ? Math.round(drag * last) : sent ?? Math.max(chosen, 0);
	const shown = drag !== undefined || sent !== undefined || chosen >= 0 ? choices[index] : undefined;
	const percent = drag !== undefined ? drag * 100 : (index / last) * 100;
	// The top level past Max (Codex's ultra, Claude Code's ultracode) fills the bar in its own colours.
	const ultra = Boolean(shown && ultraValue?.(shown.value));

	const commit = (next: number) => {
		setDrag(undefined);
		const choice = choices[next];
		if (!choice || choice.value === value) return setSent(undefined);
		setSent(next);
		onCommit(choice.value);
	};
	const alongAt = (event: PointerEvent) => {
		const rect = trackRef.current?.getBoundingClientRect();
		if (!rect || rect.width <= THUMB_W) return index / last;
		return Math.min(Math.max((event.clientX - rect.left - THUMB_W / 2) / (rect.width - THUMB_W), 0), 1);
	};

	return (
		<OptionMenuItem
			// Announced as a slider, not a plain row, so its range and level are read out.
			role="slider"
			aria-label={label}
			aria-valuemin={0}
			aria-valuemax={Math.max(choices.length - 1, 0)}
			aria-valuenow={index}
			aria-valuetext={valueLabel(shown)}
			aria-orientation="horizontal"
			disabled={disabled}
			// The row stays open on a click or Enter: the level is set on the track, with Left and Right,
			// or with Home and End for the lowest and highest.
			onSelect={(event) => event.preventDefault()}
			onKeyDown={(event) => {
				const next = { ArrowLeft: index - 1, ArrowRight: index + 1, Home: 0, End: last }[event.key];
				if (next === undefined) return;
				event.preventDefault();
				event.stopPropagation();
				commit(Math.min(Math.max(next, 0), choices.length - 1));
			}}
			// No hover or highlight fill like the other rows: the slider is not picked by clicking the row.
			// A keyboard visit rings the thumb instead.
			// Less padding above than below: the label's line height already leaves room over its letters,
			// so the bar gets a little more room below it than the text has above.
			className="group/effort flex-col items-stretch gap-0.5 pt-0.5 pb-2 text-xs text-foreground focus:before:bg-transparent data-highlighted:before:bg-transparent"
		>
			<span className="flex items-baseline justify-between gap-3">
				<span>{label}</span>
				<span className={cn(shown && ultraValue?.(shown.value) ? "ultra-text" : "text-muted-foreground")}>{valueLabel(shown)}</span>
			</span>
			{/* The hit area is the thumb's height; the bar sits inside it. */}
			<div
				ref={trackRef}
				aria-hidden="true"
				className={cn("relative touch-none", disabled && "opacity-50", drag !== undefined && "cursor-grabbing")}
				style={{ height: THUMB_H }}
				onPointerDown={(event) => {
					if (disabled || event.button !== 0) return;
					event.currentTarget.setPointerCapture(event.pointerId);
					pressX.current = event.clientX;
				}}
				onPointerMove={(event) => {
					if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
					// The thumb follows the pointer only once the press has moved: a click does not jump it there.
					if (drag === undefined && Math.abs(event.clientX - (pressX.current ?? event.clientX)) < DRAG_SLOP_PX) return;
					setDrag(alongAt(event));
				}}
				onPointerUp={(event) => {
					if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
					event.currentTarget.releasePointerCapture(event.pointerId);
					pressX.current = undefined;
					commit(Math.round(alongAt(event) * last));
				}}
				onPointerCancel={() => {
					pressX.current = undefined;
					setDrag(undefined);
				}}
			>
				<div className="absolute inset-x-0 top-1/2 -translate-y-1/2 overflow-hidden rounded-[3px] bg-interactive-active" style={{ height: TRACK_H }}>
					{/* The filled part, up to the thumb's centre: a lighter grey than the bar, or the ultra colours. */}
					<div
						// Eased only into a level, never while it follows the pointer.
						className={cn("absolute inset-y-0 left-0", ultra ? "ultra-fill" : "bg-foreground/20", drag === undefined && "motion-safe:transition-[width] motion-safe:duration-150")}
						style={{ width: centre(percent) }}
					/>
				</div>
				{choices.map((choice, step) => (
					<span
						key={choice.value}
						className="absolute top-1/2 h-2 w-0.5 -translate-x-1/2 -translate-y-1/2 bg-foreground/25"
						style={{ left: centre((step / last) * 100) }}
					/>
				))}
				<span
					className={cn(
						// The brightest part of the slider, in either theme, with a hairline edge.
						"absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-[3px] border-[0.5px] border-(--color-border-strong) bg-white shadow-[0_1px_2px_rgb(0_0_0/0.2)] group-focus-visible/effort:ring-2 group-focus-visible/effort:ring-ring/60",
						drag === undefined && "motion-safe:transition-[left] motion-safe:duration-150",
						!disabled && (drag === undefined ? "cursor-grab" : "cursor-grabbing"),
						chosen < 0 && drag === undefined && sent === undefined && "opacity-70",
					)}
					style={{
						left: centre(percent),
						width: THUMB_W,
						height: THUMB_H,
					}}
				/>
			</div>
		</OptionMenuItem>
	);
}
