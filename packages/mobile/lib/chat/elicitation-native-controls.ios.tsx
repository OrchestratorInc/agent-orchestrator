import { Host } from "@expo/ui";
import { Button, Divider, HStack, Image, Spacer, Text, TextField, VStack, useNativeState } from "@expo/ui/swift-ui";
import {
	accessibilityAddTraits,
	accessibilityHidden,
	buttonStyle,
	contentShape,
	controlSize,
	disabled as disabledModifier,
	fixedSize,
	font,
	foregroundStyle,
	frame,
	multilineTextAlignment,
	padding,
	shapes,
	textFieldStyle,
	tint,
	type ViewModifier,
} from "@expo/ui/swift-ui/modifiers";
import { glassPanel } from "../glass";
import { haptics } from "../haptics";
import { useTheme, useThemeState } from "../ThemeProvider";
import type { ElicitationActionProps, ElicitationChoiceListProps, ElicitationTextFieldProps } from "./elicitation-native-controls";
import { useActionFit } from "./elicitationActionFit";
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

// Spans the row behind the actions, unseen and untouchable: it only measures.
const measureStyle = { position: "absolute", left: 0, right: 0, top: 0, bottom: 0, opacity: 0 } as const;

export function ElicitationAction({ label, primary, disabled, width, reportsOutcome, onPress }: ElicitationActionProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const fit = useActionFit(label);
	const button = (cap: number | undefined, extra: ViewModifier[], press?: () => void) => <Button
		onPress={press}
		modifiers={[
			buttonStyle(primary ? "borderedProminent" : "plain"),
			// The large prominent button is at least the 44pt target. A plain one is
			// only as tappable as its label, so the label carries the target.
			controlSize(primary ? "large" : "regular"),
			...(cap !== undefined ? [frame({ width: cap })] : width ? [frame({ minWidth: width })] : []),
			multilineTextAlignment("center"),
			tint(primary ? t.accent : t.textSecondary),
			disabledModifier(disabled),
			...extra,
		]}
	>
		<Text modifiers={primary ? [] : [frame({ minWidth: touchTarget, minHeight: touchTarget }), contentShape(shapes.rectangle())]}>{label}</Text>
	</Button>;
	// The button takes its label's own width, so a long decision is never cut
	// short, and wraps only when that is wider than the row. `width` reserves a
	// slot the button may outgrow.
	return <>
		<Host
			pointerEvents="none"
			accessibilityElementsHidden
			importantForAccessibility="no-hide-descendants"
			style={measureStyle}
			colorScheme={scheme}
			onLayout={fit.onRowLayout}
			onLayoutContent={fit.onLabelLayout}
		>
			{button(undefined, [fixedSize(), accessibilityHidden(true)])}
		</Host>
		<Host matchContents style={{ maxWidth: "100%" }} colorScheme={scheme}>
			{button(fit.cap, [], () => {
				if (!reportsOutcome) haptics.tap();
				onPress();
			})}
		</Host>
	</>;
}
