import { useRouter } from "expo-router";
import { useEffect } from "react";
import { StyleSheet, View } from "react-native";
import { reportErrorShown } from "./telemetry/uiUsageRegistry";
import { Button, EmptyState } from "./ui";

/**
 * What a link to a route the app does not have opens: an old notification, a
 * link from an older desktop, a mistyped aomobile:// URL. It stands in for
 * expo-router's own "Unmatched Route" page, whose Sitemap link reads
 * `window.location.origin`, which is undefined on native, so tapping it
 * terminated the app. `app/_sitemap.tsx` shows this too, which keeps that
 * sitemap out of reach of a direct link.
 *
 * Reported by kind only: the path can name sessions and machines.
 */
export function NotFoundScreen() {
	const router = useRouter();
	useEffect(() => {
		reportErrorShown("screen", "route_not_found");
	}, []);
	return (
		<View style={styles.center}>
			<EmptyState
				icon="link"
				title="This page doesn't exist"
				// Back down to the board under this page, not a second board on top of it.
				action={<Button title="Open board" icon="workers" onPress={() => router.dismissTo("/")} />}
			/>
		</View>
	);
}

// No background: the route's contentStyle (the base colour) shows through.
const styles = StyleSheet.create({
	center: { flex: 1, alignItems: "center", justifyContent: "center" },
});
