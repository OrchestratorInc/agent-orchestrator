import { Stack } from "expo-router";
import { useCallback, useState, type RefObject } from "react";
import type { SearchBarCommands } from "react-native-screens";
import { useTheme } from "../ThemeProvider";
import type { ComposerSearchFieldProps } from "./ComposerSearchField.types";

export type { ComposerSearchFieldProps } from "./ComposerSearchField.types";

/**
 * The picker's search in the sheet's own navigation bar: UIKit's search
 * controller, under the title, filtering the list in place.
 */
export function ComposerSearchField({ initialQuery, placeholder, onQuery }: ComposerSearchFieldProps) {
	const t = useTheme();
	// Made once: the bar has no initial-text prop, and a new ref would seed it again.
	const [search] = useState(() => seededSearch(initialQuery));
	const changed = useCallback((event: { nativeEvent: { text: string } }) => onQuery(event.nativeEvent.text), [onQuery]);
	const cleared = useCallback(() => onQuery(""), [onQuery]);
	return (
		<Stack.SearchBar
			ref={search}
			placeholder={placeholder}
			placement="stacked"
			hideWhenScrolling={false}
			obscureBackground={false}
			autoCapitalize="none"
			tintColor={t.highlight}
			textColor={t.textPrimary}
			hintTextColor={t.textTertiary}
			headerIconColor={t.accent}
			onChangeText={changed}
			onCancelButtonPress={cleared}
		/>
	);
}

/**
 * A ref that puts `text` in the bar the first time its commands attach. The bar
 * belongs to the header, which mounts after this page, so an effect here would
 * run before there is a bar to write to. The commands are handed over again on
 * every render of the bar, so it seeds only once.
 */
function seededSearch(text: string): RefObject<SearchBarCommands | null> {
	let current: SearchBarCommands | null = null;
	let seeded = !text;
	return {
		get current() {
			return current;
		},
		set current(commands) {
			current = commands;
			if (commands && !seeded) {
				seeded = true;
				commands.setText(text);
			}
		},
	};
}
