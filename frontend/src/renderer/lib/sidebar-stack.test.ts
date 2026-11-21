import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { nestStackedSessions } from "./sidebar-stack";

const pr = (number: number, stackParent?: number, state: "open" | "draft" | "merged" = "open") => ({
	url: `https://github.com/acme/app/pull/${number}`, number, state, ci: "unknown", review: "none", mergeability: "unknown",
	reviewComments: false, updatedAt: "2026-11-21T00:00:00Z", ...(stackParent ? { stackParent } : {}),
});
const session = (id: string, prs: ReturnType<typeof pr>[] = []) => ({ id, prs }) as unknown as WorkspaceSession;
const ids = (list: WorkspaceSession[]) => list.map((s) => s.id);

describe("nestStackedSessions", () => {
	it("moves a stacked session under the session of the PR below it, and keeps the rest in order", () => {
		const { ordered, placement } = nestStackedSessions([
			session("top", [pr(3, 2)]),
			session("other"),
			session("base", [pr(1)]),
			session("mid", [pr(2, 1)]),
		]);
		expect(ids(ordered)).toEqual(["other", "base", "mid", "top"]);
		expect(placement.get("base")).toEqual({ depth: 0, base: true, onNumber: undefined });
		expect(placement.get("mid")).toEqual({ depth: 1, base: false, onNumber: 1 });
		expect(placement.get("top")).toEqual({ depth: 2, base: false, onNumber: 2 });
		expect(placement.has("other")).toBe(false);
	});

	it("leaves the list alone when no PR is stacked on another listed session's PR", () => {
		const sessions = [session("a", [pr(1)]), session("b", [pr(2, 99)])];
		const result = nestStackedSessions(sessions);
		expect(result.ordered).toBe(sessions);
		expect(result.placement.size).toBe(0);
	});

	it("ignores merged PRs, and leaves a cycle flat", () => {
		expect(nestStackedSessions([session("a", [pr(1, undefined, "merged")]), session("b", [pr(2, 1)])]).placement.size).toBe(0);
		const cycle = nestStackedSessions([session("x", [pr(1, 2)]), session("y", [pr(2, 1)])]);
		expect(ids(cycle.ordered)).toEqual(["x", "y"]);
		expect(cycle.placement.size).toBe(0);
	});
});
