import { useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { Feather } from "../icons";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { fontScaleCap, iconSize, space, touchTarget, type } from "../tokens";
import { Button } from "../ui";

/** Recovery retains the original request identity; it is not a second start. */
export function CloudSpawnRecoveryNotice({ accepted, busy, problem, onViewSessions, onNewRequest }: { accepted: boolean; busy: boolean; problem?: string; onViewSessions(): void; onNewRequest(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [expanded, setExpanded] = useState(false);
	return <View style={styles.notice}>
		<View accessibilityRole="alert" accessibilityLiveRegion="polite" style={styles.message}>
			<Feather name={accepted ? "check-circle" : "help-circle"} size={iconSize.md} color={accepted ? t.green : t.amber} />
			<View style={styles.copy}>
				<Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.title}>{accepted ? "Worker created" : "Couldn’t confirm the worker started"}</Text>
				<Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.body}>{accepted
					? "Open your worker to continue. No new worker will be started."
					: "Your previous request is saved. Retry uses the same task settings to avoid creating a duplicate worker."}</Text>
				{!accepted && problem ? <Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.body}>{problem}</Text> : null}
			</View>
		</View>
		{!accepted ? <>
			<View style={styles.actions}>
				<Button title="View Cloud sessions" variant="text" disabled={busy} onPress={() => { if (!busy) onViewSessions(); }} />
				<Pressable accessibilityRole="button" accessibilityLabel="More recovery options" accessibilityState={{ expanded, disabled: busy }} disabled={busy} onPress={() => { if (!busy) setExpanded(value => !value); }} style={styles.disclosure}>
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.body}>More</Text>
					<Feather name={expanded ? "chevron-up" : "chevron-down"} size={iconSize.xs} color={t.textSecondary} />
				</Pressable>
			</View>
			{expanded ? <View style={styles.more}>
				<Button title="Start a new request…" variant="text" disabled={busy} onPress={() => { if (!busy) onNewRequest(); }} />
			</View> : null}
		</> : null}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	notice: { gap: space.xs },
	message: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
	copy: { flex: 1, gap: space.xs },
	title: { ...type.subheadline, fontFamily: "Geist_600SemiBold", fontWeight: "600", color: t.textPrimary },
	body: { ...type.footnote, color: t.textSecondary },
	actions: { flexDirection: "row", flexWrap: "wrap", alignItems: "center", columnGap: space.lg },
	disclosure: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs },
	more: { alignItems: "flex-start", paddingLeft: iconSize.md + space.sm },
});
