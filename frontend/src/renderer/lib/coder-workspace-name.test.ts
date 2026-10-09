import { describe, expect, it } from "vitest";
import { isValidCoderWorkspaceNamePrefix } from "./coder-workspace-name";

describe("isValidCoderWorkspaceNamePrefix", () => {
	it.each(["", "a", "acme", "team-1", "a1b2", "abcdefghijklmnopqrst"])("accepts %j", (value) => {
		expect(isValidCoderWorkspaceNamePrefix(value)).toBe(true);
	});

	it.each(["1team", "-team", "Team", "team-", "te--am", "team_1", "team 1", "abcdefghijklmnopqrstu"])("rejects %j", (value) => {
		expect(isValidCoderWorkspaceNamePrefix(value)).toBe(false);
	});
});
