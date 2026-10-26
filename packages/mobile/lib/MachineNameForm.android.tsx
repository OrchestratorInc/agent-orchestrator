import { Host } from "@expo/ui/jetpack-compose";
import { StyleSheet, Text, View } from "react-native";
import { FormTextField } from "./formFields";
import { machineSymbolLabels } from "./machineAppearance";
import { MACHINE_NAME_HINT, type MachineNameFormProps } from "./MachineNameForm.types";
import { MachineSymbolPicker } from "./MachineSymbolPicker";
import type { Theme } from "./theme";
import { useThemeState, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";
import { Button, SheetScreen } from "./ui";

export { MACHINE_NAME_HINT, type MachineNameFormProps } from "./MachineNameForm.types";

// Android: a Material bottom sheet's body, with its title and Save button in
// it, an outlined name field and the grid of machine shapes.
export function MachineNameForm({ name, onChangeName, symbol, onSelectSymbol, iconHint, canSave, onSave }: MachineNameFormProps) {
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	return (
		<SheetScreen title="Name this machine" subtitle={MACHINE_NAME_HINT}>
			<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.field}>
				<FormTextField
					label="Name"
					value={name}
					onChangeText={onChangeName}
					placeholder="Studio Mac"
					maxLength={48}
					autoFocus
					returnKeyType="done"
					onSubmitEditing={onSave}
					testID="machine-name-input"
				/>
			</Host>
			<View style={styles.field}>
				<Text accessibilityRole="header" style={styles.label}>
					Icon <Text style={styles.labelValue}>· {machineSymbolLabels[symbol]}</Text>
				</Text>
				<MachineSymbolPicker testIDPrefix="machine-name-symbol" selected={symbol} onSelect={onSelectSymbol} />
				{iconHint ? <Text testID="machine-name-symbol-shared" style={styles.hint}>{iconHint}</Text> : null}
			</View>
			<Button title="Save machine" disabled={!canSave} onPress={onSave} style={styles.button} />
		</SheetScreen>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	field: { marginTop: space.lg },
	label: { fontFamily: "Geist_500Medium", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500", marginBottom: space.sm },
	labelValue: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontWeight: "400" },
	hint: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.sm },
	button: { marginTop: space.lg },
});
