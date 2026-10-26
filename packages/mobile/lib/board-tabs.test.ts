import { describe, expect, it } from "vitest";
import type { DashboardSession } from "./api";
import { boardTabs, boardTitle, workersNeedingYou } from "./board-tabs";

describe("board tabs", () => {
	// The groups add a stack to each tab without changing its URL, so every link,
	// push and notification that opens a tab still lands on it.
	it("keeps each tab on the URL it always had", () => {
		expect(boardTabs.map(({ name, href }) => [name, href])).toEqual([
			["(workers)", "/"],
			["(projects)", "/projects"],
			["(prs)", "/prs"],
		]);
	});

	it.each([
		[undefined, "Workers"],
		["(workers)", "Workers"],
		["(projects)", "Projects"],
		["(prs)", "Pull Requests"],
	])("names the board after its %s tab", (tab, expected) => {
		expect(boardTitle(tab)).toBe(expected);
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
