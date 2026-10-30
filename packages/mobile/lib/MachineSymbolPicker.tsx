import { Pressable, StyleSheet, View } from "react-native";
import { haptics } from "./haptics";
import { MachineAvatar } from "./MachineIdentity";
import { machineAppearancePalette, machineSymbolLabels, type MachineSymbol } from "./machineAppearance";
import { usePressFeedback } from "./pressFeedback";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { radius, space, stateOpacity, touchTarget } from "./tokens";

// The selected cell is ringed as well as filled, so the choice reads by shape
// and not by a faint tint alone.
const SELECTED_RING_WIDTH = 2;

/** The machine icon choices, as a grid of system glyphs. */
export function MachineSymbolPicker({
	selected,
	onSelect,
	testIDPrefix,
	disabled = false,
}: {
	selected: MachineSymbol;
	onSelect: (symbol: MachineSymbol) => void;
	/** Each choice is `${testIDPrefix}-${symbol}`. */
	testIDPrefix: string;
	disabled?: boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button");
	return (
		<View accessibilityRole="radiogroup" style={[styles.choices, disabled && styles.disabled]}>
			{machineAppearancePalette.symbols.map((symbol) => {
				const isSelected = symbol === selected;
				return (
					<Pressable
						key={symbol}
						testID={`${testIDPrefix}-${symbol}`}
						accessibilityRole="radio"
						accessibilityLabel={machineSymbolLabels[symbol]}
						accessibilityState={{ selected: isSelected, checked: isSelected, disabled }}
						disabled={disabled}
						android_ripple={feedback.android_ripple}
						onPress={() => {
							if (isSelected) return;
							haptics.select();
							onSelect(symbol);
						}}
						style={({ pressed }) => [styles.choice, isSelected && styles.selected, pressed && feedback.pressed]}
					>
						<MachineAvatar appearance={{ symbol }} size={32} color={isSelected ? t.accent : t.textSecondary} />
					</Pressable>
				);
			})}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		choices: { flexDirection: "row", flexWrap: "wrap", gap: space.sm },
		choice: {
			width: touchTarget,
			height: touchTarget,
			alignItems: "center",
			justifyContent: "center",
			borderWidth: SELECTED_RING_WIDTH,
			borderColor: "transparent",
			borderRadius: radius.md,
			borderCurve: "continuous",
			overflow: "hidden",
		},
		selected: { borderColor: t.accent, backgroundColor: t.accentTint },
		disabled: { opacity: stateOpacity.disabled },
	});
