import { describe, expect, it } from "vitest";
import type { DashboardSession } from "./api";
import { boardTitle, workersNeedingYou } from "./board-tabs";

describe("boardTitle", () => {
	// The board is the Workers page alone; Projects moved into the sidebar.
	it.each([undefined, "(workers)", "(projects)"])("names the board Workers for %s", (route) => {
		expect(boardTitle(route)).toBe("Workers");
	});
});

describe("workersNeedingYou", () => {
	const session = (over: Partial<DashboardSession> = {}): DashboardSession =>
		({ id: "s", projectId: "p", lastActivityAt: "2026-09-16T10:00:00Z", ...over }) as DashboardSession;

	// Only the number worth acting on. A total would never fall to zero, and a
	// badge that is always lit stops being read.
	it("counts the workers waiting on a person", () => {
		const sessions = [
			session({ id: "a", status: "needs_input" }),
			session({ id: "b", status: "stuck" }),
			session({ id: "c", status: "working" }),
		];
		expect(workersNeedingYou(sessions)).toBe(2);
	});

	it("counts nothing when nothing is waiting", () => {
		expect(workersNeedingYou([session({ status: "working" })])).toBe(0);
		expect(workersNeedingYou([])).toBe(0);
	});

	it("ignores ended workers", () => {
		expect(workersNeedingYou([session({ status: "needs_input", isTerminated: true })])).toBe(0);
		expect(workersNeedingYou([session({ status: "terminated" })])).toBe(0);
	});
});
