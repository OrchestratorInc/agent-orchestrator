import { FieldGroup, Host, ListItem } from "@expo/ui";
import { ListItem as ComposeListItem, RadioButton, RNHostView, Surface, Text as ComposeText } from "@expo/ui/jetpack-compose";
import { alpha, fillMaxWidth, selectable } from "@expo/ui/jetpack-compose/modifiers";
import { Text as SwiftUIText } from "@expo/ui/swift-ui";
import { accessibilityAddTraits, disabled as disabledModifier, font, foregroundStyle, refreshable, scrollContentBackground } from "@expo/ui/swift-ui/modifiers";
import { type ReactElement, type ReactNode } from "react";
import { FlatList, Platform, RefreshControl, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { refreshControlColors } from "./refreshControlColors";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";
import { iconSize, sheetInset, space, stateOpacity, type } from "./tokens";

export type PickerOption = {
	id: string;
	title: string;
	/** A second line under the title. */
	detail?: string;
	/** Draws the detail in amber: something the user can act on. */
	warn?: boolean;
	/** Shown, with its detail, but not choosable. */
	disabled?: boolean;
	/** A React Native mark beside the title, such as an agent's logo. */
	mark?: ReactElement;
};

/**
 * A single choice in a sheet, as each platform draws one: on iOS a Form whose
 * current row carries a checkmark, on Android Material list items led by a
 * radio button. Pull down on either to refresh.
 *
 * `header` is React Native and sits above the rows. `footer` follows them and
 * is native: SwiftUI `FieldGroup.Section`s on iOS, a React Native element
 * (holding its own Compose host) on Android.
 */
export function NativePickerList({ header, options, selected, onSelect, refreshing, onRefresh, empty, footer }: {
	header: ReactNode;
	options: PickerOption[];
	selected: string;
	onSelect(id: string): void;
	refreshing: boolean;
	/** Settles once the list has been fetched again; iOS holds its spinner until then. */
	onRefresh(): Promise<void>;
	/** Said in place of the rows when there are none. */
	empty?: string;
	footer?: ReactNode;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	if (Platform.OS === "ios") {
		return (
			<View style={styles.screen}>
				<View style={styles.header}>{header}</View>
				<Host style={styles.form} colorScheme={scheme}>
					<FieldGroup modifiers={[scrollContentBackground("hidden"), refreshable(onRefresh)]}>
						<FieldGroup.Section>
							{options.map((option) => <OptionRow key={option.id} option={option} selected={option.id === selected} onSelect={onSelect} />)}
							{options.length === 0 && empty ? <SwiftUIText modifiers={[foregroundStyle({ type: "hierarchical", style: "secondary" })]}>{empty}</SwiftUIText> : null}
						</FieldGroup.Section>
						{footer}
					</FieldGroup>
				</Host>
			</View>
		);
	}
	return (
		<FlatList
			style={styles.list}
			data={options}
			// Prefixed, so a choice whose id is empty (the model picker's Auto) has a key.
			keyExtractor={(option) => `option:${option.id}`}
			// Keeps an Android drag with the list; without it the sheet's own pan
			// takes the gesture and dismisses instead of scrolling back up.
			nestedScrollEnabled
			// A row under the custom-model field must take the first tap while the
			// keyboard is up, rather than spend it on dismissing the keyboard.
			keyboardShouldPersistTaps="handled"
			contentContainerStyle={styles.content}
			refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void onRefresh()} {...refreshControlColors(t)} />}
			ListHeaderComponent={<View style={styles.header}>{header}</View>}
			ListEmptyComponent={empty ? <Text style={styles.empty}>{empty}</Text> : undefined}
			ListFooterComponent={footer ? <View style={styles.footer}>{footer}</View> : undefined}
			renderItem={({ item }) => (
				<Host matchContents={{ vertical: true }} style={styles.row} colorScheme={scheme}>
					<OptionRow option={item} selected={item.id === selected} onSelect={onSelect} />
				</Host>
			)}
		/>
	);
}

function OptionRow({ option, selected, onSelect }: { option: PickerOption; selected: boolean; onSelect(id: string): void }) {
	const t = useTheme();
	if (Platform.OS === "ios") {
		const detail = option.detail && option.warn
			? <SwiftUIText modifiers={[font({ textStyle: "footnote" }), foregroundStyle(t.amber)]}>{option.detail}</SwiftUIText>
			: option.detail;
		return (
			<ListItem
				onPress={option.disabled ? undefined : () => onSelect(option.id)}
				leading={option.mark}
				trailing={selected ? <AppSymbol name="checkmark" size={iconSize.md} color={t.accent} /> : undefined}
				supportingText={detail}
				modifiers={[
					...(selected ? [accessibilityAddTraits(["isSelected"])] : []),
					...(option.disabled ? [disabledModifier(true)] : []),
				]}
			>
				{option.title}
			</ListItem>
		);
	}
	const row = (
		// The row is one radio choice: the whole item selects, and TalkBack hears
		// a radio button and whether it is checked.
		<ComposeListItem
			colors={{ containerColor: "transparent" }}
			modifiers={option.disabled ? [alpha(stateOpacity.disabled)] : [selectable(selected, () => onSelect(option.id), "radioButton")]}
		>
			<ComposeListItem.LeadingContent><RadioButton selected={selected} /></ComposeListItem.LeadingContent>
			<ComposeListItem.HeadlineContent><ComposeText>{option.title}</ComposeText></ComposeListItem.HeadlineContent>
			{option.detail ? <ComposeListItem.SupportingContent><ComposeText color={option.warn ? t.amber : undefined}>{option.detail}</ComposeText></ComposeListItem.SupportingContent> : null}
			{option.mark ? <ComposeListItem.TrailingContent><RNHostView matchContents>{option.mark}</RNHostView></ComposeListItem.TrailingContent> : null}
		</ComposeListItem>
	);
	if (!option.disabled) return row;
	// A disabled click target, never pressed: Compose merges the row into one
	// node, so TalkBack reads the title with its status and says it is disabled.
	return <Surface onClick={ignore} enabled={false} color="transparent" modifiers={[fillMaxWidth()]}>{row}</Surface>;
}

function ignore() {}

const makeStyles = (t: Theme) => StyleSheet.create({
	// iOS sets the form's rows on the grouped background they are drawn for.
	screen: { flex: 1, backgroundColor: t.bgBase },
	header: { paddingHorizontal: sheetInset.horizontal, paddingTop: sheetInset.top, gap: space.sm },
	form: { flex: 1 },
	list: { flex: 1, backgroundColor: t.bgSurface },
	content: { paddingBottom: sheetInset.bottom },
	// Stretched by the list, not given a percentage width, which a Compose host
	// cannot take as a layout value.
	row: { alignSelf: "stretch" },
	footer: { paddingHorizontal: sheetInset.horizontal, paddingTop: space.lg },
	empty: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: sheetInset.horizontal, paddingVertical: space.md },
});
