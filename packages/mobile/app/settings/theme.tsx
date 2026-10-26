import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { THEMES, type ThemeStyle } from "../../../shared/theme/palettes";
import { haptics } from "../../lib/haptics";
import { Feather } from "../../lib/icons";
import { usePressFeedback } from "../../lib/pressFeedback";
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
	const { scheme, style, setStyle } = useThemeState();
	const insets = useSafeAreaInsets();
	const feedback = usePressFeedback("row");

	function choose(next: ThemeStyle) {
		if (next === style) return;
		haptics.select();
		setStyle(next);
	}

	return (
		<View style={styles.screen}>
			{/* The rounded card is the scroll viewport, not a child of one: rows scroll
			    inside it, so its corners stay round however far the list moves. A card
			    scrolled inside a square viewport was cut flat at the top edge. */}
			<View style={[styles.card, { marginBottom: insets.bottom + space.md }]}>
				<ScrollView style={styles.list} testID="settings-theme-list">
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
									android_ripple={feedback.android_ripple}
									style={({ pressed }) => [styles.row, pressed && feedback.pressed]}
								>
									<ThemePaletteSwatch theme={themeFor(scheme, theme.id)} />
									<Text style={[styles.rowLabel, selected && styles.rowLabelSelected]} numberOfLines={1}>{theme.label}</Text>
									<View style={styles.spacer} />
									{selected ? <Feather name="check" size={17} color={t.textPrimary} /> : null}
								</Pressable>
							</View>
						);
					})}
				</ScrollView>
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	// Shrinks to fit a short list and stops at the safe area on a long one.
	card: { flexShrink: 1, marginHorizontal: space.lg, marginTop: space.xs, backgroundColor: t.bgElevated, borderRadius: 16, borderCurve: "continuous", overflow: "hidden" },
	list: { flexGrow: 0 },
	separator: { height: StyleSheet.hairlineWidth, backgroundColor: t.borderSubtle, marginLeft: space.md },
	row: { minHeight: 52, flexDirection: "row", alignItems: "center", paddingHorizontal: space.md, gap: space.md },
	spacer: { flex: 1 },
	rowLabel: { fontFamily: "Geist_400Regular", flexShrink: 1, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	rowLabelSelected: { fontFamily: "Geist_600SemiBold", fontWeight: "600" },
});
