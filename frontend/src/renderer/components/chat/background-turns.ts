import { isUserQueuePrompt, type ConversationItem } from "../../types/conversation";
import { readAutomation } from "./AutomationMessage";
import { finalReply, isTaskTitleUpdate, isWorkerLead, needsYouNow, relayedVia, renamedTitle, workerUpdatesOf, type WorkerUpdateIndex } from "./WorkerUpdates";

export type TurnStanding = "yours" | "replaced" | "failed" | "up" | "background";

const isYours = (item: ConversationItem) => isUserQueuePrompt(item) || relayedVia(item) !== null;

export function turnStanding(items: readonly ConversationItem[], state: string, index: WorkerUpdateIndex | null): TurnStanding {
	const lead = items[0];
	if (!lead || items.some(isYours)) return "yours";
	const automation = lead.kind === "message" && lead.role === "user" ? readAutomation(lead.text) : null;
	if (automation?.kind === "notice" && automation.notice === "orchestratorReplaced") return "replaced";
	if (!isWorkerLead(lead) || (isTaskTitleUpdate(lead) && !renamedTitle(items))) return "yours";
	if (state !== "completed" && state !== "recovered") return "failed";
	if (items.some((item) => workerUpdatesOf(item).some((update) => needsYouNow(index, update)))) return "up";
	return /\?[*_]*$/.test(finalReply(items)) ? "up" : "background";
}
