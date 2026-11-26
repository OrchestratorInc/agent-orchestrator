import AsyncStorage from "@react-native-async-storage/async-storage";
import * as Linking from "expo-linking";
import { useGlobalSearchParams, usePathname, useRootNavigationState, useRouter, useSegments, type Href } from "expo-router";
import { useEffect, useRef } from "react";
import { AppState } from "react-native";
import { launchNotificationResponse } from "./launchNotification";
import { reportOpenedBy, sessionIdInPath } from "./telemetry/uiUsageRegistry";
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
	// Leaving the app while the launch's reads run: kept, so the screen saved
	// once they land still says when the app was left (null: back in front).
	const leftDuringLaunch = useRef<number | null>(null);
	// Where the app is now, read when the launch's reads come back: a screen
	// opened meanwhile (by the person, a link or a notification) is not
	// replaced by the one saved.
	const latest = useRef({ pathname, href });
	latest.current = { pathname, href };
	const launchPath = useRef(pathname);
	const navigated = useRef(false);
	if (pathname !== launchPath.current) navigated.current = true;

	// Remembered as it changes: a screen worth reopening, or none (the board).
	// When the app was left stays as the AppState listener last set it: a route
	// that changes in the background (the reopened screen landing after a launch
	// that finished there) is still a screen the app was left on.
	useEffect(() => {
		if (!decided.current) return;
		current.current = { href: restorableHref(href) ? href : null, leftAt: current.current.leftAt };
		void AsyncStorage.setItem(LAST_ROUTE_KEY, JSON.stringify(current.current)).catch(() => {});
	}, [href]);

	// Leaving the app stamps the time, which is what makes it reopen there.
	useEffect(() => {
		const subscription = AppState.addEventListener("change", (state) => {
			const leftAt = state === "active" ? null : Date.now();
			if (!decided.current) {
				leftDuringLaunch.current = leftAt;
				return;
			}
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
			let reopen: string | null = null;
			try {
				const [raw, url, response] = await Promise.all([
					// Unreadable: nothing to reopen, and the launch goes on from here.
					AsyncStorage.getItem(LAST_ROUTE_KEY).catch(() => null),
					Linking.getInitialURL().catch(() => null),
					launchNotificationResponse(),
				]);
				if (!active) return;
				const launchedElsewhere = Boolean(url || response) || navigated.current || latest.current.pathname !== "/";
				reopen = launchedElsewhere ? null : routeToRestore(parseLastRoute(raw), Date.now());
				if (reopen) {
					// A session reopened here counts as restored, not opened from the board.
					const sessionId = sessionIdInPath(reopen);
					if (sessionId) reportOpenedBy("restore", sessionId);
					router.push(reopen as Href);
				}
			} catch {
				// A launch check that failed reopens nothing: the app stays where it is.
				reopen = null;
			}
			if (!active) return;
			// What is open now replaces what was saved, so an old screen is never reopened twice.
			const now = latest.current.href;
			current.current = { href: reopen ?? (restorableHref(now) ? now : null), leftAt: leftDuringLaunch.current };
			decided.current = true;
			void AsyncStorage.setItem(LAST_ROUTE_KEY, JSON.stringify(current.current)).catch(() => {});
		})();
		return () => { active = false; };
		// Decided once, against the screen the app launched on.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [navState?.key]);

	return null;
}
