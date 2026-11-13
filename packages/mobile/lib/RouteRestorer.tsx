import AsyncStorage from "@react-native-async-storage/async-storage";
import * as Linking from "expo-linking";
import * as Notifications from "expo-notifications";
import { useGlobalSearchParams, usePathname, useRootNavigationState, useRouter, type Href } from "expo-router";
import { useEffect, useRef } from "react";
import { AppState } from "react-native";
import { LAST_ROUTE_KEY, parseLastRoute, restorableHref, routeHref, routeToRestore, type LastRoute } from "./lastRoute";

/**
 * Headless. Remembers the screen that is open and, at launch, reopens it when
 * the phone closed the app in the background shortly before (see lastRoute).
 */
export function RouteRestorer() {
	const pathname = usePathname();
	const params = useGlobalSearchParams();
	const navState = useRootNavigationState();
	const router = useRouter();
	const href = routeHref(pathname, params as Record<string, string | string[] | undefined>);
	const current = useRef<LastRoute>({ href: null, leftAt: null });
	const decided = useRef(false);

	// Remembered as it changes: a screen worth reopening, or none (the board).
	useEffect(() => {
		if (!decided.current) return;
		current.current = { href: restorableHref(href) ? href : null, leftAt: null };
		void AsyncStorage.setItem(LAST_ROUTE_KEY, JSON.stringify(current.current)).catch(() => {});
	}, [href]);

	// Leaving the app stamps the time, which is what makes it reopen there.
	useEffect(() => {
		const subscription = AppState.addEventListener("change", (state) => {
			if (!decided.current) return;
			const leftAt = state === "active" ? null : Date.now();
			current.current = { ...current.current, leftAt };
			void AsyncStorage.setItem(LAST_ROUTE_KEY, JSON.stringify(current.current)).catch(() => {});
		});
		return () => subscription.remove();
	}, []);

	// Once, when navigation is ready: reopen the last screen if the app started
	// on the board by itself, not from a link or a notification.
	useEffect(() => {
		if (decided.current || !navState?.key) return;
		let active = true;
		void (async () => {
			try {
				const [raw, url, response] = await Promise.all([
					AsyncStorage.getItem(LAST_ROUTE_KEY),
					Linking.getInitialURL().catch(() => null),
					Notifications.getLastNotificationResponseAsync().catch(() => null),
				]);
				if (!active) return;
				const target = url ? null : routeToRestore(parseLastRoute(raw), Date.now(), { notificationAt: response?.notification.date ?? null });
				const reopen = target && pathname === "/" ? target : null;
				if (reopen) router.push(reopen as Href);
				// What is open now replaces what was saved, so an old screen is never reopened twice.
				current.current = { href: reopen ?? (restorableHref(href) ? href : null), leftAt: null };
				void AsyncStorage.setItem(LAST_ROUTE_KEY, JSON.stringify(current.current)).catch(() => {});
			} finally {
				if (active) decided.current = true;
			}
		})();
		return () => { active = false; };
		// Decided once, against the screen the app launched on.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [navState?.key]);

	return null;
}
