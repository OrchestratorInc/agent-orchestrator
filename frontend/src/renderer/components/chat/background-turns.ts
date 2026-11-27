import { isSteer, isUserQueuePrompt, type ConversationItem } from "../../types/conversation";
import { automationOf } from "./AutomationMessage";
import { finalReply, isTaskTitleUpdate, isWorkerLead, needsYouNow, relayedVia, renamedTitle, workerUpdatesOf, type WorkerUpdate, type WorkerUpdateIndex } from "./WorkerUpdates";

export type TurnStanding = "yours" | "replaced" | "failed" | "up" | "background";

type Outcome = { state: string; usageLimit?: unknown; failureCode?: string };

export const accountFailure = ({ state, usageLimit, failureCode }: Outcome) => state === "failed" && Boolean(usageLimit || failureCode === "auth_required");

export const turnUpdates = (items: readonly ConversationItem[], carried: ReadonlyMap<string, readonly WorkerUpdate[]>) =>
	items.flatMap((item) => [...workerUpdatesOf(item), ...(carried.get(item.id) ?? [])]);

const isYours = (item: ConversationItem) =>
	isUserQueuePrompt(item) || relayedVia(item) !== null ||
	(item.kind === "activity" && ((isSteer(item) && !item.detail?.senderSessionId) || item.activityKind === "approval" || item.activityKind === "user_input"));

export function turnStanding(items: readonly ConversationItem[], outcome: Outcome, index: WorkerUpdateIndex | null, carried: ReadonlyMap<string, readonly WorkerUpdate[]>): TurnStanding {
	const { state } = outcome;
	const lead = items[0];
	if (!lead || items.some(isYours)) return "yours";
	const automation = lead.kind === "message" && lead.role === "user" ? automationOf(lead) : null;
	const settled = state === "completed" || state === "recovered";
	if (automation?.kind === "notice" && automation.notice === "orchestratorReplaced") return settled ? "replaced" : "up";
	if (lead.kind !== "message" || !isWorkerLead(lead) || (isTaskTitleUpdate(lead) && !renamedTitle(items))) return "yours";
	if (items.some((item) => item !== lead && item.kind === "message" && item.role === "user" && !isWorkerLead(item))) return "up";
	if (turnUpdates(items, carried).some((update) => needsYouNow(index, update))) return "up";
	const asks = /[?？][*_)\]"'”’`」』）】》〉»›]*$/.test(finalReply(items));
	if (asks) return "up";
	if (state === "failed") return accountFailure(outcome) ? "background" : "failed";
	return settled ? "background" : "up";
}
