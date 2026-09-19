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
