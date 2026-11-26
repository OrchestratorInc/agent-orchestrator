/**
 * The timing of the live turn's lattice loader (see LatticeLoader.tsx), kept free
 * of React Native so it stays unit-testable under Node.
 *
 * A port of the desktop's `.chat-lattice` (frontend styles.css and
 * LatticeLoader.tsx): a 4×4 lattice whose outer twelve dots share one
 * brighten-and-fade beat, each a twelfth of a lap behind the one before, so a
 * short bright trail runs clockwise round the edge while the inner four rest.
 */

export const LATTICE_SIDE = 4;
/** The outer dots in the order the trail visits them: clockwise from the top left. */
export const LATTICE_TRAIL = [0, 1, 2, 3, 7, 11, 15, 14, 13, 12, 8, 4] as const;
/** One lap round the edge, the desktop's keyframe duration. */
export const LATTICE_LAP_MS = 1200;

/**
 * Where a cell's dot sits, from the lattice's top left. Each dot is placed
 * outright rather than left to flex wrapping: Yoga wraps when the float32 sum of
 * dots and gaps exceeds the box by any amount, so at some Android densities
 * (2.625 among them) the fourth dot of every row spilled onto a new line.
 */
export function latticeCellOrigin(cell: number, dot: number, gap: number): { left: number; top: number } {
	const pitch = dot + gap;
	return { left: (cell % LATTICE_SIDE) * pitch, top: Math.floor(cell / LATTICE_SIDE) * pitch };
}

/** Resting opacity of the four inner dots. */
export const LATTICE_INNER_OPACITY = 0.1;
/** Opacity the trail dots hold when Reduce Motion stops the lap. */
export const LATTICE_STILL_OPACITY = 0.45;

/** The desktop's `chat-lattice-trail` keyframes: [offset, opacity]. */
const KEYFRAMES: readonly (readonly [number, number])[] = [
	[0, 0.2],
	[0.12, 1],
	[0.22, 1],
	[0.4, 0.2],
	[1, 0.2],
];

/**
 * CSS `cubic-bezier(0.65, 0, 0.35, 1)`, which the desktop applies between each
 * pair of keyframes. Newton's method on x(s) = t: the curve's slope never drops
 * below ~0.5, so a handful of steps lands well inside a pixel of opacity.
 */
function easeInOut(t: number): number {
	"worklet";
	const x1 = 0.65;
	const x2 = 0.35;
	let s = t;
	for (let i = 0; i < 6; i++) {
		const inv = 1 - s;
		const x = 3 * inv * inv * s * x1 + 3 * inv * s * s * x2 + s * s * s - t;
		const dx = 3 * inv * inv * x1 + 6 * inv * s * (x2 - x1) + 3 * s * s * (1 - x2);
		s = Math.min(1, Math.max(0, s - x / dx));
	}
	const inv = 1 - s;
	return 3 * inv * s * s + s * s * s;
}

/**
 * A trail dot's opacity at `phase` laps into its own beat. Any real number is
 * accepted and wrapped, so a dot's offset can simply be subtracted from the lap.
 */
export function latticeTrailOpacity(phase: number): number {
	"worklet";
	const p = phase - Math.floor(phase);
	for (let i = 1; i < KEYFRAMES.length; i++) {
		const [end, to] = KEYFRAMES[i]!;
		if (p > end) continue;
		const [start, from] = KEYFRAMES[i - 1]!;
		return from + (to - from) * easeInOut((p - start) / (end - start));
	}
	return KEYFRAMES[KEYFRAMES.length - 1]![1];
}

/** Opacity the twelve edge dots rest at under the comet: the trail's floor. */
export const LATTICE_EDGE_OPACITY = 0.2;

/**
 * The trail as a comet that steps one edge dot clockwise every twelfth of a
 * lap: each comet dot's opacity, head first. These are the desktop's trail
 * sampled once a step (latticeTrailOpacity a step and a half, two and a half
 * and three and a half behind the newest dot), less the dot just fading in and
 * the one already back at rest, which sit within a few tenths of the floor.
 */
export const LATTICE_COMET = [1, 1, 0.8] as const;

/**
 * Where a comet dot `behind` dots back from the head sits at each step of the
 * lap, as an interpolation's input range (the step) and the left and top
 * offsets it maps to.
 */
export function latticeCometPath(behind: number, dot: number, gap: number): { inputRange: number[]; left: number[]; top: number[] } {
	const steps = LATTICE_TRAIL.length;
	const inputRange: number[] = [];
	const left: number[] = [];
	const top: number[] = [];
	for (let step = 0; step < steps; step++) {
		const origin = latticeCellOrigin(LATTICE_TRAIL[(step - behind + steps) % steps]!, dot, gap);
		inputRange.push(step);
		left.push(origin.left);
		top.push(origin.top);
	}
	return { inputRange, left, top };
}

/**
 * One clock for every lattice on screen: it steps the comet on a twelfth of a
 * lap, and runs only while some lattice holds it. Stepping, it costs a step's
 * work ten times a second, where a per-frame animation cost a frame's work on
 * every frame for every lattice, whether or not a dot moved.
 */
export function createLatticeClock(onStep: (step: number) => void, schedule: { setInterval: typeof setInterval; clearInterval: typeof clearInterval } = globalThis): () => () => void {
	let holders = 0;
	let step = 0;
	let timer: ReturnType<typeof setInterval> | undefined;
	return function hold() {
		if (holders++ === 0) {
			timer = schedule.setInterval(() => {
				step = (step + 1) % LATTICE_TRAIL.length;
				onStep(step);
			}, LATTICE_LAP_MS / LATTICE_TRAIL.length);
		}
		let held = true;
		return () => {
			if (!held) return;
			held = false;
			if (--holders === 0) schedule.clearInterval(timer);
		};
	};
}
