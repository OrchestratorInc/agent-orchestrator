import { StyleSheet, Text, View } from "react-native";
import { Feather } from "../icons";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { iconSize, space, type } from "../tokens";
import { isAutomationSteer } from "./timelineModel";
import type { ActivityDetail } from "./types";

export { isAutomationSteer };

export function steerNoteText(detail: ActivityDetail | undefined): string {
	return detail?.deliveredBeforeDelete ? "Steered: the agent took it before your delete could stop it" : "Steered";
}

/**
 * The line under a steered message, the desktop's: a ↳ and "Steered", at the
 * bubble's edge for the person's own steer and at the start for another agent's.
 */
export function SteerNote({ detail }: { detail: ActivityDetail | undefined }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={[styles.note, isAutomationSteer(detail) ? styles.start : styles.end]}>
		<Feather name="corner-down-right" size={iconSize.xs} color={t.textTertiary} />
		<Text style={styles.text}>{steerNoteText(detail)}</Text>
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	note: { flexDirection: "row", alignItems: "center", gap: space.xxs, marginTop: space.xxs },
	start: { alignSelf: "flex-start" },
	end: { alignSelf: "flex-end" },
	text: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize },
});
