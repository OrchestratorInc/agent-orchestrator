import { FieldGroup, Host, ListItem } from "@expo/ui";
import { Button, HStack, Image, Label, LabeledContent, Picker, ProgressView, RNHostView, Spacer, Text, Toggle } from "@expo/ui/swift-ui";
import {
	accessibilityAddTraits,
	accessibilityHidden,
	background,
	buttonStyle,
	defaultScrollAnchor,
	disabled as disabledModifier,
	font,
	frame,
	foregroundStyle,
	imageScale,
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
	settingsStatusDotColor,
	settingsToneColor,
	type SettingsAccountRowProps,
	type SettingsButtonRowProps,
	type SettingsChoiceSectionProps,
	type SettingsFormProps,
	type SettingsIconName,
	type SettingsLinkRowProps,
	type SettingsPickerRowProps,
	type SettingsSectionProps,
	type SettingsStatusRowProps,
	type SettingsToggleRowProps,
	type SettingsValueRowProps,
} from "./SettingsForm.types";
import { appFont } from "./swiftUIFont";
import { useTheme, useThemeState } from "./ThemeProvider";
import { rowIconWidth, space } from "./tokens";

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
					// Opens at the top. The Form is laid out under the clear glass bar,
					// and the bar's inset reaches it after its first layout, which left
					// the page opening scrolled by the bar's height, its first section
					// under the title.
					defaultScrollAnchor("top"),
					background(t.bgBase),
					// Every row's text in the app's family, scaled with Dynamic Type.
					appFont("body"),
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
export function SettingsSection({ title, footer, children, testID, modifiers }: SettingsSectionProps & { modifiers?: ModifierConfig[] }) {
	return (
		<FieldGroup.Section testID={testID} modifiers={modifiers}>
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
				appFont("footnote"),
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
	return <Image systemName={settingsIcons[icon].ios} color={color ?? t.textSecondary} modifiers={[font({ textStyle: "body" }), frame({ width: rowIconWidth }), accessibilityHidden(true)]} />;
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
 * The Form has no navigation stack of its own (the app's stack pushes the
 * page), so a row that opens one draws the disclosure chevron a
 * NavigationLink would.
 */
function Disclosure() {
	const t = useTheme();
	return <Image systemName="chevron.forward" color={t.textTertiary} modifiers={[font({ textStyle: "footnote", weight: "semibold" }), accessibilityHidden(true)]} />;
}

/** A row that opens a page. */
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
					<Disclosure />
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

/** The signed-in account: photo or initials, the name, and the email under it. */
export function SettingsAccountRow({ name, email, avatar, loading = false, testID }: SettingsAccountRowProps) {
	const t = useTheme();
	return (
		<ListItem
			testID={testID}
			modifiers={rowModifiers(t)}
			leading={<RNHostView matchContents>{avatar}</RNHostView>}
			supportingText={email ? <Text modifiers={[appFont("subheadline"), foregroundStyle(t.textSecondary), lineLimit(1)]}>{email}</Text> : undefined}
			trailing={loading ? <ProgressView /> : undefined}
		>
			<Text modifiers={[foregroundStyle(t.textPrimary), lineLimit(1)]}>{name}</Text>
		</ListItem>
	);
}

/** A link row with its live status under the name, behind a dot in its tone. */
export function SettingsStatusRow({ icon, label, status, tone, onPress, testID }: SettingsStatusRowProps) {
	const t = useTheme();
	return (
		<ListItem
			onPress={onPress}
			testID={testID}
			modifiers={[buttonStyle("automatic"), ...rowModifiers(t)]}
			leading={<RowIcon icon={icon} />}
			supportingText={
				<HStack spacing={space.xs}>
					<Image systemName="circle.fill" color={settingsStatusDotColor(t, tone)} modifiers={[font({ textStyle: "caption2" }), imageScale("small"), accessibilityHidden(true)]} />
					<Text modifiers={[appFont("subheadline"), foregroundStyle(t.textSecondary), lineLimit(1)]}>{status}</Text>
				</HStack>
			}
			trailing={<Disclosure />}
		>
			<Text modifiers={[foregroundStyle(t.textPrimary), lineLimit(1)]}>{label}</Text>
		</ListItem>
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
 * A menu picker row. It draws its value and the up-down chevrons in the tint,
 * the pop-up button iOS Settings shows, so the row reads as a dropdown rather
 * than a plain value; the app's tint stands in for system blue.
 */
export function SettingsPickerRow<T extends string>({ icon, label, options, selectedValue, onValueChange, disabled = false, testID }: SettingsPickerRowProps<T>) {
	const t = useTheme();
	return (
		<Picker
			label={<RowLabel icon={icon} label={label} />}
			selection={selectedValue}
			onSelectionChange={(next) => onValueChange(String(next) as T)}
			testID={testID}
			modifiers={[pickerStyle("menu"), tint(t.accent), ...rowModifiers(t, disabled)]}
		>
			{options.map((option) => (
				<Text key={option.value} modifiers={[tag(option.value)]}>{option.label}</Text>
			))}
		</Picker>
	);
}

/**
 * A whole section that picks one: every option a row, the chosen one checked
 * in the theme's accent, and VoiceOver reading each as selected or not. Rows
 * of our own rather than an inline Picker: its checkmark is the system accent
 * and follows neither the picker's tint nor the section's, so it stayed blue.
 */
export function SettingsChoiceSection<T extends string>({ options, selectedValue, onValueChange, footer, testID }: SettingsChoiceSectionProps<T>) {
	const t = useTheme();
	return (
		<SettingsSection footer={footer} testID={testID}>
			{options.map((option) => {
				const selected = option.value === selectedValue;
				return (
					<Button
						key={option.value}
						onPress={() => onValueChange(option.value)}
						testID={testID ? `${testID}-${option.value}` : undefined}
						modifiers={[...rowModifiers(t), accessibilityAddTraits(selected ? ["isSelected"] : [])]}
					>
						<HStack spacing={space.md}>
							{option.accessory ?? null}
							<Text modifiers={[foregroundStyle(t.textPrimary)]}>{option.label}</Text>
							<Spacer />
							{selected ? <Image systemName="checkmark" color={t.accent} modifiers={[font({ textStyle: "body", weight: "semibold" }), accessibilityHidden(true)]} /> : null}
						</HStack>
					</Button>
				);
			})}
		</SettingsSection>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
});
