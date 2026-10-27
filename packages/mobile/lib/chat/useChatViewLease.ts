import type { ServerConfig } from "../config";
import { useEffect, useRef, useState } from "react";
import { useIsFocused, useNavigation, useRoute } from "expo-router";
import { AppState } from "react-native";
import { apiRequest } from "../api";

let viewSequence = 0;

type StackRoute = { key: string; name: string };

/**
 * The chat is still on screen when only chat sheets sit above it. A sheet is its
 * own route and blurs the chat beneath, so a focus-only lease was released when
 * Android opened turn settings, and the chat hibernated behind the open sheet.
 */
export function coveredOnlyBySheets(routes: readonly StackRoute[] | undefined, key: string): boolean {
	if (!routes) return false;
	const index = routes.findIndex((route) => route.key === key);
	return index >= 0 && routes.slice(index + 1).every((route) => route.name.startsWith("sheets/"));
}

/**
 * Keep a viewed chat awake. Older daemons can safely ignore this endpoint.
 *
 * `onViewing` runs once each time the chat becomes viewed, when the daemon first
 * accepts the lease (the registration, or a renewal if that failed). The daemon
 * wakes a hibernated chat before it answers that request and reports the wake no
 * other way, so this is when the screen should read the conversation again.
 */
export function useChatViewLease(config: ServerConfig | null, sessionId: string, onViewing?: () => void): void {
	const focused = useIsFocused();
	const navigation = useNavigation();
	const { key } = useRoute();
	// Read from the stack while rendering, not kept in state: the render that
	// first sees the chat blurred must also see the sheet that blurred it, or the
	// lease is released and taken again as the sheet opens. The listeners below
	// only re-render for stack changes that leave focus alone.
	const [, setStackVersion] = useState(0);
	const underSheet = coveredOnlyBySheets(navigation.getState()?.routes, key);
	const viewing = focused || underSheet;
	const onViewingRef = useRef(onViewing);
	useEffect(() => { onViewingRef.current = onViewing; }, [onViewing]);

	useEffect(() => {
		const update = () => setStackVersion((version) => version + 1);
		const unsubscribe = [
			navigation.addListener("state", update),
			navigation.addListener("focus", update),
			navigation.addListener("blur", update),
		];
		return () => { for (const remove of unsubscribe) remove(); };
	}, [key, navigation]);

	useEffect(() => {
		if (!config || !viewing) return;
		const viewId = `mobile-${Date.now().toString(36)}-${++viewSequence}`;
		let active = false;
		let timer: ReturnType<typeof setInterval> | undefined;
		let tail: Promise<void> = Promise.resolve();
		let pending = 0;
		// Each activation announces once, on its first request that succeeds: a
		// registration that times out or fails is retried by the next renewal, and
		// that renewal is the one that woke the chat.
		let activation = 0;
		let announced = 0;
		const send = (viewActive: boolean) => {
			pending++;
			const sentFor = activation;
			// Release follows any in-flight renewal, so a delayed true request
			// cannot leave the chat marked as viewed after navigation/background.
			tail = tail.then(async () => {
				await apiRequest(config, `/api/v1/sessions/${encodeURIComponent(sessionId)}/chat-view`, {
					method: "POST", body: JSON.stringify({ viewId, active: viewActive }),
				});
				if (viewActive && active && sentFor === activation && announced !== activation) {
					announced = activation;
					onViewingRef.current?.();
				}
			}).catch(() => {}).finally(() => { pending--; });
		};
		const update = (next: boolean) => {
			if (next === active) return;
			active = next;
			if (next) activation++;
			clearInterval(timer);
			send(next);
			if (next) timer = setInterval(() => { if (pending === 0) send(true); }, 15_000);
		};
		update(AppState.currentState === "active");
		const subscription = AppState.addEventListener("change", (state) => update(state === "active"));
		return () => {
			subscription.remove();
			clearInterval(timer);
			// Inactive first, so a registration still in flight cannot report a
			// chat that is no longer viewed.
			if (active) {
				active = false;
				send(false);
			}
		};
	}, [config, sessionId, viewing]);
}
