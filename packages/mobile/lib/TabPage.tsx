import type { ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { BAR_SEAM, Frost, SEAM_STOPS, useTabBarFrost } from "./frost";

/**
 * A tab whose page is also shown on its own (Pull Requests, Settings): the
 * page ends where Android's clear tab bar begins, and the bar's frost lies
 * over its last stretch, so the bar reads the same over every tab. Its top
 * bar stays opaque, as the page's own screen draws it, and a short seam under
 * it lets rows scrolling up dissolve into it as they do on the board tabs.
 */
export function TabPage({ children }: { children: ReactNode }) {
	const tabBar = useTabBarFrost();
	return (
		<View style={[styles.page, { paddingBottom: tabBar.inset }]}>
			{children}
			{tabBar.frost ? <Frost from="top" stops={SEAM_STOPS} style={styles.seam} /> : null}
			{tabBar.frost}
		</View>
	);
}

const styles = StyleSheet.create({
	page: { flex: 1 },
	seam: { position: "absolute", top: 0, left: 0, right: 0, height: BAR_SEAM },
});
