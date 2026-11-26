import { describe, expect, it } from "vitest";
import { hasConfirmedHarnessUpdate, maintenanceMethodId, harnessUpdateNoticeKey, isActiveInstallJob, isFailedInstallJob, needsHarnessLogin, updateAdvisoryRefreshInterval } from "./useHarnessUpdates";

describe("harness update evidence", () => {
	it("never offers an update without both observed versions and a confirmed verdict", () => {
		const advisory = { agentId: "codex", status: "behind_latest" as const, currentVersion: "1.2.3", latestVersion: "1.3.0", checkedAt: "2026-10-07T00:00:00Z" };
		expect(hasConfirmedHarnessUpdate(advisory)).toBe(true);
		for (const patch of [{ status: "unknown" as const }, { currentVersion: "" }, { latestVersion: " " }, { latestVersion: "1.2.3" }]) {
			expect(hasConfirmedHarnessUpdate({ ...advisory, ...patch })).toBe(false);
		}
	});
	it("does not infer installer ownership from an official release lookup", () => {
		const advisory = { agentId: "claude-code", status: "behind_latest" as const, source: "official-release", checkedAt: "2026-10-07T00:00:00Z" };
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

describe("updateAdvisoryRefreshInterval", () => {
	it("reads the daemon's cached advisory every fifteen minutes", () => {
		expect(updateAdvisoryRefreshInterval()).toBe(15 * 60_000);
	});

	it("retries a failed fetch after five minutes", () => {
		expect(updateAdvisoryRefreshInterval(true)).toBe(5 * 60_000);
	});
});

describe("harness job and login helpers", () => {
	it("classifies running and failed install jobs", () => {
		for (const status of ["running", "installing", "verifying"] as const) expect(isActiveInstallJob({ status })).toBe(true);
		for (const status of ["failed", "unsupported", "interrupted"] as const) expect(isFailedInstallJob({ status })).toBe(true);
		expect(isActiveInstallJob({ status: "succeeded" })).toBe(false);
		expect(isFailedInstallJob({ status: "succeeded" })).toBe(false);
		expect(isActiveInstallJob(undefined)).toBe(false);
	});

	it("asks for login only when the harness has a login flow and reads as signed out", () => {
		expect(needsHarnessLogin({ action: "login" }, "unauthorized")).toBe(true);
		expect(needsHarnessLogin({ action: "instructions" }, "unauthorized")).toBe(false);
		expect(needsHarnessLogin({ action: "login" }, "authorized")).toBe(false);
		expect(needsHarnessLogin(undefined, "unauthorized")).toBe(false);
	});
});
