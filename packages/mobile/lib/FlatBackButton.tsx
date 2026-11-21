import { useRouter } from "expo-router";
import { Pressable, StyleSheet } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { backOr } from "./backNavigation";
import { useTheme } from "./ThemeProvider";

const EDGE = 36;

/**
 * The app's back button: a flat chevron in the secondary ink, with no glass
 * circle or tinted fill, as the desktop draws its own chrome. The
 * conversation's top bar drew it first; every page's header now shares it, in
 * place of the system's glass-circled back button.
 *
 * Goes back in the stack the page is in, or home when there is nothing under it.
 */
export function FlatBackButton({ onPress, testID = "header-back" }: { onPress?(): void; testID?: string }) {
	const t = useTheme();
	const router = useRouter();
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel="Back"
			onPress={onPress ?? (() => backOr(router))}
			hitSlop={6}
			style={({ pressed }) => [styles.button, pressed && { backgroundColor: t.bgSubtle }]}
			testID={testID}
		>
			<AppSymbol name="back" size={18} color={t.textSecondary} />
		</Pressable>
	);
}

const styles = StyleSheet.create({
	button: { width: EDGE, height: EDGE, borderRadius: 8, alignItems: "center", justifyContent: "center" },
});
