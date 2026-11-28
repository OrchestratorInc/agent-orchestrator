import { describe, expect, it } from "vitest";
import {
	batonAt,
	blinkAt,
	breathAt,
	FINALE_MS,
	FINALE_NOD,
	finaleAt,
	glowAt,
	LOOP_MS,
	pokeAt,
	SHEEN_MS,
	sheenAt,
} from "./updateMascotPose";

const close = (a: number, b: number, digits = 6) => expect(a).toBeCloseTo(b, digits);

describe("idle", () => {
	it("loops seamlessly: the last frame is the first", () => {
		expect(LOOP_MS % SHEEN_MS).toBe(0);
		close(breathAt(LOOP_MS), breathAt(0));
		close(blinkAt(LOOP_MS), blinkAt(0));
		close(batonAt(LOOP_MS), batonAt(0));
		close(glowAt(LOOP_MS), glowAt(0));
		close(sheenAt(LOOP_MS), sheenAt(0));
	});

	it("rests still at time zero: the Reduce Motion pose", () => {
		expect(breathAt(0)).toBe(0);
		expect(blinkAt(0)).toBe(0);
		close(batonAt(0), 0);
		expect(sheenAt(0)).toBe(-1);
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

	it("sweeps the sheen across Ruto once per period and hides it in between", () => {
		let visible = 0;
		let last = -1;
		for (let ms = 0; ms < SHEEN_MS; ms += 10) {
			const s = sheenAt(ms);
			if (s >= 0) {
				visible += 10;
				expect(s).toBeGreaterThanOrEqual(last);
				last = s;
			}
		}
		expect(visible / SHEEN_MS).toBeGreaterThan(0.2);
		expect(visible / SHEEN_MS).toBeLessThan(0.4);
	});

	it("keeps the motion small", () => {
		for (let ms = 0; ms < LOOP_MS; ms += 7) {
			expect(Math.abs(batonAt(ms))).toBeLessThanOrEqual(2);
			for (const v of [breathAt(ms), blinkAt(ms), glowAt(ms)]) {
				expect(v).toBeGreaterThanOrEqual(0);
				expect(v).toBeLessThanOrEqual(1 + 1e-9);
			}
		}
	});
});

describe("poke", () => {
	it("starts and ends at rest", () => {
		for (const p of [0, 1]) expect(pokeAt(p)).toEqual({ scaleX: 1, scaleY: 1 });
	});

	it("is a small squish", () => {
		const mid = pokeAt(0.25);
		expect(mid.scaleY).toBeLessThan(1);
		expect(mid.scaleY).toBeGreaterThan(0.9);
		expect(mid.scaleX).toBeGreaterThan(1);
	});
});

describe("finale", () => {
	it("starts at rest and fades out completely", () => {
		const start = finaleAt(0);
		expect(start.dip).toBe(0);
		expect(start.opacity).toBe(1);
		expect(finaleAt(1).opacity).toBe(0);
	});

	it("nods with eyes closed at the haptic", () => {
		const nod = finaleAt(FINALE_NOD);
		expect(nod.dip).toBeGreaterThan(10);
		expect(nod.scaleY).toBeLessThan(1);
		expect(nod.shut).toBeGreaterThan(0.9);
	});

	it("sweeps one bright sheen before the fade", () => {
		expect(finaleAt(0.3).sheen).toBeGreaterThan(0);
		expect(finaleAt(0.3).sheen).toBeLessThan(1);
		expect(finaleAt(0.8).sheen).toBe(-1);
	});

	it("takes about a second", () => {
		expect(FINALE_MS).toBeGreaterThanOrEqual(1000);
		expect(FINALE_MS).toBeLessThanOrEqual(1300);
	});
});
