import { Host } from "@expo/ui";
import { Button, Divider, HStack, Image, Spacer, Text, TextField, VStack, useNativeState } from "@expo/ui/swift-ui";
import {
	accessibilityAddTraits,
	accessibilityHidden,
	buttonStyle,
	controlSize,
	disabled as disabledModifier,
	fixedSize,
	font,
	foregroundStyle,
	frame,
	multilineTextAlignment,
	padding,
	textFieldStyle,
	tint,
} from "@expo/ui/swift-ui/modifiers";
import { glassPanel } from "../glass";
import { haptics } from "../haptics";
import { useTheme, useThemeState } from "../ThemeProvider";
import type { ElicitationActionProps, ElicitationChoiceListProps, ElicitationTextFieldProps } from "./elicitation-native-controls";
import { iconSize, touchTarget } from "../tokens";
import { useNativeTextInput } from "../useNativeTextInput";

export function ElicitationChoiceList({ choices, selected, multi, onChange }: ElicitationChoiceListProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return <Host matchContents={{ vertical: true }} style={{ width: "100%" }} colorScheme={scheme}>
		<VStack alignment="leading" spacing={0} modifiers={[frame({ maxWidth: 1000, alignment: "leading" })]}>
			{choices.map((choice, index) => {
				const checked = selected(choice.value);
				return <VStack key={choice.value} alignment="leading" spacing={0} modifiers={[frame({ maxWidth: 1000, alignment: "leading" })]}>
					<Button
						onPress={() => { haptics.select(); onChange(choice.value); }}
						// VoiceOver reads the chosen row as selected, the way a native
						// choice list does; the glyph says it only by its shape.
						modifiers={[buttonStyle("plain"), frame({ maxWidth: 1000, minHeight: 56, alignment: "leading" }), ...(checked ? [accessibilityAddTraits(["isSelected"])] : [])]}
					>
						<HStack alignment="top" spacing={10} modifiers={[frame({ maxWidth: 1000, alignment: "leading" }), padding({ vertical: 8, trailing: 4 })]}>
							<Image systemName={checked ? (multi ? "checkmark.circle.fill" : "largecircle.fill.circle") : "circle"} size={iconSize.md} color={checked ? t.accent : t.textTertiary} modifiers={[padding({ top: 1 }), accessibilityHidden(true)]} />
							<VStack alignment="leading" spacing={2} modifiers={[frame({ maxWidth: 1000, alignment: "leading" })]}>
								<Text modifiers={[font({ textStyle: "subheadline", weight: "semibold" }), foregroundStyle(checked ? t.accent : t.textPrimary), multilineTextAlignment("leading"), fixedSize({ horizontal: false, vertical: true })]}>{choice.label}</Text>
								{choice.description ? <Text modifiers={[font({ textStyle: "footnote" }), foregroundStyle({ type: "hierarchical", style: "secondary" }), multilineTextAlignment("leading"), fixedSize({ horizontal: false, vertical: true })]}>{choice.description}</Text> : null}
							</VStack>
							<Spacer minLength={0} />
						</HStack>
					</Button>
					{index < choices.length - 1 ? <Divider modifiers={[padding({ leading: 28 })]} /> : null}
				</VStack>;
			})}
		</VStack>
	</Host>;
}

export function ElicitationTextField({ value, label, numeric, maxLength, onChange }: ElicitationTextFieldProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const controlledText = value === undefined ? "" : String(value);
	const text = useNativeState(controlledText);
	const changeText = useNativeTextInput(controlledText, text, onChange);
	// Sized by its text, so the field grows with Dynamic Type rather than clipping it.
	return <Host matchContents={{ vertical: true }} style={{ width: "100%" }} colorScheme={scheme}>
		<TextField
			text={text}
			placeholder={label}
			maxLength={maxLength}
			onTextChange={(next) => {
				const changed = numeric ? (next === "" ? "" : Number(next)) : next;
				changeText(changed);
			}}
			modifiers={[
				textFieldStyle("plain"),
				frame({ maxWidth: 1000, minHeight: touchTarget, alignment: "leading" }),
				padding({ horizontal: 14 }),
				glassPanel(),
				font({ textStyle: "callout" }),
				// The caret and selection take the tint; this is the colour the app's
				// React Native fields use for them.
				tint(t.highlight),
			]}
		/>
	</Host>;
}

export function ElicitationAction({ label, primary, disabled, width, onPress }: ElicitationActionProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	// The host takes the button's own size, so a long decision is never cut
	// short. `width` only reserves a slot the button may outgrow.
	return <Host matchContents colorScheme={scheme}>
		<Button
			label={label}
			onPress={() => { haptics.tap(); onPress(); }}
			modifiers={[
				buttonStyle(primary ? "borderedProminent" : "plain"),
				controlSize("regular"),
				...(width ? [frame({ minWidth: width })] : []),
				tint(primary ? t.accent : t.textSecondary),
				disabledModifier(disabled),
			]}
		/>
	</Host>;
}
