import { useEffect, useState } from "react";
import { StyleSheet, View, type StyleProp, type ViewStyle } from "react-native";
import { Spinner } from "../Spinner";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { radius, space } from "../tokens";
import { renderSkeletonLines } from "./renderFrameModel";

/**
 * Where a page will be: a block for its content and, when the frame is tall
 * enough, two caption lines, with the system's activity indicator in the block
 * while it loads. The blocks hold still; the indicator is the platform's own,
 * so the OS decides how it moves when motion is reduced.
 * With a delay, it appears only if loading is still going then, so a page that
 * comes from cache does not flash a skeleton on its way in.
 */
export function RenderSkeleton({ height, label, style, delayMs = 0 }: { height: number; label: string; style?: StyleProp<ViewStyle>; delayMs?: number }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [shown, setShown] = useState(delayMs <= 0);
	useEffect(() => {
		if (shown) return;
		const timer = setTimeout(() => setShown(true), delayMs);
		return () => clearTimeout(timer);
	}, [shown, delayMs]);
	if (!shown) return null;
	const lines = renderSkeletonLines(height);
	return <View
		accessible
		accessibilityRole="progressbar"
		accessibilityLabel={`Loading ${label}`}
		accessibilityState={{ busy: true }}
		style={[styles.root, style]}
	>
		<View style={[styles.block, styles.content]}><Spinner color={t.textTertiary} /></View>
		{lines.map((fraction, index) => <View key={index} style={[styles.block, styles.line, { width: `${fraction * 100}%` }]} />)}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	root: { flex: 1, gap: space.sm, paddingVertical: space.xs },
	block: { borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgSubtle },
	content: { flex: 1, alignItems: "center", justifyContent: "center" },
	line: { height: space.md, borderRadius: radius.xs },
});
