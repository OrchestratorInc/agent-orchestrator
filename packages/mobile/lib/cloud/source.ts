import type { CloudClient } from "@aoagents/cloud-client";
import type { DashboardSession, ProjectInfo } from "../api";
import type { ConversationEvent } from "../chat/sse";
import type { SessionSource } from "../environment/types";
import { fetchConversationReplay, pollCloudEvents, toConversationItems } from "./events";
import { toDashboardSession, toProjectInfo } from "./mapping";

/** How often the cloud transcript is polled for new events. Matches the
 * tunnel-path conversation poll interval mobile already uses elsewhere. */
const CLOUD_EVENT_POLL_MS = 2_000;

/** Page through a cursor-paginated cloud list until it ends. */
async function collect<T>(
	fetchPage: (cursor?: string) => Promise<{ items: T[]; page: { hasMore?: boolean; nextCursor?: string } }>,
): Promise<T[]> {
	const all: T[] = [];
	let cursor: string | undefined;
	for (;;) {
		const page = await fetchPage(cursor);
		all.push(...page.items);
		if (page.page?.hasMore !== true || !page.page.nextCursor) return all;
		cursor = page.page.nextCursor;
	}
}

export function createCloudSessionSource(input: {
	client: CloudClient;
	orgId: string;
}): SessionSource {
	const { client, orgId } = input;
	return {
		kind: "cloud",
		listProjects: async (): Promise<ProjectInfo[]> =>
			(await collect((cursor) => client.listProjects(orgId, { cursor }))).map(toProjectInfo),
		listSessions: async (): Promise<DashboardSession[]> =>
			(await collect((cursor) => client.listSessions(orgId, { cursor }))).map(toDashboardSession),
		createSession: async (options) => {
			if (!options.projectId) throw new Error("Pick a project first");
			const response = await client.createSession(
				orgId,
				{
					projectId: options.projectId,
					kind: "worker",
					harness: options.harness ?? "claude-code",
					displayName: (options.prompt ?? "New session").slice(0, 80),
					prompt: options.prompt ?? "",
					// Cloud's `mode` is a trust level, distinct from mobile's chat/tui
					// controller mode; a spawned session starts fully trusted, matching
					// desktop's default for worker sessions.
					mode: "trusted",
					deniedCommands: [],
				},
				// Idempotent: a retry on a flaky phone network must not spawn twice.
				{ idempotencyKey: `spawn-${Date.now()}-${Math.random().toString(36).slice(2)}` },
			);
			return { id: response.session.id };
		},
		deleteSession: async (id) => { await client.deleteSession(orgId, id); },
		getConversationPage: async (id) => {
			const [{ session }, { events, latestSequence }] = await Promise.all([
				client.getSession(orgId, id),
				// A single replayEvents call is capped at the server's page limit
				// and reports hasMore; paging to the end here is required so this
				// shows the live tail of a long conversation, not just its start.
				fetchConversationReplay(client, orgId, id),
			]);
			return {
				conversationId: id,
				sessionId: id,
				harness: session.harness,
				// Cloud's `mode` is a trust level, distinct from mobile's chat/tui
				// controller mode; cloud sessions are always Chat (see mapping.ts).
				mode: "chat" as const,
				controller: { state: "ready" as const },
				latestSequence,
				oldestSequence: events.length > 0 ? events[0].sequence : latestSequence,
				// No backward pagination yet: a full forward replay is the whole
				// transcript the control plane will hand back today.
				hasMoreBefore: false,
				turns: [],
				items: toConversationItems(events),
				settings: {},
			};
		},
		sendMessage: async (id, input) => {
			// The composer's clientMessageId is already a per-message unique id,
			// so it doubles as the idempotency key: a retry after a dropped
			// response cannot post the message twice.
			await client.sendMessage(orgId, id, input.text, { idempotencyKey: input.clientMessageId });
			// The wire response is `{ event: UserMessageEvent }`, not a turn — the
			// control plane does not hand back a turn id for the message just
			// posted. Reporting a turnId here would be a guess the UI could
			// mistake for a real turn (e.g. treating it as cancellable), so it is
			// left undefined; nothing in mobile reads the immediate sendMessage
			// return value today (useConversation's `deliver` discards it and
			// relies on the polled transcript instead), so this is safe.
			return { duplicate: false };
		},
		cancelTurn: async (id, turnId) => {
			// Keyed by the turn id, not freshly generated per call, so a retried
			// cancel of the same turn is idempotent instead of firing a second
			// distinct request.
			await client.cancelTurn(orgId, id, turnId, { idempotencyKey: `cancel-${turnId}` });
		},
		subscribeEvents: (id, listener) => {
			const controller = new AbortController();
			void pollCloudEvents({
				client,
				orgId,
				sessionId: id,
				after: 0,
				signal: controller.signal,
				intervalMs: CLOUD_EVENT_POLL_MS,
				onEvents: (events) => {
					for (const event of events) {
						const conversationEvent: ConversationEvent = {
							seq: event.sequence,
							// Cloud has no project-scoped stream the way the daemon's global
							// CDC feed does; nothing downstream reads this field yet.
							projectId: "",
							sessionId: event.sessionId,
							type: event.type,
							payload: event.payload,
							createdAt: event.createdAt,
						};
						listener(conversationEvent);
					}
				},
			});
			return () => controller.abort();
		},
	};
}
