import { useEffect, useRef, useState, type PointerEvent } from "react";
import { cn } from "../../lib/utils";
import { OptionMenuItem } from "../ui/option-menu";

// An upright pill, a little taller than the bar it sits on.
const THUMB_W = 16;
const THUMB_H = 24;
const TRACK_H = 18;
// How long a sent level is held on screen while the agent has not reported it.
const SENT_HOLD_MS = 2500;
// The scale's colour, Orchestrator's brand blue (themed): the dots of the fill and the thumb's ring.
const BRAND = "var(--color-brand-logo)";

/** Where a level's centre sits on the track: inset by half a thumb at each end, so the thumb stays inside. */
function centre(percent: number): string {
	return `calc(${percent}% + ${(THUMB_W / 2) * (1 - percent / 50)}px)`;
}

// Cells of the dot field: a 2px dot every 4px.
const CELL = 4;
const DOT = 2;

/** A stable pseudo-random number in [0, 1) for a cell, so the pattern does not reshuffle on each frame. */
function hash(column: number, row: number, salt: number): number {
	const value = Math.sin(column * 127.1 + row * 311.7 + salt * 74.7) * 43758.5453;
	return value - Math.floor(value);
}

/**
 * The filled part's texture: a grid of small dots that grows denser and brighter towards the thumb,
 * each twinkling on its own beat. Still, and drawn once, when motion is reduced or the slider is off.
 */
function DotField({ still }: { still: boolean }) {
	const canvasRef = useRef<HTMLCanvasElement>(null);
	useEffect(() => {
		const canvas = canvasRef.current;
		const context = canvas?.getContext("2d");
		// No drawing surface (a test DOM): the gradient alone stands in.
		if (!canvas || !context || typeof context.fillRect !== "function") return;
		const reduce = still || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
		let width = 0;
		let height = 0;
		const draw = (time: number) => {
			const ratio = window.devicePixelRatio || 1;
			const box = canvas.getBoundingClientRect();
			if (box.width !== width || box.height !== height) {
				width = box.width;
				height = box.height;
				canvas.width = Math.round(width * ratio);
				canvas.height = Math.round(height * ratio);
			}
			context.setTransform(ratio, 0, 0, ratio, 0, 0);
			context.clearRect(0, 0, width, height);
			// The canvas carries the brand colour as its text colour, so a theme change is picked up here.
			context.fillStyle = getComputedStyle(canvas).color;
			const columns = Math.floor(width / CELL);
			const rows = Math.floor(height / CELL);
			const top = (height - rows * CELL) / 2;
			for (let column = 0; column < columns; column++) {
				// How far along the fill: dots thin out towards its start.
				const along = columns > 1 ? column / (columns - 1) : 1;
				for (let row = 0; row < rows; row++) {
					if (hash(column, row, 1) > 0.15 + along * 0.6) continue;
					const beat = reduce ? 1 : 0.5 + 0.5 * Math.sin(time / (600 + hash(column, row, 2) * 900) + hash(column, row, 3) * 6.28);
					const alpha = (0.15 + along * 0.75) * (0.35 + 0.65 * beat);
					context.globalAlpha = alpha;
					context.fillRect(column * CELL + (CELL - DOT) / 2, top + row * CELL + (CELL - DOT) / 2, DOT, DOT);
				}
			}
		};
		draw(performance.now());
		// The fill widens and narrows with the level, so the field is drawn again to its new size.
		const resized = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(() => draw(performance.now()));
		resized?.observe(canvas);
		// A gentle shimmer is enough: a dozen frames a second, only while the menu is open.
		const timer = reduce ? undefined : window.setInterval(() => draw(performance.now()), 80);
		return () => {
			resized?.disconnect();
			if (timer !== undefined) window.clearInterval(timer);
		};
	}, [still]);
	return <canvas ref={canvasRef} aria-hidden="true" className="absolute inset-0 size-full" style={{ color: BRAND }} />;
}

/**
 * The reasoning-effort setting as a stepped slider: a tick per level, filled up to the chosen one
 * with a field of dots that grows denser towards the thumb.
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
	const index = drag !== undefined ? Math.round(drag * last) : sent ?? Math.max(chosen, 0);
	const shown = drag !== undefined || sent !== undefined || chosen >= 0 ? choices[index] : undefined;
	const percent = drag !== undefined ? drag * 100 : (index / last) * 100;

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
			{/* The hit area is the thumb's height; the bar sits inside it. */}
			<div
				ref={trackRef}
				aria-hidden="true"
				className={cn("relative touch-none", disabled && "opacity-50")}
				style={{ height: THUMB_H }}
				onPointerDown={(event) => {
					if (disabled || event.button !== 0) return;
					event.currentTarget.setPointerCapture(event.pointerId);
					setDrag(alongAt(event));
				}}
				onPointerMove={(event) => {
					if (event.currentTarget.hasPointerCapture(event.pointerId)) setDrag(alongAt(event));
				}}
				onPointerUp={(event) => {
					if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
					event.currentTarget.releasePointerCapture(event.pointerId);
					commit(Math.round(alongAt(event) * last));
				}}
				onPointerCancel={() => setDrag(undefined)}
			>
				<div className="absolute inset-x-0 top-1/2 -translate-y-1/2 overflow-hidden rounded-md bg-interactive-active" style={{ height: TRACK_H }}>
					{/* The filled part reaches the thumb's centre: the bar keeps its own colour, the dots mark the fill. */}
					<div
						// Eased only into a level, never while it follows the pointer.
						className={cn("absolute inset-y-0 left-0 overflow-hidden", drag === undefined && "motion-safe:transition-[width] motion-safe:duration-150")}
						style={{ width: centre(percent) }}
					>
						<DotField still={Boolean(disabled)} />
					</div>
				</div>
				{choices.map((choice, step) => (
					<span
						key={choice.value}
						className="absolute top-1/2 h-2.5 w-0.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-foreground/25"
						style={{ left: centre((step / last) * 100) }}
					/>
				))}
				<span
					className={cn(
						"absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 bg-white",
						drag === undefined && "motion-safe:transition-[left] motion-safe:duration-150",
						chosen < 0 && drag === undefined && sent === undefined && "opacity-70",
					)}
					style={{
						left: centre(percent),
						width: THUMB_W,
						height: THUMB_H,
						borderColor: BRAND,
					}}
				/>
			</div>
		</OptionMenuItem>
	);
}
