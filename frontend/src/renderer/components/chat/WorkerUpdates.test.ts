import { describe, expect, it } from "vitest";
import type { ConversationActivity, ConversationItem, ConversationMessage } from "../../types/conversation";
import {
	isWorkerLead,
	isDuplicateSpawnAction,
	piggybackedUpdates,
	placeBatchedReports,
	renamedTitle,
	spawnedSessionIds,
	splitHeadline,
	workerUpdatesOf,
} from "./WorkerUpdates";

const createdAt = "2026-10-22T03:59:00Z";

function message(overrides: Partial<ConversationMessage>): ConversationMessage {
	return { kind: "message", id: "m1", sequence: 1, revision: 0, role: "user", origin: "automation", text: "", streaming: false, createdAt, ...overrides };
}

function command(command: string, output = ""): ConversationActivity {
	return { kind: "activity", id: command, sequence: 2, revision: 0, activityKind: "command", status: "completed", summary: command, detail: { command, output }, createdAt };
}

const batch = [
	"Reports since your previous turn:",
	"",
	"[needs_input] [Load chat \\[iOS\\] pages](ao://sessions/proj/proj-286)",
	"Tunnel or loopback?",
	"",
	"[done] [Check regressions](ao://sessions/proj/proj-288) (repeated 2 times)",
	"One strong candidate: #7043",
	"artifact: /home/u/.ao/data/artifacts/proj-288/check.md",
].join("\n");

describe("worker updates", () => {
	it("splits a report batch into one update per worker", () => {
		expect(workerUpdatesOf(message({ text: batch }))).toEqual([
			{ key: "m1:0", state: "needs_input", name: "Load chat [iOS] pages", projectId: "proj", sessionId: "proj-286", body: "Tunnel or loopback?", createdAt },
			{
				key: "m1:1", state: "done", name: "Check regressions", projectId: "proj", sessionId: "proj-288",
				body: "One strong candidate: #7043\nartifact: /home/u/.ao/data/artifacts/proj-288/check.md", createdAt,
			},
		]);
	});

	it("reads the older report format that linked the session without a name", () => {
		const text = "Reports since your previous turn:\n\n[done] ao://sessions/proj/proj-208\nInvestigated the screenshot.\n\n[checkpoint] ao://sessions/proj/proj-191 (repeated 2 times)\nPR #5391 updated";
		expect(workerUpdatesOf(message({ text }))).toMatchObject([
			{ state: "done", name: "proj-208", projectId: "proj", sessionId: "proj-208", body: "Investigated the screenshot." },
			{ state: "checkpoint", name: "proj-191", projectId: "proj", sessionId: "proj-191", body: "PR #5391 updated" },
		]);
	});

	it("keeps a malformed session link from breaking the timeline", () => {
		const text = "Reports since your previous turn:\n\n[done] [x](ao://sessions/p/100%)\nok";
		expect(workerUpdatesOf(message({ text }))).toMatchObject([{ projectId: "p", sessionId: "100%" }]);
	});

	it("reads a direct worker message or mid-turn steer without its sender prefix", () => {
		const direct = message({ text: '[from proj-151 "sqlc generation"] Regenerated the queries', senderSessionId: "proj-151", senderProjectId: "proj", senderDisplayName: "sqlc generation" });
		expect(workerUpdatesOf(direct)).toMatchObject([{ name: "sqlc generation", sessionId: "proj-151", body: "Regenerated the queries" }]);
		const steer: ConversationActivity = {
			kind: "activity", id: "s1", sequence: 3, revision: 0, activityKind: "system", status: "completed", summary: "Steer", createdAt,
			detail: { event: "steer", text: "[from proj-286] Loopback works", senderSessionId: "proj-286", senderProjectId: "proj" },
		};
		expect(workerUpdatesOf(steer)).toMatchObject([{ name: "proj-286", sessionId: "proj-286", body: "Loopback works" }]);
	});

	it("reads the sender from the prefix when an older message has no sender metadata", () => {
		expect(workerUpdatesOf(message({ text: "[from proj-155] Session proj-155 finished the rebase" }))).toMatchObject([
			{ name: "proj-155", sessionId: "proj-155", projectId: undefined, body: "Session proj-155 finished the rebase" },
		]);
		expect(workerUpdatesOf(message({ text: '[from worker a1b2c3d4 "Builder"] Please help' }))).toMatchObject([
			{ name: "Builder", sessionId: "a1b2c3d4", body: "Please help" },
		]);
		expect(workerUpdatesOf(message({ origin: "human", text: '[from worker a1b2c3d4 "Builder"] Please help' }))).toEqual([]);
	});

	it("treats AO's task-naming prompt as a worker turn and reads the name it produced", () => {
		const prompt = message({ text: "AO TASK TITLE UPDATE\nChoose a concise task title and run:\nao session rename proj-150 \"<title>\"" });
		expect(isWorkerLead(prompt)).toBe(true);
		expect(isWorkerLead(message({ origin: "human", text: "AO TASK TITLE UPDATE" }))).toBe(false);
		expect(renamedTitle([prompt, command('ao session rename proj-150 "Chat Link Browser"')])).toBe("Chat Link Browser");
		expect(renamedTitle([prompt])).toBeUndefined();
	});

	it("leaves the user's own messages alone, but finds reports folded into them", () => {
		const human = message({ origin: "human", text: `How is it going?\n\n<ao-worker-reports>\n${batch}\n</ao-worker-reports>` });
		expect(workerUpdatesOf(human)).toEqual([]);
		expect(piggybackedUpdates(human).map((update) => update.sessionId)).toEqual(["proj-286", "proj-288"]);
		expect(piggybackedUpdates(message({ origin: "human", text: "How is it going?" }))).toEqual([]);
	});

	it("takes a short first line as the headline, else the first sentence", () => {
		expect(splitHeadline("Fixed the menu\nIt now renders in a portal.")).toEqual({ headline: "Fixed the menu", detail: "It now renders in a portal." });
		expect(splitHeadline("Rebased **#5051**; disk full during validation. Retrying.").headline).toBe("Rebased #5051");
	});

	it("reads spawned workers from what `ao spawn` prints", () => {
		const items: ConversationItem[] = [
			command("ao spawn --prompt 'check regressions'", 'spawned session proj-288 "Check regressions" (working)\n'),
			command("ao spawn --prompt 'fix menus'", 'spawned session proj-285 "Fix menus" (working) [prompt 812 B, system 4096 B]'),
			command("ao status", "spawned session proj-1 (listing, not a spawn)"),
		];
		expect(spawnedSessionIds(items)).toEqual(["proj-288", "proj-285"]);
	});

	it("matches only the completed action for the worker shown by a spawn command", () => {
		const spawned = new Set(spawnedSessionIds([command("ao spawn --prompt audit", 'spawned session proj-288 "Audit" (idle)')]));
		const action: ConversationActivity = {
			kind: "activity", id: "ao-action:session.spawned:proj-288", sequence: 3, revision: 0,
			activityKind: "ao_action", status: "completed", summary: "Spawned an agent", createdAt,
			detail: { action: "session.spawned", operationId: "proj-288", targetSessionId: "proj-288" },
		};
		expect(isDuplicateSpawnAction(action, spawned)).toBe(true);
		expect(isDuplicateSpawnAction({ ...action, detail: { ...action.detail, targetSessionId: "proj-285" } }, spawned)).toBe(false);
		expect(isDuplicateSpawnAction({ ...action, detail: { ...action.detail, operationId: "another-operation" } }, spawned)).toBe(false);
		expect(isDuplicateSpawnAction({ ...action, status: "failed" }, spawned)).toBe(false);
		expect(isDuplicateSpawnAction(command("ao spawn --prompt audit"), spawned)).toBe(false);
		expect(spawnedSessionIds([{ ...command("ao spawn --prompt audit", 'spawned session proj-288 "Audit" (idle)'), status: "failed" }])).toEqual([]);
	});
});

describe("placeBatchedReports", () => {
	const drained = (turnId: string, deliveredWithTurnId: string) => ({ turnId, deliveredWithTurnId });
	const human = (id: string, turnId: string, sequence: number) => message({ id, turnId, sequence, origin: "human", text: "hi" });
	const report = (id: string, turnId: string, sequence: number) => message({ id, turnId, sequence, text: batch });
	const steer = (id: string): ConversationActivity => ({
		kind: "activity", id, sequence: 9, revision: 0, activityKind: "system", status: "completed", summary: "go",
		detail: { event: "steer" }, createdAt, turnId: "running",
	});

	it("puts a drained report under the batch's last human message and hides its turn", () => {
		const placed = placeBatchedReports(
			[drained("t-human", "lead"), drained("t-report", "lead")],
			[report("r0", "lead", 1), human("h1", "t-human", 2), report("r1", "t-report", 3)],
		);
		expect(placed.under.get("h1")?.map((update) => update.key)).toEqual(["r1:0", "r1:1"]);
		expect([...placed.moved]).toEqual(["t-report"]);
	});

	it("puts a steered report under the steer that carried it, not the turn's prompt", () => {
		const placed = placeBatchedReports(
			[{ turnId: "t-report", deliveredWithTurnId: "running", steerActivityId: "steer-1" }],
			[human("h0", "running", 1), steer("steer-1"), report("r1", "t-report", 10)],
		);
		expect([...placed.under.keys()]).toEqual(["steer-1"]);
	});

	it("keeps a steered report on its own line when the steer is not loaded", () => {
		const placed = placeBatchedReports(
			[{ turnId: "t-report", deliveredWithTurnId: "running", steerActivityId: "steer-1" }],
			[human("h0", "running", 1), report("r1", "t-report", 10)],
		);
		expect(placed.under.size).toBe(0);
		expect(placed.moved.size).toBe(0);
	});

	it("leaves a report batched with no human message on its own line", () => {
		const placed = placeBatchedReports([drained("t-report", "lead")], [report("r0", "lead", 1), report("r1", "t-report", 2)]);
		expect(placed.moved.size).toBe(0);
	});
});
