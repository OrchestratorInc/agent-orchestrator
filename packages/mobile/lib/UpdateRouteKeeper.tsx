import { useGlobalSearchParams, usePathname, useRootNavigationState, useRouter, useSegments, type Href } from "expo-router";
import { useEffect, useRef, useState } from "react";

import { classifyRoute, restoreAction } from "./updateResume";
import { setCurrentPage, takeResumeRoute } from "./updateResumeStore";

// Headless, mounted once in the root layout. Two jobs around an OTA restart:
//
//   - Keeps updateResumeStore told which page the user is on, so the restart
//     can save it. Sheets and onboarding leave the previous page in place.
//   - On launch, reopens the page a restart saved as soon as navigation is ready
//     — not after the machine answers, which can take minutes, by when the user
//     has moved on (pages already handle a machine that is still loading, as a
//     cold-start notification tap does). Pushed over the board, so Back still
//     leads home; if the reload came back on some other page (expo-router
//     reopens the last deep link after a reload), that page is replaced. If the
//     user has left the page the reload landed on, nothing is restored.
export function UpdateRouteKeeper(): null {
	const router = useRouter();
	const pathname = usePathname();
	const segments = useSegments();
	const params = useGlobalSearchParams();
	const navState = useRootNavigationState();

	useEffect(() => {
		const route = classifyRoute(pathname, segments, params);
		if (route.kind === "page") setCurrentPage(route.href);
		else if (route.kind === "root") setCurrentPage(null);
	}, [pathname, segments, params]);

	const [saved, setSaved] = useState<string | null | undefined>(undefined);
	useEffect(() => {
		void takeResumeRoute().then(setSaved);
	}, []);

	const restored = useRef(false);
	// Where the reload landed: the page showing when navigation first became ready.
	const landing = useRef<string | null>(null);
	useEffect(() => {
		if (restored.current) return;
		if (!navState?.key) return; // wait until navigation can accept routes
		if (landing.current === null) landing.current = pathname;
		if (saved === undefined) return; // still reading storage
		restored.current = true;
		if (saved === null) return;
		const action = restoreAction(pathname, landing.current, saved);
		if (action === "push") router.push(saved as Href);
		else if (action === "replace") router.replace(saved as Href);
	}, [saved, navState?.key, pathname, router]);

	return null;
}
