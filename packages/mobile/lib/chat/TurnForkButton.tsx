import { useState } from "react";
import { Alert, Pressable, StyleSheet } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { Spinner } from "../Spinner";
import { useTheme } from "../ThemeProvider";
import { iconSize, touchTarget } from "../tokens";

// "Fork", in each platform's own glyph for branching.
const FORK_SYMBOL = { ios: "arrow.triangle.branch", android: "call_split" } as const;

/**
 * Copies the session as this turn left it into a new session, and opens it.
 * Nothing is lost, so it needs no confirmation. Shown only when the daemon
 * supports session forks.
 */
export function TurnForkButton({ turnId, onFork }: { turnId: string; onFork(turnId: string): Promise<void> }) {
	const t = useTheme();
	const feedback = usePressFeedback("icon");
	const [forking, setForking] = useState(false);
	const fork = () => {
		if (forking) return;
		haptics.tap();
		setForking(true);
		void onFork(turnId)
			.catch((cause) => Alert.alert("Couldn't fork", userFacingError(cause)))
			.finally(() => setForking(false));
	};
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel="Fork from this turn"
			accessibilityState={{ disabled: forking, busy: forking }}
			disabled={forking}
			onPress={fork}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [styles.fork, pressed && feedback.pressed]}
		>
			{forking ? <Spinner size={iconSize.sm} color={t.textTertiary} /> : <AppSymbol name={FORK_SYMBOL} size={iconSize.md} color={t.textTertiary} />}
		</Pressable>
	);
}

const styles = StyleSheet.create({
	fork: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});
