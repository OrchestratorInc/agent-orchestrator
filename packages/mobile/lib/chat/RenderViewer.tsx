import { useState } from "react";
import { StyleSheet, View } from "react-native";
import { useApp } from "../store";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { space } from "../tokens";
import { Button, EmptyState } from "../ui";
import { RenderDocument, SKELETON_DELAY_MS, useRenderPage } from "./RenderFrame";
import { RenderSkeleton } from "./RenderSkeleton";

/**
 * An agent's page over the whole screen, scrollable and pinch-zoomable, with
 * its links live. The route around it (app/render.tsx) supplies the native
 * header and the modal presentation, so it swipes away like any other.
 */
export function RenderViewer({ path, title }: { path: string; title: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { config } = useApp();
	const [failed, setFailed] = useState(false);
	const page = useRenderPage();
	if (!config || failed) {
		return (
			<View accessibilityRole="alert" style={styles.center}>
				<EmptyState
					icon="warning"
					iconColor={t.red}
					title="Couldn't load this page"
					message={config ? undefined : "This machine isn't connected."}
					action={config ? <Button title="Retry" icon="refresh" onPress={() => { setFailed(false); page.reset(); }} /> : undefined}
				/>
			</View>
		);
	}
	return (
		<View style={styles.body}>
			<RenderDocument key={page.mount} path={path} displayMode="fullscreen" onReady={page.ready} onGone={() => page.gone(() => setFailed(true))} onFailed={() => setFailed(true)} />
			{page.loading ? <RenderSkeleton height={Number.POSITIVE_INFINITY} label={title} style={styles.skeleton} delayMs={SKELETON_DELAY_MS} /> : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	// Edge to edge: the page carries the thread's inset itself (VIEWER_STYLE_SCRIPT).
	body: { flex: 1, backgroundColor: t.bgBase },
	skeleton: { position: "absolute", top: 0, left: space.xl, right: space.xl, height: 360 },
	center: { flex: 1, backgroundColor: t.bgBase },
});
