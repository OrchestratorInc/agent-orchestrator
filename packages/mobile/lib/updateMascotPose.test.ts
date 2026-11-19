import { describe, expect, it } from "vitest";
import { BLINK_MS, eyesAt, glowAt, HOP_MS, hopAt, LOOP_MS, sparkleAt, SPARKLE_COUNT, wandAt } from "./updateMascotPose";

const close = (a: number, b: number) => expect(a).toBeCloseTo(b, 6);

describe("update mascot choreography", () => {
	it("loops seamlessly: the last frame is the first", () => {
		expect(LOOP_MS % HOP_MS).toBe(0);
		expect(LOOP_MS % BLINK_MS).toBe(0);
		close(hopAt(LOOP_MS).lift, hopAt(0).lift);
		close(hopAt(LOOP_MS).squash, hopAt(0).squash);
		close(wandAt(LOOP_MS), wandAt(0));
		close(eyesAt(LOOP_MS).open, eyesAt(0).open);
		close(glowAt(LOOP_MS), glowAt(0));
		for (let i = 0; i < SPARKLE_COUNT; i++) close(sparkleAt(LOOP_MS, i).x, sparkleAt(0, i).x);
	});

	it("rests on the ground, eyes open and wand level at time zero (the reduced-motion pose)", () => {
		expect(hopAt(0)).toEqual({ lift: 0, squash: 0 });
		expect(eyesAt(0)).toEqual({ open: 1, glance: 0 });
		close(wandAt(0), 0);
	});

	it("keeps every curve inside its range across the loop", () => {
		for (let ms = 0; ms < LOOP_MS; ms += 7) {
			const hop = hopAt(ms);
			expect(hop.lift).toBeGreaterThanOrEqual(0);
			expect(hop.lift).toBeLessThanOrEqual(1);
			expect(hop.squash).toBeGreaterThanOrEqual(0);
			expect(hop.squash).toBeLessThanOrEqual(1);
			expect(Math.abs(wandAt(ms))).toBeLessThanOrEqual(12);
			const eyes = eyesAt(ms);
			expect(eyes.open).toBeGreaterThanOrEqual(0.1 - 1e-9);
			expect(eyes.open).toBeLessThanOrEqual(1);
			expect(eyes.glance).toBeGreaterThanOrEqual(0);
			expect(eyes.glance).toBeLessThanOrEqual(1);
			for (let i = 0; i < SPARKLE_COUNT; i++) {
				const s = sparkleAt(ms, i);
				expect(s.opacity).toBeGreaterThanOrEqual(0);
				expect(s.opacity).toBeLessThanOrEqual(1);
				// Sparkles only ever rise from the tip.
				expect(s.y).toBeLessThanOrEqual(1e-9);
			}
		}
	});

	it("blinks twice per blink period", () => {
		let closures = 0;
		let wasClosed = false;
		for (let ms = 0; ms < BLINK_MS; ms += 5) {
			const closed = eyesAt(ms).open < 0.3;
			if (closed && !wasClosed) closures++;
			wasClosed = closed;
		}
		expect(closures).toBe(2);
	});
});
