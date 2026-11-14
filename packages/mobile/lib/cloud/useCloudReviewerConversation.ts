import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ConversationSnapshot } from "../chat/types";
import type { CloudClient } from "./client";
import { appendCloudEvents, cloudConversationSnapshot } from "./conversation";
import { cloudErrorMessage } from "./errors";
import type { CloudClientEvent, CloudSession } from "./types";

// A Cloud review that runs in Chat, as the desktop shows it
// (useCloudReviewerConversation): the review's own events, polled, read by
// the session chat's projection, and the person's follow-ups and stop. Its
// approvals are declined in the sandbox, so it never asks.

const POLL_MS = 1_500;

/**
 * A review conversation's events as the session chat's: review.* events
 * become chat.* ones and their reviewTurnId the turnId, so the Cloud chat
 * projection reads them unchanged.
 */
export function reviewEventsAsChat(events: readonly CloudClientEvent[]): CloudClientEvent[] {
	return events.flatMap((event) => {
		if (!event.type.startsWith("review.")) return [];
		const payload = event.payload && typeof event.payload === "object" ? event.payload as Record<string, unknown> : {};
		const { reviewTurnId, ...rest } = payload;
		return [{ ...event, type: `chat.${event.type.slice("review.".length)}`, payload: { ...rest, turnId: reviewTurnId } }];
	});
}

/** Everything after what is held, page by page. */
export async function loadReviewChatEvents(client: Pick<CloudClient, "listReviewChatEvents">, orgId: string, sessionId: string, reviewRunId: string, existing: readonly CloudClientEvent[], signal?: AbortSignal): Promise<CloudClientEvent[]> {
	let events = [...existing];
	let after = existing.at(-1)?.sequence ?? 0;
	for (;;) {
		const page = await client.listReviewChatEvents(orgId, sessionId, reviewRunId, { after, limit: 500, signal });
		events = appendCloudEvents(events, page.events);
		if (!page.hasMore) return events;
		if (page.nextAfter <= after) throw new Error("Review event cursor did not advance.");
		after = page.nextAfter;
	}
}

export type CloudReviewerConversation = {
	snapshot?: ConversationSnapshot;
	loading: boolean;
	error?: string;
	/** Sends a follow-up; resolves with its id once the control plane has it. */
	send(text: string): Promise<string>;
	interrupt(): Promise<void>;
	refresh(): Promise<void>;
};

export function useCloudReviewerConversation(input: {
	client: CloudClient;
	orgId: string | undefined;
	/** The worker the review belongs to: its events and messages are under it. */
	session: CloudSession | undefined;
	reviewRunId: string;
	/** The reviewer's own agent, beside the worker's. */
	harness?: string;
	active: boolean;
	newKey(): string;
}): CloudReviewerConversation {
	const { client, orgId, session, reviewRunId, harness, active, newKey } = input;
	const sessionId = session?.id;
	const [events, setEvents] = useState<CloudClientEvent[]>();
	const [error, setError] = useState<string>();
	const held = useRef<CloudClientEvent[]>([]);
	const loading = useRef(false);

	const refresh = useCallback(async () => {
		if (!orgId || !sessionId || !reviewRunId || loading.current) return;
		loading.current = true;
		try {
			const next = await loadReviewChatEvents(client, orgId, sessionId, reviewRunId, held.current);
			held.current = next;
			setEvents(next);
			setError(undefined);
		} catch (cause) {
			setError(cloudErrorMessage(cause));
			setEvents((current) => current ?? held.current);
		} finally {
			loading.current = false;
		}
	}, [client, orgId, reviewRunId, sessionId]);

	// A different review starts from nothing.
	useEffect(() => {
		held.current = [];
		setEvents(undefined);
		setError(undefined);
	}, [orgId, reviewRunId, sessionId]);

	useEffect(() => {
		if (!active) return;
		void refresh();
		const timer = setInterval(() => void refresh(), POLL_MS);
		return () => clearInterval(timer);
	}, [active, refresh]);

	const snapshot = useMemo(() => {
		if (!events || !session) return undefined;
		const projected = cloudConversationSnapshot({ ...session, harness: harness || session.harness }, reviewEventsAsChat(events));
		return { ...projected, conversationId: `cloud-review:${reviewRunId}` };
	}, [events, harness, reviewRunId, session]);

	const send = useCallback(async (text: string) => {
		if (!orgId || !sessionId) throw new Error("Sign in to Orchestrator Cloud first.");
		const key = newKey();
		try {
			await client.sendReviewChatMessage(orgId, sessionId, reviewRunId, text, key);
		} catch (cause) {
			throw new Error(cloudErrorMessage(cause), { cause });
		}
		void refresh();
		return key;
	}, [client, newKey, orgId, refresh, reviewRunId, sessionId]);

	const interrupt = useCallback(async () => {
		if (!orgId || !sessionId) return;
		try {
			await client.interruptReviewChat(orgId, sessionId, reviewRunId);
		} catch (cause) {
			throw new Error(cloudErrorMessage(cause), { cause });
		}
		void refresh();
	}, [client, orgId, refresh, reviewRunId, sessionId]);

	return { snapshot, loading: events === undefined && !error, error, send, interrupt, refresh };
}
