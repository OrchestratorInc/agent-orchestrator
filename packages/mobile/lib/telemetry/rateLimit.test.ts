import { describe, expect, it } from "vitest";
import {
	checkRateLimit,
	mergeRateState,
	EVENTS_PER_NAME_PER_DAY,
	EVENTS_PER_NAME_PER_MINUTE,
	type RateLimitState,
} from "./rateLimit";

const T0 = Date.parse("2026-08-07T10:00:00Z");

function run(count: number, startState: RateLimitState = {}, name = "mobile.app.feature_used", now = T0) {
	let state = startState;
	const results: boolean[] = [];
	for (let i = 0; i < count; i++) {
		const r = checkRateLimit(state, name, now);
		state = r.state;
		results.push(r.allowed);
	}
	return { state, results };
}

describe("checkRateLimit", () => {
	it("allows up to the per-minute cap, then denies within the same minute", () => {
		const { results } = run(EVENTS_PER_NAME_PER_MINUTE + 3);
		expect(results.slice(0, EVENTS_PER_NAME_PER_MINUTE).every(Boolean)).toBe(true);
		expect(results.slice(EVENTS_PER_NAME_PER_MINUTE).every((x) => x === false)).toBe(true);
	});

	it("reopens the minute window after 60s", () => {
		const first = run(EVENTS_PER_NAME_PER_MINUTE);
		const later = checkRateLimit(first.state, "mobile.app.feature_used", T0 + 61_000);
		expect(later.allowed).toBe(true);
	});

	// The real backstop: a runaway that paces itself under the minute cap still
	// cannot exceed the daily ceiling.
	it("enforces the daily ceiling across many minute windows", () => {
		let state: RateLimitState = {};
		let allowed = 0;
		// One event every 30s for a full day's worth of attempts.
		for (let i = 0; i < EVENTS_PER_NAME_PER_DAY + 50; i++) {
			const r = checkRateLimit(state, "mobile.app.connected", T0 + i * 30_000);
			state = r.state;
			if (r.allowed) allowed++;
		}
		expect(allowed).toBe(EVENTS_PER_NAME_PER_DAY);
	});

	it("resets the daily counter on a new UTC day", () => {
		let state: RateLimitState = {};
		for (let i = 0; i < EVENTS_PER_NAME_PER_DAY; i++) {
			state = checkRateLimit(state, "mobile.app.active", T0 + i * 1000).state;
		}
		const capped = checkRateLimit(state, "mobile.app.active", T0 + 5000);
		expect(capped.allowed).toBe(false);
		const nextDay = checkRateLimit(capped.state, "mobile.app.active", Date.parse("2026-08-08T00:01:00Z"));
		expect(nextDay.allowed).toBe(true);
	});

	it("caps each event name independently", () => {
		let state: RateLimitState = {};
		for (let i = 0; i < EVENTS_PER_NAME_PER_MINUTE; i++) {
			state = checkRateLimit(state, "a", T0).state;
		}
		expect(checkRateLimit(state, "a", T0).allowed).toBe(false);
		expect(checkRateLimit(state, "b", T0).allowed).toBe(true);
	});
});

describe("mergeRateState (restart persistence)", () => {
	const DAY = "2026-08-07";
	// The race this exists for: an event advanced in memory before the persisted
	// state loaded. The higher persisted day count must survive, or a restart
	// resets the ceiling.
	it("keeps the higher day count for the same day, so a restart cannot reset it", () => {
		const persisted = { "mobile.app.connected": { minuteStart: 0, minuteCount: 0, day: DAY, dayCount: 199 } };
		const current = { "mobile.app.connected": { minuteStart: 1, minuteCount: 1, day: DAY, dayCount: 1 } };
		expect(mergeRateState(persisted, current)["mobile.app.connected"].dayCount).toBe(199);
	});

	it("drops a persisted entry from an older day", () => {
		const persisted = { x: { minuteStart: 0, minuteCount: 0, day: "2026-08-06", dayCount: 200 } };
		const current = { x: { minuteStart: 0, minuteCount: 1, day: DAY, dayCount: 1 } };
		expect(mergeRateState(persisted, current).x.dayCount).toBe(1);
	});

	it("carries a persisted name the current session hasn't touched", () => {
		const persisted = { y: { minuteStart: 0, minuteCount: 0, day: DAY, dayCount: 42 } };
		expect(mergeRateState(persisted, {}).y.dayCount).toBe(42);
	});
});
