import { duration, press, spring } from "./tokens";

/**
 * The app's motion language, in one place.
 *
 * Two halves, deliberately:
 *
 *   - **Named durations** for the transitions specific to this app — a board row
 *     moving between sections, a banner arriving above a list, a paged question
 *     sliding out. Each is a number lifted from the screen that first needed it,
 *     so a change here is a visible decision rather than a drive-by edit.
 *   - **The ladders and hooks** — the duration/easing/press steps in `tokens.ts`
 *     plus the entrance hook the shared controls use.
 *
 * Two rules decide everything here:
 *
 *   - **Fast for the frequent.** Anything the finger triggers repeatedly gets a
 *     ≤150ms transition on transform or opacity only, so it composites on the
 *     GPU and never delays the next tap.
 *   - **Motion is never the only signal.** Every animated state change in this
 *     app also changes color, an icon or a label, so the interface still reads
 *     with motion switched off — which is exactly what happens when the user has
 *     Reduce Motion on, where these helpers become no-ops.
 *
 * Free of React Native imports so the rules stay unit-testable under Node; the
 * hooks that need `Animated` live in motionHooks.ts, and the hook that reads the
 * OS setting lives in useReducedMotion.ts.
 */

/** `Dot`'s pulse half-period. */
export const BREATHE_MS = 1200;

/**
 * One sweep of a loading skeleton's shimmer, then a rest before the next.
 *
 * Slow and linear: the sweep says "still loading", and a quick or eased one
 * reads as busy. The rest keeps a screen with several skeletons from strobing.
 */
export const SHIMMER_MS = 1300;
export const SHIMMER_REST_MS = 400;

/** Fallback used when the keyboard event carries no duration. */
export const KEYBOARD_FALLBACK_MS = 250;

/** A row moving between sections, or a list re-laying out. */
export const LAYOUT_MS = 160;
/** A banner entering or leaving above a list. */
export const BANNER_MS = 180;
/** Swapping content in place — filter changes, destination changes. */
export const CROSSFADE_MS = 140;
/**
 * One paged question leaving while the next arrives, in the direction of the
 * swipe. Longer than a crossfade because the eye is following a direction here,
 * not just a change of content.
 */
export const PAGE_SLIDE_MS = 200;
/** A status colour changing on a rail or dot. */
export const TINT_MS = 200;
/** A row arriving or leaving. Shorter than `LAYOUT_MS`, so it never outlasts the move beside it. */
export const ROW_ENTER_MS = 120;

export type MotionDurations = {
	breathe: number;
	layout: number;
	banner: number;
	crossfade: number;
	tint: number;
};

/**
 * Every duration, zeroed when the user has asked for reduced motion.
 *
 * Zero rather than "skip the animation" so callers stay branch-free: a zero
 * duration lands the view in its final state on the next frame, which is what
 * reduce-motion asks for, without each call site growing an `if`.
 */
export function motionDurations(reduced: boolean): MotionDurations {
	if (reduced) {
		return { breathe: 0, layout: 0, banner: 0, crossfade: 0, tint: 0 };
	}
	return {
		breathe: BREATHE_MS,
		layout: LAYOUT_MS,
		banner: BANNER_MS,
		crossfade: CROSSFADE_MS,
		tint: TINT_MS,
	};
}

/**
 * Whether a layout transition should run at all.
 *
 * Distinct from a zero duration: a looping animation (the breathing dot) must
 * not start in the first place, because a zero-duration loop is a busy loop.
 */
export function shouldAnimateLayout(reduced: boolean): boolean {
	return !reduced;
}

/** Whether the breathing status dot should loop. */
export function shouldBreathe(reduced: boolean, breathing: boolean): boolean {
	return breathing && !reduced;
}

export { duration, press, spring };
