import Hand from "lucide-react-native/icons/hand";
import PencilLine from "lucide-react-native/icons/pencil-line";
import ShieldAlert from "lucide-react-native/icons/shield-alert";
import ShieldCheck from "lucide-react-native/icons/shield-check";
import { memo, useEffect, type ComponentType } from "react";
import { StyleSheet, Text, View } from "react-native";
import { Pressable } from "react-native-gesture-handler";
import { COMPOSER_RADIUS } from "./chat/composerStyles";
import type { ApprovalMode } from "./chat/types";
import { COMPOSER_CONTROL_RISE, ComposerOverlay, OverlayHeading } from "./composer-overlay";
import { useWarmAfterIdle } from "./effort-slider-host";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, touchTarget, type } from "./tokens";
import { markTouchInside } from "./touchOutside";
import { useTouchDownPress } from "./useTouchDownPress";
import { useEffortSliderOpen, useEffortSliderSwitchOpen, type EffortSliderSwitch } from "./useEffortSlider";

/*
 * The chat's permission mode, picked the way its effort is: the composer's
 * button lays the modes over the composer, headed "Permissions › Auto" as the
 * slider is, as a grid of large buttons two to a row, the mode's icon over
 * its name. A stack of four tall rows reached far up the screen, out of the
 * thumb's way; the grid is centred on the composer's control row, where the
 * thumb already is. The one in force sits in a soft wash of the text colour
 * rather than being ticked; what each mode lets the agent do is its button's
 * screen reader hint.
 */

export type PermissionChoice = {
	value: string;
	label: string;
	description?: string;
	selected: boolean;
	/** The mode whose icon and colour it takes (see approvalLook); none for an agent's own tier. */
	look?: ApprovalMode;
};

// Every button this tall: a full thumb's press, whatever its name.
const TILE_HEIGHT = 64;
const TILE_GAP = space.xs;
// The grid is laid over the composer's pill, its full width, and the
// buttons' corners are concentric with the pill's, an even inset in.
const LIST_INSET = space.xs;
const TILE_RADIUS = COMPOSER_RADIUS - LIST_INSET;

/** The grid's height for `count` modes, two to a row. */
export function permissionGridHeight(count: number): number {
	const rows = Math.max(1, Math.ceil(count / 2));
	return rows * TILE_HEIGHT + (rows - 1) * TILE_GAP + LIST_INSET * 2;
}

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
	const warm = useWarmAfterIdle();
	if (!(open || warm)) return null;
	return (
		<PermissionPickerOverlay
			visible={open}
			onDismiss={() => slider.setOpen(false)}
			choices={choices}
			onChoose={(value) => {
				slider.setOpen(false);
				onChoose(value);
			}}
		/>
	);
});

export function PermissionPickerOverlay({ visible = true, onDismiss, choices, onChoose }: { visible?: boolean; onDismiss?: () => void; choices: readonly PermissionChoice[]; onChoose: (value: string) => void }) {
	// The grid's middle on the composer's control row, the heading above it.
	const keysOverlap = permissionGridHeight(choices.length) / 2 - COMPOSER_CONTROL_RISE;
	return (
		<ComposerOverlay overKeyboard keysOverlap={keysOverlap} onDismiss={onDismiss} visible={visible} fill>
			<PermissionPicker choices={choices} onChoose={onChoose} />
		</ComposerOverlay>
	);
}

export function PermissionPicker({ choices, onChoose }: { choices: readonly PermissionChoice[]; onChoose: (value: string) => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const current = choices.find((choice) => choice.selected);
	// Two to a row; an odd one out takes the last row to itself.
	const rows = Array.from({ length: Math.ceil(choices.length / 2) }, (_, row) => choices.slice(row * 2, row * 2 + 2));
	return (
		<View style={styles.stack}>
			{current ? <OverlayHeading title="Permissions" value={current.label} /> : null}
			<View style={styles.grid} accessibilityRole="radiogroup" accessibilityLabel="Permissions" testID="permission-picker">
				{rows.map((pair) => (
					<View key={pair[0].value} style={styles.row}>
						{pair.map((choice) => {
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
									style={({ pressed }) => [styles.tile, choice.selected ? styles.tileSelected : pressed && styles.tilePressed]}
								>
									<Icon size={iconSize.md} strokeWidth={2} color={warning ? t.amber : choice.selected ? t.textPrimary : t.textSecondary} />
									<Text style={[styles.title, !choice.selected && styles.titleIdle, warning && styles.warning]} numberOfLines={1} adjustsFontSizeToFit minimumFontScale={0.85}>{choice.label}</Text>
								</Pressable>
							);
						})}
					</View>
				))}
			</View>
		</View>
	);
}

const buttonStyles = StyleSheet.create({
	target: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	dim: { opacity: 0.5 },
});

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		stack: { gap: space.xs, alignItems: "center", alignSelf: "stretch" },
		grid: { alignSelf: "stretch", padding: LIST_INSET, gap: TILE_GAP },
		row: { flexDirection: "row", gap: TILE_GAP },
		tile: {
			flex: 1,
			height: TILE_HEIGHT,
			alignItems: "center",
			justifyContent: "center",
			gap: space.xxs,
			paddingHorizontal: space.sm,
			borderRadius: TILE_RADIUS,
			borderCurve: "continuous",
			backgroundColor: t.bgSubtle,
			// Every button outlined alike, so the one in force gains no size.
			borderWidth: 1.5,
			borderColor: "transparent",
		},
		// The mode in force: a low wash of the text colour, outlined, in place of a tick.
		tileSelected: { backgroundColor: t.accentTint, borderColor: t.accentBorder },
		tilePressed: { backgroundColor: t.bgElevatedHover },
		title: { ...type.subheadline, fontFamily: type.headline.fontFamily, color: t.textPrimary },
		titleIdle: { color: t.textSecondary },
		warning: { color: t.amber },
	});
