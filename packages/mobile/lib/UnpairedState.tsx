import { useRouter } from "expo-router";

import { useCloudAuth } from "./cloud/authStore";
import { useCloudSignInAction } from "./cloud/useCloudSignInAction";
import { Button, EmptyState } from "./ui";

/**
 * What every screen shows before a desktop has been paired.
 *
 * The three tabs each had their own answer to this: Workers explained what was
 * missing and offered the scanner, while Projects and PRs said "No server /
 * Connect to AO in Settings" with no way to act on it — and sent the user to
 * hunt through Settings for a field rather than to the scanner that fixes it.
 * PRs did not even render a header, so the tab lost its title at the one moment
 * a new user most needs to know where they are.
 *
 * This is the Workers copy, which was the good one, made shared. Deliberately
 * not a restatement of the welcome screen: someone reaching this has already
 * read that and chosen to move past it.
 */
export function UnpairedState({ resolving = false }: { resolving?: boolean }) {
	const router = useRouter();
	// On launch the store has no config until the endpoint race finishes, which
	// on a slow network takes seconds. Offering the scanner during that window
	// told a paired user their phone had forgotten the desktop, moments before it
	// connected on its own.
	if (resolving) {
		return (
			<EmptyState
				icon="monitor-smartphone"
				pulse
				title="Connecting to your desktop…"
			/>
		);
	}
	return (
		<EmptyState
			icon="monitor-smartphone"
			title="No desktop paired"
			action={<Button title="Scan pairing code" icon="maximize" onPress={() => router.push("/pair")} />}
		/>
	);
}

/**
 * What the same tabs show for the cloud environment instead of `UnpairedState`.
 *
 * `signedIn` is `null` while the stored session is still being read (see
 * CloudAuthProvider) — that must not flash "sign in" for a user who already
 * is, so it renders a plain loading state rather than guessing.
 *
 * A signed-in account needs an organization before the board can load. Show
 * that resolution's progress or retryable failure instead of an empty board.
 */
export function CloudUnreadyState() {
	const cloudAuth = useCloudAuth();
	const cloudSignIn = useCloudSignInAction();

	if (cloudAuth.signedIn === null) {
		return <EmptyState icon="cloud" title="Loading your account…" />;
	}

	if (!cloudAuth.signedIn) {
		return (
			<EmptyState
				icon="cloud"
				title="Sign in to AO Cloud"
				message="Sign in to see and drive the agents running in your cloud workspace."
				action={<Button title="Sign in" icon="log-in" loading={cloudSignIn.busy} onPress={cloudSignIn.signIn} />}
			/>
		);
	}

	if (cloudAuth.orgLoading) {
		return <EmptyState icon="cloud" title="Loading your cloud workspace…" />;
	}

	return (
		<EmptyState
			icon="cloud"
			title="Could not load your cloud workspace"
			message={cloudAuth.orgError ?? "Your account is signed in. Retry to load your workspace."}
			action={<Button title="Retry" icon="refresh-cw" onPress={() => void cloudAuth.retryOrgResolution()} />}
		/>
	);
}
