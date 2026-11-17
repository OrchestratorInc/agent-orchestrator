import { useRouter } from "expo-router";
import { useState } from "react";
import { Alert } from "react-native";

import { unconfiguredView } from "./configLoading";
import { useCloud } from "./cloud/CloudProvider";
import { cloudErrorMessage } from "./cloud/errors";
import { useApp } from "./store";
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
	const { selectedHostName, loading, reloadConfig, configResolved } = useApp();
	if (selectedHostName) {
		const connecting = loading || !configResolved;
		return (
			<EmptyState
				icon="server"
				pulse={connecting}
				title={connecting ? `Connecting to ${selectedHostName}` : `${selectedHostName} is unavailable`}
				message={connecting ? "Checking saved addresses…" : "This machine is paired but cannot be reached right now."}
				action={connecting ? undefined : <Button title="Retry connection" icon="refresh-cw" onPress={() => { void reloadConfig(); }} />}
			/>
		);
	}
	// On launch the store has no config until the endpoint race finishes, which
	// on a slow network takes seconds. Offering the scanner during that window
	// told a paired user their phone had forgotten the desktop, moments before it
	// connected on its own.
	if (unconfiguredView({ resolved: configResolved }) === "resolving") {
		return (
			<EmptyState
				icon="monitor-smartphone"
				pulse
				title="Connecting to your machine…"
			/>
		);
	}
	return (
		<EmptyState
			icon="monitor-smartphone"
			title="No machine paired"
			message="Pair the computer running Orchestrator, or sign in to run agents in Orchestrator Cloud."
			action={[
				<Button key="pair" title="Scan pairing code" icon="qrScanner" onPress={() => router.push("/pair")} />,
				<SignInButton key="sign-in" />,
			]}
		/>
	);
}

/**
 * The account's sign-in, as onboarding offers it: Orchestrator Cloud's
 * workers and projects, and any machines linked to the account, fill the
 * board once it lands.
 */
function SignInButton() {
	const { signIn, status } = useCloud();
	const [busy, setBusy] = useState(false);
	// Signed in already (Cloud still answering, or unavailable): nothing to offer.
	if (status !== "signed-out") return null;
	return (
		<Button
			title="Sign in"
			variant="ghost"
			loading={busy}
			onPress={() => {
				setBusy(true);
				void signIn().catch((error) => Alert.alert("Couldn't sign in", cloudErrorMessage(error))).finally(() => setBusy(false));
			}}
		/>
	);
}
