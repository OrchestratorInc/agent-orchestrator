import { describe, expect, it } from "vitest";
import {
	BAR_MS,
	batonAt,
	BEAT_MS,
	BEATS_PER_BAR,
	blinkAt,
	bobAt,
	breathAt,
	BURST_NOTES,
	burstNoteAt,
	FINALE_ICTUS,
	finaleAt,
	LOOP_MS,
	NOTE_LIFE_MS,
	noteAt,
	pokeAt,
	RUTO_TIP,
	tipAt,
} from "./updateMascotPose";

const close = (a: number, b: number, digits = 6) => expect(a).toBeCloseTo(b, digits);

describe("idle conducting", () => {
	it("loops seamlessly: the last frame is the first", () => {
		expect(LOOP_MS % BAR_MS).toBe(0);
		close(batonAt(LOOP_MS), batonAt(0));
		close(bobAt(LOOP_MS), bobAt(0));
		close(breathAt(LOOP_MS), breathAt(0));
		close(blinkAt(LOOP_MS), blinkAt(0));
		for (let beat = 0; beat < BEATS_PER_BAR; beat++) {
			close(noteAt(LOOP_MS, beat).y, noteAt(0, beat).y);
			close(noteAt(LOOP_MS, beat).opacity, noteAt(0, beat).opacity);
		}
	});

	it("lands each beat on the pattern: down, left, right, up", () => {
		const ictus = [0, 1, 2, 3].map((beat) => batonAt(beat * BEAT_MS));
		expect(ictus).toEqual([22, -10, 18, -20]);
	});

	it("moves the baton continuously across each beat, however sharp the landing", () => {
		for (let beat = 1; beat <= BEATS_PER_BAR; beat++) {
			const at = beat * BEAT_MS;
			expect(Math.abs(batonAt(at - 0.5) - batonAt(at))).toBeLessThan(0.1);
		}
	});

	it("rests still at time zero: the Reduce Motion pose", () => {
		expect(bobAt(0)).toBe(0);
		expect(breathAt(0)).toBe(0);
		expect(blinkAt(0)).toBe(0);
	});

	it("blinks four times a loop, at irregular moments", () => {
		const starts: number[] = [];
		let shut = false;
		for (let ms = 0; ms < LOOP_MS; ms += 5) {
			const now = blinkAt(ms) > 0.5;
			if (now && !shut) starts.push(ms);
			shut = now;
		}
		expect(starts).toHaveLength(4);
		const gaps = starts.slice(1).map((ms, i) => ms - starts[i]);
		expect(new Set(gaps.map((gap) => Math.round(gap / 100))).size).toBeGreaterThan(1);
	});

	it("keeps every curve in range", () => {
		for (let ms = 0; ms < LOOP_MS; ms += 7) {
			expect(Math.abs(batonAt(ms))).toBeLessThanOrEqual(30);
			for (const v of [bobAt(ms), breathAt(ms), blinkAt(ms)]) {
				expect(v).toBeGreaterThanOrEqual(0);
				expect(v).toBeLessThanOrEqual(1 + 1e-9);
			}
		}
	});
});

describe("notes", () => {
	it("leave from where the tip was on their beat, and only rise", () => {
		for (let beat = 0; beat < BEATS_PER_BAR; beat++) {
			const justAfter = noteAt(beat * BEAT_MS + 1, beat);
			const tip = tipAt(batonAt(beat * BEAT_MS));
			close(justAfter.from.x, tip.x, 3);
			close(justAfter.from.y, tip.y, 3);
			for (let age = 0; age < NOTE_LIFE_MS; age += 50) {
				expect(noteAt(beat * BEAT_MS + age, beat).y).toBeLessThanOrEqual(1e-9);
			}
		}
	});

	it("are gone once their life is over", () => {
		expect(noteAt(NOTE_LIFE_MS + 10, 0).opacity).toBe(0);
	});

	it("bursts fan upward and vanish at both ends", () => {
		for (let i = 0; i < BURST_NOTES; i++) {
			expect(burstNoteAt(0, i).opacity).toBe(0);
			expect(burstNoteAt(1, i).opacity).toBe(0);
			expect(burstNoteAt(0.5, i).y).toBeLessThan(0);
		}
	});

	it("start at the tip at rest", () => {
		expect(tipAt(0)).toEqual(RUTO_TIP);
	});
});

describe("poke", () => {
	it("starts and ends at rest", () => {
		for (const p of [0, 1]) expect(pokeAt(p)).toEqual({ scaleX: 1, scaleY: 1, spin: 0, squint: 0, burst: -1 });
	});

	it("squashes, twirls the baton once and squints", () => {
		expect(pokeAt(0.08).scaleY).toBeLessThan(0.9);
		expect(pokeAt(0.08).scaleX).toBeGreaterThan(1);
		close(pokeAt(0.7).spin, 360);
		expect(pokeAt(0.45).squint).toBeGreaterThan(0.4);
	});
});

describe("finale", () => {
	it("picks up from wherever the idle baton was", () => {
		close(finaleAt(0, 12).angle, 12);
		close(finaleAt(0, -9).angle, -9);
		expect(finaleAt(0, 12).opacity).toBe(1);
	});

	it("lifts before the downstroke, lands it with a burst, then holds with eyes closed", () => {
		expect(finaleAt(0.16, 0).angle).toBeCloseTo(-32, 6);
		expect(finaleAt(0.16, 0).lift).toBeLessThan(0);
		close(finaleAt(FINALE_ICTUS, 0).angle, 30);
		expect(finaleAt(FINALE_ICTUS - 0.01, 0).burst).toBe(-1);
		expect(finaleAt(FINALE_ICTUS + 0.05, 0).burst).toBeGreaterThan(0);
		expect(finaleAt(0.45, 0).shut).toBe(1);
		close(finaleAt(0.45, 0).angle, 30);
	});

	it("bows and fades out completely by the end", () => {
		const end = finaleAt(1, 0);
		close(end.angle, 75);
		expect(end.lift).toBeGreaterThan(0);
		expect(end.opacity).toBe(0);
	});
});
