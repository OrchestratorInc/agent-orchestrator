import type { components } from "../../api/schema";
import type { CloudCpClient } from "./cloud-cp";
import { cloudWorkspaceReviewSearchQueryOptions } from "../hooks/useCloudWorkspaceReview";
import { apiErrorMessage } from "./api-client";
import { clientForSessionHost } from "./host-clients";
import type { WorkspaceFileSearchItem, WorkspaceFileSearchTarget } from "./command-palette";

export const COMMAND_PALETTE_FILE_SEARCH_LIMIT = 20;

/** How often the palette asks again while the session's first file index builds. */
export const INDEXING_REFETCH_MS = 1_000;

type SearchResponse = components["schemas"]["SearchResponse"];
export type PaletteSearchFileName = components["schemas"]["SearchFileNameHit"];
export type PaletteSearchContentHit = components["schemas"]["SearchFileContentHit"];
export type PaletteSearchMessageHit = components["schemas"]["SearchMessageHit"];

/**
 * What the palette shows from a search: file names (scored by the daemon,
 * or name-matched in the palette for Cloud sessions), passages inside files,
 * chat messages, and whether the file index is still being built.
 */
export type CommandPaletteFileSearchResponse = {
	results: WorkspaceFileSearchItem[];
	truncated: boolean;
	fileNames?: PaletteSearchFileName[];
	fileContent?: PaletteSearchContentHit[];
	messages?: PaletteSearchMessageHit[];
	files?: SearchResponse["files"];
};

type CommandPaletteFileSearchArgs = {
	/** The session whose files are searched; absent searches chats only. */
	target?: WorkspaceFileSearchTarget;
	query: string;
	/** Include chat messages (the root palette); the file picker leaves them out. */
	includeMessages: boolean;
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
	refetchInterval?: (query: { state: { data?: CommandPaletteFileSearchResponse } }) => number | false;
};

function normalizeCloudResponse(response: {
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
	includeMessages = false,
): boolean {
	if (!target) return includeMessages;
	return !target.cloudOrgId || cloudReady;
}

export function commandPaletteFileSearchQueryOptions(
	args: CommandPaletteFileSearchArgs,
): CommandPaletteFileSearchQueryOptions {
	const { target } = args;
	if (target?.cloudOrgId) {
		// Cloud sandboxes have no daemon search index: names only, as before.
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
				normalizeCloudResponse(await options.queryFn(context)),
		};
	}
	const kinds = target ? (args.includeMessages ? "fileNames,fileContent,messages" : "fileNames,fileContent") : "messages";
	return {
		queryKey: ["palette-search", target?.hostId ?? "", target?.sessionId ?? "", kinds, args.query] as const,
		queryFn: async ({ signal }: { signal?: AbortSignal } = {}) => {
			const { data, error } = await clientForSessionHost(target?.hostId).GET("/api/v1/search", {
				params: {
					query: {
						q: args.query,
						...(target ? { sessionId: target.sessionId } : {}),
						kinds,
					},
				},
				signal,
			});
			if (error) throw new Error(apiErrorMessage(error, args.errorMessage));
			if (!data) throw new Error(args.errorMessage);
			return fromSearchResponse(data);
		},
		// Content results appear when the first index finishes, not on the next keystroke.
		refetchInterval: (query) => (query.state.data?.files === "building" ? INDEXING_REFETCH_MS : false),
	};
}

function fromSearchResponse(data: SearchResponse): CommandPaletteFileSearchResponse {
	return {
		results: data.fileNames.map((hit) => ({ path: hit.path })),
		truncated: false,
		fileNames: data.fileNames,
		fileContent: data.fileContent,
		messages: data.messages,
		files: data.files,
	};
}
