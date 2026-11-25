import { describe, expect, it } from "vitest";
import { deviceRevealStep } from "./device-reveal";

describe("deviceRevealStep", () => {
	it("opens the tab for a lease an agent opened while the session is watched", () => {
		const first = deviceRevealStep(null, { sessionId: "b", key: "", dataUpdatedAt: 20, enteredAt: 10 });
		expect(first).toEqual({ baseline: { sessionId: "b", key: "" }, reveal: false });
		expect(deviceRevealStep(first.baseline, { sessionId: "b", key: "D:1", dataUpdatedAt: 30, enteredAt: 10 }).reveal).toBe(true);
	});

	it("does not take a baseline from an answer cached before the session was entered", () => {
		// Session B was last seen with no device; its agent opened one while the user was away.
		const cached = deviceRevealStep({ sessionId: "a", key: "" }, { sessionId: "b", key: "", dataUpdatedAt: 5, enteredAt: 10 });
		expect(cached).toEqual({ baseline: { sessionId: "a", key: "" }, reveal: false });
		const fresh = deviceRevealStep(cached.baseline, { sessionId: "b", key: "D:1", dataUpdatedAt: 12, enteredAt: 10 });
		expect(fresh).toEqual({ baseline: { sessionId: "b", key: "D:1" }, reveal: false });
	});

	it("stays quiet when a lease ends or nothing changed", () => {
		const held = { sessionId: "b", key: "D:1" };
		expect(deviceRevealStep(held, { sessionId: "b", key: "D:1", dataUpdatedAt: 30, enteredAt: 10 }).reveal).toBe(false);
		expect(deviceRevealStep(held, { sessionId: "b", key: "", dataUpdatedAt: 30, enteredAt: 10 })).toEqual({ baseline: { sessionId: "b", key: "" }, reveal: false });
	});
});
