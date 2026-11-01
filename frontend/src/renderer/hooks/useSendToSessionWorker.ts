import type { WorkspaceSession } from "../types/workspace";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { useCloudCp } from "./useCloudCp";

/**
 * Sends a message to the session's worker agent the way its mode takes one:
 * Cloud through the control plane, a chat session as a conversation message,
 * a terminal session as typed input.
 */
export function useSendToSessionWorker(session: WorkspaceSession, hostId?: string) {
	const { client: cloudClient } = useCloudCp();
	return async (message: string, fallbackError: string) => {
		if (session.cloud) {
			await cloudClient.sendSessionMessage(session.cloud.orgId, session.id, { text: message });
			return;
		}
		if (session.mode === "chat") {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/conversation/messages", {
				params: { path: { sessionId: session.id } },
				body: { text: message, clientMessageId: crypto.randomUUID() },
			});
			if (error) throw new Error(apiErrorMessage(error, fallbackError));
			return;
		}
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId: session.id } },
			body: { message, userAuthored: true },
		});
		if (error) throw new Error(apiErrorMessage(error, fallbackError));
	};
}
