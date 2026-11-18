import { describe, expect, it } from "vitest";
import { chatFixtureEmpty } from "../lib/chat-fixture";
import { mergeConversationPages } from "./useConversation";

describe("mergeConversationPages", () => {
	it("keeps every page's batched deliveries, the live page winning a repeat", () => {
		const live = { ...chatFixtureEmpty, batchedDeliveries: [{ turnId: "t2", deliveredWithTurnId: "lead-2" }, { turnId: "t1", deliveredWithTurnId: "lead-1", steerActivityId: "steer-new" }] };
		const older = { ...chatFixtureEmpty, batchedDeliveries: [{ turnId: "t0", deliveredWithTurnId: "lead-0" }, { turnId: "t1", deliveredWithTurnId: "lead-1" }] };
		const merged = mergeConversationPages([live, older]);
		expect(merged?.batchedDeliveries?.map((delivery) => delivery.turnId).sort()).toEqual(["t0", "t1", "t2"]);
		expect(merged?.batchedDeliveries?.find((delivery) => delivery.turnId === "t1")?.steerActivityId).toBe("steer-new");
	});
});
