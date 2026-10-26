import { Host } from "@expo/ui";
import { Form, Section } from "@expo/ui/swift-ui";
import { FormFooter, formModifiers, FormTextField, sectionModifiers } from "./formFields";
import { MACHINE_NAME_HINT, type MachineNameFormProps } from "./MachineNameForm.types";
import { MachineSymbolMenu } from "./MachineSymbolMenu";
import { useTheme, useThemeState } from "./ThemeProvider";

export { MACHINE_NAME_HINT, type MachineNameFormProps } from "./MachineNameForm.types";

// iOS: a Form under the sheet's native bar, which holds the title, Cancel and
// Save (app/sheets/machine-name.tsx). Return on the name saves too.
export function MachineNameForm({ name, onChangeName, symbol, onSelectSymbol, iconHint, onSave }: MachineNameFormProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host style={{ flex: 1 }} colorScheme={scheme}>
			<Form modifiers={formModifiers(t)}>
				<Section footer={<FormFooter>{MACHINE_NAME_HINT}</FormFooter>} modifiers={sectionModifiers(t)}>
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
				</Section>
				<Section footer={iconHint ? <FormFooter>{iconHint}</FormFooter> : undefined} modifiers={sectionModifiers(t)}>
					<MachineSymbolMenu selected={symbol} onSelect={onSelectSymbol} testID="machine-name-symbol" />
				</Section>
			</Form>
		</Host>
	);
}
