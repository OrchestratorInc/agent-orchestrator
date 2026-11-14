import AsyncStorage from "@react-native-async-storage/async-storage";
import * as Linking from "expo-linking";
import { useGlobalSearchParams, usePathname, useRootNavigationState, useRouter, useSegments, type Href } from "expo-router";
import { useEffect, useRef } from "react";
import { AppState } from "react-native";
import { launchNotificationResponse } from "./launchNotification";
import { LAST_ROUTE_KEY, parseLastRoute, restorableHref, routeHref, routeToRestore, type LastRoute } from "./lastRoute";

/**
 * Headless. Remembers the screen that is open and, at launch, reopens it when
 * the phone closed the app in the background shortly before (see lastRoute).
 */
export function RouteRestorer() {
	const pathname = usePathname();
	const params = useGlobalSearchParams();
	const segments = useSegments();
	const navState = useRootNavigationState();
	const router = useRouter();
	const href = routeHref(pathname, params as Record<string, string | string[] | undefined>, segments);
	const current = useRef<LastRoute>({ href: null, leftAt: null });
	const decided = useRef(false);
	// Where the app is now, read when the launch's reads come back: a screen
	// opened meanwhile (by the person, a link or a notification) is not
	// replaced by the one saved.
	const latest = useRef({ pathname, href });
	latest.current = { pathname, href };
	const launchPath = useRef(pathname);
	const navigated = useRef(false);
	if (pathname !== launchPath.current) navigated.current = true;

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
	// on the board by itself and stayed there, not from a link or a
	// notification tap (the one PushManager routes, see launchNotification).
	useEffect(() => {
		if (decided.current || !navState?.key) return;
		let active = true;
		void (async () => {
			try {
				const [raw, url, response] = await Promise.all([
					AsyncStorage.getItem(LAST_ROUTE_KEY),
					Linking.getInitialURL().catch(() => null),
					launchNotificationResponse(),
				]);
				if (!active) return;
				const launchedElsewhere = Boolean(url || response) || navigated.current || latest.current.pathname !== "/";
				const reopen = launchedElsewhere ? null : routeToRestore(parseLastRoute(raw), Date.now());
				if (reopen) router.push(reopen as Href);
				// What is open now replaces what was saved, so an old screen is never reopened twice.
				const now = latest.current.href;
				current.current = { href: reopen ?? (restorableHref(now) ? now : null), leftAt: null };
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
