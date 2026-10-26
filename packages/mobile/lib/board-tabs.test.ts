import { describe, expect, it } from "vitest";
import { boardTabs, boardTitle } from "./board-tabs";

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
