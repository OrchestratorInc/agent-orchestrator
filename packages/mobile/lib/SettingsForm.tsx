import { FieldGroup, Host, ListItem } from "@expo/ui";
import { Button, HStack, Image, Label, LabeledContent, Picker, ProgressView, Spacer, Text, Toggle } from "@expo/ui/swift-ui";
import {
	accessibilityAddTraits,
	accessibilityHidden,
	background,
	buttonStyle,
	disabled as disabledModifier,
	font,
	foregroundStyle,
	labelsHidden,
	lineLimit,
	listRowBackground,
	pickerStyle,
	scrollContentBackground,
	tag,
	tint,
	type ModifierConfig,
} from "@expo/ui/swift-ui/modifiers";
import { StyleSheet } from "react-native";
import {
	settingsIcons,
	settingsToneColor,
	type SettingsButtonRowProps,
	type SettingsChoiceSectionProps,
	type SettingsFormProps,
	type SettingsIconName,
	type SettingsLinkRowProps,
	type SettingsPickerRowProps,
	type SettingsSectionProps,
	type SettingsToggleRowProps,
	type SettingsValueRowProps,
} from "./SettingsForm.types";
import { geistFont } from "./swiftUIFont";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space } from "./tokens";

export { settingsIcons, type SettingsIconName, type SettingsOption, type SettingsTone } from "./SettingsForm.types";

// iOS settings are SwiftUI's own Form: grouped sections, row separators that
// start at the label, the row highlight, Dynamic Type and VoiceOver's header and
// button traits all come from the system. The rows only add the app's palette
// and type, which a Form would otherwise draw in the system's grouped greys.

/**
 * The page: one Form filling the screen. It scrolls on its own and keeps clear
 * of the home indicator, so it is the page's only child under its header.
 */
export function SettingsForm({ children, testID }: SettingsFormProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host style={[styles.fill, { backgroundColor: t.bgBase }]} colorScheme={scheme}>
			<FieldGroup
				testID={testID}
				modifiers={[
					scrollContentBackground("hidden"),
					background(t.bgBase),
					// Every row's text in the app's family, scaled with Dynamic Type.
					geistFont("body"),
					foregroundStyle(t.textPrimary),
				]}
			>
				{children}
			</FieldGroup>
		</Host>
	);
}

/**
 * The title and footer are footnote text drawn here, not the Form's own header
 * text, which would pick up the Form's body font. The title keeps VoiceOver's
 * heading trait.
 */
export function SettingsSection({ title, footer, children, testID }: SettingsSectionProps) {
	return (
		<FieldGroup.Section testID={testID}>
			{title ? <FieldGroup.SectionHeader><SectionText header>{title}</SectionText></FieldGroup.SectionHeader> : null}
			{children}
			{footer ? <FieldGroup.SectionFooter><SectionText>{footer}</SectionText></FieldGroup.SectionFooter> : null}
		</FieldGroup.Section>
	);
}

function SectionText({ children, header = false }: { children: string; header?: boolean }) {
	const t = useTheme();
	return (
		<Text
			modifiers={[
				// Its own footnote face, or it would take the Form's body font.
				geistFont("footnote"),
				foregroundStyle(t.textSecondary),
				...(header ? [accessibilityAddTraits(["isHeader"])] : []),
			]}
		>
			{children}
		</Text>
	);
}

/**
 * The row's glyph, in the secondary ink and scaled with the row's text. It is
 * decoration: VoiceOver reads the row's label, not the symbol's name.
 */
function RowIcon({ icon, color }: { icon: SettingsIconName; color?: string }) {
	const t = useTheme();
	return <Image systemName={settingsIcons[icon].ios} color={color ?? t.textSecondary} modifiers={[font({ textStyle: "body" }), accessibilityHidden(true)]} />;
}

/** Icon and title as one Label, so the Form lines separators up with the title. */
function RowLabel({ icon, label, color }: { icon: SettingsIconName; label: string; color?: string }) {
	return <Label title={label} icon={<RowIcon icon={icon} color={color} />} modifiers={color ? [foregroundStyle(color)] : undefined} />;
}

function ValueText({ value, color }: { value: string; color: string }) {
	return <Text modifiers={[foregroundStyle(color), lineLimit(1)]}>{value}</Text>;
}

function rowModifiers(t: { bgElevated: string }, disabled = false): ModifierConfig[] {
	return [listRowBackground(t.bgElevated), ...(disabled ? [disabledModifier(true)] : [])];
}

/**
 * A row that opens a page. The Form has no navigation stack of its own (the
 * app's stack pushes the page), so the row draws the disclosure chevron a
 * NavigationLink would.
 */
export function SettingsLinkRow({ icon, label, value, onPress, testID }: SettingsLinkRowProps) {
	const t = useTheme();
	return (
		<ListItem
			onPress={onPress}
			testID={testID}
			// The list's own button style, for the row highlight a plain button lacks.
			modifiers={[buttonStyle("automatic"), ...rowModifiers(t)]}
			leading={<RowIcon icon={icon} />}
			trailing={
				<HStack spacing={space.xs}>
					{value ? <ValueText value={value} color={t.textSecondary} /> : null}
					<Image systemName="chevron.forward" color={t.textTertiary} modifiers={[font({ textStyle: "footnote", weight: "semibold" }), accessibilityHidden(true)]} />
				</HStack>
			}
		>
			<Text modifiers={[foregroundStyle(t.textPrimary)]}>{label}</Text>
		</ListItem>
	);
}

export function SettingsValueRow({ icon, label, value, tone, loading = false, testID }: SettingsValueRowProps) {
	const t = useTheme();
	return (
		<LabeledContent label={<RowLabel icon={icon} label={label} />} testID={testID} modifiers={rowModifiers(t)}>
			{loading ? <ProgressView /> : value ? <ValueText value={value} color={settingsToneColor(t, tone)} /> : null}
		</LabeledContent>
	);
}

/**
 * An action in place, as iOS Settings draws one: a tinted button row, red with
 * the destructive role when it removes something.
 */
export function SettingsButtonRow({ icon, label, value, tone, destructive = false, disabled = false, loading = false, onPress, testID }: SettingsButtonRowProps) {
	const t = useTheme();
	const ink = destructive ? t.red : t.accent;
	return (
		<Button
			role={destructive ? "destructive" : undefined}
			onPress={onPress}
			testID={testID}
			modifiers={[tint(ink), ...rowModifiers(t, disabled || loading)]}
		>
			<HStack spacing={space.sm}>
				<RowLabel icon={icon} label={label} color={ink} />
				<Spacer />
				{loading ? <ProgressView /> : value ? <ValueText value={value} color={settingsToneColor(t, tone)} /> : null}
			</HStack>
		</Button>
	);
}

/** The system switch keeps the system's on colour; the app's accent is hue-less. */
export function SettingsToggleRow({ icon, label, value, disabled = false, onValueChange, testID }: SettingsToggleRowProps) {
	const t = useTheme();
	return (
		<Toggle isOn={value} onIsOnChange={onValueChange} testID={testID} modifiers={rowModifiers(t, disabled)}>
			<RowLabel icon={icon} label={label} />
		</Toggle>
	);
}

/**
 * A menu picker row. It draws its value in the tint, system blue on its own;
 * secondary ink is how a grouped list shows a row's value, and how the rows
 * around it show theirs.
 */
export function SettingsPickerRow<T extends string>({ icon, label, options, selectedValue, onValueChange, disabled = false, testID }: SettingsPickerRowProps<T>) {
	const t = useTheme();
	return (
		<Picker
			label={<RowLabel icon={icon} label={label} />}
			selection={selectedValue}
			onSelectionChange={(next) => onValueChange(String(next) as T)}
			testID={testID}
			modifiers={[pickerStyle("menu"), tint(t.textSecondary), ...rowModifiers(t, disabled)]}
		>
			{options.map((option) => (
				<Text key={option.value} modifiers={[tag(option.value)]}>{option.label}</Text>
			))}
		</Picker>
	);
}

/**
 * An inline picker: every option is a row, the chosen one checked, and
 * VoiceOver reads each as selected or not.
 */
export function SettingsChoiceSection<T extends string>({ label, options, selectedValue, onValueChange, footer, testID }: SettingsChoiceSectionProps<T>) {
	const t = useTheme();
	return (
		<SettingsSection footer={footer}>
			<Picker
				label={label}
				selection={selectedValue}
				onSelectionChange={(next) => onValueChange(String(next) as T)}
				testID={testID}
				// The checkmark in the theme's accent rather than system blue.
				modifiers={[pickerStyle("inline"), labelsHidden(), tint(t.accent), listRowBackground(t.bgElevated)]}
			>
				{options.map((option) => (
					// Each option is a row of its own, so it takes the row background
					// too rather than relying on the Picker's reaching it.
					<HStack key={option.value} spacing={space.md} testID={testID ? `${testID}-${option.value}` : undefined} modifiers={[tag(option.value), listRowBackground(t.bgElevated)]}>
						{option.accessory ?? null}
						<Text modifiers={[foregroundStyle(t.textPrimary)]}>{option.label}</Text>
					</HStack>
				))}
			</Picker>
		</SettingsSection>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
});
