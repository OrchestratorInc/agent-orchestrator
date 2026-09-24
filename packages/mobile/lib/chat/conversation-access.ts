import type { ServerConfig } from "../config";
import type { SessionSource } from "../environment/types";
import { CONVERSATION_POLL_MS, conversationPollIntervalFor } from "./conversationPoll";

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
		return {
			source,
			cacheKey: `cloud/${sessionId}`,
			pollInterval: CONVERSATION_POLL_MS,
			// Cloud polling owns its cursor separately; subscribing from zero would
			// replay the entire transcript as new events after every remount.
			subscribeToEvents: false,
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
