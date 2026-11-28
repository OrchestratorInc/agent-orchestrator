import { describe, expect, it } from "vitest";
import { ensemblePhrase, NOTE_MS, PLAYERS, trackEnd, type Track } from "./ensemblePhrase";

const phrase = ensemblePhrase();
const tracks: [string, Track][] = [
	["baton", phrase.baton],
	["lean", phrase.lean],
	...phrase.hops.map((track, i): [string, Track] => [`hop ${i}`, track]),
	...phrase.notes.map((track, i): [string, Track] => [`note ${i}`, track]),
];

describe("the ensemble's phrase", () => {
	// Each track plays as one sequence of timings: a key that started before
	// the last one ended would shift every cue after it off the beat.
	it.each(tracks)("plays %s in order, without overlaps, inside the phrase", (_, track) => {
		let cursor = 0;
		for (const key of track.keys) {
			expect(key.at).toBeGreaterThanOrEqual(cursor);
			cursor = key.at + key.ms;
		}
		expect(cursor).toBeLessThanOrEqual(phrase.length);
	});

	// The phrase loops: a track that ended anywhere but where it began would jump.
	it.each(tracks)("brings %s back to where it started", (_, track) => {
		expect(trackEnd(track)).toBe(track.from);
	});

	it("cues every player in every bar, on the beat", () => {
		const ictuses = new Set(phrase.baton.keys.map((key) => key.at + key.ms));
		for (let player = 0; player < PLAYERS; player++) {
			const cued = phrase.cues.filter((c) => c.player === player);
			// Two grooves (a solo and the tutti each), the wave and the cut-off.
			expect(cued).toHaveLength(6);
		}
		// The groove's solos land exactly on the beat.
		const solos = phrase.cues.slice(0, PLAYERS);
		for (const solo of solos) expect(ictuses.has(solo.at)).toBe(true);
	});

	it("lets a player's note rise and vanish before it sings the next", () => {
		for (const track of phrase.notes) {
			const starts = track.keys.filter((key) => key.to === 1).map((key) => key.at);
			for (let i = 1; i < starts.length; i++) expect(starts[i]! - starts[i - 1]!).toBeGreaterThanOrEqual(NOTE_MS);
		}
	});

	it("ends with a bow from everyone", () => {
		for (const hop of phrase.hops) expect(Math.min(...hop.keys.map((key) => key.to))).toBeLessThan(0);
		expect(phrase.length).toBeGreaterThan(7000);
		expect(phrase.length).toBeLessThan(9500);
	});
});
