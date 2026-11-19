import { useState } from "react";
import { Alert, StyleSheet, View } from "react-native";
import { useTheme } from "../ThemeProvider";
import { useGlassBarInset } from "../useHeaderOverlap";
import { Button, EmptyState } from "../ui";
import { useCloud } from "./CloudProvider";
import { cloudErrorMessage } from "./errors";

/**
 * What a signed-out phone sees in place of a cloud page: the board's empty
 * state, saying what signing in gives, with the account's own Sign in. The same
 * account signs in to Orchestrator Cloud and syncs the account's machines.
 */
export function CloudSignIn() {
	const t = useTheme();
	const glassInset = useGlassBarInset();
	const { signIn } = useCloud();
	const [busy, setBusy] = useState(false);
	return (
		<View style={[styles.fill, { backgroundColor: t.bgBase, paddingTop: glassInset }]}>
			<EmptyState
				icon="cloud"
				title="Sign in to use Orchestrator Cloud"
				message="Cloud sessions run agents without your computer. Sign in with your Orchestrator account to start them and get notified when they need you."
				action={
					<Button
						title="Sign in"
						icon="account"
						loading={busy}
						onPress={() => {
							setBusy(true);
							void signIn().catch((error) => Alert.alert("Couldn't sign in", cloudErrorMessage(error))).finally(() => setBusy(false));
						}}
					/>
				}
			/>
		</View>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1, justifyContent: "center" },
});
