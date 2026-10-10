import { useEffect, useRef } from "react";
import { AppState } from "react-native";
import { useApp } from "../store";
import { watchBridge } from "./bridge";
import { buildWatchSnapshot, type WatchSnapshot } from "./snapshot";

export function WatchManager() {
	const { hostStates, configResolved } = useApp();
	const current = useRef({ hostStates, configResolved });
	current.current = { hostStates, configResolved };
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
	return null;
}
