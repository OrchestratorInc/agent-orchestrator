import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { StyleSheet, View } from "react-native";
import { WebView } from "react-native-webview";
import { sharedArtifactAppURL } from "../../shared/artifacts/shared-artifact-link";
import { ARTIFACT_VIEWER_ORIGINS, artifactScreenTitle } from "../lib/artifactViewer";
import { backOr } from "../lib/backNavigation";
import { useGlassBarInset } from "../lib/useHeaderOverlap";
import { Button, EmptyState } from "../lib/ui";

/** The published page checks access itself; never attach host/API credentials. */
export default function SharedArtifactScreen() {
	const { url: raw } = useLocalSearchParams<{ url?: string | string[] }>();
	const url = typeof raw === "string" ? sharedArtifactAppURL(raw, ARTIFACT_VIEWER_ORIGINS) : null;
	const router = useRouter();
	const inset = useGlassBarInset();
	const [failedURL, setFailedURL] = useState<string | null>(null);
	// The page never names the screen: off the viewer (signing in), the header
	// shows the host it is on.
	const [shownURL, setShownURL] = useState<string | null>(null);
	// Each artifact opened on this screen gets a fresh load attempt.
	useEffect(() => {
		setFailedURL(null);
		setShownURL(null);
	}, [url]);
	return <>
		<Stack.Screen options={{ title: artifactScreenTitle(shownURL), headerBackVisible: true }} />
		<View style={[styles.page, { paddingTop: inset }]}>
			{url && failedURL !== url ? <WebView
				key={url}
				source={{ uri: url }}
				originWhitelist={["*"]}
				onShouldStartLoadWithRequest={(request) => request.url.startsWith("https://") || request.url === "about:blank"}
				allowFileAccess={false}
				allowFileAccessFromFileURLs={false}
				allowUniversalAccessFromFileURLs={false}
				mixedContentMode="never"
				setSupportMultipleWindows={false}
				onNavigationStateChange={(state) => setShownURL(state.url)}
				onError={() => setFailedURL(url)}
			/> : <EmptyState icon="warning" title="This artifact can't be shown" message="Check your connection or open the original shared link again." />}
			<Button variant="ghost" title="Close" onPress={() => backOr(router)} />
		</View>
	</>;
}

const styles = StyleSheet.create({ page: { flex: 1 } });
export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
