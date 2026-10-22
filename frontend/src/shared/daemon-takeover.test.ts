// Unit tests for shouldReplacePortHolder. Run with:
//   cd frontend && npx vitest run src/shared/daemon-takeover.test.ts
import { describe, expect, it } from "vitest";
import { DAEMON_SERVICE_NAME, type DaemonProbe } from "./daemon-attach";
import {
	browserDaemonOwnershipDecision,
	browserRuntimeRecoveryDecision,
	HANDSHAKE_CLOSED_BEFORE_ACCEPT,
	keepAliveForReplacement,
	shouldReplaceDaemonForStartup,
	shouldReplacePortHolder,
} from "./daemon-takeover";

// A minimal valid DaemonProbe (non-null means the AO daemon answered /healthz).
const healthyProbe: DaemonProbe = {
	status: "ok",
	service: DAEMON_SERVICE_NAME,
	pid: 1234,
};

describe("shouldReplacePortHolder", () => {
	it("returns true when a probe answered (rejected responder, any holderPidAlive)", () => {
		expect(shouldReplacePortHolder(healthyProbe, false)).toBe(true);
		expect(shouldReplacePortHolder(healthyProbe, true)).toBe(true);
	});

	it("returns true when probe is null but the run-file PID is still alive (hung holder)", () => {
		expect(shouldReplacePortHolder(null, true)).toBe(true);
	});

	it("returns false when probe is null and no live holder PID (nothing to kill)", () => {
		expect(shouldReplacePortHolder(null, false)).toBe(false);
	});
});

describe("browserDaemonOwnershipDecision", () => {
	it("reuses a daemon authenticated by this app launch", () => {
		expect(browserDaemonOwnershipDecision("run-1", { owner: "app", appRunId: "run-1" })).toEqual({
			action: "attach",
		});
	});

	it("replaces an older app daemon without making it persistent", () => {
		expect(browserDaemonOwnershipDecision("run-2", { owner: "app", appRunId: "run-1" })).toEqual({
			action: "replace",
			keepAlive: false,
		});
	});

	it("preserves persistent and headless daemon lifetime during replacement", () => {
		expect(browserDaemonOwnershipDecision("run-2", { owner: "persistent", appRunId: "run-1" })).toEqual({
			action: "replace",
			keepAlive: true,
		});
		expect(browserDaemonOwnershipDecision("run-2", {})).toEqual({ action: "replace", keepAlive: true });
	});
});

describe("browserRuntimeRecoveryDecision", () => {
	it("retires an adopted daemon that refuses this app's token", () => {
		expect(
			browserRuntimeRecoveryDecision({ rejectedReason: "invalid-token", spawnedHere: false, attempted: false }),
		).toEqual({ action: "replace", reason: "invalid-token" });
	});

	it("retires an adopted daemon that hangs up before accepting the hello", () => {
		expect(
			browserRuntimeRecoveryDecision({
				rejectedReason: HANDSHAKE_CLOSED_BEFORE_ACCEPT,
				spawnedHere: false,
				attempted: false,
			}),
		).toEqual({ action: "replace", reason: HANDSHAKE_CLOSED_BEFORE_ACCEPT });
	});

	it("waits while the daemon this app spawned is still ours to fix", () => {
		expect(
			browserRuntimeRecoveryDecision({ rejectedReason: "invalid-token", spawnedHere: true, attempted: false }),
		).toEqual({ action: "wait" });
	});

	it("waits for refusal reasons a restart would not fix", () => {
		expect(
			browserRuntimeRecoveryDecision({ rejectedReason: "unsupported-version", spawnedHere: false, attempted: false }),
		).toEqual({ action: "wait" });
		expect(browserRuntimeRecoveryDecision({ rejectedReason: null, spawnedHere: false, attempted: false })).toEqual({
			action: "wait",
		});
	});

	it("replaces at most once per launch", () => {
		expect(
			browserRuntimeRecoveryDecision({ rejectedReason: "invalid-token", spawnedHere: false, attempted: true }),
		).toEqual({ action: "wait" });
	});
});

describe("startup replacement", () => {
	it("honors a forced replacement of an adopted daemon", () => {
		expect(
			shouldReplaceDaemonForStartup({
				ownership: { action: "attach" },
				forceRequested: true,
			}),
		).toBe(true);
	});

	it("replaces only on an ownership mismatch when nothing forced it", () => {
		expect(
			shouldReplaceDaemonForStartup({ ownership: { action: "attach" }, forceRequested: false }),
		).toBe(false);
		expect(
			shouldReplaceDaemonForStartup({ ownership: { action: "replace", keepAlive: false }, forceRequested: false }),
		).toBe(true);
	});

	it("keeps a forced replacement's own daemon alive", () => {
		// The retired daemon may not have a lifetime tag at all, so a forced
		// replacement must not adopt its guidance.
		expect(keepAliveForReplacement({ ownership: { action: "attach" }, forceRequested: true })).toBe(true);
		expect(keepAliveForReplacement({ ownership: { action: "replace", keepAlive: false }, forceRequested: false })).toBe(
			false,
		);
		expect(keepAliveForReplacement({ ownership: { action: "replace", keepAlive: true }, forceRequested: false })).toBe(
			true,
		);
	});
});
