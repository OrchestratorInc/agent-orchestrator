import { apiErrorException } from "./api-error-exception";
import type { CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";
import { apiClient } from "./api-client";
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
			if (error) throw apiErrorException(error, errorMessage);
			if (!data) throw new Error(errorMessage);
			return data;
		},
	};
}

/**
 * How long Cloud chat search waits for typing to pause. The control plane
 * allows each person 60 chat searches a minute.
 */
export const CLOUD_CHAT_SEARCH_DEBOUNCE_MS = 300;
/** How long an organization's chat search rests after a 429 that says no Retry-After. */
export const CLOUD_CHAT_SEARCH_BACKOFF_MS = 60_000;
/** Until when each control plane's organization's chat search rests, after it answered 429. */
const cloudChatSearchRestsUntil = new Map<string, number>();
const restKey = (baseUrl: string, orgId: string) => `${baseUrl}|${orgId}`;

/** Resolves after `ms`, or rejects once `signal` aborts. */
function pause(ms: number, signal?: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		const aborted = () => reject(signal?.reason ?? new DOMException("The search was replaced.", "AbortError"));
		if (signal?.aborted) {
			aborted();
			return;
		}
		let timer: ReturnType<typeof setTimeout> | undefined;
		const stop = () => {
			clearTimeout(timer);
			aborted();
		};
		timer = setTimeout(() => {
			signal?.removeEventListener("abort", stop);
			resolve();
		}, ms);
		signal?.addEventListener("abort", stop, { once: true });
	});
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
			// Typing on changes the query, which cancels this one while it waits.
			await pause(CLOUD_CHAT_SEARCH_DEBOUNCE_MS, signal);
			const now = Date.now();
			const searched = orgIds.filter((orgId) => (cloudChatSearchRestsUntil.get(restKey(cloud.baseUrl, orgId)) ?? 0) <= now);
			const answers = await Promise.allSettled(searched.map((orgId) => cloud.client.searchChats(orgId, { query, limit: 20 }, { signal })));
			// An organization that says this person searched too often rests,
			// quietly, for as long as it asks: its hits are simply absent.
			answers.forEach((answer, index) => {
				if (answer.status !== "rejected" || !(answer.reason instanceof CloudCpError) || answer.reason.status !== 429) return;
				cloudChatSearchRestsUntil.set(restKey(cloud.baseUrl, searched[index]), Date.now() + (answer.reason.retryAfterMs ?? CLOUD_CHAT_SEARCH_BACKOFF_MS));
			});
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
			if (error) throw apiErrorException(error, errorMessage);
			if (!data) throw new Error(errorMessage);
			return data;
		},
	};
}
