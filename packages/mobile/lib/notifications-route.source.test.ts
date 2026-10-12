import { describe, expect, it } from "vitest";
import { notificationIdentity } from "./notificationView";

describe("notifications host routing", () => {
	it("keeps same-id actions on different machines independently addressable", () => {
		expect(notificationIdentity("host-a", "same-id")).toBe("host-a:same-id");
		expect(notificationIdentity("host-b", "same-id")).toBe("host-b:same-id");
		expect(notificationIdentity("host-a", "same-id")).not.toBe(notificationIdentity("host-b", "same-id"));
	});
});
