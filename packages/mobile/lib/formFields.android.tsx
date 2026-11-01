import { Icon, IconButton, ListItem, OutlinedTextField, Switch, Text, useNativeState, type TextFieldKeyboardOptions } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, semantics, testID as testTag, toggleable, type ModifierConfig } from "@expo/ui/jetpack-compose/modifiers";
import { useState } from "react";
import type { FormPasswordFieldProps, FormTextFieldProps, FormToggleProps } from "./formFields.types";
import { useTheme } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { useNativeTextInput } from "./useNativeTextInput";

export type { FormPasswordFieldProps, FormTextFieldProps, FormToggleProps } from "./formFields.types";

// Android: Material 3 controls for a Compose column. Fields are outlined, with
// a floating label, and show their own error as supporting text.

function keyboardOptions(props: FormTextFieldProps): TextFieldKeyboardOptions {
	return {
		keyboardType: props.keyboardType === "url" ? "uri" : props.keyboardType === "number-pad" ? "number" : "text",
		capitalization: props.autoCapitalize ?? "sentences",
		autoCorrectEnabled: props.autoCorrect ?? true,
		imeAction: props.returnKeyType ?? "default",
	};
}

function keyboardActions(onSubmit: (() => void) | undefined) {
	return onSubmit ? { onDone: onSubmit, onNext: onSubmit, onGo: onSubmit } : undefined;
}

function fieldModifiers(testID: string | undefined, extra: ModifierConfig[] = []): ModifierConfig[] {
	return [fillMaxWidth(), ...extra, ...(testID ? [testTag(testID)] : [])];
}

export function FormTextField(props: FormTextFieldProps) {
	const text = useNativeState(props.value);
	const change = useNativeTextInput(props.value, text, props.onChangeText);
	return (
		<OutlinedTextField
			value={text}
			onValueChange={(next) => change(next)}
			singleLine
			enabled={props.editable !== false}
			autoFocus={props.autoFocus}
			maxLength={props.maxLength}
			isError={Boolean(props.error)}
			keyboardOptions={keyboardOptions(props)}
			keyboardActions={keyboardActions(props.onSubmitEditing)}
			modifiers={fieldModifiers(props.testID)}
		>
			{/* The label is the field's accessible name for TalkBack as well as its visible title. */}
			<OutlinedTextField.Label><Text>{props.label}</Text></OutlinedTextField.Label>
			{props.placeholder ? <OutlinedTextField.Placeholder><Text>{props.placeholder}</Text></OutlinedTextField.Placeholder> : null}
			{props.error ? <OutlinedTextField.SupportingText><Text>{props.error}</Text></OutlinedTextField.SupportingText> : null}
		</OutlinedTextField>
	);
}

/** A password field whose trailing icon shows or hides what was typed. */
export function FormPasswordField(props: FormPasswordFieldProps) {
	const [visible, setVisible] = useState(false);
	const text = useNativeState(props.value);
	const change = useNativeTextInput(props.value, text, props.onChangeText);
	return (
		<OutlinedTextField
			value={text}
			onValueChange={(next) => change(next)}
			singleLine
			enabled={props.editable !== false}
			autoFocus={props.autoFocus}
			isError={Boolean(props.error)}
			visualTransformation={visible ? "none" : "password"}
			keyboardOptions={{ keyboardType: "password", capitalization: "none", autoCorrectEnabled: false, imeAction: props.returnKeyType ?? "default" }}
			keyboardActions={keyboardActions(props.onSubmitEditing)}
			// Autofill reads the content type, which is how a saved password is offered here.
			modifiers={fieldModifiers(props.testID, [semantics({ contentType: "password" })])}
		>
			<OutlinedTextField.Label><Text>{props.label}</Text></OutlinedTextField.Label>
			{props.placeholder ? <OutlinedTextField.Placeholder><Text>{props.placeholder}</Text></OutlinedTextField.Placeholder> : null}
			<OutlinedTextField.TrailingIcon>
				<IconButton onClick={() => setVisible((shown) => !shown)} enabled={props.editable !== false}>
					<Icon source={materialDrawable(visible ? "visibility_off" : "visibility")} contentDescription={visible ? "Hide password" : "Show password"} />
				</IconButton>
			</OutlinedTextField.TrailingIcon>
			{props.error ? <OutlinedTextField.SupportingText><Text>{props.error}</Text></OutlinedTextField.SupportingText> : null}
		</OutlinedTextField>
	);
}

/** A switch row the whole width of the form; the row and the switch both toggle it. */
export function FormToggle({ label, value, onValueChange, disabled = false, testID }: FormToggleProps) {
	const t = useTheme();
	return (
		<ListItem
			colors={{ containerColor: "transparent", contentColor: t.textPrimary }}
			modifiers={[
				fillMaxWidth(),
				...(disabled ? [] : [toggleable(value, () => onValueChange(!value), { role: "switch" })]),
				...(testID ? [testTag(testID)] : []),
			]}
		>
			<ListItem.HeadlineContent><Text>{label}</Text></ListItem.HeadlineContent>
			<ListItem.TrailingContent>
				<Switch value={value} enabled={!disabled} onCheckedChange={onValueChange} />
			</ListItem.TrailingContent>
		</ListItem>
	);
}
