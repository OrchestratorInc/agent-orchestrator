import { Feather } from "../icons";
import * as Clipboard from "expo-clipboard";
import { useState } from "react";
import { Modal, Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptics } from "../haptics";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { prose, space, type } from "../tokens";

/** UIKit gives a read-only multiline TextInput range handles; selectable Text only copies its whole block on iOS. */
export function ResponseSelectionModal({ text, onClose }: { text: string; onClose(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const [selection, setSelection] = useState({ start: 0, end: 0 });
	const [copied, setCopied] = useState(false);
	const selectedText = text.slice(selection.start, selection.end);

	return <Modal visible animationType="slide" onRequestClose={onClose}>
		<View style={[styles.root, { paddingTop: insets.top, paddingBottom: insets.bottom }]}>
			<View style={styles.header}>
				<Text style={styles.title}>Select response text</Text>
				<Pressable accessibilityRole="button" accessibilityLabel="Close text selection" hitSlop={10} onPress={onClose} style={styles.close}>
					<Feather name="x" size={20} color={t.textPrimary} />
				</Pressable>
			</View>
			<Text style={styles.hint}>Touch and hold the response, then drag the handles to select a passage.</Text>
			<TextInput
				accessibilityLabel="Response text"
				value={text}
				multiline
				editable={false}
				showSoftInputOnFocus={false}
				selectionColor={t.accent}
				onSelectionChange={(event) => {
					const next = event.nativeEvent.selection;
					// Tapping Copy can collapse the native selection before its press fires.
					if (next.end > next.start) { setSelection(next); setCopied(false); }
				}}
				style={styles.response}
			/>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel="Copy selected text"
				disabled={!selectedText}
				onPress={() => { void Clipboard.setStringAsync(selectedText); haptics.success(); setCopied(true); }}
				style={[styles.copy, !selectedText && styles.copyDisabled]}
			>
				<Feather name={copied ? "check" : "copy"} size={16} color={t.bgBase} />
				<Text style={styles.copyText}>{copied ? "Copied" : "Copy selection"}</Text>
			</Pressable>
		</View>
	</Modal>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	root: { flex: 1, backgroundColor: t.bgBase },
	header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: space.xl, paddingVertical: space.md, borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
	title: { fontFamily: "Geist_600SemiBold", fontSize: type.title3.fontSize, fontWeight: "600", color: t.textPrimary },
	close: { width: 36, height: 36, alignItems: "center", justifyContent: "center" },
	hint: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, paddingHorizontal: space.xl, paddingTop: space.lg },
	response: { flex: 1, marginHorizontal: space.xl, marginVertical: space.md, color: t.textPrimary, fontFamily: "Geist_400Regular", fontSize: prose.fontSize, lineHeight: prose.lineHeight, textAlignVertical: "top" },
	copy: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, marginHorizontal: space.xl, marginBottom: space.md, paddingVertical: space.md, borderRadius: 12, backgroundColor: t.accent },
	copyDisabled: { opacity: 0.4 },
	copyText: { color: t.bgBase, fontFamily: "Geist_600SemiBold", fontSize: type.callout.fontSize, fontWeight: "600" },
});
