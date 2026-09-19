import { useRouter } from "expo-router";

import { useCloudAuth } from "./cloud/authStore";
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
export function UnpairedState() {
	const router = useRouter();
	return (
		<EmptyState
			icon="server"
			title="No desktop paired"
			message="Scan the pairing code from AO → Settings → Connect Mobile to drive your agents from here."
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
 * Signed-in cloud accounts get an honest "not wired up yet" message rather
 * than the board's empty state: `sessions`/`projects` in the store are still
 * populated by the local daemon poll only (see lib/store.tsx), so a cloud
 * account with real running sessions would otherwise see "No active workers" —
 * indistinguishable from actually having none, which is exactly the kind of
 * silent-wrong default this exists to avoid.
 */
export function CloudUnreadyState() {
	const router = useRouter();
	const cloudAuth = useCloudAuth();

	if (cloudAuth.signedIn === null) {
		return <EmptyState icon="cloud" title="Loading your account…" />;
	}

	if (!cloudAuth.signedIn) {
		return (
			<EmptyState
				icon="cloud"
				title="Sign in to AO Cloud"
				message="Sign in to see and drive the agents running in your cloud workspace."
				action={<Button title="Sign in" icon="log-in" onPress={() => router.push("/sheets/cloud-signin")} />}
			/>
		);
	}

	return (
		<EmptyState
			icon="cloud"
			title="You're signed in to AO Cloud"
			message="Cloud sessions don't show on this board yet — that part is still being built."
		/>
	);
}
