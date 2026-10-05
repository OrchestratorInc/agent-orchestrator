import { usePathname, useRootNavigationState, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { useCloudAuth } from "./cloud/authStore";
import { deriveOnboardingInput, shouldOnboard } from "./onboarding";
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
	const cloudAuth = useCloudAuth();
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
		// `deriveOnboardingInput` is the one place that turns this raw state into
		// shouldOnboard's input — see lib/onboarding.ts. This component holds no
		// literals of its own: a `config` that hasn't loaded yet, or a cloud auth
		// state still mid-read, both stay `null` all the way through rather than
		// being guessed at here.
		const input = deriveOnboardingInput({
			config,
			skipped,
			cloud: { signedIn: cloudAuth.signedIn },
		});
		if (!shouldOnboard(input)) return;
		// Don't fight the user if they've already navigated somewhere deliberately
		// (e.g. straight to the scanner from a deep link).
		if (pathname !== "/") return;
		redirected.current = true;
		router.replace("/onboarding");
	}, [config, skipped, cloudAuth.signedIn, pathname, router, navState?.key]);

	return null;
}
