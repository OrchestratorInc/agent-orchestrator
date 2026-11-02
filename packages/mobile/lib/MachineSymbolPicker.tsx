import { Pressable, StyleSheet, View } from "react-native";
import { rowDividerWidth } from "./divider";
import { haptics } from "./haptics";
import { MachineAvatar } from "./MachineIdentity";
import { machineAppearancePalette, machineSymbolLabels, type MachineSymbol } from "./machineAppearance";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { press, radius, space } from "./tokens";

/** The machine icon choices, shared by pairing and the Environments editor. */
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
	const styles = useThemedStyles(makeStyles);
	return (
		<View accessibilityRole="radiogroup" style={styles.choices}>
			{machineAppearancePalette.symbols.map((symbol) => {
				const isSelected = symbol === selected;
				return (
					<Pressable
						key={symbol}
						testID={`${testIDPrefix}-${symbol}`}
						accessibilityRole="radio"
						accessibilityLabel={machineSymbolLabels[symbol]}
						accessibilityState={{ selected: isSelected, disabled }}
						disabled={disabled}
						onPress={() => {
							if (isSelected) return;
							haptics.select();
							onSelect(symbol);
						}}
						style={({ pressed }) => [styles.choice, isSelected && styles.selected, pressed && styles.pressed]}
					>
						<MachineAvatar appearance={{ symbol }} size={32} />
					</Pressable>
				);
			})}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		choices: { flexDirection: "row", flexWrap: "wrap", gap: space.sm },
		choice: { width: 44, height: 44, alignItems: "center", justifyContent: "center", borderWidth: rowDividerWidth, borderColor: "transparent", borderRadius: radius.md, borderCurve: "continuous" },
		selected: { borderColor: t.accentBorder, backgroundColor: t.accentTint },
		pressed: { opacity: press.opacity },
	});
