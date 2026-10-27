import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import { beginWorkspaceRead, patchWorkspaceSession } from "./useWorkspaceQuery";
import { conversationQueryKey } from "./useConversation";

/** Own the primary Chat tab's wake/keep-alive lease across navigation. */
export function useChatViewRegistration(sessionId: string, chatViewActive: boolean, hostId?: string): void {
	const queryClient = useQueryClient();
	useEffect(() => {
		if (!chatViewActive) return;
		const viewId = crypto.randomUUID();
		let left = false;
		let refreshed = false;
		let pending = Promise.resolve();
		const refreshSession = async () => {
			const readVersion = beginWorkspaceRead(queryClient);
			const { data, error } = await clientForSessionHost().GET("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId } },
			});
			if (!error && !left && data?.session?.id === sessionId) {
				// An absent workspace/project is left to authoritative workspace events;
				// opening one tab must not fall back to fetching the entire list.
				patchWorkspaceSession(queryClient, data.session, readVersion);
			}
		};
		const setViewActive = async (active: boolean) => {
			try {
				const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/chat-view", {
					params: { path: { sessionId } },
					body: { viewId, active },
				});
				if (error) throw error;
				// Successful registration leaves history refresh to conversation events.
				if (active && !left && !refreshed && !hostId) {
					refreshed = true;
					void refreshSession().catch(() => {});
				}
			} finally {
				// Release a late registration too; leaving must not wait on wake.
				if (active && left) void setViewActive(false).catch(() => {});
			}
		};
		const renewView = () => {
			pending = pending.catch(() => {}).then(() => {
				return left ? undefined : setViewActive(true);
			});
			return pending;
		};
		const refreshAfterWakeError = () => {
			// The clear-marker event can arrive while waking. A failed resume may
			// then stop the controller without another event, leaving that snapshot stale.
			if (left || hostId) return;
			void queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId) }).catch(() => {});
		};
		void renewView().catch(refreshAfterWakeError);
		const renewal = window.setInterval(() => { void renewView().catch(refreshAfterWakeError); }, 10_000);
		return () => {
			left = true;
			window.clearInterval(renewal);
			void setViewActive(false).catch(() => {});
		};
	}, [chatViewActive, hostId, queryClient, sessionId]);
}
