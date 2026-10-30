import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useLayoutEffect, useState } from "react";
import { ActivityIndicator, StyleSheet, View } from "react-native";
import { WebView } from "react-native-webview";
import { previewArtifactPath } from "../../lib/api";
import { authHeaders, httpBase } from "../../lib/config";
import { hostRouteMatches } from "../../lib/hostRoute";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { space } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * A file a worker reported, opened from its report row the way the desktop opens
 * an artifact: through the daemon's artifact route, which renders Markdown as a
 * page and serves anything else as itself.
 */
export default function ArtifactScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ArtifactContent /></HostScope> : <ArtifactContent />;
}

function ArtifactContent() {
	const { sessionId: rawSession, entry: rawEntry, hostId: routeHostId } = useLocalSearchParams<{ sessionId: string; entry: string; hostId?: string }>();
	const sessionId = String(rawSession ?? "");
	const entry = String(rawEntry ?? "");
	const name = entry.split("/").at(-1) || "Artifact";
	const navigation = useNavigation();
	const router = useRouter();
	const { config, currentHostId, connection } = useApp();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [failed, setFailed] = useState<string>();

	useLayoutEffect(() => {
		navigation.setOptions({ title: name });
	}, [name, navigation]);

	if (routeHostId && !hostRouteMatches(routeHostId, currentHostId)) {
		return <View style={styles.center}><EmptyState icon="file-text" title="Artifact belongs to another machine" message="Open it from that machine's chat." action={<Button title="Open board" icon="activity" onPress={() => router.navigate("/")} />} /></View>;
	}
	if (!config) {
		return <View style={styles.center}>{connection === "closed" ? <EmptyState icon="wifi-off" title="Machine offline" message="This artifact loads once the app reconnects." /> : <ActivityIndicator color={t.accent} />}</View>;
	}
	if (failed) {
		return <View style={styles.center}><EmptyState icon="alert-triangle" title="Couldn't open this artifact" message={failed} action={<Button title="Retry" icon="refresh-cw" onPress={() => setFailed(undefined)} />} /></View>;
	}
	return (
		<WebView
			source={{ uri: `${httpBase(config)}${previewArtifactPath(sessionId, entry)}`, headers: authHeaders(config) }}
			style={styles.screen}
			startInLoadingState
			renderLoading={() => <View style={styles.center}><ActivityIndicator color={t.accent} /></View>}
			onHttpError={(event) => setFailed(event.nativeEvent.statusCode === 404 ? "The worker no longer has this file." : `The machine answered ${event.nativeEvent.statusCode}.`)}
			onError={(event) => setFailed(event.nativeEvent.description || "The machine didn't answer.")}
		/>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { ...StyleSheet.absoluteFill, alignItems: "center", justifyContent: "center", paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
});
