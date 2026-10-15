import { afterEach, describe, expect, it, vi } from "vitest";
import NativeAbortController from "abort-controller";
import type { Session, SessionInterfaceTransition, SessionInterfaceTransitionStatus } from "@aoagents/cloud-client";
import { cloudInterfaceDelay, cloudInterfaceDrainWaiting, cloudInterfaceInputLocked, cloudInterfacePollDelay, cloudInterfaceSettledTarget, mergeCloudInterfaceStatus } from "./interfaceTransition";
const transition = (phase: SessionInterfaceTransition["phase"] = "target_starting"): SessionInterfaceTransition => ({ id: "h1", sessionId: "s1", sourceMode: "chat", targetMode: "tui", phase, policy: "drain", createdAt: "now", updatedAt: "now" });
const session = { interfaceMode: "tui", runtimeConnected: true, isTerminated: false } as Session;
describe("Cloud handoff delay with native cancellation signals", () => {
	afterEach(() => vi.useRealTimers());
	it("finishes a native polling delay without requiring throwIfAborted", async () => {
		vi.useFakeTimers();
		const controller = new NativeAbortController();
		const delayed = cloudInterfaceDelay(1000, controller.signal as AbortSignal).then(() => "completed", (error: unknown) => error);
		await vi.advanceTimersByTimeAsync(1000); expect(await delayed).toBe("completed");
		expect(vi.getTimerCount()).toBe(0);
	});
	it("rejects native cancellation during a polling delay and removes its timer", async () => {
		vi.useFakeTimers();
		const controller = new NativeAbortController();
		const delayed = expect(cloudInterfaceDelay(1000, controller.signal as AbortSignal)).rejects.toMatchObject({ name: "AbortError" });
		controller.abort(); await delayed;
		expect(vi.getTimerCount()).toBe(0);
	});
	it("preserves the cancellation reason on runtimes that provide it", async () => {
		const controller = new AbortController(); const reason = new Error("Account retired"); controller.abort(reason);
		await expect(cloudInterfaceDelay(1000, controller.signal)).rejects.toBe(reason);
	});
	it("preserves an explicit null cancellation reason", async () => {
		const controller = new AbortController(); controller.abort(null);
		await expect(cloudInterfaceDelay(1000, controller.signal)).rejects.toBe(null);
	});
});
describe("Cloud interface adoption", () => {
	it.each(["requested", "preflighting", "draining"] as const)("keeps the TUI source usable while its cancellable drain is %s", (phase) => {
		const status = mergeCloudInterfaceStatus(undefined, { supported: true, targetMode: "chat", transition: { ...transition(phase), sourceMode: "tui", targetMode: "chat" } }, session);
		expect(cloudInterfaceDrainWaiting(status)).toBe(true);
	});
	it.each(["source_stopping", "source_stopped", "target_starting", "activating", "completed", "cancelled", "failed", "recovery_required"] as const)("fences the TUI source after a drain reaches %s", (phase) => {
		const status = mergeCloudInterfaceStatus(undefined, { supported: true, targetMode: "chat", transition: { ...transition(phase), sourceMode: "tui", targetMode: "chat" } }, session);
		expect(cloudInterfaceDrainWaiting(status)).toBe(false);
	});
	it("does not preserve source input for interrupt or after the committed mode leaves TUI", () => {
		const draining = { supported: true, targetMode: "chat", transition: { ...transition("draining"), sourceMode: "tui", targetMode: "chat" } } as const;
		expect(cloudInterfaceDrainWaiting(mergeCloudInterfaceStatus(undefined, { ...draining, transition: { ...draining.transition, policy: "interrupt" } }, session))).toBe(false);
		expect(cloudInterfaceDrainWaiting(mergeCloudInterfaceStatus(undefined, draining, { ...session, interfaceMode: "chat" }))).toBe(false);
		expect(cloudInterfaceDrainWaiting(mergeCloudInterfaceStatus(undefined, { ...draining, transition: transition("draining") }, session))).toBe(false);
		expect(cloudInterfaceDrainWaiting(mergeCloudInterfaceStatus(undefined, { supported: true, targetMode: "chat" }, session))).toBe(false);
		expect(cloudInterfaceDrainWaiting()).toBe(false);
	});
	it.each([
		["active", "idle", true, false],
		["waiting_input", "idle", true, true],
		["blocked", "idle", true, true],
		["idle", "working", true, false],
		["idle", "needs_input", true, true],
		["idle", "idle", false, false],
		["exited", "idle", false, false],
	] as const)("uses fresh source activity %s and status %s for switch policy", (activityState, currentStatus, sourceBusy, sourceWaitingForInput) => {
		const previous = { supported: true, targetMode: "chat", sourceBusy: !sourceBusy, sourceWaitingForInput: !sourceWaitingForInput } as const;
		const status = mergeCloudInterfaceStatus(previous, { supported: true, targetMode: "chat" }, { ...session, activityState, status: currentStatus });
		expect(status).toMatchObject({ sourceBusy, sourceWaitingForInput });
	});
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
