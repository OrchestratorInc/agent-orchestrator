import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { describeConversationActivity, isOrdinaryActivity } from "./activity-action";

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
		}))?.label).toBe("AO action");
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

	it("batches AO actions with ordinary tool calls and keeps boundaries", () => {
		expect(isOrdinaryActivity(activity({ detail: { action: "session.renamed" } }))).toBe(true);
		expect(isOrdinaryActivity(activity({ activityKind: "command", detail: { command: "rg foo" } }))).toBe(true);
		expect(isOrdinaryActivity(activity({ activityKind: "approval", status: "pending" }))).toBe(false);
		expect(isOrdinaryActivity(activity({ activityKind: "system", detail: { event: "compaction" } }))).toBe(false);
		expect(isOrdinaryActivity(activity({ activityKind: "error", status: "failed" }))).toBe(false);
		expect(isOrdinaryActivity({ kind: "message", id: "m", sequence: 1, revision: 1, role: "assistant", origin: "provider", text: "hi", streaming: false, createdAt: "" })).toBe(false);
	});
});
