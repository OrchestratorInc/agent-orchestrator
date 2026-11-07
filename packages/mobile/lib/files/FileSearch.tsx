import { Stack } from "expo-router";
import { useCallback, useRef, useState } from "react";
import type { SearchBarCommands } from "react-native-screens";
import type { ServerConfig } from "../config";
import { useTheme } from "../ThemeProvider";
import { useWorkspaceSearch } from "./FolderList";

/**
 * Finding a file anywhere in the worktree from the header's own search. Pass
 * a null config while the search is not on offer.
 */
export function useFileSearch(config: ServerConfig | null, sessionId: string) {
	const [query, setQuery] = useState("");
	const bar = useRef<SearchBarCommands>(null);
	const found = useWorkspaceSearch(config, sessionId, query);
	const onChangeText = useCallback((event: { nativeEvent: { text: string } }) => setQuery(event.nativeEvent.text), []);
	const clear = useCallback(() => setQuery(""), []);
	// Closes the search before its bar leaves the header. A UIKit search
	// controller taken out while still active, or an Android search view taken
	// out while expanded, leaves the header as the open search had it.
	const dismiss = useCallback(() => {
		bar.current?.cancelSearch();
		setQuery("");
	}, []);
	return { ...found, bar, onChangeText, clear, dismiss };
}

export type FileSearch = ReturnType<typeof useFileSearch>;

/**
 * The search field under the title on iOS, the top app bar's search action on
 * Android. It filters the page in place; cancelling or closing it clears it.
 */
export function FileSearchBar({ search }: { search: FileSearch }) {
	const t = useTheme();
	return (
		<Stack.SearchBar
			ref={search.bar}
			placeholder="Find a file"
			placement="stacked"
			hideWhenScrolling={false}
			obscureBackground={false}
			autoCapitalize="none"
			tintColor={t.highlight}
			textColor={t.textPrimary}
			hintTextColor={t.textTertiary}
			headerIconColor={t.accent}
			onChangeText={search.onChangeText}
			onCancelButtonPress={search.clear}
			onClose={search.clear}
		/>
	);
}
