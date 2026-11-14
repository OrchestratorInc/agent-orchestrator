import type { DictationEvent } from "./dictation";

// The dictation level meter's motion, shared by the chat composer
// (DictationMeter) and the browser annotation composer, which draws outside React.

export const DICTATION_METER_BARS = 11;
/** Height steps per bar, 1px apart, from the resting dot (3px) to the pill's inner height (20px). */
export const DICTATION_METER_STEPS = 18;
const MIDDLE = (DICTATION_METER_BARS - 1) / 2;
/** How tall each bar may grow: full in the middle, about a third at the ends. */
const REACH = Array.from({ length: DICTATION_METER_BARS }, (_, bar) => {
	const distance = Math.abs(bar - MIDDLE) / MIDDLE;
	return 1 - 0.65 * distance * distance;
});
/** Helper levels at or below this are room noise; speech sits well above it. */
const NOISE_FLOOR = 0.2;
const SPEECH_SPAN = 0.6;
/** The bars catch up with louder sound within a frame or two, and settle back gently. */
const ATTACK_S = 0.03;
const RELEASE_S = 0.14;
/** A standing wave over the bell, so the bars rise and fall in place instead of travelling sideways. */
const WAVE_DEPTH = 0.22;
const WAVE_HZ = 1.6;

/** Each bar's height step (0 is the resting dot) for a level in 0...1. */
export function dictationMeterSteps(level: number, seconds: number): number[] {
	const swing = Math.cos(seconds * WAVE_HZ * 2 * Math.PI);
	return REACH.map((reach, index) => {
		const wave = 1 - WAVE_DEPTH * (0.5 + 0.5 * Math.sin((index / (DICTATION_METER_BARS - 1)) * 3 * Math.PI) * swing);
		return Math.round(level * reach * wave * (DICTATION_METER_STEPS - 1));
	});
}

/**
 * Animates the meter from the helper's levels until the returned function is
 * called. `draw` receives each bar's height step every frame.
 */
export function runDictationMeter(
	subscribe: (listener: (event: DictationEvent) => void) => () => void,
	draw: (steps: number[]) => void,
): () => void {
	let loudness = 0;
	const unsubscribe = subscribe((event) => {
		if (event.type === "levels") loudness = Math.max(loudness, ...event.values);
	});
	let position = 0;
	let last = performance.now();
	let frame = 0;
	const tick = (now: number) => {
		const dt = Math.min((now - last) / 1000, 0.05);
		last = now;
		const speech = Math.min(Math.max((loudness - NOISE_FLOOR) / SPEECH_SPAN, 0), 1);
		loudness = 0;
		position += (speech - position) * (1 - Math.exp(-dt / (speech > position ? ATTACK_S : RELEASE_S)));
		draw(dictationMeterSteps(position, now / 1000));
		frame = requestAnimationFrame(tick);
	};
	frame = requestAnimationFrame(tick);
	return () => {
		cancelAnimationFrame(frame);
		unsubscribe();
	};
}
