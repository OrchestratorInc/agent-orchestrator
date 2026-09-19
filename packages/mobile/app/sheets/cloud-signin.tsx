import { useLocalSearchParams, useRouter } from "expo-router";
import { CloudSignInSheet } from "../../lib/CloudSignInSheet";
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
		else router.back();
	}

	return <CloudSignInSheet onDone={done} onClose={() => router.back()} />;
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
