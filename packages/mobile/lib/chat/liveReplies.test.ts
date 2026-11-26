import { describe, expect, it, vi } from "vitest";
import { createLiveReplies } from "./liveReplies";

describe("live replies", () => {
	it("tells only the reply's listeners about newer text, and keeps the newest", () => {
		const replies = createLiveReplies();
		const growing = vi.fn();
		const other = vi.fn();
		replies.subscribe("m2", growing);
		replies.subscribe("m9", other);
		replies.publish([{ id: "m2", text: "On it", revision: 2 }]);
		expect(replies.get("m2")).toEqual({ text: "On it", revision: 2 });
		expect(growing).toHaveBeenCalledTimes(1);
		// A read that answers late, with older text, changes nothing.
		replies.publish([{ id: "m2", text: "On", revision: 1 }]);
		expect(replies.get("m2")?.text).toBe("On it");
		expect(growing).toHaveBeenCalledTimes(1);
		expect(other).not.toHaveBeenCalled();
	});

	it("stops telling a listener once it unsubscribes, and forgets on clear", () => {
		const replies = createLiveReplies();
		const listener = vi.fn();
		const stop = replies.subscribe("m2", listener);
		stop();
		replies.publish([{ id: "m2", text: "On it", revision: 2 }]);
		expect(listener).not.toHaveBeenCalled();
		replies.clear();
		expect(replies.get("m2")).toBeUndefined();
	});
});
