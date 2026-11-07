import { describe, expect, it } from "vitest";
import type { components } from "../../api/schema";
import type { SessionBranchState } from "../types/workspace";
import { gitActionPlan } from "./SessionBranchSummary";

type WorkspaceFileSummary = components["schemas"]["WorkspaceFileSummary"];

const mergedHead = "62f2bda9e7a3da8c114387688c620cd98d07d012";
const laterHead = "0123456789abcdef0123456789abcdef01234567";

function facts(branch: SessionBranchState, uncommitted: WorkspaceFileSummary[] = []) {
	return { loaded: true, uncommitted, committed: [], branch };
}

describe("gitActionPlan after a merged PR", () => {
	it.each([
		// Squash and rebase merges leave the branch commits outside the base, so
		// they still count as commits ahead.
		["squash", { commits: 2, remoteBranch: "origin/feat/x", unpushed: 0, headSha: mergedHead }],
		["rebase", { commits: 2, remoteBranch: "origin/feat/x", unpushed: 0, headSha: mergedHead }],
		// A merge commit makes them ancestors once the base ref catches up; before
		// that they still count as ahead.
		["merge commit, base not yet fetched", { commits: 2, remoteBranch: "origin/feat/x", unpushed: 0, headSha: mergedHead }],
		["merge commit, base fetched", { commits: 0, remoteBranch: "origin/feat/x", unpushed: 0, headSha: mergedHead }],
	])("offers no create-PR action when HEAD is the merged PR head (%s)", (_merge, branch) => {
		expect(gitActionPlan(facts(branch), undefined, [mergedHead])).toBeNull();
	});

	it("offers Create PR for commits pushed after the merge", () => {
		const branch = { commits: 3, remoteBranch: "origin/feat/x", unpushed: 0, headSha: laterHead };
		expect(gitActionPlan(facts(branch), undefined, [mergedHead])).toEqual({ primary: "createPR", menu: ["createDraftPR"] });
	});

	it("offers Push & create PR for unpushed commits made after the merge", () => {
		const branch = { commits: 3, remoteBranch: "origin/feat/x", unpushed: 1, headSha: laterHead };
		expect(gitActionPlan(facts(branch), undefined, [mergedHead])).toEqual({ primary: "createPR", menu: ["push", "createDraftPR"] });
	});

	it("offers Create PR for uncommitted changes on the merged head", () => {
		const branch = { commits: 2, remoteBranch: "origin/feat/x", unpushed: 0, headSha: mergedHead };
		const dirty = [{ path: "a.go", status: "modified" } as WorkspaceFileSummary];
		expect(gitActionPlan(facts(branch, dirty), undefined, [mergedHead])?.primary).toBe("createPR");
	});

	it("keeps Create PR when the branch HEAD is not known yet", () => {
		const branch = { commits: 2, remoteBranch: "origin/feat/x", unpushed: 0 };
		expect(gitActionPlan(facts(branch), undefined, [mergedHead])?.primary).toBe("createPR");
	});

	it("still targets the open PR when one exists alongside a merged one", () => {
		const branch = { commits: 3, remoteBranch: "origin/feat/x", unpushed: 1, headSha: laterHead };
		expect(gitActionPlan(facts(branch), 60, [mergedHead])).toEqual({ primary: "push", menu: [] });
	});
});
