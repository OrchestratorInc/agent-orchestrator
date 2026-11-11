import { useMemo } from "react";
import { StyleSheet, View } from "react-native";
import { THEMES, type ThemeStyle } from "../../../shared/theme/palettes";
import { haptics } from "../../lib/haptics";
import { SettingsChoiceSection, SettingsForm } from "../../lib/SettingsForm";
import { themeFor } from "../../lib/theme";
import { ThemePaletteSwatch } from "../../lib/ThemePaletteSwatch";
import { useTheme, useThemeState } from "../../lib/ThemeProvider";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * The colour theme picker: the same catalog desktop offers, applied as soon as
 * it is chosen. Light, dark or system stays on the Appearance row; every theme
 * ships both, so the swatches follow whichever is showing now. iOS lists the
 * themes as an inline picker, Android as radio buttons; the form scrolls itself.
 */
export default function ThemeScreen() {
	const t = useTheme();
	const { scheme, style, setStyle } = useThemeState();
	const options = useMemo(
		() => THEMES.map((theme) => ({ value: theme.id, label: theme.label, accessory: <ThemePaletteSwatch theme={themeFor(scheme, theme.id)} /> })),
		[scheme],
	);

	function choose(next: ThemeStyle) {
		if (next === style) return;
		haptics.select();
		setStyle(next);
	}

	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
			<SettingsForm testID="settings-theme-list">
				<SettingsChoiceSection label="Theme" options={options} selectedValue={style} onValueChange={choose} testID="settings-theme" />
			</SettingsForm>
		</View>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
});
