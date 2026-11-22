import { MenuView, type NativeActionEvent } from "@expo/ui/community/menu";
import { SymbolView } from "expo-symbols";
import { Children, Fragment, isValidElement, useState, type ReactNode } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Switch, Text, View } from "react-native";
import { haptics } from "./haptics";
import {
	settingsIcons,
	settingsMeterColor,
	settingsStatusDotColor,
	settingsToneColor,
	type SettingsAccountRowProps,
	type SettingsButtonRowProps,
	type SettingsChoiceSectionProps,
	type SettingsFormProps,
	type SettingsIconName,
	type SettingsLinkRowProps,
	type SettingsMeterRowProps,
	type SettingsPickerRowProps,
	type SettingsSectionProps,
	type SettingsStatusRowProps,
	type SettingsToggleRowProps,
	type SettingsValueRowProps,
} from "./SettingsForm.types";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { font, fontScaleCap } from "./tokens";

export { settingsIcons, type SettingsIconName, type SettingsOption, type SettingsTone } from "./SettingsForm.types";

// iOS settings drawn as the desktop's Settings are (DESIGN.md, Settings): a
// sentence-case heading per section in the label ink, then one card with no
// outline (the filled surface one step off the page groups it), hairline
// dividers between its rows, and rows that fit their content with 10pt of
// vertical padding: a muted glyph, the label, an optional muted line under it,
// and the control or value at the end. The page title, headings and rows share
// one 16pt text inset. The Settings screens use it; the new-project forms
// keep SwiftUI's Form (SettingsForm), whose fields they build in it, and
// Android keeps Material's own list (SettingsPage.android).

/** The text inset every edge on the page lines up with: headings, rows, footers. */
const INSET = 16;
/** A row's vertical padding (the desktop's 10px). */
const ROW_PAD = 10;
const ICON = 17;

/** The page: one scroll view, laid out under the clear bar and inset from it. */
export function SettingsForm({ children, testID }: SettingsFormProps) {
	const styles = useThemedStyles(makeStyles);
	return (
		<ScrollView
			style={styles.page}
			contentContainerStyle={styles.pageContent}
			contentInsetAdjustmentBehavior="automatic"
			keyboardShouldPersistTaps="handled"
			testID={testID}
		>
			{children}
		</ScrollView>
	);
}

/** A section: its heading, one card of rows with hairlines between them, and its footer. */
export function SettingsSection({ title, footer, children, testID }: SettingsSectionProps) {
	const styles = useThemedStyles(makeStyles);
	const rows = flatten(children);
	return (
		<View style={styles.section} testID={testID}>
			{title ? <Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.title} style={styles.heading}>{title}</Text> : null}
			{rows.length ? (
				<View style={styles.card}>
					{rows.map((row, index) => (
						<View key={index} style={index > 0 ? styles.divided : null}>{row}</View>
					))}
				</View>
			) : null}
			{footer ? <Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.footer}>{footer}</Text> : null}
		</View>
	);
}

/** A section's rows one by one, through fragments and past empty slots, so each gets its divider. */
function flatten(children: ReactNode): ReactNode[] {
	const out: ReactNode[] = [];
	for (const child of Children.toArray(children)) {
		if (isValidElement(child) && child.type === Fragment) out.push(...flatten((child.props as { children?: ReactNode }).children));
		else if (child !== null && child !== undefined && child !== "") out.push(child);
	}
	return out;
}

/** The row's glyph in the muted ink: decoration, as VoiceOver reads the label. */
function RowIcon({ icon, color }: { icon: SettingsIconName; color?: string }) {
	const t = useTheme();
	return (
		<View style={glyphSlot} accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
			<SymbolView name={settingsIcons[icon].ios} size={ICON} tintColor={color ?? t.textSecondary} />
		</View>
	);
}

const glyphSlot = { width: 22, alignItems: "center", justifyContent: "center" } as const;

/** The disclosure chevron of a row that opens a page. */
function Disclosure() {
	const t = useTheme();
	return <SymbolView name="chevron.forward" size={12} weight="semibold" tintColor={t.textTertiary} />;
}

/**
 * One row: the glyph (or a leading element), the label with an optional muted
 * line under it, and whatever sits at its end. Pressable when it acts.
 */
function Row({ icon, leading, label, labelColor, detail, trailing, onPress, disabled = false, accessibilityLabel, accessibilityRole, accessibilityState, testID }: {
	icon?: SettingsIconName;
	leading?: ReactNode;
	label: string;
	labelColor?: string;
	detail?: ReactNode;
	trailing?: ReactNode;
	onPress?(): void;
	disabled?: boolean;
	accessibilityLabel?: string;
	accessibilityRole?: "button" | "switch" | "text" | "adjustable";
	accessibilityState?: { checked?: boolean; disabled?: boolean; selected?: boolean; busy?: boolean };
	testID?: string;
}) {
	const styles = useThemedStyles(makeStyles);
	const body = (
		<>
			{leading ? <View style={glyphSlot}>{leading}</View> : icon ? <RowIcon icon={icon} color={labelColor} /> : null}
			<View style={styles.labelColumn}>
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.label, labelColor ? { color: labelColor } : null]} numberOfLines={1}>{label}</Text>
				{detail}
			</View>
			{trailing ? <View style={styles.trailing}>{trailing}</View> : null}
		</>
	);
	if (!onPress) {
		return <View style={[styles.row, disabled && styles.disabled]} accessible accessibilityLabel={accessibilityLabel} accessibilityRole={accessibilityRole} accessibilityState={accessibilityState} testID={testID}>{body}</View>;
	}
	return (
		<Pressable
			onPress={onPress}
			disabled={disabled}
			accessibilityRole={accessibilityRole ?? "button"}
			accessibilityLabel={accessibilityLabel}
			accessibilityState={{ disabled, ...accessibilityState }}
			style={({ pressed }) => [styles.row, pressed && styles.pressed, disabled && styles.disabled]}
			testID={testID}
		>
			{body}
		</Pressable>
	);
}

function Value({ value, color }: { value: string; color: string }) {
	const styles = useThemedStyles(makeStyles);
	return <Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.value, { color }]} numberOfLines={1}>{value}</Text>;
}

function Detail({ children }: { children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	return <Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.detail} numberOfLines={2}>{children}</Text>;
}

/** A row that opens a page. */
export function SettingsLinkRow({ icon, label, value, onPress, testID }: SettingsLinkRowProps) {
	const t = useTheme();
	return (
		<Row
			icon={icon}
			label={label}
			onPress={onPress}
			accessibilityLabel={value ? `${label}, ${value}` : label}
			trailing={<>{value ? <Value value={value} color={t.textSecondary} /> : null}<Disclosure /></>}
			testID={testID}
		/>
	);
}

export function SettingsValueRow({ icon, label, value, tone, loading = false, testID }: SettingsValueRowProps) {
	const t = useTheme();
	return (
		<Row
			icon={icon}
			label={label}
			accessibilityLabel={value ? `${label}, ${value}` : label}
			accessibilityState={loading ? { busy: true } : undefined}
			trailing={loading ? <ActivityIndicator size="small" color={t.textSecondary} /> : value ? <Value value={value} color={settingsToneColor(t, tone)} /> : null}
			testID={testID}
		/>
	);
}

/** An action in place: the label in the label ink, red when it removes something. */
export function SettingsButtonRow({ icon, label, value, tone, destructive = false, disabled = false, loading = false, onPress, testID }: SettingsButtonRowProps) {
	const t = useTheme();
	return (
		<Row
			icon={icon}
			label={label}
			labelColor={destructive ? t.red : undefined}
			onPress={onPress}
			disabled={disabled || loading}
			accessibilityLabel={value ? `${label}, ${value}` : label}
			accessibilityState={loading ? { busy: true } : undefined}
			trailing={loading ? <ActivityIndicator size="small" color={t.textSecondary} /> : value ? <Value value={value} color={settingsToneColor(t, tone)} /> : null}
			testID={testID}
		/>
	);
}

/** The signed-in account: photo or initials, the name, and the email under it. */
export function SettingsAccountRow({ name, email, avatar, loading = false, testID }: SettingsAccountRowProps) {
	const t = useTheme();
	return (
		<Row
			leading={avatar}
			label={name}
			detail={email ? <Detail>{email}</Detail> : undefined}
			accessibilityLabel={email ? `${name}, ${email}` : name}
			trailing={loading ? <ActivityIndicator size="small" color={t.textSecondary} /> : undefined}
			testID={testID}
		/>
	);
}

/** A measured amount: the label and amount, then a bar and a line such as when it resets. */
export function SettingsMeterRow({ icon, label, value, fraction, severity, tone, detail, testID }: SettingsMeterRowProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<Row
			icon={icon}
			label={label}
			accessibilityLabel={[label, value, detail].filter(Boolean).join(", ")}
			detail={(
				<>
					{fraction !== undefined ? (
						<View style={styles.track}>
							<View style={[styles.fill, { width: `${Math.round(Math.min(1, Math.max(0, fraction)) * 100)}%`, backgroundColor: settingsMeterColor(t, severity) }]} />
						</View>
					) : null}
					{detail ? <Detail>{detail}</Detail> : null}
				</>
			)}
			trailing={<Value value={value} color={settingsToneColor(t, tone)} />}
			testID={testID}
		/>
	);
}

/** A row that opens a page, with its live status under the name behind a dot in its tone. */
export function SettingsStatusRow({ icon, label, status, tone, leading, onPress, testID }: SettingsStatusRowProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<Row
			icon={icon}
			leading={leading}
			label={label}
			onPress={onPress}
			accessibilityLabel={`${label}, ${status}`}
			detail={(
				<View style={styles.status}>
					<View style={[styles.dot, { backgroundColor: settingsStatusDotColor(t, tone) }]} />
					<Detail>{status}</Detail>
				</View>
			)}
			trailing={<Disclosure />}
			testID={testID}
		/>
	);
}

/** A switch at the row's end, in the label ink when on, as the desktop's are. */
export function SettingsToggleRow({ icon, label, value, disabled = false, onValueChange, testID }: SettingsToggleRowProps) {
	const t = useTheme();
	return (
		<Row
			icon={icon}
			label={label}
			disabled={disabled}
			accessibilityLabel={label}
			trailing={<Switch value={value} disabled={disabled} onValueChange={onValueChange} trackColor={{ true: t.textPrimary }} accessibilityLabel={label} testID={testID} />}
		/>
	);
}

/**
 * A choice: the current option and a ▾ at the row's end, opening the system
 * menu with the chosen one checked, as the desktop's settings select opens its
 * dropdown.
 */
export function SettingsPickerRow<T extends string>({ icon, label, options, selectedValue, onValueChange, disabled = false, testID }: SettingsPickerRowProps<T>) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const current = options.find((option) => option.value === selectedValue)?.label ?? "";
	// The menu host sizes itself to what it wraps; the row is given its slot's width.
	const [width, setWidth] = useState<number>();
	return (
		<View onLayout={(event) => { const next = Math.round(event.nativeEvent.layout.width); setWidth((prev) => (prev === next ? prev : next)); }}>
		<MenuView
			style={width ? { width } : undefined}
			title={label}
			actions={options.map((option) => ({ id: option.value, title: option.label, state: option.value === selectedValue ? "on" : "off" }))}
			onPressAction={(event: NativeActionEvent) => { haptics.select(); onValueChange(event.nativeEvent.event as T); }}
			shouldOpenOnLongPress={false}
			testID={testID}
		>
			<Row
				icon={icon}
				label={label}
				disabled={disabled}
				accessibilityLabel={`${label}, ${current}`}
				accessibilityRole="button"
				trailing={(
					<View style={styles.select}>
						<Value value={current} color={t.textSecondary} />
						<SymbolView name="chevron.down" size={11} weight="semibold" tintColor={t.textTertiary} />
					</View>
				)}
			/>
		</MenuView>
		</View>
	);
}

/** A section that picks one: every option a row, the chosen one checked. */
export function SettingsChoiceSection<T extends string>({ options, selectedValue, onValueChange, footer, testID }: SettingsChoiceSectionProps<T>) {
	const t = useTheme();
	return (
		<SettingsSection footer={footer} testID={testID}>
			{options.map((option) => {
				const selected = option.value === selectedValue;
				return (
					<Row
						key={option.value}
						leading={option.accessory ?? undefined}
						label={option.label}
						onPress={() => onValueChange(option.value)}
						accessibilityLabel={option.label}
						accessibilityState={{ selected }}
						trailing={selected ? <SymbolView name="checkmark" size={15} weight="semibold" tintColor={t.textPrimary} /> : null}
						testID={testID ? `${testID}-${option.value}` : undefined}
					/>
				);
			})}
		</SettingsSection>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	page: { flex: 1, backgroundColor: t.bgBase },
	pageContent: { paddingHorizontal: 16, paddingTop: 12, paddingBottom: 40, gap: 24 },
	section: { gap: 8 },
	heading: { paddingHorizontal: INSET, fontFamily: font.medium, fontSize: 15, lineHeight: 20, color: t.textPrimary },
	card: { overflow: "hidden", borderRadius: 10, backgroundColor: t.bgElevated, paddingVertical: 4 },
	divided: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderDefault },
	footer: { paddingHorizontal: INSET, fontFamily: font.regular, fontSize: 13, lineHeight: 17, color: t.textTertiary },
	row: { flexDirection: "row", alignItems: "center", gap: 10, paddingHorizontal: INSET, paddingVertical: ROW_PAD, minHeight: 44 },
	pressed: { backgroundColor: t.bgSubtle },
	disabled: { opacity: 0.45 },
	labelColumn: { flex: 1, minWidth: 0, gap: 2 },
	label: { fontFamily: font.regular, fontSize: 15, lineHeight: 20, color: t.textPrimary },
	detail: { fontFamily: font.regular, fontSize: 13, lineHeight: 17, color: t.textSecondary },
	trailing: { flexDirection: "row", alignItems: "center", gap: 6, flexShrink: 0, maxWidth: "55%" },
	value: { fontFamily: font.regular, fontSize: 15, lineHeight: 20, flexShrink: 1 },
	select: { flexDirection: "row", alignItems: "center", gap: 5 },
	status: { flexDirection: "row", alignItems: "center", gap: 6 },
	dot: { width: 6, height: 6, borderRadius: 3 },
	track: { height: 4, marginTop: 6, borderRadius: 2, overflow: "hidden", backgroundColor: t.bgSubtle },
	fill: { height: 4, borderRadius: 2 },
});
