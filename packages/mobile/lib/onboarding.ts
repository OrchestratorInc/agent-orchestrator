// Pure decision logic for first-run onboarding. Deliberately free of React
// Native / Expo imports so it can be unit-tested directly (mirroring
// `pushStatus.ts`); the AsyncStorage side lives in `onboardingStore.ts`.

/** Persisted flag: the user dismissed onboarding without pairing. */
export const ONBOARDING_SKIPPED_KEY = "ao.onboardingSkipped";

export type OnboardingInput = {
	// Whether a server is configured. `null` means "not loaded yet".
	configured: boolean | null;
	// Whether the user has dismissed onboarding. `null` means "not loaded yet".
	skipped: boolean | null;
	// Whether the user is signed into the cloud environment. `null` means "not
	// loaded yet". Required (not optional) so that a caller which gains real
	// cloud auth state later and forgets to thread it through fails to
	// typecheck, rather than silently keeping the old "cloud doesn't exist"
	// behavior and bouncing a signed-in cloud user onto the welcome screen.
	cloudSignedIn: boolean | null;
};

/**
 * Should the app take the user to onboarding?
 *
 * Both `configured` and `skipped` are read asynchronously (config from
 * AsyncStorage + SecureStore, the flag from AsyncStorage), so either can
 * still be `null` on the first render. Redirecting on incomplete state would
 * yank a paired user onto the welcome screen for a frame on every cold start,
 * so unknown means "do nothing yet" rather than "assume false". The same
 * applies to `cloudSignedIn`: a signed-in cloud user never needs onboarding
 * regardless of the local pairing state, but a still-loading auth state must
 * not bounce them onto the welcome screen for a frame either.
 */
export function shouldOnboard({ configured, skipped, cloudSignedIn }: OnboardingInput): boolean {
	if (configured === null || skipped === null) return false;
	if (cloudSignedIn === null) return false;
	if (cloudSignedIn === true) return false;
	return !configured && !skipped;
}

/**
 * The raw state `OnboardingGate.tsx` actually holds, before it has been
 * interpreted into an `OnboardingInput`.
 *
 * `config` is the loaded `ServerConfig | null` (null until the store's first
 * load resolves); `cloud` is the cloud auth state the gate reads from
 * `useCloudAuth()` — also `null` while it is still loading, never a value the
 * gate invents.
 */
export type OnboardingGateState = {
	config: { host: string } | null;
	skipped: boolean | null;
	cloud: { signedIn: boolean | null } | null;
};

/**
 * Turns the gate's raw state into `shouldOnboard`'s input.
 *
 * This is the one place that gets to decide what "configured" or
 * "cloudSignedIn" mean from what the gate holds — moved out of the component
 * so a test can call it with the gate's exact fresh-install shape (no config,
 * no skip flag yet, cloud auth not wired up) and catch a regression like the
 * one this function exists to prevent: passing `cloudSignedIn: null` for a
 * gate that has no cloud auth at all, which silently defers onboarding
 * forever instead of running it for a fresh, unpaired install.
 */
export function deriveOnboardingInput(state: OnboardingGateState): OnboardingInput {
	return {
		configured: state.config === null ? null : state.config.host.trim().length > 0,
		skipped: state.skipped,
		// No cloud auth wired in at all (`state.cloud === null`) is definitively
		// "not signed in", not "still loading" — a device with no cloud auth
		// concept must still be onboardable. Once cloud auth exists, its own
		// `signedIn` may legitimately be `null` while it loads.
		cloudSignedIn: state.cloud === null ? false : state.cloud.signedIn,
	};
}
