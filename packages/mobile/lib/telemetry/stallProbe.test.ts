import { describe, expect, it } from "vitest";
import { createStallProbe } from "./stallProbe";

function setup() {
	let now = 0;
	const timers: Array<{ fn: () => void; at: number; id: number }> = [];
	let ids = 0;
	const late: number[] = [];
	const probe = createStallProbe({
		intervalMs: 500,
		now: () => now,
		setTimer: (fn, ms) => {
			const timer = { fn, at: now + ms, id: (ids += 1) };
			timers.push(timer);
			return timer.id;
		},
		clearTimer: (id) => {
			const index = timers.findIndex((timer) => timer.id === id);
			if (index >= 0) timers.splice(index, 1);
		},
		onLate: (ms) => late.push(ms),
	});
	// Fires the next timer `delay` ms after it was due.
	const fire = (delay: number) => {
		const timer = timers.shift()!;
		now = timer.at + delay;
		timer.fn();
	};
	return { probe, fire, late, timers };
}

describe("createStallProbe", () => {
	it("reports how late each run was", () => {
		const t = setup();
		t.probe.start();
		t.fire(0);
		t.fire(320);
		t.fire(5);
		expect(t.late).toEqual([320, 5]);
	});

	// An app the OS suspended comes back with its timer long overdue.
	it("does not call a suspension a stall", () => {
		const t = setup();
		t.probe.start();
		t.fire(60_000);
		expect(t.late).toEqual([]);
	});

	it("starts once and stops cleanly", () => {
		const t = setup();
		t.probe.start();
		t.probe.start();
		expect(t.timers).toHaveLength(1);
		t.probe.stop();
		expect(t.timers).toHaveLength(0);
		t.probe.start();
		expect(t.timers).toHaveLength(1);
	});
});
