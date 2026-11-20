// The update screen mascot's choreography, as pure functions of one clock.
//
// Every moving part reads the same millisecond value, so the hop, the baton
// flick, the blink and the sparkles never drift apart. The clock runs from 0 to
// LOOP_MS and wraps; LOOP_MS is a multiple of every period below, so the frame
// at LOOP_MS is the frame at 0 and the wrap is invisible.
//
// Each function is a Reanimated worklet (UI thread) and plain TypeScript
// (vitest), so the curves are tested without a React Native runtime.

/** One hop, one baton beat and one sparkle burst. */
export const HOP_MS = 1400;
/** The eyes blink twice per this period. */
export const BLINK_MS = 4200;
/** Shortest loop containing whole periods of both. */
export const LOOP_MS = 8400;
/** Sparkles alive at once, evenly staggered across a hop. */
export const SPARKLE_COUNT = 5;

export type HopPose = {
	/** Lift above the ground, 0..1 (multiply by the hop height). */
	lift: number;
	/** Landing squash, 0..1: wider and shorter when it touches down. */
	squash: number;
};

const TAU = Math.PI * 2;

function phase(ms: number, period: number): number {
	"worklet";
	const p = (ms % period) / period;
	return p < 0 ? p + 1 : p;
}

/** A 0→1→0 bump across [start, start + width), 0 outside it. */
function bump(p: number, start: number, width: number): number {
	"worklet";
	if (p < start || p >= start + width) return 0;
	return Math.sin((Math.PI * (p - start)) / width);
}

export function hopAt(ms: number): HopPose {
	"worklet";
	const p = phase(ms, HOP_MS);
	// Airborne for the first 36%, a squash on landing, then a small crouch at the
	// end of the cycle as anticipation for the next jump.
	const lift = bump(p, 0, 0.36);
	const squash = bump(p, 0.36, 0.14) + 0.45 * bump(p, 0.86, 0.14);
	return { lift, squash };
}

/**
 * Baton angle in degrees around the hand, clockwise positive: a conductor's
 * beat, lifting as Ruto leaves the ground and coming down on the landing.
 */
export function batonAt(ms: number): number {
	"worklet";
	const p = phase(ms, HOP_MS);
	return -9 * Math.sin(TAU * p) + 3 * Math.sin(2 * TAU * p);
}

export type EyePose = {
	/** 1 open → 0 shut. */
	open: number;
};

export function eyesAt(ms: number): EyePose {
	"worklet";
	const p = phase(ms, BLINK_MS);
	const open = 1 - (bump(p, 0.02, 0.045) + bump(p, 0.62, 0.045));
	return { open };
}

export type SparklePose = {
	/** Offset from the baton tip, in choreography units. */
	x: number;
	y: number;
	scale: number;
	opacity: number;
	rotate: number;
};

// Fixed headings, fanned up and outward from the tip, so the burst looks
// scattered without randomness (which would differ between frames and runs).
const HEADINGS = [-62, -100, -30, -128, -80];
const DISTANCES = [70, 54, 62, 46, 78];

export function sparkleAt(ms: number, index: number): SparklePose {
	"worklet";
	const p = phase(ms + (index * HOP_MS) / SPARKLE_COUNT, HOP_MS);
	const heading = (HEADINGS[index % HEADINGS.length] * Math.PI) / 180;
	const distance = DISTANCES[index % DISTANCES.length];
	// Fast out, slow settle: ease-out on travel.
	const travel = 1 - (1 - p) * (1 - p);
	return {
		x: Math.cos(heading) * distance * travel,
		y: Math.sin(heading) * distance * travel,
		scale: Math.sin(Math.PI * p),
		opacity: p < 0.7 ? 1 : 1 - (p - 0.7) / 0.3,
		rotate: 90 * p,
	};
}

/** Wand-tip glow, 0..1, brightest just after each flick. */
export function glowAt(ms: number): number {
	"worklet";
	const p = phase(ms, HOP_MS);
	return 0.55 + 0.45 * Math.cos(TAU * (p - 0.2));
}
