import { describe, expect, it } from "vitest";
import { hasConfirmedHarnessUpdate, maintenanceMethodId, harnessUpdateNoticeKey } from "./useHarnessUpdates";

describe("harness update evidence", () => {
	it("never offers an update without both observed versions and a confirmed verdict", () => {
		const advisory = { agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", checkedAt: "2026-10-07T00:00:00Z" };
		expect(hasConfirmedHarnessUpdate(advisory)).toBe(true);
		for (const patch of [{ status: "unknown" }, { currentVersion: "" }, { latestVersion: " " }, { latestVersion: "1.2.3" }]) {
			expect(hasConfirmedHarnessUpdate({ ...advisory, ...patch })).toBe(false);
		}
	});
	it("does not infer installer ownership from an official release lookup", () => {
		const advisory = { agentId: "claude-code", status: "behind_latest", source: "official-release", checkedAt: "2026-10-07T00:00:00Z" };
		expect(maintenanceMethodId(advisory)).toBe("");
		expect(maintenanceMethodId({ ...advisory, maintenanceMethod: "official-installer" })).toBe("official-installer");
		expect(maintenanceMethodId({ ...advisory, reason: "ownership_unconfirmed" })).toBe("");
		expect(maintenanceMethodId({ ...advisory, source: "homebrew" })).toBe("homebrew");
	});
	it("scopes dismissals to host, agent and target release", () => {
		const key = harnessUpdateNoticeKey("claude-code", "2.1.292");
		expect(key).not.toBe(harnessUpdateNoticeKey("claude-code", "2.1.293"));
		expect(key).not.toBe(harnessUpdateNoticeKey("codex", "2.1.292"));
		expect(key).not.toBe(harnessUpdateNoticeKey("claude-code", "2.1.292", "remote"));
	});
});
