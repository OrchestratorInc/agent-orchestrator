// Ruto's performance on the update screen, as pure functions of time.
//
// Ruto conducts. Three states, the way character apps build mascots (a quiet
// idle layer plus reactions, blended rather than cut):
//
//   - **Idle** — a calm four-beat pattern (down, left, right, up). The baton
//     falls into each beat and rebounds off it, as a conductor's does; the body
//     dips on the beat and breathes across the bar; a note leaves the baton tip
//     on every beat; the eyes blink at irregular moments.
//   - **Poke** — tap Ruto: a squash and stretch, a twirl of the baton, a burst of
//     notes and a happy squint.
//   - **Finale** — Restart: the preparatory lift, a downstroke onto the last
//     beat (notes burst, a success haptic), a freeze with eyes closed, a bow,
//     and the stage fades into the restart.
//
// The idle clock runs from 0 to LOOP_MS and wraps; LOOP_MS is a whole number of
// bars and of the blink pattern, so the wrap is invisible. Poke and finale take
// their own 0..1 progress. Angles are degrees, clockwise positive; distances
// are units of the brand file's 1024 grid.
//
// Each function is a Reanimated worklet (UI thread) and plain TypeScript
// (vitest), so the curves are tested without a React Native runtime.

/** One beat: about 86 bpm, a calm tempo. */
export const BEAT_MS = 700;
export const BEATS_PER_BAR = 4;
export const BAR_MS = BEAT_MS * BEATS_PER_BAR;
/** Three bars; the blink pattern repeats once per loop. */
export const LOOP_MS = BAR_MS * 3;
export const POKE_MS = 900;
/** The finale plays while the screen holds before the reload. */
export const FINALE_MS = 1500;
/** Where in the finale the downstroke lands: the burst and the haptic. */
export const FINALE_ICTUS = 0.27;
/** How long a beat's note stays in the air. */
export const NOTE_LIFE_MS = 1800;
export const BURST_NOTES = 7;

/** The baton turns around the middle of its end inside the hand. */
export const RUTO_PIVOT = { x: 779, y: 421 };
/** The baton's tip at rest. */
export const RUTO_TIP = { x: 835.5, y: 236 };

/** Baton angle at each beat's ictus: down, left, right, up. */
const ICTUS = [22, -10, 18, -20];
/** How far the baton lifts off between beats (counterclockwise is up). */
const REBOUND = -7;
/** Blink midpoints across the loop, irregular on purpose, one a double blink. */
const BLINKS = [0.05, 0.31, 0.355, 0.72];
const BLINK_WIDTH = 0.024;

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

function lerp(a: number, b: number, t: number): number {
	"worklet";
	return a + (b - a) * t;
}

function easeOut(t: number): number {
	"worklet";
	return 1 - (1 - t) * (1 - t) * (1 - t);
}

function easeIn(t: number): number {
	"worklet";
	return t * t * t;
}

/** Progress of `p` through [start, end], clamped. */
function span(p: number, start: number, end: number): number {
	"worklet";
	return clamp01((p - start) / (end - start));
}

/**
 * The conducting pattern. After each ictus the baton rebounds (lifts, slowing)
 * and then accelerates into the next one, so the beat lands sharply.
 */
export function batonAt(ms: number): number {
	"worklet";
	const t = mod(ms, BAR_MS);
	const beat = Math.floor(t / BEAT_MS);
	const u = (t - beat * BEAT_MS) / BEAT_MS;
	const from = ICTUS[beat];
	const to = ICTUS[(beat + 1) % BEATS_PER_BAR];
	return from + (to - from) * u * u + REBOUND * Math.sin(Math.PI * u);
}

/** Body dip just after each beat lands, 0..1: the beat carried through the knees. */
export function bobAt(ms: number): number {
	"worklet";
	const u = mod(ms, BEAT_MS) / BEAT_MS;
	return bump(u, 0, 0.4);
}

/** Breathing across the bar, 0..1. */
export function breathAt(ms: number): number {
	"worklet";
	return 0.5 - 0.5 * Math.cos((2 * Math.PI * mod(ms, BAR_MS)) / BAR_MS);
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

export type Point = { x: number; y: number };

/** Where the baton tip is when the baton is turned by `angle`. */
export function tipAt(angle: number): Point {
	"worklet";
	const a = (angle * Math.PI) / 180;
	const dx = RUTO_TIP.x - RUTO_PIVOT.x;
	const dy = RUTO_TIP.y - RUTO_PIVOT.y;
	return {
		x: RUTO_PIVOT.x + dx * Math.cos(a) - dy * Math.sin(a),
		y: RUTO_PIVOT.y + dx * Math.sin(a) + dy * Math.cos(a),
	};
}

export type NotePose = {
	/** Where the note left the tip. */
	from: Point;
	/** Drift from there. */
	x: number;
	y: number;
	scale: number;
	opacity: number;
	rotate: number;
};

/** The note that leaves the tip on beat `beat` (0..3) of every bar. */
export function noteAt(ms: number, beat: number): NotePose {
	"worklet";
	const age = mod(ms - beat * BEAT_MS, BAR_MS);
	const p = age / NOTE_LIFE_MS;
	const from = tipAt(batonAt(ms - age));
	if (p >= 1) return { from, x: 0, y: 0, scale: 0, opacity: 0, rotate: 0 };
	const side = beat % 2 === 0 ? 1 : -1;
	return {
		from,
		x: side * 34 * p + 16 * Math.sin(2 * Math.PI * 1.3 * p),
		y: -170 * easeOut(p),
		scale: p < 0.15 ? p / 0.15 : 1 - 0.25 * p,
		opacity: p < 0.1 ? p / 0.1 : p > 0.6 ? 1 - (p - 0.6) / 0.4 : 1,
		rotate: side * (8 + 10 * Math.sin(2 * Math.PI * p)),
	};
}

/** Note `i` of a burst fanned up from the tip, at burst progress `p` (0..1). */
export function burstNoteAt(p: number, i: number): { x: number; y: number; scale: number; opacity: number; rotate: number } {
	"worklet";
	if (p <= 0 || p >= 1) return { x: 0, y: 0, scale: 0, opacity: 0, rotate: 0 };
	const heading = ((-160 + (140 * i) / (BURST_NOTES - 1)) * Math.PI) / 180;
	const reach = (150 + 40 * (i % 3)) * easeOut(p);
	return {
		x: Math.cos(heading) * reach,
		y: Math.sin(heading) * reach - 40 * p * p,
		scale: p < 0.12 ? p / 0.12 : 1.1 - 0.4 * p,
		opacity: p < 0.08 ? p / 0.08 : p > 0.55 ? 1 - (p - 0.55) / 0.45 : 1,
		rotate: (i % 2 === 0 ? 1 : -1) * 40 * p,
	};
}

export type PokePose = {
	scaleX: number;
	scaleY: number;
	/** Extra baton turn: one twirl. */
	spin: number;
	/** Happy squint, 0..1 of a full lid. */
	squint: number;
	/** Progress of its note burst, outside 0..1 when none. */
	burst: number;
};

export function pokeAt(p: number): PokePose {
	"worklet";
	if (p <= 0 || p >= 1) return { scaleX: 1, scaleY: 1, spin: 0, squint: 0, burst: -1 };
	// Squash, stretch, settle.
	const scaleY = 1 - 0.14 * bump(p, 0, 0.16) + 0.09 * bump(p, 0.16, 0.2) - 0.03 * bump(p, 0.36, 0.2);
	return {
		scaleX: 1 + 0.6 * (1 - scaleY),
		scaleY,
		spin: 360 * easeOut(span(p, 0.05, 0.6)),
		squint: 0.5 * bump(p, 0.08, 0.75),
		burst: span(p, 0.1, 0.95),
	};
}

export type FinalePose = {
	angle: number;
	scaleY: number;
	/** Body offset in grid units, negative up. */
	lift: number;
	/** Eyes, 0 open → 1 shut. */
	shut: number;
	/** The whole stage, fading into the restart. */
	opacity: number;
	/** Progress of the downstroke's note burst, outside 0..1 when none. */
	burst: number;
};

/**
 * The finale at progress `p`, starting from the baton angle `from` the idle
 * pattern was at when Restart was tapped.
 */
export function finaleAt(p: number, from: number): FinalePose {
	"worklet";
	const prep = easeOut(span(p, 0, 0.16));
	const down = easeIn(span(p, 0.16, FINALE_ICTUS));
	const bow = easeOut(span(p, 0.52, 0.78));
	let angle = lerp(from, -32, prep);
	angle = lerp(angle, 30, down);
	angle = lerp(angle, 75, bow);
	// Rise onto the toes for the lift, land on the downstroke, settle, bow.
	const settle = span(p, FINALE_ICTUS, 0.4);
	const scaleY = 1 + 0.05 * prep - 0.1 * down + 0.05 * settle - 0.07 * bow;
	const lift = -10 * prep + 14 * down - 4 * settle + 22 * bow;
	return {
		angle,
		scaleY,
		lift,
		shut: span(p, 0.29, 0.34),
		opacity: 1 - span(p, 0.7, 1),
		burst: span(p, FINALE_ICTUS, 0.75) > 0 ? span(p, FINALE_ICTUS, 0.75) : -1,
	};
}
