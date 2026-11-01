import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { useMemo } from "react";
import { Platform, StyleSheet, View } from "react-native";
import { backOr } from "../lib/backNavigation";
import { RenderViewer } from "../lib/chat/RenderViewer";
import { isRenderPath } from "../lib/chat/renderFrameModel";
import { HostScope } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useThemedStyles } from "../lib/ThemeProvider";
import { toolbarIcon } from "../lib/toolbarIcon";
import { EmptyState } from "../lib/ui";

/**
 * An agent's rendered page, expanded from its frame in the conversation:
 * presented modally under a native bar titled with the page. iOS closes it
 * from the bar or with a swipe down; Android's top app bar has the system back.
 */
export default function RenderViewerRoute() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><RenderViewerPage /></HostScope> : <RenderViewerPage />;
}

function RenderViewerPage() {
	const router = useRouter();
	const styles = useThemedStyles(makeStyles);
	const { path, title } = useLocalSearchParams<{ path?: string; title?: string }>();
	const pageTitle = title || "Page";
	const headerItems = useMemo(() => Platform.OS === "ios" ? (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("close")} accessibilityLabel="Close" onPress={() => backOr(router)} />
		</Stack.Toolbar>
	) : null, [router]);
	return (
		<>
			<Stack.Screen options={{ title: pageTitle }} />
			{headerItems}
			{/* A link can name any path; only a session's rendered page loads here. */}
			{path && isRenderPath(path) ? (
				<RenderViewer path={path} title={pageTitle} />
			) : (
				<View style={styles.center}>
					<EmptyState icon="warning" title="This page can't be shown" message="Open it again from its conversation." />
				</View>
			)}
		</>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	center: { flex: 1, backgroundColor: t.bgBase },
});

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
