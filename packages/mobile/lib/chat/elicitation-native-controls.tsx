import { Button, Checkbox, Column, Host, ListItem, OutlinedTextField, RadioButton, Text, TextButton, useNativeState } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, selectable, selectableGroup, testID, toggleable, width as fixedWidth } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet, View } from "react-native";
import { haptics } from "../haptics";
import { useTheme, useThemeState } from "../ThemeProvider";
import { font, type } from "../tokens";
import { useNativeTextInput } from "../useNativeTextInput";
import { useActionFit } from "./elicitationActionFit";

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
	numeric?: boolean;
	maxLength?: number;
	onChange(value: string | number): void;
};
export type ElicitationActionProps = {
	label: string;
	primary?: boolean;
	disabled?: boolean;
	/** The caller owns the press's haptic: one when the work lands (success or error), or none for a step between questions. The press itself stays silent. */
	reportsOutcome?: boolean;
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

export function ElicitationTextField({ value, label, numeric, maxLength, onChange }: ElicitationTextFieldProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const controlledText = value === undefined ? "" : String(value);
	const text = useNativeState(controlledText);
	const changeText = useNativeTextInput(controlledText, text, onChange);
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme}>
			<OutlinedTextField
				value={text}
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

export function ElicitationAction({ label, primary, disabled, reportsOutcome, onPress }: ElicitationActionProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const fit = useActionFit(label);
	const button = (cap: number | undefined, press?: () => void) => {
		const modifiers = cap !== undefined ? [fixedWidth(cap)] : undefined;
		const title = <Text style={{ typography: "labelLarge", fontFamily: font.semibold, textAlign: "center" }}>{label}</Text>;
		return primary ? (
			<Button onClick={press} enabled={!disabled} colors={{ containerColor: t.accent, contentColor: t.onAccent }} modifiers={modifiers}>
				{title}
			</Button>
		) : (
			<TextButton onClick={press} enabled={!disabled} colors={{ contentColor: t.accent }} modifiers={modifiers}>
				{title}
			</TextButton>
		);
	};
	// Sized by its label, so a long decision is never cut short, and wrapped only
	// when that is wider than the row.
	return (
		<>
			<View
				pointerEvents="none"
				accessibilityElementsHidden
				importantForAccessibility="no-hide-descendants"
				style={styles.measure}
				onLayout={fit.onRowLayout}
			>
				<Host matchContents colorScheme={scheme} onLayoutContent={fit.onLabelLayout}>
					{button(undefined)}
				</Host>
			</View>
			<Host matchContents colorScheme={scheme} style={styles.action}>
				{button(fit.cap, () => {
					if (!reportsOutcome) haptics.tap();
					onPress();
				})}
			</Host>
		</>
	);
}

const styles = StyleSheet.create({
	// Spans the row behind the actions, unseen and untouchable: it only measures.
	measure: { position: "absolute", left: 0, right: 0, top: 0, bottom: 0, opacity: 0 },
	action: { maxWidth: "100%" },
});
