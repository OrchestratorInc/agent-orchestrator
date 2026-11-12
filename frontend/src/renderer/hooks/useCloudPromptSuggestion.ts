import { CloudCpError } from "../lib/cloud-cp/errors";
import { useCloudCp } from "./useCloudCp";
import { promptSuggestionBusy, usePromptSuggestion } from "./useConversationPromptSuggestion";
import type { ConversationSnapshot } from "../types/conversation";
import type { WorkspaceSession } from "../types/workspace";

// The transcript a suggestion reads: the daemon's own limit.
const SUGGESTION_MESSAGE_LIMIT = 8;

/** The conversation's most recent text messages, oldest first. */
export function promptSuggestionMessages(snapshot: ConversationSnapshot): { role: "user" | "assistant"; text: string }[] {
	const messages: { role: "user" | "assistant"; text: string }[] = [];
	for (let index = snapshot.items.length - 1; index >= 0 && messages.length < SUGGESTION_MESSAGE_LIMIT; index--) {
		const item = snapshot.items[index];
		if (item.kind !== "message" || (item.role !== "user" && item.role !== "assistant") || !item.text.trim()) continue;
		messages.push({ role: item.role, text: item.text });
	}
	return messages.reverse();
}

/**
 * A Cloud Chat's next-message suggestion after a settled turn, as a local
 * Chat offers one: the session's agent reads the recent transcript once in
 * its sandbox. A busy worker is asked again shortly; a control plane or
 * worker that cannot suggest answers with an error, and nothing is shown.
 */
export function useCloudPromptSuggestion(
	session: WorkspaceSession,
	snapshot: ConversationSnapshot | undefined,
	enabled: boolean,
): { id: string; text: string; onDismiss: () => void } | undefined {
	const { client, ready, baseUrl } = useCloudCp();
	const orgId = session.cloud?.orgId;
	return usePromptSuggestion(
		snapshot,
		enabled && ready && Boolean(orgId) && session.runtimeConnected === true && session.isTerminated !== true,
		`cloud:${baseUrl}:${orgId ?? ""}`,
		async ({ sessionId, turnId, snapshot: current, signal }) => {
			try {
				const answer = await client.suggestChatPrompt(orgId!, sessionId, { turnId, messages: promptSuggestionMessages(current) }, { signal });
				return answer.turnId === turnId ? answer.prompt : "";
			} catch (error) {
				// A worker busy with a turn or another suggestion is asked again
				// shortly, as the local daemon's busy answer is.
				if (error instanceof CloudCpError && error.code === "CHAT_PROMPT_SUGGESTION_BUSY") throw promptSuggestionBusy;
				throw error;
			}
		},
	);
}
