import { describe, expect, it } from "vitest";
import { isValidCoderWorkspaceNamePrefix, coderWorkspaceNamePrefixFor } from "./coder-workspace-name";

describe("isValidCoderWorkspaceNamePrefix", () => {
	it.each(["", "a", "acme", "team-1", "a1b2", "abcdefghijklmnopqrst"])("accepts %j", (value) => {
		expect(isValidCoderWorkspaceNamePrefix(value)).toBe(true);
	});

	it.each(["1team", "-team", "Team", "team-", "te--am", "team_1", "team 1", "abcdefghijklmnopqrstu"])("rejects %j", (value) => {
		expect(isValidCoderWorkspaceNamePrefix(value)).toBe(false);
	});
});

describe("coderWorkspaceNamePrefixFor", () => {
	// Same table as the control plane's TestCoderWorkspaceNamePrefixFor.
	it.each([
		["alice", "alice"],
		["Alice Backend", "alice-backend"],
		["  11x / Alice__API  ", "x-alice-api"],
		["2024", "ao"],
		["", "ao"],
		["---", "ao"],
		["a-very-long-project-name-here", "a-very-long-project"],
		["Ünïcode Project", "n-code-project"],
		["agreemint-byo", "agreemint-byo"],
	])("derives %j as %j", (name, want) => {
		const got = coderWorkspaceNamePrefixFor(name);
		expect(got).toBe(want);
		expect(isValidCoderWorkspaceNamePrefix(got)).toBe(true);
	});
});
