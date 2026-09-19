import type { CloudClient } from "@aoagents/cloud-client";
import type { DashboardSession, ProjectInfo } from "../api";
import type { SessionSource } from "../environment/types";
import { toDashboardSession, toProjectInfo } from "./mapping";

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
		getConversationPage: async () => {
			throw new Error("Cloud conversation pages arrive in Task 12.");
		},
		sendMessage: async () => {
			throw new Error("Cloud message sending arrives in Task 13.");
		},
		cancelTurn: async () => {
			throw new Error("Cloud turn cancellation arrives in Task 13.");
		},
		subscribeEvents: () => {
			throw new Error("Cloud event subscription arrives in Task 12.");
		},
	};
}
