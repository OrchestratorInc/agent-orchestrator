import { describe, expect, it } from "vitest";
import { formatTimeCompact, formatTimeTerse } from "./format-time";

describe("formatTimeTerse", () => {
	const now = new Date("2026-08-26T12:00:00Z");

	it.each([
		["2026-08-26T11:59:30Z", "now"],
		["2026-08-26T11:55:00Z", "5m"],
		["2026-08-26T09:00:00Z", "3h"],
		["2026-08-14T12:00:00Z", "12d"],
	])("formats %s as %s", (timestamp, expected) => {
		expect(formatTimeTerse(timestamp, now)).toBe(expected);
	});
});

describe("formatTimeCompact with long units", () => {
	const now = new Date("2026-10-16T12:00:00Z");

	it.each([
		["2026-10-16T09:00:00Z", "3h ago"],
		["2026-09-24T12:00:00Z", "22d ago"],
		["2026-08-01T12:00:00Z", "2mo ago"],
		["2012-03-06T23:06:50Z", "14y ago"],
	])("formats %s as %s", (timestamp, expected) => {
		expect(formatTimeCompact(timestamp, { longUnits: true, now })).toBe(expected);
	});

	it("keeps counting days without long units", () => {
		expect(formatTimeCompact("2012-03-06T23:06:50Z", { now })).toBe("5336d ago");
	});
});
