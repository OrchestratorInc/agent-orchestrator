import { StyleSheet, TextInput } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import type { Theme } from "../theme";
import { textSelectionColors } from "../textSelection";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { gutter, space, type } from "../tokens";

/**
 * The composer's draft, full screen: one field filling the page above the
 * keyboard, for a prompt too long to write in a pill that stops at six lines.
 * The page's bar holds the way back and Send (see `app/sheets/composer-editor`).
 */
export function ComposerEditor({ value, placeholder, onChangeText }: { value: string; placeholder: string; onChangeText(text: string): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		// Padding rather than a resize, so the field's bottom stays above the
		// keyboard and the caret is never written under it.
		<KeyboardAvoidingView behavior="padding" style={styles.screen}>
			<TextInput
				accessibilityLabel="Message the agent"
				value={value}
				onChangeText={onChangeText}
				placeholder={placeholder}
				placeholderTextColor={t.textFaint}
				{...textSelectionColors(t)}
				multiline
				autoFocus
				maxLength={40_000}
				style={styles.field}
			/>
		</KeyboardAvoidingView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	// Body size: the pill's footnote-sized line suits a short reply, not a page.
	field: { ...type.body, flex: 1, color: t.textPrimary, paddingHorizontal: gutter, paddingTop: space.md, paddingBottom: space.lg, textAlignVertical: "top" },
});
