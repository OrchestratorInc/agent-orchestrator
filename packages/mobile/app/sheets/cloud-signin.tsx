import { useLocalSearchParams, useRouter } from "expo-router";
import { CloudSignInSheet } from "../../lib/CloudSignInSheet";
import { backOr } from "../../lib/backNavigation";
import { useEnvironment } from "../../lib/store";

/**
 * Signing in here is an implicit "switch me to cloud" — the same move the
 * first-run choice and the drawer switcher make, just reached through the
 * empty-state CTA instead.
 */
export default function CloudSignInSheetRoute() {
	const router = useRouter();
	const { setEnvironment } = useEnvironment();
	const { from } = useLocalSearchParams<{ from?: string }>();

	function done() {
		setEnvironment("cloud");
		if (from === "onboarding") router.replace("/");
		else backOr(router);
	}

	return <CloudSignInSheet onDone={done} onClose={() => backOr(router)} />;
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
