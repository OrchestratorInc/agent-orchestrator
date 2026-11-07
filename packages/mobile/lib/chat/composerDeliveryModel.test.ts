import { describe, expect, it } from "vitest";

import { composerDeliveryPresentation, composerDeliveryRoute, composerPrimaryAction, hardwareReturnIntent } from "./composerDeliveryModel";

describe("mobile chat delivery", () => {
	it("queues ordinary sends while a turn is active", () => {
		expect(composerDeliveryRoute("send", true)).toBe("queue");
		expect(composerDeliveryPresentation({ active: true, canSteer: true, hasDraft: true, hasAttachments: false, hasQueued: false })).toEqual({
			placeholder: "Send after this…",
			showSteerAction: true,
		});
	});

	it("steers only an explicit eligible text-only draft", () => {
		expect(composerDeliveryRoute("steer", true)).toBe("steer");
		expect(composerDeliveryRoute("steer", false)).toBe("queue");
		expect(composerDeliveryPresentation({ active: true, canSteer: true, hasDraft: true, hasAttachments: true, hasQueued: false }).showSteerAction).toBe(false);
		expect(composerDeliveryPresentation({ active: true, canSteer: true, hasDraft: true, hasAttachments: false, hasQueued: true }).showSteerAction).toBe(false);
	});

	it("keeps the idle composer free of queue controls", () => {
		expect(composerDeliveryPresentation({ active: false, canSteer: true, hasDraft: true, hasAttachments: false, hasQueued: false })).toEqual({
			placeholder: "Message this worker…",
			showSteerAction: false,
		});
	});

	it("uses the primary composer control to stop only when active and empty", () => {
		expect(composerPrimaryAction({ active: true, hasDraft: false, hasAttachments: false })).toBe("stop");
		expect(composerPrimaryAction({ active: true, hasDraft: true, hasAttachments: false })).toBe("send");
		expect(composerPrimaryAction({ active: true, hasDraft: false, hasAttachments: true })).toBe("send");
		expect(composerPrimaryAction({ active: false, hasDraft: false, hasAttachments: false })).toBe("send");
	});

	it("maps hardware Return to the desktop composer's send and steer keys", () => {
		const none = { command: false, control: false, option: false };
		expect(hardwareReturnIntent(none)).toBe("send");
		expect(hardwareReturnIntent({ ...none, option: true })).toBe("send");
		expect(hardwareReturnIntent({ ...none, command: true })).toBe("steer");
		expect(hardwareReturnIntent({ ...none, control: true })).toBe("steer");
		// Cmd+Return on a draft that cannot steer (attachments, quotes, idle turn) still sends.
		expect(composerDeliveryRoute(hardwareReturnIntent({ ...none, command: true }), false)).toBe("queue");
	});
});
