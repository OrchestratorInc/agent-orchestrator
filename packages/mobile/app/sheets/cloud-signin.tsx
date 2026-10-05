import { useLocalSearchParams, useRouter } from "expo-router";
import { CloudSignInSheet } from "../../lib/CloudSignInSheet";
import { backOr } from "../../lib/backNavigation";

/**
 * Cloud sign-in adds the Cloud workspace alongside the paired desktop.
 */
export default function CloudSignInSheetRoute() {
	const router = useRouter();
	const { from } = useLocalSearchParams<{ from?: string }>();

	function done() {
		if (from === "onboarding") router.replace("/");
		else backOr(router);
	}

	return <CloudSignInSheet onDone={done} onClose={() => backOr(router)} />;
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
