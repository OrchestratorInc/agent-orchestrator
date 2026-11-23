/**
 * The loading screen's little concert, as data: one phrase of four bars that
 * an ensemble of three small Rutos plays, written as keyframe tracks on one
 * clock so every cue lands on the beat. The beat is kept as an unseen
 * conductor's baton, so the timing reads as a bar of music.
 *
 *   bars 1-2  the groove: a bar of four; beats one to three are each one
 *             player's solo, the fourth is everyone
 *   bar 3     a flourish: the ensemble leans back, then answers in a wave
 *   bar 4     the finale: a crescendo everyone rises with, a sharp cut-off
 *             they all hit, a held fermata, and a bow
 *
 * Free of React Native so the choreography stays unit-testable; the loader
 * turns each track into a looping animation.
 */

export type Ease = "linear" | "fall" | "rebound" | "smooth";
export type Key = { at: number; to: number; ms: number; ease: Ease };
/** A track's keys, in order and never overlapping, and the value it rests at. */
export type Track = { from: number; keys: Key[] };

export const PLAYERS = 3;

/** One bar of the groove, as a conductor beats it: a quick fall onto each
 * beat and a softer rebound; the downbeat lands hardest, the fourth lifts
 * into the preparation for the next bar. Angles are degrees about the grip,
 * positive carrying the tip down and out. */
const GROOVE: readonly { ictus: number; fall: number; rebound: number; lift: number; weight: number }[] = [
	{ ictus: 26, fall: 170, rebound: 300, lift: -2, weight: 1 },
	{ ictus: 13, fall: 140, rebound: 260, lift: -6, weight: 0.45 },
	{ ictus: 20, fall: 150, rebound: 270, lift: -3, weight: 0.7 },
	{ ictus: 9, fall: 130, rebound: 380, lift: -16, weight: 0.3 },
];
/** The baton's rest: raised, ready for the downbeat. */
export const PREPARATION = -16;
const HOLD_MS = 140;
/** A player's hop: up, then down. */
const HOP_UP = 110;
const HOP_DOWN = 150;
/** A sung note's rise above a player, until it is gone. */
export const NOTE_MS = 760;

export type Phrase = {
	length: number;
	baton: Track;
	/** Degrees the ensemble sways into the beat. */
	lean: Track;
	/** Each player's hop: 1 is a full hop, below 0 a bow. */
	hops: Track[];
	/** Each player's sung note, 0 to 1 over its rise. */
	notes: Track[];
	/** When each player is cued, for the tests. */
	cues: { at: number; player: number }[];
};

export function ensemblePhrase(): Phrase {
	const baton: Key[] = [];
	const lean: Key[] = [];
	const hops: Key[][] = Array.from({ length: PLAYERS }, () => []);
	const notes: Key[][] = Array.from({ length: PLAYERS }, () => []);
	const cues: { at: number; player: number }[] = [];
	const everyone = Array.from({ length: PLAYERS }, (_, i) => i);

	function cue(at: number, players: number[], { sing = true, height = 1 } = {}) {
		for (const player of players) {
			cues.push({ at, player });
			hops[player]!.push({ at, to: height, ms: HOP_UP, ease: "rebound" }, { at: at + HOP_UP, to: 0, ms: HOP_DOWN, ease: "fall" });
			if (sing) notes[player]!.push({ at, to: 1, ms: NOTE_MS, ease: "linear" }, { at: at + NOTE_MS, to: 0, ms: 0, ease: "linear" });
		}
	}

	function groove(t0: number): number {
		let t = t0;
		GROOVE.forEach((beat, i) => {
			const ictus = t + beat.fall;
			baton.push({ at: t, to: beat.ictus, ms: beat.fall, ease: "fall" }, { at: ictus, to: beat.lift, ms: beat.rebound, ease: "rebound" });
			lean.push({ at: t, to: beat.weight * 1.6, ms: beat.fall, ease: "fall" }, { at: ictus, to: 0, ms: beat.rebound, ease: "rebound" });
			// Beats one to three are solos; the fourth is everyone, without a note
			// (the soloists' notes are still rising).
			if (i < PLAYERS) cue(ictus, [i]);
			else cue(ictus, everyone, { sing: false, height: 0.6 });
			t = ictus + beat.rebound;
		});
		return t + HOLD_MS;
	}

	function flourish(t0: number): number {
		const windUp = 380;
		const sweepAt = t0 + windUp + 80;
		const sweep = 300;
		const landed = sweepAt + sweep;
		baton.push(
			{ at: t0, to: -32, ms: windUp, ease: "rebound" },
			{ at: sweepAt, to: 42, ms: sweep, ease: "fall" },
			{ at: landed, to: -8, ms: 420, ease: "rebound" },
			{ at: landed + 420, to: PREPARATION, ms: 300, ease: "smooth" },
		);
		lean.push({ at: t0, to: -4, ms: windUp, ease: "rebound" }, { at: sweepAt, to: 5, ms: sweep, ease: "fall" }, { at: landed, to: 0, ms: 420, ease: "rebound" });
		// The ensemble answers the sweep in a wave, left to right.
		everyone.forEach((player) => cue(sweepAt + 140 + player * 120, [player]));
		return landed + 420 + 300 + 100;
	}

	function finale(t0: number): number {
		const crescendo = 700;
		const cutAt = t0 + crescendo + 250;
		const cut = 110;
		const fermata = 520;
		const bowAt = cutAt + cut + fermata;
		const bow = 380;
		const upAt = bowAt + bow + 250;
		const up = 450;
		baton.push(
			{ at: t0, to: -30, ms: crescendo, ease: "smooth" },
			{ at: t0 + crescendo, to: -34, ms: 250, ease: "linear" },
			{ at: cutAt, to: 34, ms: cut, ease: "fall" },
			{ at: bowAt, to: 60, ms: bow, ease: "smooth" },
			{ at: upAt, to: PREPARATION, ms: up, ease: "smooth" },
		);
		// Everyone rises with the crescendo, hits the cut-off together, holds the
		// fermata, and bows.
		everyone.forEach((player) => {
			hops[player]!.push({ at: t0, to: 0.35, ms: crescendo, ease: "smooth" });
			cues.push({ at: cutAt, player });
			hops[player]!.push(
				{ at: cutAt, to: 1.3, ms: HOP_UP, ease: "rebound" },
				{ at: cutAt + HOP_UP, to: 0, ms: HOP_DOWN, ease: "fall" },
				{ at: bowAt + 60 * player, to: -1, ms: bow, ease: "smooth" },
				{ at: upAt, to: 0, ms: up, ease: "smooth" },
			);
			notes[player]!.push({ at: cutAt, to: 1, ms: NOTE_MS, ease: "linear" }, { at: cutAt + NOTE_MS, to: 0, ms: 0, ease: "linear" });
		});
		return upAt + up + 200;
	}

	let t = groove(0);
	t = groove(t);
	t = flourish(t);
	const length = finale(t);

	return {
		length,
		baton: { from: PREPARATION, keys: baton },
		lean: { from: 0, keys: lean },
		hops: hops.map((keys) => ({ from: 0, keys })),
		notes: notes.map((keys) => ({ from: 0, keys })),
		cues,
	};
}

/** The value a track ends the phrase on, which must be where it starts. */
export function trackEnd(track: Track): number {
	return track.keys.length ? track.keys[track.keys.length - 1]!.to : track.from;
}
