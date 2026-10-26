import {
	CircularProgressIndicator,
	Column,
	DropdownMenu,
	DropdownMenuItem,
	Host,
	Icon,
	LazyColumn,
	ListItem,
	RadioButton,
	SegmentedButton,
	SingleChoiceSegmentedButtonRow,
	Switch,
	Text,
} from "@expo/ui/jetpack-compose";
import {
	alpha,
	background,
	clickable,
	clip,
	fillMaxSize,
	fillMaxWidth,
	padding,
	selectable,
	selectableGroup,
	Shapes,
	size,
	testID as testIDModifier,
	toggleable,
	type ModifierConfig,
} from "@expo/ui/jetpack-compose/modifiers";
import { Children, createContext, Fragment, isValidElement, useContext, useState, type ReactNode } from "react";
import { StyleSheet } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { pickerDrawables, settingsDrawable } from "./settingsDrawables.android";
import {
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
import { useTheme, useThemeState } from "./ThemeProvider";
import { font as typeface, gutter, iconSize, radius, space, stateOpacity } from "./tokens";

export { settingsIcons, type SettingsIconName, type SettingsOption, type SettingsTone } from "./SettingsForm.types";

// Android settings are a Material 3 list: Compose ListItems grouped into
// sections, each row a rounded segment of its group, with the ripple, the row
// heights and TalkBack's roles coming from Compose. The universal FieldGroup draws
// the same grouping, but wraps each row in a ListItem of its own that cannot be
// tapped, so a tappable row inside it nests a second ListItem; these rows are
// the ListItem itself.

type RowPosition = "leading" | "middle" | "trailing" | "only";

const RowPositionContext = createContext<RowPosition>("only");

function rowPosition(index: number, count: number): RowPosition {
	if (count <= 1) return "only";
	if (index === 0) return "leading";
	if (index === count - 1) return "trailing";
	return "middle";
}

/** A group's ends are fully round and the seams between its rows nearly square, as M3's segmented lists draw them. */
function rowShape(position: RowPosition) {
	const full = radius.xl;
	const seam = radius.xs;
	switch (position) {
		case "only":
			return Shapes.RoundedCorner(full);
		case "leading":
			return Shapes.RoundedCorner({ topStart: full, topEnd: full, bottomStart: seam, bottomEnd: seam });
		case "trailing":
			return Shapes.RoundedCorner({ topStart: seam, topEnd: seam, bottomStart: full, bottomEnd: full });
		default:
			return Shapes.RoundedCorner(seam);
	}
}

/** The section's rows, with fragments opened and conditional rows that rendered nothing left out. */
function sectionRows(children: ReactNode): ReactNode[] {
	const rows: ReactNode[] = [];
	Children.forEach(children, (child) => {
		if (child == null || typeof child === "boolean") return;
		if (isValidElement<{ children?: ReactNode }>(child) && child.type === Fragment) rows.push(...sectionRows(child.props.children));
		else rows.push(child);
	});
	return rows;
}

/**
 * The page: one lazy list filling the screen. It ends above the navigation
 * bar, which the edge-to-edge page draws under.
 */
export function SettingsForm({ children, testID }: SettingsFormProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	return (
		<Host style={[styles.fill, { backgroundColor: t.bgBase }]} colorScheme={scheme}>
			<LazyColumn
				verticalArrangement={{ spacedBy: space.xxl }}
				contentPadding={{ start: gutter, end: gutter, top: gutter, bottom: gutter + insets.bottom }}
				modifiers={[fillMaxSize(), background(t.bgBase), ...(testID ? [testIDModifier(testID)] : [])]}
			>
				{children}
			</LazyColumn>
		</Host>
	);
}

export function SettingsSection({ title, footer, children, testID }: SettingsSectionProps) {
	const rows = sectionRows(children);
	return (
		<Column verticalArrangement={{ spacedBy: space.xxs }} modifiers={[fillMaxWidth(), ...(testID ? [testIDModifier(testID)] : [])]}>
			{title ? <SectionTitle>{title}</SectionTitle> : null}
			{rows.length > 0 ? (
				<Column verticalArrangement={{ spacedBy: space.hair }} modifiers={[fillMaxWidth()]}>
					{rows.map((row, index) => (
						<RowPositionContext.Provider key={isValidElement(row) && row.key != null ? row.key : index} value={rowPosition(index, rows.length)}>
							{row}
						</RowPositionContext.Provider>
					))}
				</Column>
			) : null}
			{footer ? <SectionFooter>{footer}</SectionFooter> : null}
		</Column>
	);
}

function SectionTitle({ children }: { children: string }) {
	const t = useTheme();
	return (
		<Text color={t.textSecondary} style={{ typography: "titleSmall", fontFamily: typeface.semibold }} modifiers={[padding(space.lg, 0, space.lg, space.sm)]}>
			{children}
		</Text>
	);
}

function SectionFooter({ children }: { children: string }) {
	const t = useTheme();
	return (
		<Text color={t.textSecondary} style={{ typography: "bodyMedium", fontFamily: typeface.regular }} modifiers={[padding(space.lg, space.xxs, space.lg, 0)]}>
			{children}
		</Text>
	);
}

function RowIcon({ icon, color }: { icon: SettingsIconName; color: string }) {
	return <Icon source={settingsDrawable(icon)} size={iconSize.xl} tint={color} />;
}

function Headline({ children, color }: { children: string; color: string }) {
	return <Text color={color} style={{ typography: "bodyLarge", fontFamily: typeface.regular }}>{children}</Text>;
}

function Supporting({ children, color }: { children: string; color: string }) {
	return <Text color={color} style={{ typography: "bodyMedium", fontFamily: typeface.regular }}>{children}</Text>;
}

function RowSpinner() {
	const t = useTheme();
	return <CircularProgressIndicator color={t.textSecondary} strokeWidth={2} modifiers={[size(iconSize.lg, iconSize.lg)]} />;
}

/**
 * One row of a section: a Material ListItem in the app's palette, shaped by
 * where it sits in its group. A tappable row takes the press modifier itself,
 * so the ripple and TalkBack's action cover the whole row.
 */
function Row({
	icon,
	label,
	ink,
	supporting,
	trailing,
	press,
	disabled = false,
	testID,
}: {
	icon: SettingsIconName;
	label: string;
	/** The label's and the glyph's colour, when the row is an action. */
	ink?: string;
	supporting?: ReactNode;
	trailing?: ReactNode;
	press?: ModifierConfig;
	disabled?: boolean;
	testID?: string;
}) {
	const t = useTheme();
	const position = useContext(RowPositionContext);
	return (
		<ListItem
			colors={{
				containerColor: t.bgElevated,
				contentColor: ink ?? t.textPrimary,
				leadingContentColor: ink ?? t.textSecondary,
				trailingContentColor: t.textSecondary,
				supportingContentColor: t.textSecondary,
			}}
			modifiers={[
				fillMaxWidth(),
				// Clipped before the press modifier so the ripple keeps the row's shape.
				clip(rowShape(position)),
				...(press && !disabled ? [press] : []),
				...(disabled ? [alpha(stateOpacity.disabled)] : []),
				...(testID ? [testIDModifier(testID)] : []),
			]}
		>
			<ListItem.HeadlineContent>
				<Headline color={ink ?? t.textPrimary}>{label}</Headline>
			</ListItem.HeadlineContent>
			{supporting != null ? <ListItem.SupportingContent>{supporting}</ListItem.SupportingContent> : null}
			<ListItem.LeadingContent>
				<RowIcon icon={icon} color={ink ?? t.textSecondary} />
			</ListItem.LeadingContent>
			{trailing != null ? <ListItem.TrailingContent>{trailing}</ListItem.TrailingContent> : null}
		</ListItem>
	);
}

/** Material lists show a setting's value under its name, and draw no chevron. */
export function SettingsLinkRow({ icon, label, value, onPress, testID }: SettingsLinkRowProps) {
	const t = useTheme();
	return (
		<Row
			icon={icon}
			label={label}
			supporting={value ? <Supporting color={t.textSecondary}>{value}</Supporting> : undefined}
			press={clickable(onPress)}
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
			supporting={value ? <Supporting color={settingsToneColor(t, tone)}>{value}</Supporting> : undefined}
			trailing={loading ? <RowSpinner /> : undefined}
			testID={testID}
		/>
	);
}

/** An action in place: the label in the accent, or in red when it removes something. */
export function SettingsButtonRow({ icon, label, value, tone, destructive = false, disabled = false, loading = false, onPress, testID }: SettingsButtonRowProps) {
	const t = useTheme();
	return (
		<Row
			icon={icon}
			label={label}
			ink={destructive ? t.red : t.accent}
			supporting={value ? <Supporting color={settingsToneColor(t, tone)}>{value}</Supporting> : undefined}
			trailing={loading ? <RowSpinner /> : undefined}
			press={clickable(onPress)}
			disabled={disabled || loading}
			testID={testID}
		/>
	);
}

/**
 * The whole row toggles, as Material asks of a list switch, and TalkBack reads
 * it as one switch. The switch keeps Material You's colours.
 */
export function SettingsToggleRow({ icon, label, value, disabled = false, onValueChange, testID }: SettingsToggleRowProps) {
	return (
		<Row
			icon={icon}
			label={label}
			trailing={<Switch value={value} enabled={!disabled} onCheckedChange={disabled ? undefined : onValueChange} />}
			press={toggleable(value, () => onValueChange(!value), { role: "switch" })}
			disabled={disabled}
			testID={testID}
		/>
	);
}

export function SettingsPickerRow<T extends string>(props: SettingsPickerRowProps<T>) {
	return props.appearance === "segmented" ? <SegmentedPickerRow {...props} /> : <MenuPickerRow {...props} />;
}

/** The row opens a dropdown of the choices under itself, the chosen one checked. */
function MenuPickerRow<T extends string>({ icon, label, options, selectedValue, onValueChange, disabled = false, testID }: SettingsPickerRowProps<T>) {
	const t = useTheme();
	const [expanded, setExpanded] = useState(false);
	const selected = options.find((option) => option.value === selectedValue);
	return (
		<DropdownMenu expanded={expanded} onDismissRequest={() => setExpanded(false)} color={t.bgElevated} modifiers={[fillMaxWidth()]}>
			<DropdownMenu.Trigger>
				<Row
					icon={icon}
					label={label}
					supporting={selected ? <Supporting color={t.textSecondary}>{selected.label}</Supporting> : undefined}
					trailing={<Icon source={pickerDrawables.dropDown} size={iconSize.xl} tint={t.textSecondary} />}
					press={clickable(() => setExpanded(true))}
					disabled={disabled}
					testID={testID}
				/>
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>
				{options.map((option) => {
					const chosen = option.value === selectedValue;
					return (
						<DropdownMenuItem
							key={option.value}
							elementColors={{ textColor: t.textPrimary, trailingIconColor: t.textPrimary }}
							modifiers={testID ? [testIDModifier(`${testID}-${option.value}`)] : undefined}
							onClick={() => {
								setExpanded(false);
								if (!chosen) onValueChange(option.value);
							}}
						>
							<DropdownMenuItem.Text>
								<Text style={{ typography: "bodyLarge", fontFamily: typeface.regular }}>{option.label}</Text>
							</DropdownMenuItem.Text>
							{chosen ? (
								<DropdownMenuItem.TrailingIcon>
									<Icon source={pickerDrawables.check} size={iconSize.xl} contentDescription="Selected" />
								</DropdownMenuItem.TrailingIcon>
							) : null}
						</DropdownMenuItem>
					);
				})}
			</DropdownMenu.Items>
		</DropdownMenu>
	);
}

/** Material's single-select segmented buttons, laid under the row's name. */
function SegmentedPickerRow<T extends string>({ icon, label, options, selectedValue, onValueChange, disabled = false, testID }: SettingsPickerRowProps<T>) {
	const t = useTheme();
	return (
		<Row
			icon={icon}
			label={label}
			supporting={
				<SingleChoiceSegmentedButtonRow modifiers={[fillMaxWidth(), padding(0, space.sm, 0, space.xxs)]}>
					{options.map((option) => (
						<SegmentedButton
							key={option.value}
							selected={option.value === selectedValue}
							enabled={!disabled}
							onClick={() => {
								if (option.value !== selectedValue) onValueChange(option.value);
							}}
							colors={{
								activeContainerColor: t.accentTint,
								activeContentColor: t.textPrimary,
								activeBorderColor: t.borderDefault,
								inactiveContainerColor: t.bgElevated,
								inactiveContentColor: t.textSecondary,
								inactiveBorderColor: t.borderDefault,
							}}
							modifiers={testID ? [testIDModifier(`${testID}-${option.value}`)] : undefined}
						>
							<SegmentedButton.Label>
								<Text style={{ typography: "labelLarge", fontFamily: typeface.medium }}>{option.label}</Text>
							</SegmentedButton.Label>
						</SegmentedButton>
					))}
				</SingleChoiceSegmentedButtonRow>
			}
			disabled={disabled}
			testID={testID}
		/>
	);
}

/**
 * A radio-button list. Each row is selectable with the radio role and the
 * group is one selectable group, so TalkBack reads "n of m" and the state.
 * Compose has no label for the group here; the page's title names it.
 */
export function SettingsChoiceSection<T extends string>({ options, selectedValue, onValueChange, footer, testID }: SettingsChoiceSectionProps<T>) {
	const t = useTheme();
	return (
		<Column verticalArrangement={{ spacedBy: space.xxs }} modifiers={[fillMaxWidth(), ...(testID ? [testIDModifier(testID)] : [])]}>
			<Column verticalArrangement={{ spacedBy: space.hair }} modifiers={[fillMaxWidth(), selectableGroup()]}>
				{options.map((option, index) => {
					const chosen = option.value === selectedValue;
					return (
						<ListItem
							key={option.value}
							colors={{ containerColor: t.bgElevated, contentColor: t.textPrimary }}
							modifiers={[
								fillMaxWidth(),
								clip(rowShape(rowPosition(index, options.length))),
								selectable(chosen, () => {
									if (!chosen) onValueChange(option.value);
								}, "radioButton"),
								...(testID ? [testIDModifier(`${testID}-${option.value}`)] : []),
							]}
						>
							<ListItem.HeadlineContent>
								<Headline color={t.textPrimary}>{option.label}</Headline>
							</ListItem.HeadlineContent>
							<ListItem.LeadingContent>
								{/* Drawn only; the row is the control, so the radio takes no click. */}
								<RadioButton selected={chosen} />
							</ListItem.LeadingContent>
							{option.accessory ? <ListItem.TrailingContent>{option.accessory}</ListItem.TrailingContent> : null}
						</ListItem>
					);
				})}
			</Column>
			{footer ? <SectionFooter>{footer}</SectionFooter> : null}
		</Column>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
});
