import type { TriageRun } from "../../shared/triage";
import { toActivity } from "../hooks/useConversation";
import type { ConversationActivity, ConversationItem, ConversationSnapshot } from "../types/conversation";
import { isTriageActive } from "./triage";

export function triageConversationSnapshot(run: TriageRun): ConversationSnapshot {
	const identity = `triage:${run.id}`;
	const items: ConversationItem[] = run.messages.map((message, index) => message.role === "system" ? {
		kind: "activity", id: message.id, sequence: index + 1, revision: 1, activityKind: "system", status: "completed", summary: message.text, createdAt: message.at,
	} : ({
		kind: "message", id: message.id, sequence: index + 1, revision: 1,
		role: message.role === "user" ? "user" : "assistant", origin: message.role === "user" ? "human" : "provider",
		text: message.text, streaming: false, createdAt: message.at,
	}));
	if (run.activity) items.push({ kind: "activity", id: `${identity}:activity`, sequence: items.length + 1, revision: 1,
		activityKind: "system", status: isTriageActive(run) ? "running" : "completed", summary: run.activity, createdAt: run.updatedAt });
	return { conversationId: identity, sessionId: identity, harness: run.request.harness, title: "Report a problem", mode: "chat",
		controller: { state: isTriageActive(run) ? "busy" : "ready" }, turns: [], items,
		latestSequence: items.at(-1)?.sequence ?? 0, oldestSequence: items[0]?.sequence ?? 0, hasMoreBefore: false,
		settings: { model: run.request.model, reasoningEffort: run.request.effort }, capabilities: [] };
}

export function triagePendingActivity(run: TriageRun): ConversationActivity | undefined {
	const input = run.pendingInput;
	if (!input) return undefined;
	return toActivity({
		kind: "activity", id: input.id, activityKind: input.kind, status: "pending", summary: input.summary,
		requestId: input.id, detail: input.detail, createdAt: run.updatedAt, sequence: run.messages.length + 1, revision: 1,
	});
}
