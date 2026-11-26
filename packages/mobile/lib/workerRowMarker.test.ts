import { describe, expect, it } from "vitest";
import type { DashboardSession } from "./api";
import { workerRowIndicator } from "./workerRowMarker";

function session(overrides: Partial<DashboardSession>): DashboardSession {
	return {
		id: "s1", projectId: "p1", status: "idle", mode: "agent", branch: null, issueId: null, issueTitle: null,
		userPrompt: null, displayName: null, summary: null, createdAt: "", lastActivityAt: "",
		...overrides,
	} as DashboardSession;
}

// The desktop sidebar's rule (getSessionRowIndicator): a lattice while working,
// an icon only for what asks something of the user, nothing (a quiet ring) else.
describe("workerRowIndicator", () => {
	it.each<[Partial<DashboardSession>, ReturnType<typeof workerRowIndicator>]>([
		[{ status: "working", activity: "active" }, "working"],
		[{ status: "spawning" }, "working"],
		[{ provisionState: "provisioning", status: "idle", activity: "idle" }, "working"],
		// Working wins over a PR that needs attention.
		[{ activity: "active", status: "ci_failed" }, "working"],
		[{ status: "needs_input", activity: "waiting_input" }, "attention"],
		[{ status: "idle", activity: "blocked" }, "attention"],
		[{ status: "ci_failed", kanbanColumn: "needs_review" }, "attention"],
		[{ status: "pr_open", kanbanColumn: "needs_review", displayStatus: "Merge conflicts" }, "attention"],
		[{ status: "review_pending", kanbanColumn: "needs_review", displayStatus: "Awaiting review" }, "review"],
		[{ status: "draft", kanbanColumn: "needs_review", displayStatus: "Draft" }, null],
		[{ status: "pr_open", kanbanColumn: "validating", displayStatus: "AO reviewing" }, null],
		[{ status: "pr_open", kanbanColumn: "validating", displayStatus: "Review failed" }, "review"],
		[{ status: "mergeable", kanbanColumn: "ready", displayStatus: "Mergeable" }, "ready"],
		[{ status: "merged", kanbanColumn: "ready", displayStatus: "Merged" }, "merged"],
		[{ status: "idle", activity: "idle", kanbanColumn: "building" }, null],
		[{ status: "terminated", isTerminated: true, activity: "active" }, null],
	])("%o → %s", (overrides, expected) => {
		expect(workerRowIndicator(session(overrides))).toBe(expected);
	});
});
