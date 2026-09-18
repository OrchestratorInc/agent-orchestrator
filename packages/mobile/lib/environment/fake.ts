import type { SessionSource } from "./types";

/**
 * A SessionSource that answers with empties unless a test overrides a method.
 * Screen and store tests use this so they exercise both environments without
 * a daemon or a control plane.
 */
export function createFakeSessionSource(overrides: Partial<SessionSource> = {}): SessionSource {
	return {
		kind: "local",
		listProjects: async () => [],
		listSessions: async () => [],
		createSession: async () => ({ id: "fake-session" }),
		deleteSession: async () => {},
		getConversationPage: async () => {
			throw new Error("createFakeSessionSource: getConversationPage not overridden");
		},
		sendMessage: async () => ({ duplicate: false }),
		cancelTurn: async () => {},
		subscribeEvents: () => () => {},
		...overrides,
	};
}
