import { Button, Divider, HStack, LabeledContent, SecureField, Section, Text, TextField, Toggle, useNativeState } from "@expo/ui/swift-ui";
import {
	accessibilityLabel,
	autocorrectionDisabled,
	background,
	buttonStyle,
	disabled as disabledModifier,
	foregroundStyle,
	frame,
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
import { useState, type ReactNode } from "react";
import type { FormPasswordFieldProps, FormTextFieldProps, FormTextFieldRowProps, FormToggleProps } from "./formFields.types";
import type { Theme } from "./theme";
import { secondaryForeground } from "./swiftUIStyles";
import { useTheme } from "./ThemeProvider";
import { space } from "./tokens";
import { useNativeTextInput } from "./useNativeTextInput";

export type { FormPasswordFieldProps, FormTextFieldProps, FormTextFieldRowProps, FormToggleProps } from "./formFields.types";

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

/**
 * A section footer: the system's footnote styling (secondary), or the error
 * colour for a problem with the section's fields. A hint and an error draw with
 * the same modifier, so switching between them changes only its style. With
 * nothing to say it draws nothing: no empty row, and nothing for VoiceOver.
 */
export function FormFooter({ children, error = false }: { children?: string; error?: boolean }) {
	const t = useTheme();
	if (!children) return null;
	return <Text modifiers={[error ? foregroundStyle(t.red) : secondaryForeground]}>{children}</Text>;
}

/**
 * A form section whose footer is always mounted. @expo/ui builds a different
 * SwiftUI Section with and without a footer, so adding or removing one rebuilds
 * the section's fields, and the field being typed in loses focus and the
 * keyboard. Here the footer slot is always there and only what it holds changes;
 * @expo/ui keeps the same Section for an empty slot, which draws nothing. Use it
 * for any footer that can come and go (formSection.source.test.ts enforces it).
 *
 * The footer shows `error` in red when there is one, otherwise `hint`: one pair
 * of props, so the text and its colour can't drift apart.
 */
export function FormSection({ title, error, hint, modifiers, children }: { title?: string; error?: string; hint?: string; modifiers?: ModifierConfig[]; children: ReactNode }) {
	return (
		<Section title={title} footer={<FormFooter error={Boolean(error)}>{error || hint}</FormFooter>} modifiers={modifiers}>
			{children}
		</Section>
	);
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

type TextContentType = Parameters<typeof textContentType>[0];

/** The bare input, without its row: a fixed `width` frames it to that size. */
function InlineTextField(props: FormTextFieldProps & { textContentType?: TextContentType }) {
	const t = useTheme();
	const text = useNativeState(props.value);
	const change = useNativeTextInput(props.value, text, props.onChangeText);
	const modifiers = inputModifiers(t, props, props.textContentType);
	if (typeof props.width === "number") modifiers.push(frame({ width: props.width }));
	return (
		<TextField
			text={text}
			placeholder={props.placeholder}
			autoFocus={props.autoFocus}
			maxLength={props.maxLength}
			testID={props.testID}
			onTextChange={(next) => change(next)}
			modifiers={modifiers}
		/>
	);
}

export function FormTextField(props: FormTextFieldProps & { textContentType?: TextContentType }) {
	return (
		<LabeledContent label={props.label}>
			<InlineTextField {...props} />
		</LabeledContent>
	);
}

/** One labelled row holding several inputs, split by dividers; each keeps its own label for VoiceOver. */
export function FormTextFieldRow({ label, fields }: FormTextFieldRowProps) {
	return (
		<LabeledContent label={label}>
			<HStack spacing={space.sm}>
				{fields.flatMap((field, index) => [
					...(index ? [<Divider key={`divider-${index}`} />] : []),
					<InlineTextField key={field.testID ?? field.label} {...field} />,
				])}
			</HStack>
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
			// Named on the switch itself too, as the text fields are, so VoiceOver
			// never lands on it without the row's title.
			modifiers={[tint(t.green), disabledModifier(disabled), accessibilityLabel(label)]}
		/>
	);
}
