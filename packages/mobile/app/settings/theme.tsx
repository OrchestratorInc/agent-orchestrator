import { useRouter } from "expo-router";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { THEMES, type ThemeStyle } from "../../../shared/theme/palettes";
import { backOr } from "../../lib/backNavigation";
import { haptics } from "../../lib/haptics";
import { Feather } from "../../lib/icons";
import { NativeHeaderButton } from "../../lib/native-header-button";
import { themeFor, type Theme } from "../../lib/theme";
import { ThemePaletteSwatch } from "../../lib/ThemePaletteSwatch";
import { useTheme, useThemedStyles, useThemeState } from "../../lib/ThemeProvider";
import { space, type } from "../../lib/tokens";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * The colour theme picker: the same catalog desktop offers, applied as soon as
 * it is tapped. Light, dark or system stays on the Appearance row; every theme
 * ships both, so the swatches follow whichever is showing now.
 */
export default function ThemeScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const { scheme, style, setStyle } = useThemeState();

	function choose(next: ThemeStyle) {
		if (next === style) return;
		haptics.select();
		setStyle(next);
	}

	return (
		<View style={styles.screen}>
			<View style={styles.header}>
				<View style={[styles.headerButton, styles.backButton]}>
					<NativeHeaderButton icon="back" label="Back to Settings" onPress={() => backOr(router, "/settings")} />
				</View>
				<Text style={styles.headerTitle}>Theme</Text>
			</View>
			<ScrollView contentContainerStyle={styles.content} testID="settings-theme-list">
				<View style={styles.card}>
					{THEMES.map((theme, index) => {
						const selected = theme.id === style;
						return (
							<View key={theme.id}>
								{index > 0 ? <View style={styles.separator} /> : null}
								<Pressable
									accessibilityRole="button"
									accessibilityLabel={theme.label}
									accessibilityState={{ selected }}
									testID={`settings-theme-${theme.id}`}
									onPress={() => choose(theme.id)}
									style={({ pressed }) => [styles.row, pressed && styles.rowPressed]}
								>
									<ThemePaletteSwatch theme={themeFor(scheme, theme.id)} border={t.borderDefault} />
									<Text style={[styles.rowLabel, selected && styles.rowLabelSelected]} numberOfLines={1}>{theme.label}</Text>
									{selected ? <Feather name="check" size={17} color={t.textPrimary} /> : null}
								</Pressable>
							</View>
						);
					})}
				</View>
				<Text style={styles.footer}>Each theme has a light and a dark version. Appearance in Settings picks which one shows.</Text>
			</ScrollView>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	header: { height: 88, alignItems: "center", justifyContent: "center", paddingHorizontal: space.lg, backgroundColor: t.bgBase, zIndex: 1 },
	headerTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, lineHeight: type.title3.lineHeight, fontWeight: "600", letterSpacing: -0.3 },
	headerButton: { position: "absolute", top: 22, width: 44, height: 44, borderRadius: 22, overflow: "hidden" },
	backButton: { left: 14 },
	content: { paddingHorizontal: space.lg, paddingTop: space.xs, paddingBottom: space.xxxl, gap: space.xs },
	card: { backgroundColor: t.bgElevated, borderRadius: 16, borderCurve: "continuous", overflow: "hidden" },
	separator: { height: StyleSheet.hairlineWidth, backgroundColor: t.borderSubtle, marginLeft: 64 },
	row: { minHeight: 52, flexDirection: "row", alignItems: "center", paddingHorizontal: space.md, gap: space.md },
	rowPressed: { backgroundColor: t.bgElevatedHover },
	rowLabel: { fontFamily: "Geist_400Regular", flex: 1, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	rowLabelSelected: { fontFamily: "Geist_600SemiBold", fontWeight: "600" },
	footer: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, paddingHorizontal: space.sm },
});
