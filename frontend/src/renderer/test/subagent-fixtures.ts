/**
 * Subagent activities in the shape Claude's ACP adapter records them: the launch is
 * filed as reasoning with an async result note, and every child step points back at
 * the launch's provider id.
 */

import type { ConversationActivity } from "../types/conversation";

export const subagentAt = (seconds: number) => new Date(Date.UTC(2026, 9, 24, 10, 0, seconds)).toISOString();

export function subagentLaunch(
	id: string,
	description: string,
	status: ConversationActivity["status"] = "completed",
	prompt = `Read-only. ${description}.`,
): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: 1,
		revision: 0,
		activityKind: "reasoning",
		status,
		summary: description,
		detail: {
			providerToolName: "Agent",
			input: { description, prompt, subagent_type: "Explore" },
			output: status === "failed"
				? "Concurrent subagent limit reached. You can run 20 subagents at once."
				: JSON.stringify([{ text: "Async agent launched successfully.\nagentId: a82259 (internal ID - do not mention to user.)", type: "text" }]),
		} as ConversationActivity["detail"],
		createdAt: subagentAt(0),
	};
}

export function subagentCommand(
	id: string,
	parent: string,
	text: string,
	status: ConversationActivity["status"] = "completed",
): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: 2,
		revision: 0,
		activityKind: "command",
		status,
		summary: text,
		detail: { parentProviderItemId: parent, command: text },
		createdAt: subagentAt(3),
	};
}

/** Something the child wrote while it worked. */
export function subagentMessage(id: string, parent: string, text: string): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: 2,
		revision: 0,
		activityKind: "mcp_tool",
		status: "completed",
		summary: "Subagent response",
		detail: { parentProviderItemId: parent, nestedAgent: true, progress: text },
		createdAt: subagentAt(5),
	};
}

export function subagentHandback(id: string, parent: string, message: string): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: 3,
		revision: 0,
		activityKind: "mcp_tool",
		status: "completed",
		summary: "SubagentHandback",
		detail: { parentProviderItemId: parent, providerToolName: "SubagentHandback", input: { message } } as ConversationActivity["detail"],
		createdAt: subagentAt(12),
	};
}
