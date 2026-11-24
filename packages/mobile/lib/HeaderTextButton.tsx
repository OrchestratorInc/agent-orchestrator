import { Pressable, StyleSheet, Text } from "react-native";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { font, fontScaleCap } from "./tokens";

/**
 * A sheet or page header's text buttons in the app's own style, in place of
 * the system's glass capsules: the way out (Cancel) as plain text, and the
 * action that finishes the sheet (Create, Save) as a filled pill, dimmed until
 * it can be pressed. Put them in a `Stack.Toolbar.View hidesSharedBackground`
 * so iOS draws no glass behind them.
 */
export function HeaderTextButton({ title, onPress, disabled = false, accessibilityLabel, testID }: {
	title: string;
	onPress(): void;
	disabled?: boolean;
	accessibilityLabel?: string;
	testID?: string;
}) {
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel ?? title}
			accessibilityState={{ disabled }}
			disabled={disabled}
			onPress={onPress}
			hitSlop={8}
			style={({ pressed }) => [styles.text, pressed && styles.pressed, disabled && styles.disabled]}
			testID={testID}
		>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.textLabel}>{title}</Text>
		</Pressable>
	);
}

export function HeaderPrimaryButton({ title, onPress, disabled = false, accessibilityLabel, testID }: {
	title: string;
	onPress(): void;
	disabled?: boolean;
	accessibilityLabel?: string;
	testID?: string;
}) {
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel ?? title}
			accessibilityState={{ disabled }}
			disabled={disabled}
			onPress={onPress}
			hitSlop={6}
			style={({ pressed }) => [styles.primary, pressed && styles.primaryPressed, disabled && styles.disabled]}
			testID={testID}
		>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.primaryLabel}>{title}</Text>
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	text: { height: 34, paddingHorizontal: 8, borderRadius: 8, alignItems: "center", justifyContent: "center" },
	textLabel: { fontFamily: font.regular, fontSize: 16, color: t.textSecondary },
	pressed: { backgroundColor: t.bgSubtle },
	primary: { height: 32, paddingHorizontal: 14, borderRadius: 8, alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	primaryPressed: { opacity: 0.85 },
	primaryLabel: { fontFamily: font.medium, fontSize: 15, color: t.onAccent },
	disabled: { opacity: 0.4 },
});
