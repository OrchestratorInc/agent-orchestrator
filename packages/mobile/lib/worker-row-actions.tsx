import { Pressable, StyleSheet, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { usePressFeedback, type PressTone } from "./pressFeedback";
import type { PlatformSymbol } from "./symbols";
import { type Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, radius, touchTarget } from "./tokens";
import { workerActionSymbol } from "./worker-action-model";
import { WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";

const ACTION_WIDTH = WORKER_ACTION_REVEAL_WIDTH / 2;

export function WorkerRowActions({
	title,
	pinned,
	onSetPinned,
	onDelete,
}: {
	title: string;
	pinned: boolean;
	onSetPinned(pinned: boolean): void;
	onDelete(): void;
}) {
	const styles = useThemedStyles(makeStyles);

	return (
		<View style={styles.actions}>
			<Action
				accessibilityLabel={pinned ? `Unpin ${title}` : `Pin ${title}`}
				icon={workerActionSymbol(pinned ? "unpin" : "pin")}
				tone="neutral"
				onPress={() => onSetPinned(!pinned)}
			/>
			<Action
				accessibilityLabel={`Archive ${title}`}
				icon={workerActionSymbol("delete")}
				tone="destructive"
				onPress={onDelete}
			/>
		</View>
	);
}

// Tonal icon buttons, as Material 3 draws a row's trailing actions. Pin is a
// neutral one in foreground ink — `amber` means "needs your attention" in this
// palette, and pinning asks for nothing. Archive is the destructive one: it
// stops the agent and deletes a clean worktree, so it carries the red.
function Action({
	accessibilityLabel,
	icon,
	tone,
	onPress,
}: {
	accessibilityLabel: string;
	icon: PlatformSymbol;
	tone: Extract<PressTone, "neutral" | "destructive">;
	onPress(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button", { tone });
	const destructive = tone === "destructive";
	return (
		<View style={styles.slot}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={accessibilityLabel}
				android_ripple={feedback.android_ripple}
				onPress={onPress}
				style={({ pressed }) => [styles.control, destructive ? styles.destructive : styles.neutral, pressed && feedback.pressed]}
			>
				<AppSymbol name={icon} size={iconSize.xl} color={destructive ? t.red : t.textPrimary} />
			</Pressable>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		actions: { width: WORKER_ACTION_REVEAL_WIDTH, flexDirection: "row" },
		slot: { width: ACTION_WIDTH, alignItems: "center", justifyContent: "center" },
		control: {
			width: touchTarget,
			height: touchTarget,
			borderRadius: radius.pill,
			borderCurve: "continuous",
			alignItems: "center",
			justifyContent: "center",
			overflow: "hidden",
		},
		neutral: { backgroundColor: t.accentTint },
		destructive: { backgroundColor: t.tintRed },
	});
