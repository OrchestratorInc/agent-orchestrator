import { describe, expect, it } from "vitest";
import { boardReadiness } from "./environment/boardSelection";
import { pushNotificationTarget, shouldUnpairPush } from "./pushLifecycle";

describe("Local push pairing across environment changes", () => {
	it("keeps Local pairing when switching from paired Local to ready Cloud", () => {
		const local = boardReadiness("local", "local", true);
		const cloud = boardReadiness("cloud", "cloud", true);
		expect(shouldUnpairPush(local.localConfigured, cloud.localConfigured)).toBe(false);
		expect(cloud.localConfigured).toBe(true);
	});
	it("does not unpair Local when the active Cloud source disappears", () => {
		const ready = boardReadiness("cloud", "cloud", true);
		const expired = boardReadiness("cloud", undefined, true);
		expect(expired.configured).toBe(false);
		expect(expired.localConfigured).toBe(true);
		expect(shouldUnpairPush(ready.localConfigured, expired.localConfigured)).toBe(false);
	});
	it("does not invent Local pairing for a Cloud-only account", () => {
		const ready = boardReadiness("cloud", "cloud", false);
		const expired = boardReadiness("cloud", undefined, false);
		expect(ready.localConfigured).toBe(false);
		expect(shouldUnpairPush(ready.localConfigured, expired.localConfigured)).toBe(false);
	});
	it("still unpairs when the saved Local pairing is explicitly removed", () => {
		const paired = boardReadiness("local", "local", true);
		const unpaired = boardReadiness("local", undefined, false);
		expect(shouldUnpairPush(paired.localConfigured, unpaired.localConfigured)).toBe(true);
		expect(shouldUnpairPush(false, false)).toBe(false);
	});
});

describe("push notification tap routing", () => {
	it("routes a legacy Local worker push to Workers because it has no host identity", () => {
		expect(pushNotificationTarget(true, { type: "needs_input", sessionId: "worker-1" })).toBe("/");
	});
	it("does not route a Local notification without a paired desktop", () => {
		expect(pushNotificationTarget(false, { type: "needs_input", sessionId: "same-id-as-local" })).toBeUndefined();
		expect(pushNotificationTarget(false, { type: "ready_to_merge" })).toBeUndefined();
	});
	it("preserves Local PR target", () => {
		expect(pushNotificationTarget(true, { type: "ready_to_merge" })).toBe("/prs");
	});
});
