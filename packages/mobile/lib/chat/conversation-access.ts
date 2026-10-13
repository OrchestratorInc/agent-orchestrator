import type { ServerConfig } from "../config";
import type { SessionSource } from "../environment/types";
import { conversationPollIntervalFor } from "./conversationPoll";

export type ConversationAccess = {
	source: SessionSource;
	cacheKey: string;
	pollInterval: number | null;
	subscribeToEvents: boolean;
};

/** Selects the active environment's conversation transport without mixing credentials. */
export function conversationAccess(input: {
	config: ServerConfig | null;
	source: SessionSource | undefined;
	sessionId: string;
}): ConversationAccess | undefined {
	const { config, source, sessionId } = input;
	if (!source) return undefined;
	if (source.kind === "cloud") {
		if (!source.scopeKey) return undefined;
		return {
			source,
			cacheKey: JSON.stringify(["cloud", source.scopeKey, sessionId]),
			pollInterval: null,
			subscribeToEvents: true,
		};
	}
	if (!config) return undefined;
	return {
		source,
		cacheKey: `${config.secure ? "https" : "http"}://${config.host}:${config.httpPort}/${config.password}/${sessionId}`,
		pollInterval: conversationPollIntervalFor(config),
		subscribeToEvents: true,
	};
}
