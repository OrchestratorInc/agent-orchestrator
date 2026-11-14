// @vitest-environment node
import { describe, it, expect } from "vitest";
import { evaluateEscalation } from "./escalation-evaluator";

const H48 = 48 * 60 * 60 * 1000;
const now = Date.now();

describe("evaluateEscalation - latest channel", () => {
	it("not escalated under 48h", () => {
		expect(
			evaluateEscalation({
				channel: "latest",
				stagedAt: now - H48 + 1000,
				now,
				important: false,
				runningVersion: "0.10.4",
				latestStableVersion: "0.10.5",
			}),
		).toBe(false);
	});

	it("escalated at exactly 48h", () => {
		expect(
			evaluateEscalation({
				channel: "latest",
				stagedAt: now - H48,
				now,
				important: false,
				runningVersion: "0.10.4",
				latestStableVersion: "0.10.5",
			}),
		).toBe(true);
	});

	it("escalated over 48h even without stable version info", () => {
		expect(
			evaluateEscalation({
				channel: "latest",
				stagedAt: now - H48 - 1,
				now,
				important: false,
				runningVersion: "0.10.4",
				latestStableVersion: undefined,
			}),
		).toBe(true);
	});
});

describe("evaluateEscalation - nightly channel", () => {
	it("escalated when the important flag is set", () => {
		expect(
			evaluateEscalation({
				channel: "nightly",
				stagedAt: now,
				now,
				important: true,
				runningVersion: "0.10.4-nightly.202607031330",
				latestStableVersion: undefined,
			}),
		).toBe(true);
	});

	it("escalated when running nightly is behind stable of the same base", () => {
		expect(
			evaluateEscalation({
				channel: "nightly",
				stagedAt: now,
				now,
				important: false,
				runningVersion: "0.10.4-nightly.202607031330",
				latestStableVersion: "0.10.4",
			}),
		).toBe(true);
	});

	it("not escalated when nightly is ahead of stable", () => {
		expect(
			evaluateEscalation({
				channel: "nightly",
				stagedAt: now,
				now,
				important: false,
				runningVersion: "0.10.4-nightly.202607031330",
				latestStableVersion: "0.10.3",
			}),
		).toBe(false);
	});

	it("not escalated when stable version info is missing", () => {
		expect(
			evaluateEscalation({
				channel: "nightly",
				stagedAt: now - H48 * 10,
				now,
				important: false,
				runningVersion: "0.10.4-nightly.202607031330",
				latestStableVersion: undefined,
			}),
		).toBe(false);
	});

	it("not escalated on unparseable version strings", () => {
		expect(
			evaluateEscalation({
				channel: "nightly",
				stagedAt: now,
				now,
				important: false,
				runningVersion: "not-a-version",
				latestStableVersion: "0.10.4",
			}),
		).toBe(false);
	});
});
