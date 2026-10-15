import { Redirect, Stack, useLocalSearchParams, useRouter } from "expo-router";
import { Platform } from "react-native";
import { CloudSignInSheet } from "../../lib/CloudSignInSheet";
import { backOr } from "../../lib/backNavigation";
import { CLOUD_BASE_URL } from "../../lib/cloud/config";
import { localAuthAvailable } from "../../lib/cloud/localAuthAvailable";

/**
 * Cloud sign-in adds the Cloud workspace alongside the paired desktop.
 */
export default function CloudSignInSheetRoute() {
	const router = useRouter();
	const { from } = useLocalSearchParams<{ from?: string }>();
	// Hosted entry points open AuthKit directly; this route is only a local form.
	if (!localAuthAvailable(CLOUD_BASE_URL)) return <Redirect href={from === "onboarding" ? "/onboarding" : "/"} />;

	function done() {
		if (from === "onboarding") router.replace("/");
		else backOr(router);
	}

	return (
		<>
			<Stack.Screen options={{
				sheetAllowedDetents: Platform.OS === "android" && localAuthAvailable(CLOUD_BASE_URL) ? [0.6, 0.95] : "fitToContents",
			}} />
			<CloudSignInSheet onDone={done} onClose={() => backOr(router)} />
		</>
	);
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
