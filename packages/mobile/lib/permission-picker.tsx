import Hand from "lucide-react-native/icons/hand";
import PencilLine from "lucide-react-native/icons/pencil-line";
import ShieldAlert from "lucide-react-native/icons/shield-alert";
import ShieldCheck from "lucide-react-native/icons/shield-check";
import { memo, useEffect, useSyncExternalStore, type ComponentType } from "react";
import { Pressable, StyleSheet, Text, View, type DimensionValue } from "react-native";
import type { ApprovalMode } from "./chat/types";
import { ComposerOverlay } from "./composer-overlay";
import { useWarmAfterIdle } from "./effort-slider-host";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, radius, space, touchTarget, type } from "./tokens";
import { markTouchInside } from "./touchOutside";
import { useTouchDownPress } from "./useTouchDownPress";
import { useEffortSliderOpen, useEffortSliderSwitchOpen, type EffortSliderSwitch } from "./useEffortSlider";

/*
 * The chat's permission mode, picked the way its effort is: the composer's
 * button opens the modes floating over the chat on the overlay's veil, with no
 * card of their own, listed as desktop's permission picker lists them. Every
 * row is the same height and lines up the same
 * way: the mode's icon, its name, and one line on what it lets
 * the agent do. The one in force sits in a soft wash of the text colour rather
 * than being ticked.
 */

export type PermissionChoice = {
	value: string;
	label: string;
	description?: string;
	selected: boolean;
	/** The mode whose icon and colour it takes (see approvalLook); none for an agent's own tier. */
	look?: ApprovalMode;
};

// The list's width: a little wider than the effort slider's card, so the
// highlight on the mode in force has room around its words.
const PICKER_WIDTH = 360;
// The veil's reach above the list: its arch is solid only over its lower half
// or so, and the list is three times the slider's height.
const VEIL_RISE = 220;
// Every row this tall, whatever its text, so the list reads as an even stack.
const ROW_HEIGHT = 58;
const ROW_RADIUS = radius.lg;
// The icons' column, so every name starts at the same place.
const ICON_COLUMN = 24;

type LucideIcon = ComponentType<{ size?: number; color?: string; strokeWidth?: number }>;

/** Desktop's permission icons: asking first, accepting edits, approving routine actions, never asking. */
function permissionIcon(look: ApprovalMode | undefined): LucideIcon {
	if (look === "bypass-permissions") return ShieldAlert;
	if (look === "accept-edits") return PencilLine;
	if (look === "auto") return ShieldCheck;
	return Hand;
}

/** The mode that never asks, drawn in the warning colour so it never reads as routine. */
function warns(look: ApprovalMode | undefined): boolean {
	return look === "bypass-permissions";
}

/**
 * The composer's button: the current mode's icon, in the warning colour while
 * nothing is asked. Like the effort gauge it toggles as the finger lands, and
 * `onPress` (closing whatever else is open) runs first.
 */
export function PermissionPickerButton({ slider, choices, disabled, onPress, testID }: {
	slider: EffortSliderSwitch;
	choices: readonly PermissionChoice[];
	disabled: boolean;
	onPress?: () => void;
	testID: string;
}) {
	const t = useTheme();
	const open = useEffortSliderSwitchOpen(slider) && !disabled && choices.length > 0;
	const current = choices.find((choice) => choice.selected);
	const Icon = permissionIcon(current?.look);
	// Once per physical touch, and still for a screen reader (see useTouchDownPress).
	const press = useTouchDownPress(() => {
		haptics.tap();
		onPress?.();
		slider.setOpen(!open);
	});
	return (
		<Pressable
			{...press}
			onTouchStart={markTouchInside}
			disabled={disabled}
			accessibilityRole="button"
			accessibilityLabel={`Permissions, ${current?.label ?? "default"}`}
			accessibilityState={{ expanded: open, disabled }}
			testID={testID}
			style={({ pressed }) => [buttonStyles.target, (pressed || disabled) && buttonStyles.dim]}
		>
			{/* The speedometer's visual size beside it: a 20pt shield reads larger than its half-circle. */}
			<Icon size={iconSize.md} strokeWidth={2} color={warns(current?.look) ? t.amber : open ? t.textPrimary : t.textSecondary} />
		</Pressable>
	);
}

/**
 * The picker over the composer, wired to the composer's switch as the effort
 * slider is: built hidden once the screen is idle, revealed on open, closed by
 * a tap outside, the keyboard, back, or a pick. No timeout: it is a list to
 * read. Memoised against a composer re-rendering under a streaming reply.
 */
export const PermissionPickerHost = memo(function PermissionPickerHost({ slider, choices, busy, onChoose }: {
	slider: EffortSliderSwitch;
	choices: readonly PermissionChoice[];
	busy: boolean;
	onChoose: (value: string) => void;
}) {
	const { open } = useEffortSliderOpen(choices, busy, undefined, slider, null);
	// As the effort slider's host: a request card replacing the composer must
	// not leave the picker to reopen by itself when the card goes.
	useEffect(() => () => slider.setOpen(false), [slider]);
	const bottom = useSyncExternalStore(slider.subscribe, slider.bottom);
	const warm = useWarmAfterIdle();
	if (!(open || warm) || !bottom) return null;
	return (
		<PermissionPickerOverlay
			visible={open}
			bottom={bottom}
			choices={choices}
			onChoose={(value) => {
				slider.setOpen(false);
				onChoose(value);
			}}
		/>
	);
});

export function PermissionPickerOverlay({ bottom, visible = true, choices, onChoose }: { bottom: DimensionValue; visible?: boolean; choices: readonly PermissionChoice[]; onChoose: (value: string) => void }) {
	return (
		<ComposerOverlay bottom={bottom} visible={visible} veilRise={VEIL_RISE}>
			<PermissionPicker choices={choices} onChoose={onChoose} />
		</ComposerOverlay>
	);
}

export function PermissionPicker({ choices, onChoose }: { choices: readonly PermissionChoice[]; onChoose: (value: string) => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.list} accessibilityRole="radiogroup" accessibilityLabel="Permissions" testID="permission-picker">
			{choices.map((choice) => {
				const Icon = permissionIcon(choice.look);
				const warning = warns(choice.look);
				return (
					<Pressable
						key={choice.value}
						onPress={() => {
							if (!choice.selected) haptics.select();
							onChoose(choice.value);
						}}
						accessibilityRole="radio"
						accessibilityState={{ checked: choice.selected }}
						accessibilityLabel={choice.label}
						accessibilityHint={choice.description}
						testID={`permission-${choice.value}`}
						style={({ pressed }) => [styles.row, choice.selected ? styles.rowSelected : pressed && styles.rowPressed]}
					>
						<View style={styles.icon}>
							<Icon size={iconSize.md} strokeWidth={2} color={warning ? t.amber : choice.selected ? t.textPrimary : t.textSecondary} />
						</View>
						<View style={styles.text}>
							<Text style={[styles.title, warning && styles.warning]} numberOfLines={1}>{choice.label}</Text>
							{choice.description ? (
								<Text style={[styles.description, warning && styles.warningSoft]} numberOfLines={1}>{choice.description}</Text>
							) : null}
						</View>
					</Pressable>
				);
			})}
		</View>
	);
}

const buttonStyles = StyleSheet.create({
	target: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	dim: { opacity: 0.5 },
});

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// At most PICKER_WIDTH, and no wider than the overlay leaves (its stack
		// is capped to the composer), so a narrow screen truncates the rows'
		// text rather than spilling the list past the composer's edges.
		list: { width: PICKER_WIDTH, maxWidth: "100%", gap: space.hair },
		row: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.md,
			height: ROW_HEIGHT,
			paddingHorizontal: space.sm,
			borderRadius: ROW_RADIUS,
			borderCurve: "continuous",
		},
		// The mode in force: a low wash of the text colour, in place of a tick.
		rowSelected: { backgroundColor: t.accentTint },
		rowPressed: { backgroundColor: t.bgSubtle },
		// A fixed column, so every name starts at the same place.
		icon: { width: ICON_COLUMN, alignItems: "center", justifyContent: "center" },
		text: { flex: 1, minWidth: 0, gap: 1, justifyContent: "center" },
		title: { ...type.subheadline, fontFamily: type.headline.fontFamily, color: t.textPrimary },
		description: { ...type.footnote, color: t.textSecondary },
		warning: { color: t.amber },
		warningSoft: { color: t.amber, opacity: 0.8 },
	});
