import { usePathname, useRootNavigationState, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { shouldOnboard } from "./onboarding";
import { loadOnboardingSkipped } from "./onboardingStore";
import { useApp } from "./store";

// Headless. Mounted beside PushManager in `app/_layout.tsx`; sends a first-run
// user to the welcome screen once both the saved config and the skip flag have
// resolved. Decision logic lives in `onboarding.ts` so it is unit-testable.
export function OnboardingGate() {
	const router = useRouter();
	const pathname = usePathname();
	const navState = useRootNavigationState();
	const { config } = useApp();
	const [skipped, setSkipped] = useState<boolean | null>(null);
	// The gate redirects once per app launch. Without this, skipping would write
	// the flag but the intervening render — before the flag is re-read — would
	// bounce the user straight back to onboarding.
	const redirected = useRef(false);

	useEffect(() => {
		loadOnboardingSkipped().then(setSkipped);
	}, []);

	useEffect(() => {
		if (redirected.current) return;
		if (!navState?.key) return; // wait until navigation is ready to accept routes
		// `config` is null until the store's first load resolves; `shouldOnboard`
		// treats that as "not known yet" and declines to act.
		const configured = config === null ? null : config.host.trim().length > 0;
		// cloudSignedIn: false — this gate has no cloud auth wired in yet, so
		// the user is definitively not signed in to cloud (not "unknown"; null
		// would defer forever and silently kill onboarding). 15b replaces this
		// with real cloud auth state.
		if (!shouldOnboard({ configured, skipped, cloudSignedIn: false })) return;
		// Don't fight the user if they've already navigated somewhere deliberately
		// (e.g. straight to the scanner from a deep link).
		if (pathname !== "/") return;
		redirected.current = true;
		router.replace("/onboarding");
	}, [config, skipped, pathname, router, navState?.key]);

	return null;
}
