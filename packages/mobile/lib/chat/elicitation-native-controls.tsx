import { Button, Checkbox, Column, Host, ListItem, OutlinedTextField, RadioButton, Text, TextButton, useNativeState } from "@expo/ui/jetpack-compose";
import { defaultMinSize, fillMaxWidth, selectable, selectableGroup, testID, toggleable } from "@expo/ui/jetpack-compose/modifiers";
import { haptics } from "../haptics";
import { useTheme, useThemeState } from "../ThemeProvider";
import { font, type } from "../tokens";
import { useNativeTextInput } from "../useNativeTextInput";

// The Material 3 controls Android draws for a request's choices, typed answers
// and actions; iOS has its own SwiftUI file. Compose gives each row its 48dp
// target, ripple and radio or checkbox semantics for TalkBack, so nothing here
// draws a control by hand.

export type ElicitationChoice = { value: string; label: string; description?: string };
export type ElicitationChoiceListProps = {
	choices: ElicitationChoice[];
	selected(value: string): boolean;
	multi: boolean;
	onChange(value: string): void;
};
export type ElicitationTextFieldProps = {
	value: unknown;
	label: string;
	autoFocus?: boolean;
	numeric?: boolean;
	maxLength?: number;
	onChange(value: string | number): void;
};
export type ElicitationActionProps = {
	label: string;
	primary?: boolean;
	disabled?: boolean;
	/** The least room the button keeps, so a label that changes (Save, Saving…) does not move its neighbours. It still grows to fit its label. */
	width?: number;
	onPress(): void;
};

export function ElicitationChoiceList({ choices, selected, multi, onChange }: ElicitationChoiceListProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme}>
			{/* One answer of several is a radio group; several answers are independent checkboxes. */}
			<Column modifiers={[fillMaxWidth(), ...(multi ? [] : [selectableGroup()])]}>
				{choices.map((choice) => {
					const checked = selected(choice.value);
					const choose = () => { haptics.select(); onChange(choice.value); };
					return (
						<ListItem
							key={choice.value}
							// The row owns the touch and the semantics; the radio and checkbox
							// inside it only draw the state, as Material lists them.
							modifiers={[
								multi ? toggleable(checked, choose, { role: "checkbox" }) : selectable(checked, choose, "radioButton"),
								testID(`elicitation-choice-${choice.value}`),
							]}
							colors={{ containerColor: "transparent", contentColor: t.textPrimary, supportingContentColor: t.textSecondary }}
						>
							<ListItem.LeadingContent>
								{multi ? <Checkbox value={checked} /> : <RadioButton selected={checked} />}
							</ListItem.LeadingContent>
							<ListItem.HeadlineContent>
								<Text style={{ typography: "bodyLarge", fontFamily: font.regular }}>{choice.label}</Text>
							</ListItem.HeadlineContent>
							{choice.description ? (
								<ListItem.SupportingContent>
									<Text style={{ typography: "bodyMedium", fontFamily: font.regular }}>{choice.description}</Text>
								</ListItem.SupportingContent>
							) : null}
						</ListItem>
					);
				})}
			</Column>
		</Host>
	);
}

export function ElicitationTextField({ value, label, autoFocus, numeric, maxLength, onChange }: ElicitationTextFieldProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const controlledText = value === undefined ? "" : String(value);
	const text = useNativeState(controlledText);
	const changeText = useNativeTextInput(controlledText, text, onChange);
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme}>
			<OutlinedTextField
				value={text}
				autoFocus={autoFocus}
				singleLine
				maxLength={maxLength}
				keyboardOptions={{ keyboardType: numeric ? "decimal" : "text" }}
				onValueChange={(next) => changeText(numeric ? (next === "" ? "" : Number(next)) : next)}
				textStyle={{ color: t.textPrimary, fontFamily: font.regular, fontSize: type.subheadline.fontSize }}
				// The caret and selection take the colour the app's React Native fields use.
				textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
				colors={{
					cursorColor: t.highlight,
					focusedIndicatorColor: t.accent,
					unfocusedIndicatorColor: t.borderDefault,
					focusedPlaceholderColor: t.textFaint,
					unfocusedPlaceholderColor: t.textFaint,
				}}
				modifiers={[fillMaxWidth()]}
			>
				<OutlinedTextField.Placeholder>
					<Text style={{ fontFamily: font.regular, fontSize: type.subheadline.fontSize }}>{label}</Text>
				</OutlinedTextField.Placeholder>
			</OutlinedTextField>
		</Host>
	);
}

export function ElicitationAction({ label, primary, disabled, width, onPress }: ElicitationActionProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const press = () => { haptics.tap(); onPress(); };
	const modifiers = width ? [defaultMinSize({ minWidth: width })] : undefined;
	const title = <Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: font.semibold }}>{label}</Text>;
	// Sized by its label, so a long decision is never cut short.
	return (
		<Host matchContents colorScheme={scheme}>
			{primary ? (
				<Button onClick={press} enabled={!disabled} colors={{ containerColor: t.accent, contentColor: t.onAccent }} modifiers={modifiers}>
					{title}
				</Button>
			) : (
				<TextButton onClick={press} enabled={!disabled} colors={{ contentColor: t.accent }} modifiers={modifiers}>
					{title}
				</TextButton>
			)}
		</Host>
	);
}
