import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { actionDetailLines, describeConversationActivity, isFailedSpawn, isOrdinaryActivity, isSpawnFailure, mergeFailedSpawns, summarizeAOActions } from "./activity-action";

function activity(overrides: Partial<ConversationActivity>): ConversationActivity {
	return {
		kind: "activity",
		id: "activity-1",
		sequence: 1,
		revision: 1,
		activityKind: "ao_action",
		status: "completed",
		summary: "Spawned an agent",
		createdAt: "2026-10-10T00:00:00Z",
		...overrides,
	};
}

describe("describeConversationActivity", () => {
	it("labels confirmed actions and keeps metadata out of the short label", () => {
		const descriptor = describeConversationActivity(activity({
			detail: {
				action: "session.spawned",
				operationId: "child-1",
				displayName: "Reviewer",
				harness: "codex",
				href: "ao://sessions/mer/child-1",
			},
		}));
		expect(descriptor?.label).toBe("Spawned an agent");
		expect(descriptor?.label).not.toContain("Reviewer");
		expect(descriptor?.batchPolicy).toBe("ordinary");
		expect(descriptor?.href).toBe("ao://sessions/mer/child-1");
		expect(descriptor?.operationId).toBe("child-1");
		expect(descriptor?.state).toBe("success");
		expect(descriptor?.ariaLabel).toContain("Reviewer");
	});

	it.each([
		["failed", "failed"],
		["cancelled", "cancelled"],
		["recovered", "recovered"],
		["running", "running"],
		["pending", "pending"],
	] as const)("keeps %s distinct", (status, state) => {
		expect(describeConversationActivity(activity({ status }))?.state).toBe(state);
	});

	it("rejects unsafe links and unknown actions without reading prose", () => {
		expect(describeConversationActivity(activity({
			detail: { action: "session.spawned", href: "javascript:alert(1)" },
		}))?.href).toBeUndefined();
		expect(describeConversationActivity(activity({
			summary: "Created a pull request",
			detail: { action: "custom.unlisted" },
		}))?.label).toBe("ao action");
		expect(describeConversationActivity(activity({
			activityKind: "command",
			summary: "ao spawn reviewer",
			detail: { command: "ao spawn reviewer --harness codex" },
		}))).toBeUndefined();
		expect(describeConversationActivity(activity({
			activityKind: "mcp_tool",
			summary: "create_pull_request",
			detail: { toolName: "create_pull_request", result: "https://github.com/acme/repo/pull/1" },
		}))).toBeUndefined();
	});

	it("keeps the same operation identity when the revision changes", () => {
		const first = describeConversationActivity(activity({
			revision: 1,
			detail: { action: "pull_request.claimed", operationId: "claim-1", href: "https://github.com/acme/repo/pull/7" },
		}));
		const replay = describeConversationActivity(activity({
			revision: 4,
			status: "failed",
			detail: { action: "pull_request.claimed", operationId: "claim-1", href: "https://github.com/acme/repo/pull/7", error: "taken" },
		}));
		expect(replay?.operationId).toBe(first?.operationId);
		expect(replay?.state).toBe("failed");
	});

	it("shows a readable switch failure for legacy activity codes", () => {
		const switched = activity({
			status: "failed",
			detail: { action: "session.agent_switched", error: "target_binary_missing" },
		});
		expect(actionDetailLines(switched)).toContain("Agent switch failed");
		expect(actionDetailLines(activity({
			status: "failed",
			detail: { action: "session.agent_switched", error: "future_switch_failure" },
		}))).toContain("Agent switch failed");
		expect(actionDetailLines(activity({
			status: "failed",
			detail: { action: "pull_request.claimed", error: "Claim failed because the PR is closed" },
		}))).toContain("Claim failed because the PR is closed");
	});

	it("lists mixed actions instead of a generic performed count", () => {
		const summary = summarizeAOActions([
			activity({ detail: { action: "session.spawned" } }),
			activity({ id: "activity-2", detail: { action: "session.agent_switched" } }),
		]);
		expect(summary).toBe("Spawned an agent · Switched the agent");
	});

	it("keeps a repeated action as one counted part inside a mixed group", () => {
		const summary = summarizeAOActions([
			activity({ detail: { action: "session.spawned" } }),
			activity({ id: "activity-2", detail: { action: "session.spawned" } }),
			activity({ id: "activity-3", detail: { action: "session.agent_switched" } }),
		]);
		expect(summary).toBe("Spawned 2 agents · Switched the agent");
	});

	it("batches AO actions with ordinary tool calls and keeps boundaries", () => {
		expect(isOrdinaryActivity(activity({ detail: { action: "session.renamed" } }))).toBe(true);
		expect(isOrdinaryActivity(activity({ activityKind: "command", detail: { command: "rg foo" } }))).toBe(true);
		expect(isOrdinaryActivity(activity({ activityKind: "approval", status: "pending" }))).toBe(false);
		expect(isOrdinaryActivity(activity({ activityKind: "system", detail: { event: "compaction" } }))).toBe(false);
		expect(isOrdinaryActivity(activity({ activityKind: "error", status: "failed" }))).toBe(false);
		expect(isOrdinaryActivity({ kind: "message", id: "m", sequence: 1, revision: 1, role: "assistant", origin: "provider", text: "hi", streaming: false, createdAt: "" })).toBe(false);
	});
});

describe("mergeFailedSpawns", () => {
	const reason = "The default account is signed out or now holds another login, and no other account is signed in.";
	const spawnCall = (id: string, sequence: number, status: ConversationActivity["status"] = "failed"): ConversationActivity => activity({
		id, sequence, activityKind: "command", status, summary: "ao spawn --name 'snapshot'",
		detail: { command: "ao spawn --project market-place --name 'snapshot'", exitCode: status === "failed" ? 1 : 0 },
	});
	const record = (id: string, sequence: number, status: ConversationActivity["status"] = "failed"): ConversationActivity => activity({
		id, sequence, status, summary: "Spawned an agent",
		detail: { action: "session.spawned", operationId: `failed:${id}`, ...(status === "failed" ? { error: reason } : { targetSessionId: "s-2" }) },
	});

	it("folds a failed spawn record into its failed call, which carries the reason and counts as failed", () => {
		const merged = mergeFailedSpawns([spawnCall("c1", 1), record("r1", 3)]);
		expect(merged.map((each) => each.id)).toEqual(["c1"]);
		expect(merged[0]!.detail).toMatchObject({ error: reason, exitCode: undefined });
		expect(isSpawnFailure(merged[0]!)).toBe(true);
	});

	it("pairs each record with the nearest failed call", () => {
		const merged = mergeFailedSpawns([spawnCall("c1", 1), spawnCall("c2", 10), record("r2", 11), record("r1", 2)]);
		expect(merged.map((each) => each.id)).toEqual(["c1", "c2"]);
		expect(merged.every(isSpawnFailure)).toBe(true);
	});

	it("keeps the call's own error when the record gives no reason", () => {
		const call = { ...spawnCall("c1", 1), detail: { command: "ao spawn --name 'snapshot'", exitCode: 1, error: "spawn: no account" } };
		const bare = { ...record("r1", 2), detail: { action: "session.spawned", operationId: "failed:r1" } };
		const merged = mergeFailedSpawns([call, bare]);
		expect(merged.map((each) => each.id)).toEqual(["c1"]);
		expect(merged[0]!.detail).toMatchObject({ error: "spawn: no account", exitCode: undefined });
		expect(isSpawnFailure(merged[0]!)).toBe(true);
	});

	it("takes a failed command for a spawn only when it runs `ao spawn`", () => {
		const search = { ...spawnCall("c1", 1), summary: "grep -rn 'ao spawn' docs/", detail: { command: "grep -rn 'ao spawn' docs/", exitCode: 1 } };
		expect(isFailedSpawn(search)).toBe(false);
		const activities = [search, record("r1", 2)];
		expect(mergeFailedSpawns(activities)).toBe(activities);
	});

	it("leaves a record with no failed call beside it, and every successful spawn", () => {
		const lone = [record("r1", 3)];
		expect(mergeFailedSpawns(lone)).toBe(lone);
		const succeeded = [spawnCall("c1", 1, "completed"), record("r1", 2, "completed")];
		expect(mergeFailedSpawns(succeeded)).toBe(succeeded);
		expect(mergeFailedSpawns([spawnCall("c1", 1, "completed"), record("r1", 2)]).map((each) => each.id)).toEqual(["c1", "r1"]);
	});
});
