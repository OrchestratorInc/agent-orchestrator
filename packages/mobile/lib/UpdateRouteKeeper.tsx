import { useGlobalSearchParams, usePathname, useRootNavigationState, useRouter, useSegments, type Href } from "expo-router";
import { useEffect, useRef, useState } from "react";

import { useApp } from "./store";
import { classifyRoute, restoreAction } from "./updateResume";
import { setCurrentPage, takeResumeRoute } from "./updateResumeStore";

// Headless, mounted once in the root layout. Two jobs around an OTA restart:
//
//   - Keeps updateResumeStore told which page the user is on, so the restart
//     can save it. Sheets and onboarding leave the previous page in place.
//   - On launch, reopens the page a restart saved, once navigation and the saved
//     config are ready. Pushed over the board, so Back still leads home; if the
//     reload came back on some other page (expo-router reopens the last deep
//     link after a reload), that page is replaced.
export function UpdateRouteKeeper(): null {
	const router = useRouter();
	const pathname = usePathname();
	const segments = useSegments();
	const params = useGlobalSearchParams();
	const navState = useRootNavigationState();
	const { config } = useApp();

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
	useEffect(() => {
		if (restored.current || saved === undefined) return;
		if (saved === null) {
			restored.current = true;
			return;
		}
		if (!navState?.key) return; // wait until navigation can accept routes
		if (config === null) return; // the page may need the paired machine; wait for the store's first load
		restored.current = true;
		const action = restoreAction(pathname, saved);
		if (action === "push") router.push(saved as Href);
		else if (action === "replace") router.replace(saved as Href);
	}, [saved, navState?.key, config, pathname, router]);

	return null;
}
