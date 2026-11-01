import { Button, HStack, LabeledContent, SecureField, Text, TextField, Toggle, useNativeState } from "@expo/ui/swift-ui";
import {
	accessibilityLabel,
	autocorrectionDisabled,
	background,
	buttonStyle,
	disabled as disabledModifier,
	foregroundStyle,
	keyboardType as keyboardTypeModifier,
	labelStyle,
	listRowBackground,
	multilineTextAlignment,
	onSubmit,
	scrollContentBackground,
	scrollDismissesKeyboard,
	submitLabel,
	textContentType,
	textInputAutocapitalization,
	tint,
	type ModifierConfig,
} from "@expo/ui/swift-ui/modifiers";
import { useState } from "react";
import type { FormPasswordFieldProps, FormTextFieldProps, FormToggleProps } from "./formFields.types";
import type { Theme } from "./theme";
import { useTheme } from "./ThemeProvider";
import { space } from "./tokens";
import { useNativeTextInput } from "./useNativeTextInput";

export type { FormPasswordFieldProps, FormTextFieldProps, FormToggleProps } from "./formFields.types";

// iOS: rows of a SwiftUI Form. Each field is a labelled row, its value aligned
// to the trailing edge as Settings lays out an editable value.

/**
 * A `Form` in the theme's surfaces rather than the system grouped colours, so a
 * named theme carries into native settings pages. Actions take the accent.
 */
export function formModifiers(t: Theme): ModifierConfig[] {
	return [scrollContentBackground("hidden"), background(t.bgBase), tint(t.accent), scrollDismissesKeyboard("interactively")];
}

/** A form section's rows on the theme's card surface. */
export function sectionModifiers(t: Theme): ModifierConfig[] {
	return [listRowBackground(t.bgElevated)];
}

/** A section footer: the system's footnote styling, or the error colour for a problem with the section's fields. */
export function FormFooter({ children, error = false }: { children: string; error?: boolean }) {
	const t = useTheme();
	return <Text modifiers={error ? [foregroundStyle(t.red)] : undefined}>{children}</Text>;
}

function inputModifiers(t: Theme, props: FormTextFieldProps, contentType?: Parameters<typeof textContentType>[0]): ModifierConfig[] {
	const modifiers: ModifierConfig[] = [
		multilineTextAlignment("trailing"),
		// The caret and selection take the tint; the app's other fields use this colour for both.
		tint(t.highlight),
		accessibilityLabel(props.label),
	];
	if (props.keyboardType === "url") modifiers.push(keyboardTypeModifier("url"));
	// SwiftUI's `numeric` is UIKit's number pad.
	if (props.keyboardType === "number-pad") modifiers.push(keyboardTypeModifier("numeric"));
	if (props.autoCapitalize === "none") modifiers.push(textInputAutocapitalization("never"));
	else if (props.autoCapitalize) modifiers.push(textInputAutocapitalization(props.autoCapitalize));
	if (props.autoCorrect === false) modifiers.push(autocorrectionDisabled(true));
	if (props.returnKeyType) modifiers.push(submitLabel(props.returnKeyType));
	if (props.onSubmitEditing) modifiers.push(onSubmit(props.onSubmitEditing));
	if (contentType) modifiers.push(textContentType(contentType));
	if (props.editable === false) modifiers.push(disabledModifier(true));
	return modifiers;
}

export function FormTextField(props: FormTextFieldProps & { textContentType?: Parameters<typeof textContentType>[0] }) {
	const t = useTheme();
	const text = useNativeState(props.value);
	const change = useNativeTextInput(props.value, text, props.onChangeText);
	return (
		<LabeledContent label={props.label}>
			<TextField
				text={text}
				placeholder={props.placeholder}
				autoFocus={props.autoFocus}
				maxLength={props.maxLength}
				testID={props.testID}
				onTextChange={(next) => change(next)}
				modifiers={inputModifiers(t, props, props.textContentType)}
			/>
		</LabeledContent>
	);
}

/**
 * A password row with a reveal button. Both faces share one text state, so
 * showing the password keeps what was typed.
 */
export function FormPasswordField(props: FormPasswordFieldProps) {
	const t = useTheme();
	const [visible, setVisible] = useState(false);
	const text = useNativeState(props.value);
	const change = useNativeTextInput(props.value, text, props.onChangeText);
	const field = { ...props, autoCapitalize: "none", autoCorrect: false } as const;
	const shared = {
		text,
		placeholder: props.placeholder,
		autoFocus: props.autoFocus,
		testID: props.testID,
		onTextChange: (next: string) => change(next),
		modifiers: inputModifiers(t, field, "password"),
	};
	return (
		<LabeledContent label={props.label}>
			<HStack spacing={space.sm}>
				{visible ? <TextField {...shared} /> : <SecureField {...shared} />}
				{/* Borderless, so only the glyph toggles; a plain button in a form row takes the whole row's taps. */}
				<Button
					systemImage={visible ? "eye.slash" : "eye"}
					label={visible ? "Hide password" : "Show password"}
					onPress={() => setVisible((shown) => !shown)}
					modifiers={[labelStyle("iconOnly"), buttonStyle("borderless"), tint(t.textSecondary), disabledModifier(props.editable === false)]}
				/>
			</HStack>
		</LabeledContent>
	);
}

export function FormToggle({ label, value, onValueChange, disabled = false, testID }: FormToggleProps) {
	const t = useTheme();
	return (
		<Toggle
			label={label}
			isOn={value}
			onIsOnChange={onValueChange}
			testID={testID}
			// The form's tint is the hue-less accent, which would draw an on switch
			// near white in the dark theme; on stays the switch's green.
			modifiers={[tint(t.green), disabledModifier(disabled)]}
		/>
	);
}
