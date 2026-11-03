import { Pressable, StyleSheet, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { usePressFeedback, type PressTone } from "./pressFeedback";
import type { PlatformSymbol } from "./symbols";
import { type Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, radius, space, touchTarget } from "./tokens";
import { workerActionSymbol } from "./worker-action-model";
import { WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";

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

// Plain tonal icon buttons, the same on both platforms, set on the list's own
// background rather than on a panel. Pin is a neutral one in
// foreground ink — `amber` means "needs your attention" in this
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
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel}
			android_ripple={feedback.android_ripple}
			onPress={onPress}
			style={({ pressed }) => [styles.control, destructive ? styles.destructive : styles.neutral, pressed && feedback.pressed]}
		>
			<AppSymbol name={icon} size={iconSize.lg} color={destructive ? t.red : t.textPrimary} />
		</Pressable>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// Ends on the row's own 16pt gutter, so the archive control lines up with
		// the status above it once the row slides over.
		actions: {
			width: WORKER_ACTION_REVEAL_WIDTH,
			flexDirection: "row",
			alignItems: "center",
			justifyContent: "flex-end",
			gap: space.md,
			paddingRight: space.lg,
		},
		control: {
			width: touchTarget,
			height: touchTarget,
			borderRadius: radius.pill,
			borderCurve: "continuous",
			alignItems: "center",
			justifyContent: "center",
			overflow: "hidden",
		},
		neutral: { backgroundColor: t.bgElevated },
		destructive: { backgroundColor: t.tintRed },
	});
