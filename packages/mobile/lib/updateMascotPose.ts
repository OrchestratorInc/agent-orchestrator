// Ruto's quiet performance on the update screen, as pure functions of time.
//
// Restrained on purpose (DESIGN.md: motion is functional and modest), but
// alive, built the way character apps build mascots: a quiet idle layer plus
// small reactions.
//
//   - **Idle** — Ruto breathes, blinks at irregular moments, the baton barely
//     sways while its tip glows softly, and a sheen passes over Ruto now and
//     then, the "new" catching the light.
//   - **Poke** — tap Ruto: a small squish.
//   - **Finale** — Restart: one slower, brighter sheen, a nod with eyes closed
//     (the one haptic), and the stage fades into the reload.
//
// The idle clock runs from 0 to LOOP_MS and wraps; LOOP_MS is a whole number of
// every period below, so the wrap is invisible. Poke and finale take their own
// 0..1 progress. Angles are degrees, clockwise positive; distances are units of
// the brand file's 1024 grid.
//
// Each function is a Reanimated worklet (UI thread) and plain TypeScript
// (vitest), so the curves are tested without a React Native runtime.

/** A breath, a sheen and the baton's sway each take this long. */
export const SHEEN_MS = 3600;
/** Two of those; the blink pattern repeats once per loop. */
export const LOOP_MS = SHEEN_MS * 2;
export const POKE_MS = 600;
/** The finale plays while the screen holds before the reload. */
export const FINALE_MS = 1200;
/** Where in the finale the nod bottoms out: the haptic. */
export const FINALE_NOD = 0.35;

/** The baton turns around the middle of its end inside the hand. */
export const RUTO_PIVOT = { x: 779, y: 421 };
/** The baton's tip at rest. */
export const RUTO_TIP = { x: 835.5, y: 236 };
/**
 * Where the sheen band (150 wide, skewed 18°) starts and ends its sweep, in
 * grid x: fully clear of Ruto on both sides, so it enters and leaves unseen.
 */
export const SHEEN_FROM = 100;
export const SHEEN_TO = 1100;

/** Part of each period the sheen is crossing Ruto. */
const SHEEN_SWEEP = 0.3;
/** Blink midpoints across the loop, irregular on purpose, one a double blink. */
const BLINKS = [0.08, 0.46, 0.505, 0.83];
const BLINK_WIDTH = 0.028;

function mod(a: number, n: number): number {
	"worklet";
	const r = a % n;
	return r < 0 ? r + n : r;
}

function clamp01(v: number): number {
	"worklet";
	return v < 0 ? 0 : v > 1 ? 1 : v;
}

/** A 0→1→0 bump across [start, start + width), 0 outside it. */
function bump(p: number, start: number, width: number): number {
	"worklet";
	if (p < start || p >= start + width) return 0;
	return Math.sin((Math.PI * (p - start)) / width);
}

/** Progress of `p` through [start, end], clamped. */
function span(p: number, start: number, end: number): number {
	"worklet";
	return clamp01((p - start) / (end - start));
}

function easeInOut(t: number): number {
	"worklet";
	return t < 0.5 ? 2 * t * t : 1 - 2 * (1 - t) * (1 - t);
}

/** Breathing, 0..1. */
export function breathAt(ms: number): number {
	"worklet";
	return 0.5 - 0.5 * Math.cos((2 * Math.PI * mod(ms, SHEEN_MS)) / SHEEN_MS);
}

/** How shut the eyes are, 0 open → 1 shut. */
export function blinkAt(ms: number): number {
	"worklet";
	const p = mod(ms, LOOP_MS) / LOOP_MS;
	let shut = 0;
	for (let i = 0; i < BLINKS.length; i++) {
		shut = Math.max(shut, bump(p, BLINKS[i] - BLINK_WIDTH / 2, BLINK_WIDTH));
	}
	return shut;
}

/** The baton's sway in degrees: barely there. */
export function batonAt(ms: number): number {
	"worklet";
	return 2 * Math.sin((2 * Math.PI * mod(ms, SHEEN_MS)) / SHEEN_MS);
}

/** The baton tip's glow, 0..1. */
export function glowAt(ms: number): number {
	"worklet";
	return 0.5 - 0.5 * Math.cos((2 * Math.PI * mod(ms, SHEEN_MS)) / SHEEN_MS);
}

/** How far the sheen has crossed Ruto, 0..1, or -1 while it is off. */
export function sheenAt(ms: number): number {
	"worklet";
	const q = mod(ms, SHEEN_MS) / SHEEN_MS;
	if (q <= 0 || q >= SHEEN_SWEEP) return -1;
	return easeInOut(q / SHEEN_SWEEP);
}

export type PokePose = { scaleX: number; scaleY: number };

export function pokeAt(p: number): PokePose {
	"worklet";
	if (p <= 0 || p >= 1) return { scaleX: 1, scaleY: 1 };
	const b = bump(p, 0, 0.5);
	return { scaleX: 1 + 0.05 * b, scaleY: 1 - 0.08 * b };
}

export type FinalePose = {
	/** How far the bright sheen has crossed, 0..1, or -1 when it is off. */
	sheen: number;
	/** The nod, in grid units down. */
	dip: number;
	scaleY: number;
	/** Eyes, 0 open → 1 shut. */
	shut: number;
	/** Extra glow at the baton tip, 0..1. */
	glow: number;
	/** The whole stage, fading into the reload. */
	opacity: number;
};

export function finaleAt(p: number): FinalePose {
	"worklet";
	const sweep = span(p, 0, 0.6);
	const nod = bump(p, 0.2, 0.3);
	return {
		sheen: sweep > 0 && sweep < 1 ? easeInOut(sweep) : -1,
		dip: 16 * nod,
		scaleY: 1 - 0.05 * nod,
		shut: bump(p, 0.22, 0.26),
		glow: bump(p, 0, 0.6),
		opacity: 1 - span(p, 0.6, 1),
	};
}
