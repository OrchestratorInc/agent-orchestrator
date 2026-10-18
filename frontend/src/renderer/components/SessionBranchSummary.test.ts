import { describe, expect, it } from "vitest";
import { gitActionPlan } from "./SessionBranchSummary";

const tip = "a2efe27cc0807baa774800fc86abfd0eb18dfa0c";
const pushedBranch = {
  commits: 1,
  remoteBranch: "origin/demo/fix",
  unpushed: 0,
  behind: 0,
  conflicts: false,
  lastCommit: { sha: tip, subject: "fix", committedAt: "2026-10-18T07:18:23Z" },
};
const facts = (overrides: Record<string, unknown> = {}) =>
  ({ loaded: true, uncommitted: [], committed: [], branch: pushedBranch, ...overrides }) as Parameters<typeof gitActionPlan>[0];

describe("gitActionPlan after a merge", () => {
  it("offers Create PR for a pushed branch with no PR yet", () => {
    expect(gitActionPlan(facts(), undefined)?.primary).toBe("createPR");
  });

  it("offers nothing when the branch tip is a merged PR's head", () => {
    expect(gitActionPlan(facts(), undefined, [tip])).toBeNull();
  });

  it("offers Create PR again once a new commit lands after the merge", () => {
    const next = { ...pushedBranch, commits: 2, lastCommit: { ...pushedBranch.lastCommit, sha: "b".repeat(40) } };
    expect(gitActionPlan(facts({ branch: next }), undefined, [tip])?.primary).toBe("createPR");
  });

  it("still offers actions for uncommitted or unpushed work on a merged tip", () => {
    const dirty = facts({ uncommitted: [{ path: "a.ts", additions: 1, deletions: 0 }] });
    expect(gitActionPlan(dirty, undefined, [tip])?.primary).toBe("createPR");
    const ahead = facts({ branch: { ...pushedBranch, unpushed: 1 } });
    expect(gitActionPlan(ahead, undefined, [tip])?.primary).toBe("createPR");
  });

  it("ignores an empty head sha", () => {
    expect(gitActionPlan(facts(), undefined, [""])?.primary).toBe("createPR");
  });
});
