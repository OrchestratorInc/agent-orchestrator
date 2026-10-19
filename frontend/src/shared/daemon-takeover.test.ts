// Unit tests for desktop takeover decisions. Run with:
//   cd frontend && npx vitest run src/shared/daemon-takeover.test.ts
import { describe, expect, it } from "vitest";
import { DAEMON_SERVICE_NAME, type DaemonProbe } from "./daemon-attach";
import { browserDaemonOwnershipDecision, orphanDaemonTakeoverDecision } from "./daemon-takeover";

// A minimal valid DaemonProbe (non-null means the AO daemon answered /healthz).
const healthyProbe: DaemonProbe = {
	status: "ok",
	service: DAEMON_SERVICE_NAME,
	pid: 1234,
};

describe("orphanDaemonTakeoverDecision", () => {
	const runFile = JSON.stringify({ pid: 1234, port: 3001, processStartId: "darwin:123:456" });
	it("blocks replacement when a new daemon responds after attach failed", () => {
		expect(orphanDaemonTakeoverDecision(null, healthyProbe)).toBe("blocked");
		expect(orphanDaemonTakeoverDecision(runFile, healthyProbe)).toBe("blocked");
	});

	it("delegates saved owner checks, including legacy records, to Go", () => {
		expect(orphanDaemonTakeoverDecision(runFile, null)).toBe("check");
		expect(orphanDaemonTakeoverDecision(JSON.stringify({ pid: 1234, port: 3001 }), null)).toBe("check");
	});

	it("permits startup when no daemon or owner record is present", () => {
		expect(orphanDaemonTakeoverDecision(null, null)).toBe("none");
	});

	it("blocks malformed or nonpositive process owners", () => {
		for (const contents of ["{bad", JSON.stringify({ port: 3001 }), JSON.stringify({ pid: -1, port: 3001 })]) {
			expect(orphanDaemonTakeoverDecision(contents, null)).toBe("blocked");
		}
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
