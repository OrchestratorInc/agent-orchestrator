import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { appendCloudEvents, toSnapshot } from "../components/chat/CloudSessionChatSurface";
import type { CloudCpClient, CloudCpClientEvent } from "../lib/cloud-cp";
import type { ConversationSnapshot } from "../types/conversation";
import type { AgentProvider, WorkspaceSession } from "../types/workspace";
import { useCloudCp } from "./useCloudCp";

export function cloudReviewerConversationQueryKey(orgId: string, sessionId: string, reviewRunId: string) {
	return ["cloud-reviewer-conversation", orgId, sessionId, reviewRunId] as const;
}

/**
 * A review conversation's events as the session chat's own: review.* events
 * become chat.* ones and their reviewTurnId the turnId, so the Cloud Chat
 * projection reads them unchanged.
 */
export function asChatEvents(events: readonly CloudCpClientEvent[]): CloudCpClientEvent[] {
	return events.flatMap((event) => {
		if (!event.type.startsWith("review.")) return [];
		const payload = event.payload && typeof event.payload === "object" ? event.payload as Record<string, unknown> : {};
		const { reviewTurnId, ...rest } = payload;
		return [{ ...event, type: `chat.${event.type.slice("review.".length)}`, payload: { ...rest, turnId: reviewTurnId } }];
	});
}

export async function loadReviewChatEvents(
	client: Pick<CloudCpClient, "listReviewChatEvents">,
	orgId: string,
	sessionId: string,
	reviewRunId: string,
	existing: CloudCpClientEvent[],
): Promise<CloudCpClientEvent[]> {
	let events = existing;
	let after = existing.at(-1)?.sequence ?? 0;
	for (;;) {
		const page = await client.listReviewChatEvents(orgId, sessionId, reviewRunId, { after, limit: 500 });
		events = appendCloudEvents(events, page.events);
		if (!page.hasMore) return events;
		if (page.nextAfter <= after) throw new Error("Review event cursor did not advance.");
		after = page.nextAfter;
	}
}

/**
 * One Cloud review that runs in Chat: its conversation, as a snapshot the
 * chat view renders, and the person's messages and stop. The reviewer runs
 * with its own agent (`harness`), beside the session's.
 */
export function useCloudReviewerConversation(session: WorkspaceSession, reviewRunId: string, harness: string) {
	const { client, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const orgId = session.cloud?.orgId ?? "";
	const queryKey = cloudReviewerConversationQueryKey(orgId, session.id, reviewRunId);
	const events = useQuery({
		queryKey,
		enabled: Boolean(orgId && ready && reviewRunId),
		refetchInterval: 1_000,
		queryFn: () => loadReviewChatEvents(client, orgId, session.id, reviewRunId,
			queryClient.getQueryData<CloudCpClientEvent[]>(queryKey) ?? []),
	});
	const snapshot = useMemo<ConversationSnapshot | undefined>(() => {
		if (!events.data) return undefined;
		const projected = toSnapshot({ ...session, provider: harness as AgentProvider }, asChatEvents(events.data));
		return { ...projected, conversationId: `cloud-review:${reviewRunId}` };
	}, [events.data, harness, reviewRunId, session]);
	const invalidate = () => queryClient.invalidateQueries({ queryKey });
	const send = useMutation({
		mutationFn: ({ text, clientMessageId }: { text: string; clientMessageId?: string }) =>
			client.sendReviewChatMessage(orgId, session.id, reviewRunId, text, { idempotencyKey: clientMessageId }),
		onSuccess: () => void invalidate(),
	});
	const interrupt = useMutation({
		mutationFn: () => client.interruptReviewChat(orgId, session.id, reviewRunId),
		onSettled: () => void invalidate(),
	});
	const error = [send.error, interrupt.error].find((value): value is Error => value instanceof Error);
	return {
		snapshot,
		isLoading: events.isLoading,
		loadError: events.error instanceof Error ? events.error.message : undefined,
		send: (text: string, clientMessageId?: string) => send.mutateAsync({ text, clientMessageId }),
		interrupt: () => interrupt.mutate(),
		busy: send.isPending || interrupt.isPending,
		error: error?.message,
	};
}
