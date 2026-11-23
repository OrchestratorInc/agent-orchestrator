import { isSteer, isUserQueuePrompt, type ConversationItem } from "../../types/conversation";
import { automationOf } from "./AutomationMessage";
import { asksQuestion } from "./ChatTimelineItems";
import { finalReply, isTaskTitleUpdate, isWorkerLead, needsYouNow, relayedVia, renamedTitle, workerUpdatesOf, type WorkerUpdate, type WorkerUpdateIndex } from "./WorkerUpdates";

export type TurnStanding = "yours" | "replaced" | "failed" | "up" | "background";

const isYours = (item: ConversationItem) =>
	isUserQueuePrompt(item) || relayedVia(item) !== null ||
	(item.kind === "activity" && ((isSteer(item) && !item.detail?.senderSessionId) || item.activityKind === "approval" || item.activityKind === "user_input"));

export function turnStanding(items: readonly ConversationItem[], state: string, index: WorkerUpdateIndex | null, carried: ReadonlyMap<string, readonly WorkerUpdate[]>): TurnStanding {
	const lead = items[0];
	if (!lead || items.some(isYours)) return "yours";
	const automation = lead.kind === "message" && lead.role === "user" ? automationOf(lead) : null;
	const settled = state === "completed" || state === "recovered";
	if (automation?.kind === "notice" && automation.notice === "orchestratorReplaced") return settled ? "replaced" : "up";
	if (lead.kind !== "message" || !isWorkerLead(lead) || (isTaskTitleUpdate(lead) && !renamedTitle(items))) return "yours";
	if (items.some((item) => [...workerUpdatesOf(item), ...(carried.get(item.id) ?? [])].some((update) => needsYouNow(index, update)))) return "up";
	if (state === "failed") return "failed";
	if (!settled) return "up";
	return asksQuestion(finalReply(items)) ? "up" : "background";
}
