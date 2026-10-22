import { describe, expect, it } from "vitest";
import type { ConversationActivity, ConversationItem, ConversationMessage } from "../../types/conversation";
import {
	isQuietWorkerTurn,
	piggybackedUpdates,
	renamedTitle,
	spawnedSessionIds,
	splitHeadline,
	splitOrchestratorReply,
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

	it("reads a direct worker message without its sender prefix", () => {
		const direct = message({ text: '[from proj-151 "sqlc generation"] Regenerated the queries', senderSessionId: "proj-151", senderProjectId: "proj", senderDisplayName: "sqlc generation" });
		expect(workerUpdatesOf(direct)).toMatchObject([{ name: "sqlc generation", sessionId: "proj-151", body: "Regenerated the queries" }]);
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

	it("keeps only the question as the orchestrator's reply", () => {
		expect(splitOrchestratorReply("Merged #7054.\n\nShould I start a fix for #7043?\n\nIt traces to #6652.")).toEqual({
			actions: ["Merged #7054."],
			question: "Should I start a fix for #7043?\n\nIt traces to #6652.",
		});
		expect(splitOrchestratorReply("Merged #7054.").question).toBe("");
	});

	it("reads task titles and spawned workers from the orchestrator's commands", () => {
		const items: ConversationItem[] = [
			command("ao session rename proj-290 \"Triage recent merges\""),
			command("ao spawn --prompt 'check regressions'", "Spawned session proj-288\n"),
			command("ao spawn --prompt 'fix menus'", "Spawned proj-285"),
		];
		expect(renamedTitle(items)).toBe("Triage recent merges");
		expect(spawnedSessionIds(items)).toEqual(["proj-288", "proj-285"]);
	});

	it("treats a settled worker turn without a question as quiet", () => {
		const reply = (text: string) => message({ id: "a1", role: "assistant", origin: "provider", text });
		expect(isQuietWorkerTurn([message({ text: batch }), reply("Noted.")])).toBe(true);
		expect(isQuietWorkerTurn([message({ text: batch }), reply("Tunnel or loopback?")])).toBe(false);
		expect(isQuietWorkerTurn([message({ origin: "human", text: "Hi" }), reply("Noted.")])).toBe(false);
	});
});
