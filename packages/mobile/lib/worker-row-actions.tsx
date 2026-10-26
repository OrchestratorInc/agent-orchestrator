import { Pressable, StyleSheet, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import type { PlatformSymbol } from "./symbols";
import { type Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, press } from "./tokens";
import { workerActionSymbol } from "./worker-action-model";

const ACTION_WIDTH = 64;
const CONTROL_SIZE = 44;

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
				onPress={() => onSetPinned(!pinned)}
			/>
			<Action
				accessibilityLabel={`Archive ${title}`}
				icon={workerActionSymbol("delete")}
				onPress={onDelete}
			/>
		</View>
	);
}

// Pin and archive are one pair of controls, so they share one surface and ink.
// Foreground ink rather than a hue: `amber` means "needs your attention" in this
// palette, and neither action is asking for anything.
function Action({
	accessibilityLabel,
	icon,
	onPress,
}: {
	accessibilityLabel: string;
	icon: PlatformSymbol;
	onPress(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.slot}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={accessibilityLabel}
				android_ripple={{ color: t.accentTint, borderless: false, radius: CONTROL_SIZE / 2 }}
				onPress={onPress}
				style={({ pressed }) => [styles.control, pressed && styles.pressed]}
			>
				<AppSymbol name={icon} size={iconSize.lg} color={t.textPrimary} />
			</Pressable>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		actions: { width: ACTION_WIDTH * 2, height: 76, flexDirection: "row" },
		slot: { width: ACTION_WIDTH, height: 76, alignItems: "center", justifyContent: "center" },
		control: {
			width: CONTROL_SIZE,
			height: CONTROL_SIZE,
			borderRadius: CONTROL_SIZE / 2, borderCurve: "continuous",
			borderWidth: StyleSheet.hairlineWidth,
			borderColor: t.accent,
			backgroundColor: t.accentTint,
			alignItems: "center",
			justifyContent: "center",
			overflow: "hidden",
		},
		pressed: { opacity: press.opacity },
	});
