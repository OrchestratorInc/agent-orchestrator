import { StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { AgentLogo } from "../AgentLogo";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { radius, space, type } from "../tokens";

/**
 * What the bar leaves the title between its back button and actions menu: two
 * 44pt glass buttons, their 16pt margins and the gaps beside them.
 */
const BAR_CHROME = 180;
const MAX_TITLE_WIDTH = 260;

/** The agent's state in a word, so the header never says it by colour alone. */
export function conversationStateLabel(state: string): string {
	switch (state) {
		case "busy": return "Working";
		case "ready": return "Ready";
		case "stopped": return "Stopped";
		case "connecting": return "Connecting";
		case "recovering": return "Reconnecting";
		case "hibernated": return "Hibernated";
		default: return state ? state[0].toUpperCase() + state.slice(1) : state;
	}
}

export function ConversationTitle({
	title,
	subtitle,
	harness,
	state,
}: {
	title: string;
	subtitle: string;
	harness?: string | null;
	state?: string;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// Sized to the window, not fixed: a set width wider than the space between
	// the bar's buttons ran the state label under the actions menu.
	const { width: windowWidth } = useWindowDimensions();
	const maxWidth = Math.min(MAX_TITLE_WIDTH, windowWidth - BAR_CHROME);
	const stateColor = state === "busy" ? t.orange : state === "ready" ? t.green : state === "stopped" ? t.red : t.amber;
	const stateLabel = state ? conversationStateLabel(state) : undefined;

	return (
		<View
			accessible
			accessibilityRole="header"
			accessibilityLabel={[title, subtitle, stateLabel].filter(Boolean).join(", ")}
			style={[styles.header, { maxWidth }]}
		>
			<AgentLogo harness={harness} size={22} />
			<View style={styles.copy}>
				<Text numberOfLines={1} style={styles.title}>{title}</Text>
				<View style={styles.subtitleRow}>
					<Text numberOfLines={1} style={styles.subtitle}>{subtitle}</Text>
					{stateLabel ? (
						<View style={styles.state}>
							<View style={[styles.dot, { backgroundColor: stateColor }]} />
							<Text numberOfLines={1} style={styles.stateLabel}>{stateLabel}</Text>
						</View>
					) : null}
				</View>
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	header: { flexDirection: "row", alignItems: "center", gap: space.sm },
	copy: { minWidth: 0, flexShrink: 1, alignItems: "flex-start", justifyContent: "center" },
	title: { fontFamily: "DMSans_600SemiBold", maxWidth: "100%", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	subtitleRow: { maxWidth: "100%", flexDirection: "row", alignItems: "center", gap: space.xs },
	subtitle: { fontFamily: "DMSans_400Regular", flexShrink: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	// The word stays whole; the project and harness give way to it.
	state: { flexShrink: 0, flexDirection: "row", alignItems: "center", gap: space.xxs },
	dot: { width: 7, height: 7, borderRadius: radius.pill },
	stateLabel: { fontFamily: "DMSans_500Medium", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
});
