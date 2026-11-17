import { Host, Switch } from "@expo/ui";
import { StyleSheet, Text, View } from "react-native";
import type { Theme } from "../theme";
import { useThemedStyles, useThemeState } from "../ThemeProvider";
import { space, type } from "../tokens";
import { ElicitationChoiceList, ElicitationTextField } from "./elicitation-native-controls";
import { humanizeInputName, inputOptions, toggleInputValue } from "./elicitationModel";
import type { InputProperty } from "./types";

/**
 * One field of an agent's request for input, in the control its schema calls
 * for: a switch for a yes/no, a choice list for options, a text field otherwise.
 */
export function ElicitationInputField({ name, property, required, value, onChange }: { name: string; property: InputProperty; required: boolean; value: unknown; onChange(value: unknown): void }) {
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	const label = `${property.title || humanizeInputName(name)}${required ? " *" : ""}`;
	if (property.type === "boolean") {
		const checked = Boolean(value);
		// The label and the switch are one control to a screen reader, as a native
		// toggle row is. The hosted switch has no label of its own on Android, so
		// it is hidden and the row answers to activate instead.
		return <View
			accessible
			accessibilityRole="switch"
			accessibilityLabel={label}
			accessibilityState={{ checked }}
			accessibilityActions={[{ name: "activate" }]}
			onAccessibilityAction={(event) => { if (event.nativeEvent.actionName === "activate") onChange(!checked); }}
			style={styles.switchRow}
		>
			<Text style={[styles.inputLabel, styles.switchLabel]}>{label}</Text>
			<View accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
				{/* No tint: the switch keeps the system's colours, as Settings' does. */}
				<Host style={styles.switchHost} colorScheme={scheme}>
					<Switch value={checked} onValueChange={onChange} />
				</Host>
			</View>
		</View>;
	}
	const options = inputOptions(property);
	if (options.length) {
		const multi = property.type === "array";
		return <View style={styles.field}>
			<Text style={styles.inputEyebrow}>{label}</Text>
			{property.description ? <Text style={styles.inputQuestion}>{property.description}</Text> : null}
			<ElicitationChoiceList choices={options} multi={multi} selected={(choice) => multi ? Array.isArray(value) && value.includes(choice) : value === choice} onChange={(choice) => onChange(multi ? toggleInputValue(Array.isArray(value) ? value : [], choice) : choice)} />
		</View>;
	}
	return <View style={styles.field}>
		<Text style={styles.inputLabel}>{label}</Text>
		{property.description ? <Text style={styles.inputHint}>{property.description}</Text> : null}
		<ElicitationTextField label={label} value={value} numeric={property.type === "number" || property.type === "integer"} maxLength={property.maxLength} onChange={onChange} />
	</View>;
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		field: { gap: space.xs },
		inputEyebrow: { fontFamily: "DMSans_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		inputQuestion: { fontFamily: "DMSans_500Medium", color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "500" },
		inputLabel: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		inputHint: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		switchRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: space.md },
		switchLabel: { flex: 1 },
		// The system switch's own footprint, as Settings hosts it.
		switchHost: { width: 54, height: 34 },
	});
