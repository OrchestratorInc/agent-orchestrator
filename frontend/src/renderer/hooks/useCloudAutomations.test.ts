import { describe, expect, it } from "vitest";
import { cloudAutomationPollInterval, cloudRunsPollInterval } from "./useCloudAutomations";

describe("cloud automation polling", () => {
	const run = (status: string) => ({ id: status, automationId: "a", scheduledFor: "2026-11-16T09:00:00Z", trigger: "schedule" as const, status: status as never, attemptCount: 1, createdAt: "2026-11-16T09:00:00Z" });

	it("follows a run in flight closely and idles slowly otherwise", () => {
		expect(cloudAutomationPollInterval(undefined)).toBe(60_000);
		expect(cloudAutomationPollInterval([])).toBe(60_000);
		expect(cloudAutomationPollInterval([{ latestRun: run("completed") }, { latestRun: undefined }])).toBe(60_000);
		for (const status of ["pending", "starting", "running"]) {
			expect(cloudAutomationPollInterval([{ latestRun: run("failed") }, { latestRun: run(status) }])).toBe(15_000);
		}
	});

	it("polls a run history the same way", () => {
		expect(cloudRunsPollInterval(undefined)).toBe(60_000);
		expect(cloudRunsPollInterval([run("skipped"), run("timed_out")])).toBe(60_000);
		expect(cloudRunsPollInterval([run("running"), run("completed")])).toBe(15_000);
	});
});
