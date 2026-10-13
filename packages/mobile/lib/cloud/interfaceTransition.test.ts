import { describe, expect, it } from "vitest";
import type { Session, SessionInterfaceTransition, SessionInterfaceTransitionStatus } from "@aoagents/cloud-client";
import { cloudInterfaceInputLocked, cloudInterfacePollDelay, cloudInterfaceSettledTarget, mergeCloudInterfaceStatus } from "./interfaceTransition";
const transition = (phase: SessionInterfaceTransition["phase"] = "target_starting"): SessionInterfaceTransition => ({ id: "h1", sessionId: "s1", sourceMode: "chat", targetMode: "tui", phase, policy: "drain", createdAt: "now", updatedAt: "now" });
const session = { interfaceMode: "tui", runtimeConnected: true, isTerminated: false } as Session;
describe("Cloud interface adoption", () => {
	it("adopts a recovered ready target when the server omits the completed row", () => {
		const previous = { supported: true, targetMode: "tui", transition: transition("recovery_required") } as SessionInterfaceTransitionStatus;
		const status = mergeCloudInterfaceStatus(previous, { supported: true, targetMode: "chat" }, session);
		expect(cloudInterfaceSettledTarget(status)).toBe("tui"); expect(cloudInterfaceInputLocked(status)).toBe(false);
	});
	it("settles cancellation by another client when the old source is authoritatively ready", () => {
		const previous = { supported: true, targetMode: "tui", transition: transition("draining") } as SessionInterfaceTransitionStatus;
		const status = mergeCloudInterfaceStatus(previous, { supported: true, targetMode: "tui" }, { ...session, interfaceMode: "chat" });
		expect(status.transition?.phase).toBe("cancelled"); expect(cloudInterfaceInputLocked(status)).toBe(false);
	});
	it("does not settle on an early mode commit while target startup is active", () => {
		const status = mergeCloudInterfaceStatus(undefined, { supported: true, targetMode: "chat", transition: transition() }, session);
		expect(cloudInterfaceSettledTarget(status)).toBeUndefined(); expect(cloudInterfaceInputLocked(status)).toBe(true);
	});
	it("retains accepted identity until an omitted completed row and ready target agree", () => {
		const previous = { supported: true, targetMode: "tui", transition: transition() } as SessionInterfaceTransitionStatus;
		expect(cloudInterfaceSettledTarget(mergeCloudInterfaceStatus(previous, { supported: true, targetMode: "chat" }, { ...session, runtimeConnected: false }))).toBeUndefined();
		const settled = mergeCloudInterfaceStatus(previous, { supported: true, targetMode: "chat" }, session);
		expect(settled.transition?.id).toBe("h1"); expect(cloudInterfaceSettledTarget(settled)).toBe("tui");
	});
	it("keeps both controllers locked after source-stop failure even after dismissing its notice", () => {
		const status = mergeCloudInterfaceStatus(undefined, { supported: true, targetMode: "chat", transition: { ...transition("recovery_required"), noticeAcknowledgedAt: "now" } }, session);
		expect(cloudInterfaceInputLocked(status)).toBe(true); expect(cloudInterfaceSettledTarget(status)).toBeUndefined();
	});
	it("uses bounded network backoff and stops rejected/deleted requests while respecting Retry-After", () => {
		expect(cloudInterfacePollDelay({ failures: 1 })).toBe(1000);
		expect(cloudInterfacePollDelay({ failures: 6 })).toBeUndefined();
		expect(cloudInterfacePollDelay({ status: { supported: true, targetMode: "tui", transition: transition() }, failures: 12 })).toBe(8000);
		expect(cloudInterfacePollDelay({ failures: 1, httpStatus: 404 })).toBeUndefined();
		expect(cloudInterfacePollDelay({ failures: 1, httpStatus: 429, retryAfterSeconds: 7 })).toBe(7000);
	});
});
