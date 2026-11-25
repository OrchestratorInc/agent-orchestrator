import { useRouter } from "expo-router";
import { Pressable, StyleSheet, Text } from "react-native";
import { haptics } from "../haptics";
import { Feather } from "../icons";
import { usePressFeedback } from "../pressFeedback";
import { useApp } from "../store";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { iconSize, space, touchTarget, type } from "../tokens";
import type { ArtifactRef } from "./renderFrameModel";

/**
 * Where an agent reported an HTML artifact mid-turn: one line naming it, not
 * the page, as desktop shows it. An artifact is a standalone document, so the
 * thread never frames it; the turn's artifact tiles preview it, and a tap
 * opens the artifact viewer.
 */
export function ReportedArtifactRow({ sessionId, artifact }: { sessionId: string; artifact: ArtifactRef }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const feedback = usePressFeedback("row");
	const { currentHostId } = useApp();
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`Open artifact ${artifact.name}`}
			android_ripple={feedback.android_ripple}
			onPress={() => {
				haptics.tap();
				router.push({ pathname: "/artifact/[sessionId]", params: { sessionId, path: artifact.path, hostId: currentHostId ?? undefined } });
			}}
			style={({ pressed }) => [styles.row, pressed && feedback.pressed]}
		>
			<Feather name="file-text" size={iconSize.xs} color={t.textTertiary} />
			<Text style={styles.label}>Artifact</Text>
			<Text numberOfLines={1} style={styles.name}>{artifact.name}</Text>
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	row: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingVertical: space.xs },
	label: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	name: { flex: 1, fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
