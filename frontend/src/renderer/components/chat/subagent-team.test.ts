import { describe, expect, it } from "vitest";
import type { ConversationActivity, ConversationItem } from "../../types/conversation";
import {
	isSubagentLaunch,
	latestSubagentLine,
	partitionSubagentSteps,
	readSubagent,
} from "./subagent-team";

const at = (seconds: number) => new Date(Date.UTC(2026, 9, 24, 10, 0, seconds)).toISOString();

// The shape Claude's ACP adapter records: the launch is filed as reasoning, and its
// background result is a note to the model, never a report.
function launch(
	id: string,
	options: { status?: ConversationActivity["status"]; output?: string; sequence?: number; createdAt?: string } = {},
): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: options.sequence ?? 1,
		revision: 0,
		activityKind: "reasoning",
		status: options.status ?? "completed",
		summary: `Audit ${id}`,
		detail: {
			providerToolName: "Agent",
			nestedAgent: true,
			input: { description: `Audit ${id}`, prompt: `Read-only. Audit ${id}.`, subagent_type: "Explore" },
			output: options.output ?? JSON.stringify([{ text: "Async agent launched successfully.\nagentId: a1 (internal ID - do not mention to user.)", type: "text" }]),
		} as ConversationActivity["detail"],
		createdAt: options.createdAt ?? at(0),
	};
}

function step(
	id: string,
	parent: string,
	options: Partial<Pick<ConversationActivity, "activityKind" | "status" | "summary" | "sequence" | "createdAt">> & {
		detail?: Record<string, unknown>;
	} = {},
): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: options.sequence ?? 2,
		revision: 0,
		activityKind: options.activityKind ?? "command",
		status: options.status ?? "completed",
		summary: options.summary ?? "ls",
		detail: { parentProviderItemId: parent, ...(options.detail ?? {}) } as ConversationActivity["detail"],
		createdAt: options.createdAt ?? at(2),
	};
}

function handback(id: string, parent: string, message: string, createdAt = at(9)): ConversationActivity {
	return step(id, parent, {
		activityKind: "mcp_tool",
		summary: "SubagentHandback",
		createdAt,
		detail: { providerToolName: "SubagentHandback", input: { message } },
	});
}

function message(id: string, sequence: number): ConversationItem {
	return {
		kind: "message",
		id,
		sequence,
		revision: 0,
		role: "assistant",
		origin: "provider",
		text: "Launching agents.",
		streaming: false,
		createdAt: at(0),
	};
}

describe("isSubagentLaunch", () => {
	it("recognizes Claude's Agent tool and its older Task name", () => {
		expect(isSubagentLaunch(launch("a"))).toBe(true);
		const task = launch("t");
		task.detail = { ...task.detail, providerToolName: "Task" };
		expect(isSubagentLaunch(task)).toBe(true);
	});

	it("does not treat an ordinary reasoning row or a child step as a launch", () => {
		expect(isSubagentLaunch({ ...launch("r"), detail: { text: "Thinking" } })).toBe(false);
		expect(isSubagentLaunch(step("s", "tool-a"))).toBe(false);
		expect(isSubagentLaunch(message("m", 1))).toBe(false);
	});
});

describe("partitionSubagentSteps", () => {
	it("moves each child's steps under its launch and keeps everything else in order", () => {
		const a = launch("a", { sequence: 2 });
		const b = launch("b", { sequence: 3 });
		const items: ConversationItem[] = [
			message("before", 1),
			a,
			b,
			step("a1", "tool-a", { sequence: 4 }),
			message("between", 5),
			step("b1", "tool-b", { sequence: 6 }),
			step("a2", "tool-a", { sequence: 7 }),
		];

		const { items: flow, steps } = partitionSubagentSteps(items);

		expect(flow.map((item) => item.id)).toEqual(["before", "a", "b", "between"]);
		expect(steps.get("a")?.map((item) => item.id)).toEqual(["a1", "a2"]);
		expect(steps.get("b")?.map((item) => item.id)).toEqual(["b1"]);
	});

	it("keeps a step whose launch is not in the list where it was", () => {
		const orphan = step("o", "tool-elsewhere");
		const { items, steps } = partitionSubagentSteps([message("m", 1), orphan]);
		expect(items).toEqual([message("m", 1), orphan]);
		expect(steps.size).toBe(0);
	});

	it("keeps a grandchild with the outermost launch instead of stranding it", () => {
		const outer = launch("outer");
		const inner = { ...launch("inner"), detail: { ...launch("inner").detail, parentProviderItemId: "tool-outer" } };
		const grandchild = step("g", "tool-inner");
		const { items, steps } = partitionSubagentSteps([outer, inner, grandchild]);
		expect(items.map((item) => item.id)).toEqual(["outer"]);
		expect(steps.get("outer")?.map((item) => item.id)).toEqual(["inner", "g"]);
	});

	it("survives a malformed parent cycle", () => {
		const a = launch("a");
		const x = step("x", "tool-y");
		const y = step("y", "tool-x");
		const { items } = partitionSubagentSteps([a, x, y]);
		expect(items.map((item) => item.id)).toEqual(["a", "x", "y"]);
	});
});

describe("readSubagent", () => {
	it("reads the title, type and prompt the parent gave the child", () => {
		const agent = readSubagent(launch("a"), [], true);
		expect(agent).toMatchObject({
			title: "Audit a",
			agentType: "Explore",
			prompt: "Read-only. Audit a.",
		});
	});

	it("keeps a background child working while the turn waits on it", () => {
		expect(readSubagent(launch("a"), [step("a1", "tool-a")], true).state).toBe("working");
	});

	it("never presents the provider's launch note as a report", () => {
		const agent = readSubagent(launch("a"), [], true);
		expect(agent.report).toBeUndefined();
		expect(JSON.stringify(agent)).not.toMatch(/report":"Async agent/);
	});

	it("is done once the child hands its report back", () => {
		const agent = readSubagent(
			launch("a"),
			[step("a1", "tool-a"), handback("h", "tool-a", "No placeholder text found.")],
			true,
		);
		expect(agent.state).toBe("done");
		expect(agent.report).toBe("No placeholder text found.");
		expect(agent.steps.map((item) => item.id)).toEqual(["a1"]);
		expect(agent.endedAt! - agent.startedAt).toBe(9_000);
	});

	it("is working while any step still runs, even after the launch call returned", () => {
		const agent = readSubagent(launch("a"), [step("a1", "tool-a", { status: "running" })], false);
		expect(agent.state).toBe("working");
		expect(agent.endedAt).toBeUndefined();
	});

	it("reports a failed launch with the provider's reason, not its trailing noise", () => {
		const failed = launch("a", {
			status: "failed",
			output: "Concurrent subagent limit reached. You can run 20 subagents at once.\n\nThis is a Next.js storefront…",
		});
		const agent = readSubagent(failed, [], true);
		expect(agent.state).toBe("failed");
		expect(agent.error).toBe("Concurrent subagent limit reached. You can run 20 subagents at once.");
	});

	it("reads the reason out of the code fence Claude wraps a launch error in", () => {
		const failed = launch("a", { status: "failed" });
		// ACP's own block shape, which the renderer's detail type does not declare.
		failed.detail = {
			...failed.detail,
			content: [{
				type: "content",
				content: { type: "text", text: "```\nConcurrent subagent limit reached. Do not retry.\n```" },
			}],
		} as ConversationActivity["detail"];
		expect(readSubagent(failed, [], true).error).toBe("Concurrent subagent limit reached. Do not retry.");
	});

	it("treats a foreground launch that completed as done, with its result as the report", () => {
		const sync = launch("a", { output: "Found three routes." });
		expect(readSubagent(sync, [], false)).toMatchObject({ state: "done", report: "Found three routes." });
	});

	it("does not repeat a pointer to a hand-back as the report", () => {
		const sync = launch("a", {
			output: JSON.stringify([{ text: "  This agent's report was delivered to you as a message. Read it there.", type: "text" }]),
		});
		expect(readSubagent(sync, [], false)).toMatchObject({ state: "done", report: undefined });
	});

	it("says unknown, not done, when a background child never reported back", () => {
		expect(readSubagent(launch("a"), [step("a1", "tool-a")], false).state).toBe("unknown");
	});

	it("marks a cancelled launch as stopped", () => {
		expect(readSubagent(launch("a", { status: "cancelled" }), [], true).state).toBe("stopped");
	});

	it("takes only its own hand-back when a child started a child of its own", () => {
		const outer = launch("outer");
		const inner = { ...launch("inner"), detail: { ...launch("inner").detail, parentProviderItemId: "tool-outer" } };
		const innerReport = handback("hi", "tool-inner", "Inner report.", at(5));
		const outerReport = handback("ho", "tool-outer", "Outer report.", at(9));
		const readOuter = (items: ConversationItem[]) =>
			readSubagent(outer, partitionSubagentSteps(items).steps.get("outer") ?? [], true);

		// The inner child finishing says nothing about the outer one.
		const midway = readOuter([outer, inner, innerReport]);
		expect(midway.state).toBe("working");
		expect(midway.report).toBeUndefined();

		const finished = readOuter([outer, inner, innerReport, outerReport]);
		expect(finished).toMatchObject({ state: "done", report: "Outer report." });
		// The inner hand-back stays readable among the outer child's steps.
		expect(finished.steps.map((item) => item.id)).toEqual(["inner", "hi"]);
	});
});

describe("latestSubagentLine", () => {
	it("says the child is starting before it has done anything", () => {
		expect(latestSubagentLine([])).toEqual({ kind: "starting" });
	});

	it("shows the latest command without the worktree cd prefix", () => {
		const running = step("a1", "tool-a", {
			status: "running",
			summary: "cd /Users/me/repo; grep -rn shipping src/app",
			detail: { command: "cd /Users/me/repo; grep -rn shipping src/app" },
		});
		expect(latestSubagentLine([running])).toEqual({
			kind: "command",
			running: true,
			command: "grep -rn shipping src/app",
		});
		const chained = step("a2", "tool-a", { detail: { command: "cd '/a b/c' && ls" } });
		expect(latestSubagentLine([chained])).toMatchObject({ command: "ls", running: false });
	});

	it("shows the last thing the child said", () => {
		const said = step("a1", "tool-a", {
			activityKind: "mcp_tool",
			summary: "Subagent response",
			detail: { nestedAgent: true, progress: "Looking at the pages.\nWishlist has no auth check." },
		});
		expect(latestSubagentLine([said])).toEqual({ kind: "said", text: "Wishlist has no auth check." });
	});

	it("names the file of an edit", () => {
		const edit = step("a1", "tool-a", {
			activityKind: "file_change",
			detail: { files: [{ path: "src/app/cart/page.tsx", additions: 1, deletions: 0 }] },
		});
		expect(latestSubagentLine([edit])).toEqual({ kind: "edit", running: false, path: "page.tsx" });
	});
});
