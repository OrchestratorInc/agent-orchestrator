import { useEffect, useRef } from "react";
import { randomUUID } from "expo-crypto";
import { getConversationPage, resolveApproval, resolveInput, sendConversationMessage } from "../chat/api";
import { createWatchActionHandler } from "./actions";
import { AppState } from "react-native";
import { useApp } from "../store";
import { watchBridge } from "./bridge";
import { buildWatchSnapshot, type WatchSnapshot } from "./snapshot";

export function WatchManager() {
	const app = useApp();
	const { configResolved } = app;
	const current = useRef(app);
	current.current = app;
	const previous = useRef<WatchSnapshot | undefined>(undefined);
	useEffect(() => {
		const bridge = watchBridge;
		if (!bridge || !configResolved) return;
		const publish = () => {
			if (AppState.currentState !== "active" || !current.current.configResolved) return;
			const snapshot = buildWatchSnapshot(current.current.hostStates, Date.now(), previous.current);
			previous.current = snapshot;
			void bridge.publishSnapshot(JSON.stringify(snapshot)).catch(() => { /* Watch retains its timestamped cache. */ });
		};
		publish();
		const timer = setInterval(publish, 30_000);
		const subscription = AppState.addEventListener("change", (state) => { if (state === "active") publish(); });
		return () => { clearInterval(timer); subscription.remove(); };
	}, [configResolved]);
	useEffect(() => {
		const bridge = watchBridge;
		if (!bridge?.setReady) return;
		const handle = createWatchActionHandler({
			configForHost: (hostId) => {
				const host = current.current.hostStates.find((entry) => entry.hostId === hostId);
				return host?.connection === "open" && !host.error ? current.current.configForHost(hostId) : null;
			},
			read: getConversationPage, approve: resolveApproval, input: resolveInput, message: sendConversationMessage,
			isActive: (id) => bridge.isRequestActive(id), now: Date.now, newId: randomUUID,
		});
		const subscription = bridge.addListener("onWatchRequest", (event) => {
			void (async () => {
				let payload: unknown;
				try { payload = JSON.parse(event.payload); } catch { payload = null; }
				const result = await handle(payload, event.id, event.deadline);
				await bridge.completeRequest(event.id, JSON.stringify(result));
				if (result.status === "sent" || result.status === "already_handled") void current.current.refreshAll();
			})().catch(() => { /* Native deadline reports uncertainty; never replay an action. */ });
		});
		const ready = () => { void bridge.setReady(AppState.currentState === "active").catch(() => {}); };
		ready();
		const lifecycle = AppState.addEventListener("change", ready);
		return () => { subscription.remove(); lifecycle.remove(); void bridge.setReady(false).catch(() => {}); };
	}, []);
	return null;
}
