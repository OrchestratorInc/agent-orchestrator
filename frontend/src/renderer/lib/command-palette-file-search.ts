import type { CloudCpClient } from "./cloud-cp";
import { apiClient, apiErrorMessage } from "./api-client";
import { clientForSessionHost } from "./host-clients";
import { cloudWorkspaceReviewSearchQueryOptions } from "../hooks/useCloudWorkspaceReview";
import { sessionWorkspaceSearchQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import type { WorkspaceFileSearchItem, WorkspaceFileSearchTarget } from "./command-palette";

export const COMMAND_PALETTE_FILE_SEARCH_LIMIT = 20;

export type CommandPaletteFileSearchResponse = {
	results: WorkspaceFileSearchItem[];
	truncated: boolean;
};

type CommandPaletteFileSearchArgs = {
	target: WorkspaceFileSearchTarget;
	query: string;
	errorMessage: string;
	cloud: {
		client: CloudCpClient;
		baseUrl: string;
		ready: boolean;
	};
};

type CommandPaletteFileSearchQueryOptions = {
	queryKey: readonly unknown[];
	queryFn: (context?: { signal?: AbortSignal }) => Promise<CommandPaletteFileSearchResponse>;
	retry?: (failureCount: number, error: Error) => boolean;
};

function normalizeSearchResponse(response: {
	results: Array<{ path: string; status?: string; binary?: boolean }>;
	truncated: boolean;
}): CommandPaletteFileSearchResponse {
	return {
		results: response.results.map((result) => ({
			path: result.path,
			...(result.status ? { status: result.status } : {}),
			...(result.binary !== undefined ? { binary: result.binary } : {}),
		})),
		truncated: response.truncated,
	};
}

export function commandPaletteFileSearchAvailable(
	target: WorkspaceFileSearchTarget | undefined,
	cloudReady: boolean,
): boolean {
	return Boolean(target && (!target.cloudOrgId || cloudReady));
}

export function commandPaletteFileSearchQueryOptions(
	args: CommandPaletteFileSearchArgs,
): CommandPaletteFileSearchQueryOptions {
	const { target } = args;
	if (target.cloudOrgId) {
		const options = cloudWorkspaceReviewSearchQueryOptions({
			client: args.cloud.client,
			baseUrl: args.cloud.baseUrl,
			orgId: target.cloudOrgId,
			sessionId: target.sessionId,
			query: { query: args.query, limit: COMMAND_PALETTE_FILE_SEARCH_LIMIT },
		});
		return {
			...options,
			queryFn: async (context: { signal?: AbortSignal } = {}) =>
				normalizeSearchResponse(await options.queryFn(context)),
		};
	}

	const options = sessionWorkspaceSearchQueryOptions(
		target.sessionId,
		args.query,
		args.errorMessage,
		target.hostId,
		COMMAND_PALETTE_FILE_SEARCH_LIMIT,
	);
	return {
		...options,
		queryFn: async (context: { signal?: AbortSignal } = {}) =>
			normalizeSearchResponse(await options.queryFn(context)),
	};
}

// A match inside a hit's text is wrapped in these, as the daemon's search does.
const MATCH_START = "\u0002";
const MATCH_END = "\u0003";

/** text with each case-insensitive occurrence of query wrapped in the match markers. */
export function markSearchMatches(text: string, query: string): string {
	const needle = query.trim().toLowerCase();
	if (!needle) return text;
	const lower = text.toLowerCase();
	// Lower-casing can change a length; such text is shown unmarked.
	if (lower.length !== text.length) return text;
	let marked = "";
	let from = 0;
	for (let at = lower.indexOf(needle); at >= 0; at = lower.indexOf(needle, at + needle.length)) {
		marked += text.slice(from, at) + MATCH_START + text.slice(at, at + needle.length) + MATCH_END;
		from = at + needle.length;
	}
	return marked + text.slice(from);
}

/**
 * Lines inside a session's files: the daemon's for a local session, the
 * worker's for a Cloud one (a worker that predates line matches lists none,
 * and its files still show among the file-name hits).
 */
export function commandPaletteContentSearchQueryOptions(
	target: WorkspaceFileSearchTarget,
	query: string,
	errorMessage: string,
	cloud?: { client: CloudCpClient; baseUrl: string },
) {
	return {
		queryKey: ["palette-content-search", target.cloudOrgId ? `cloud:${cloud?.baseUrl ?? ""}:${target.cloudOrgId}` : target.hostId ?? "", target.sessionId, query] as const,
		queryFn: async ({ signal }: { signal?: AbortSignal } = {}): Promise<{ results: Array<{ path: string; line: number; text: string }> }> => {
			if (target.cloudOrgId) {
				if (!cloud) throw new Error(errorMessage);
				const response = await cloud.client.searchWorkspaceReview(target.cloudOrgId, target.sessionId,
					{ query, limit: COMMAND_PALETTE_FILE_SEARCH_LIMIT, mode: "content" }, { signal });
				return {
					results: response.results.flatMap((result) =>
						(result.matches ?? []).map((match) => ({ path: result.path, line: match.line, text: markSearchMatches(match.text, query) }))),
				};
			}
			const { data, error } = await clientForSessionHost(target.hostId).GET("/api/v1/sessions/{sessionId}/workspace/search/content", {
				params: { path: { sessionId: target.sessionId }, query: { query } },
				signal,
			});
			if (error) throw new Error(apiErrorMessage(error, errorMessage));
			if (!data) throw new Error(errorMessage);
			return data;
		},
	};
}

/**
 * Messages across the chats of the signed-in Cloud organizations, in the
 * daemon's hit shape. A prompt is named by the id Cloud Chat gives it, so the
 * chat scrolls to it; a reply opens its session.
 */
export function commandPaletteCloudChatSearchQueryOptions(
	cloud: { client: CloudCpClient; baseUrl: string },
	orgIds: readonly string[],
	query: string,
) {
	return {
		queryKey: ["palette-cloud-chat-search", cloud.baseUrl, [...orgIds].sort().join(","), query] as const,
		queryFn: async ({ signal }: { signal?: AbortSignal } = {}) => {
			const answers = await Promise.allSettled(orgIds.map((orgId) => cloud.client.searchChats(orgId, { query, limit: 20 }, { signal })));
			return {
				results: answers.flatMap((answer) => answer.status === "fulfilled" ? answer.value.results : []).map((hit) => ({
					messageId: hit.role === "user" ? `cloud-event-${hit.sequence}` : `cloud-search-${hit.sessionId}-${hit.sequence}`,
					projectId: hit.projectId,
					sessionId: hit.sessionId,
					text: markSearchMatches(hit.snippet, query),
					title: hit.sessionTitle,
				})),
			};
		},
	};
}

/** Messages across this machine's chats. */
export function commandPaletteChatSearchQueryOptions(query: string, errorMessage: string) {
	return {
		queryKey: ["palette-chat-search", query] as const,
		queryFn: async ({ signal }: { signal?: AbortSignal } = {}) => {
			const { data, error } = await apiClient.GET("/api/v1/conversations/search", {
				params: { query: { query } },
				signal,
			});
			if (error) throw new Error(apiErrorMessage(error, errorMessage));
			if (!data) throw new Error(errorMessage);
			return data;
		},
	};
}
