import { beforeEach, describe, expect, it, vi } from "vitest";
import { lastAnsweredRequestAt, noteRequestAnswered, onRequestAnswered, resetAnsweredRequestsForTests } from "./answeredRequests";

describe("answeredRequests", () => {
	beforeEach(() => resetAnsweredRequestsForTests());

	it("keeps the send time of the newest answered request", () => {
		expect(lastAnsweredRequestAt()).toBe(0);
		noteRequestAnswered(2_000);
		// A slower request sent earlier must not move it back.
		noteRequestAnswered(1_000);
		expect(lastAnsweredRequestAt()).toBe(2_000);
	});

	it("tells listeners only about newer answers, until they unsubscribe", () => {
		const listener = vi.fn();
		const unsubscribe = onRequestAnswered(listener);
		noteRequestAnswered(10);
		noteRequestAnswered(5);
		expect(listener).toHaveBeenCalledTimes(1);
		unsubscribe();
		noteRequestAnswered(20);
		expect(listener).toHaveBeenCalledTimes(1);
	});
});
