import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { boardSearchText, filterBoardSessions } from "./board-search";

function session(overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id" | "title">): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

const pr = (number: number) => ({ url: `https://github.com/o/r/pull/${number}`, number, state: "open", ci: "passing", review: "none" }) as WorkspaceSession["prs"][number];

describe("board search", () => {
	const sessions = [
		session({ id: "ao-12", title: "Fix login redirect", branch: "ao/login-fix", prs: [pr(812)] }),
		session({ id: "ao-13", title: "Board search", branch: "ao/board-search" }),
		session({ id: "ao-14", title: "Theme picker", branch: "ao/themes", prs: [pr(77), pr(95)] }),
	];
	const ids = (query: string) => filterBoardSessions(sessions, query).map((candidate) => candidate.id);

	it("matches title, session id, branch and PR number, case-insensitively", () => {
		expect(ids("LOGIN")).toEqual(["ao-12"]);
		expect(ids("ao-13")).toEqual(["ao-13"]);
		expect(ids("ao/themes")).toEqual(["ao-14"]);
		expect(ids("#95")).toEqual(["ao-14"]);
		expect(ids("812")).toEqual(["ao-12"]);
	});

	it("needs every word and keeps the original order", () => {
		expect(ids("fix redirect")).toEqual(["ao-12"]);
		expect(ids("fix themes")).toEqual([]);
		expect(ids("ao-1")).toEqual(["ao-12", "ao-13", "ao-14"]);
	});

	it("returns the same list for a blank query", () => {
		expect(filterBoardSessions(sessions, "   ")).toBe(sessions);
	});

	it("leaves out a branch the card does not show yet", () => {
		const naming = session({ id: "ao-15", title: "New task", branch: "ao/provisional-name", branchNaming: true });
		expect(boardSearchText(naming)).not.toContain("provisional");
	});
});
