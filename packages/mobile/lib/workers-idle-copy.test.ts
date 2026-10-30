import { describe, expect, it } from "vitest";
import { idleFleet, workersIdleCopy } from "./workers-idle-copy";

const now = Date.parse("2026-10-30T20:00:00Z");

describe("workersIdleCopy", () => {
	it("names the quiet machine and how long since anything happened", () => {
		expect(workersIdleCopy({ machines: ["Lab Mac"], connection: "open", lastFinishedAt: "2026-10-30T15:00:00Z", now })).toEqual({
			title: "Nothing running",
			status: "Lab Mac is idle.",
			history: "Last worker finished 5h ago.",
		});
	});

	it("counts machines when several are in scope", () => {
		expect(workersIdleCopy({ machines: ["Lab Mac", "Windows"], connection: "open", now }).status).toBe("All 2 machines are idle.");
	});

	it("does not call a machine idle while it is still connecting", () => {
		expect(workersIdleCopy({ machines: ["Lab Mac"], connection: "connecting", now }).status).toBe("Connecting to Lab Mac…");
		expect(workersIdleCopy({ machines: [], connection: "connecting", now }).status).toBe("Connecting…");
	});

	it("falls back when no machine is known by name", () => {
		expect(workersIdleCopy({ machines: [], connection: "open", now }).status).toBe("Your machine is idle.");
	});

	it("says where workers will appear when there is no history", () => {
		expect(workersIdleCopy({ machines: ["Lab Mac"], connection: "open", now }).history).toBe("New workers will show up here.");
		expect(workersIdleCopy({ machines: ["Lab Mac"], connection: "open", lastFinishedAt: "not a date", now }).history)
			.toBe("New workers will show up here.");
	});

	it("reads a finish under a minute ago as just now", () => {
		expect(workersIdleCopy({ machines: ["Lab Mac"], connection: "open", lastFinishedAt: "2026-10-30T19:59:40Z", now }).history)
			.toBe("Last worker finished just now.");
	});
});

describe("idleFleet", () => {
	const mac = { name: "Lab Mac", connection: "open" as const };
	const windows = { name: "Windows", connection: "connecting" as const };

	// Windows may not have sent its workers yet, so it cannot be called idle.
	it("names only open machines while another is still connecting", () => {
		const fleet = idleFleet([mac, windows], "open");
		expect(fleet).toEqual({ machines: ["Lab Mac"], connection: "open" });
		expect(workersIdleCopy({ ...fleet, now }).status).toBe("Lab Mac is idle.");
	});

	it("counts every open machine", () => {
		expect(idleFleet([mac, { name: "Studio", connection: "open" }], "open").machines).toEqual(["Lab Mac", "Studio"]);
	});

	it("names the connecting machines when none is open yet", () => {
		const fleet = idleFleet([windows, { name: "Studio", connection: "closed" }], "open");
		expect(fleet).toEqual({ machines: ["Windows"], connection: "connecting" });
		expect(workersIdleCopy({ ...fleet, now }).status).toBe("Connecting to Windows…");
	});

	it("follows the scope it is given, not the whole fleet", () => {
		expect(idleFleet([windows], "open")).toEqual({ machines: ["Windows"], connection: "connecting" });
	});

	it("falls back to the single-host connection with no host list", () => {
		expect(idleFleet([], "connecting")).toEqual({ machines: [], connection: "connecting" });
	});
});
