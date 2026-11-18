import { useState } from "react";
import { Alert, Pressable, StyleSheet } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { Spinner } from "../Spinner";
import { useTheme } from "../ThemeProvider";
import { iconSize, touchTarget } from "../tokens";

// "Roll back", in each platform's own glyph for undoing back to an earlier point.
const ROLLBACK_SYMBOL = { ios: "arrow.uturn.backward", android: "undo" } as const;

/**
 * A turn's action row is this tall (the desktop's 28px row), and each control
 * in it this square. The slop takes each touch out to the minimum target
 * without growing the row, so the reply's foot stays short.
 */
export const TURN_ACTION_SIZE = 28;
export const TURN_ACTION_SLOP = Math.ceil((touchTarget - TURN_ACTION_SIZE) / 2);

/**
 * One control in a turn's action row (copy, fork, roll back): every one the
 * same square, given TURN_ACTION_SLOP as its hitSlop, so each reaches the same
 * minimum target on either platform.
 */
export const turnActionControl = { width: TURN_ACTION_SIZE, height: TURN_ACTION_SIZE, borderRadius: TURN_ACTION_SIZE / 2, alignItems: "center", justifyContent: "center" } as const;

/**
 * Makes the agent forget a settled turn and everything after it. That cannot be
 * undone, so the system's destructive confirmation asks first, the way Archive
 * does.
 */
export function TurnRollbackButton({ turnId, onRollback, compact = false }: {
	turnId: string;
	onRollback(turnId: string): Promise<number>;
	/** Sized for a turn's action row (TURN_ACTION_SIZE) rather than a line of its own. */
	compact?: boolean;
}) {
	const t = useTheme();
	const feedback = usePressFeedback("icon");
	const [rollingBack, setRollingBack] = useState(false);
	const confirm = () => {
		if (rollingBack) return;
		haptics.warning();
		Alert.alert(
			"Make the agent forget this turn and everything after it?",
			"Files stay changed. Only conversation memory is rolled back, and this cannot be undone.",
			[
				{ text: "Cancel", style: "cancel" },
				{ text: "Roll back", style: "destructive", onPress: () => {
					setRollingBack(true);
					void onRollback(turnId)
						.catch((cause) => Alert.alert("Couldn't roll back", userFacingError(cause)))
						.finally(() => setRollingBack(false));
				} },
			],
			{ cancelable: true },
		);
	};
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel="Roll back to before this turn"
			accessibilityState={{ disabled: rollingBack, busy: rollingBack }}
			disabled={rollingBack}
			onPress={confirm}
			hitSlop={compact ? TURN_ACTION_SLOP : undefined}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [compact ? styles.compact : styles.rollback, pressed && feedback.pressed]}
		>
			{rollingBack ? <Spinner size={iconSize.sm} color={t.textTertiary} /> : <AppSymbol name={ROLLBACK_SYMBOL} size={compact ? iconSize.sm : iconSize.md} color={t.textTertiary} />}
		</Pressable>
	);
}

const styles = StyleSheet.create({
	rollback: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	compact: turnActionControl,
});
