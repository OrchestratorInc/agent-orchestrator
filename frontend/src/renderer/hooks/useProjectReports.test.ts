import { describe, expect, it } from "vitest";

import { summarizeReportDelivery, type ProjectReport } from "./useProjectReports";

const report = (overrides: Partial<ProjectReport>): ProjectReport => ({
	id: "rpt_1",
	sessionId: "ao-1",
	projectId: "ao",
	createdAt: "2026-10-08T00:00:00Z",
	repeatCount: 1,
	...overrides,
});

describe("summarizeReportDelivery", () => {
	it("counts an empty outbox as zero", () => {
		expect(summarizeReportDelivery([])).toEqual({ undelivered: 0, errored: 0 });
	});

	it("counts pending and claimed as undelivered and skips acknowledged", () => {
		const summary = summarizeReportDelivery([
			report({ id: "a", deliveryState: "pending" }),
			report({ id: "b", deliveryState: "claimed" }),
			report({ id: "c", deliveryState: "acknowledged" }),
		]);
		expect(summary).toEqual({ undelivered: 2, errored: 0 });
	});

	it("flags undelivered reports that already failed delivery", () => {
		const summary = summarizeReportDelivery([
			report({ id: "a", deliveryState: "claimed", deliveryAttempts: 3, lastError: "no active orchestrator" }),
			report({ id: "b", deliveryState: "pending", availableAt: "2026-10-08T01:00:00Z" }),
			report({ id: "c", deliveryState: "acknowledged", lastError: "stale" }),
		]);
		expect(summary).toEqual({ undelivered: 2, errored: 1 });
	});
});
