import type { ServerConfig } from "../config";
import { useCallback } from "react";
import { useFocusEffect } from "expo-router";
import { AppState } from "react-native";
import { apiRequest } from "../api";

let viewSequence = 0;

/** Keep a viewed chat awake. Older daemons can safely ignore this endpoint. */
export function useChatViewLease(config: ServerConfig | null, sessionId: string): void {
	useFocusEffect(useCallback(() => {
		if (!config) return;
		const viewId = `mobile-${Date.now().toString(36)}-${++viewSequence}`;
		let active = false;
		let timer: ReturnType<typeof setInterval> | undefined;
		let tail: Promise<void> = Promise.resolve();
		let pending = 0;
		const send = (viewActive: boolean) => {
			pending++;
			// Release follows any in-flight renewal, so a delayed true request
			// cannot leave the chat marked as viewed after navigation/background.
			tail = tail.then(async () => {
				await apiRequest(config, `/api/v1/sessions/${encodeURIComponent(sessionId)}/chat-view`, {
					method: "POST", body: JSON.stringify({ viewId, active: viewActive }),
				});
			}).catch(() => {}).finally(() => { pending--; });
		};
		const update = (next: boolean) => {
			if (next === active) return;
			active = next;
			clearInterval(timer);
			send(next);
			if (next) timer = setInterval(() => { if (pending === 0) send(true); }, 15_000);
		};
		update(AppState.currentState === "active");
		const subscription = AppState.addEventListener("change", (state) => update(state === "active"));
		return () => {
			subscription.remove();
			clearInterval(timer);
			if (active) send(false);
		};
	}, [config, sessionId]));
}
