import { Pressable, StyleSheet } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { usePressFeedback } from "../pressFeedback";
import { Spinner } from "../Spinner";
import { useTheme } from "../ThemeProvider";
import { iconSize, touchTarget } from "../tokens";

export type NoticeDismissButtonProps = {
	/** What it closes, said by the screen reader: two notices can be up at once. */
	accessibilityLabel: string;
	onPress: () => void;
	/** The dismissal is being saved: a spinner in the glyph's place, and no taps. */
	busy?: boolean;
};

/** The close button that ends a notice row on the terminal screen: an xmark the size of the touch target. */
export function NoticeDismissButton({ accessibilityLabel, onPress, busy = false }: NoticeDismissButtonProps) {
	const t = useTheme();
	const feedback = usePressFeedback("icon");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel}
			accessibilityState={{ disabled: busy, busy }}
			disabled={busy}
			onPress={onPress}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [styles.button, pressed && feedback.pressed]}
		>
			{busy ? <Spinner size={iconSize.sm} /> : <AppSymbol name="close" size={iconSize.sm} color={t.textSecondary} />}
		</Pressable>
	);
}

const styles = StyleSheet.create({
	button: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});
