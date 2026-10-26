import type { ConversationItem, ConversationSnapshot } from "../../src/renderer/types/conversation";

/** Deterministic synthetic workload, not a substitute for the five sandbox chats. */
export function largeTurnFixture(toolRows = 1850): ConversationSnapshot {
	const turnId = "large-turn";
	const at = "2026-10-26T00:00:00Z";
	const items: ConversationItem[] = [{
		kind: "message", id: "large-prompt", turnId, sequence: 1, revision: 0,
		role: "user", origin: "human", text: "Inspect the synthetic large turn.",
		streaming: false, createdAt: at,
	}];
	for (let index = 0; index < toolRows; index++) {
		items.push({
			kind: "activity", id: `large-tool-${index}`, turnId, sequence: items.length + 1, revision: 0,
			activityKind: "command", status: "completed", summary: `Read synthetic file ${index}`,
			detail: { command: `cat synthetic-${index}.ts`, output: `export const value${index} = ${index};\n`.repeat(20), exitCode: 0 },
			createdAt: at,
		});
		// Interleaved prose prevents the workload from being just one ActivityRun.
		if (index % 25 === 24) items.push({
			kind: "message", id: `large-note-${index}`, turnId, sequence: items.length + 1, revision: 0,
			role: "assistant", origin: "provider", text: `Inspected batch ${index}.\n\n\`\`\`typescript\nconst batch = ${index};\n\`\`\``,
			streaming: false, createdAt: at,
		});
	}
	items.push({
		kind: "message", id: "large-final", turnId, sequence: items.length + 1, revision: 0,
		role: "assistant", origin: "provider", text: "Synthetic large turn complete.", streaming: false, createdAt: at,
	});
	return {
		conversationId: "large-conversation", sessionId: "large-session", harness: "codex", mode: "chat",
		controller: { state: "ready" }, items, latestSequence: items.length, oldestSequence: 1,
		hasMoreBefore: false, settings: {},
		turns: [{ id: turnId, state: "completed", requestedAt: at, startedAt: at, completedAt: "2026-10-26T00:01:00Z" }],
	};
}
