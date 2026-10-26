import { Host } from "@expo/ui";
import { Icon, IconButton, Shape, Text as ComposeText, TextField, useNativeState } from "@expo/ui/jetpack-compose";
import { fillMaxWidth } from "@expo/ui/jetpack-compose/modifiers";
import { useState } from "react";
import { StyleSheet } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { space } from "../tokens";
import { materialDrawable } from "../toolbarIcon.android";
import type { ComposerSearchFieldProps } from "./ComposerSearchField.types";

export type { ComposerSearchFieldProps } from "./ComposerSearchField.types";

const SEARCH = require("../../assets/icons/search.xml");

/**
 * The picker's search, docked at the top of the sheet: Material's search-bar
 * input field, a pill-shaped field led by a search glyph, with a clear button
 * once there is a query. Compose's DockedSearchBar cannot be opened on a query,
 * which this picker must be when the composer already holds one.
 */
export function ComposerSearchField({ initialQuery, placeholder, onQuery }: ComposerSearchFieldProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const text = useNativeState(initialQuery);
	const [query, setQuery] = useState(initialQuery);
	const change = (next: string) => {
		setQuery(next);
		onQuery(next);
	};
	return (
		<Host matchContents={{ vertical: true }} style={styles.host} colorScheme={scheme}>
			<TextField
				value={text}
				onValueChange={change}
				singleLine
				shape={Shape.Pill({})}
				keyboardOptions={{ capitalization: "none", autoCorrectEnabled: false, imeAction: "search" }}
				// No underline: Material's search field is a filled pill.
				colors={{ focusedIndicatorColor: "transparent", unfocusedIndicatorColor: "transparent", cursorColor: t.highlight }}
				textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
				modifiers={[fillMaxWidth()]}
			>
				<TextField.Placeholder><ComposeText>{placeholder}</ComposeText></TextField.Placeholder>
				<TextField.LeadingIcon><Icon source={SEARCH} /></TextField.LeadingIcon>
				{query ? (
					<TextField.TrailingIcon>
						<IconButton onClick={() => { text.value = ""; change(""); }}>
							<Icon source={materialDrawable("close")} contentDescription="Clear search" />
						</IconButton>
					</TextField.TrailingIcon>
				) : null}
			</TextField>
		</Host>
	);
}

const styles = StyleSheet.create({
	// Stretched by its parent, not given a percentage width, which a Compose host
	// cannot take as a layout value.
	host: { alignSelf: "stretch", marginTop: space.lg },
});
