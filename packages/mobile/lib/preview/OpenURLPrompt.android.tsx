import { AlertDialog, Host, Text as NativeText, TextButton, TextField, useNativeState } from "@expo/ui/jetpack-compose";
import { useState } from "react";
import { StyleSheet } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { font } from "../tokens";
import { openURLPromptCopy, type OpenURLPromptProps } from "./OpenURLPrompt.types";

export type { OpenURLPromptProps } from "./OpenURLPrompt.types";

/**
 * "Open URL…" as Material 3's dialog with one text field, which Android's own
 * alert cannot hold: the URL keyboard, Go to open, and Cancel or back to close.
 */
export function OpenURLPrompt({ visible, ...props }: OpenURLPromptProps) {
	// Mounted only while up, so each opening starts empty.
	return visible ? <OpenURLDialog {...props} /> : null;
}

function OpenURLDialog({ onSubmit, onDismiss }: Omit<OpenURLPromptProps, "visible">) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const text = useNativeState("");
	const [value, setValue] = useState("");
	const submit = (entered: string) => {
		const url = entered.trim();
		if (!url) return;
		onDismiss();
		onSubmit(url);
	};
	const buttonColors = { contentColor: t.accent, disabledContentColor: t.textFaint };
	return (
		<Host matchContents colorScheme={scheme} style={styles.host}>
			<AlertDialog
				onDismissRequest={onDismiss}
				colors={{ containerColor: t.bgElevated, titleContentColor: t.textPrimary, textContentColor: t.textSecondary }}
			>
				<AlertDialog.Title>
					<NativeText style={{ typography: "headlineSmall", fontFamily: font.regular }}>{openURLPromptCopy.title}</NativeText>
				</AlertDialog.Title>
				<AlertDialog.Text>
					<TextField
						value={text}
						autoFocus
						singleLine
						keyboardOptions={{ keyboardType: "uri", imeAction: "go", autoCorrectEnabled: false, capitalization: "none" }}
						keyboardActions={{ onGo: submit }}
						onValueChange={setValue}
						textStyle={{ fontFamily: font.regular }}
						// The app's fields carry the theme's caret and selection colour.
						textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
						colors={{
							focusedTextColor: t.textPrimary,
							unfocusedTextColor: t.textPrimary,
							focusedContainerColor: t.bgSubtle,
							unfocusedContainerColor: t.bgSubtle,
							cursorColor: t.highlight,
							focusedIndicatorColor: t.accent,
							unfocusedIndicatorColor: t.borderStrong,
							focusedLabelColor: t.accent,
							unfocusedLabelColor: t.textSecondary,
							focusedPlaceholderColor: t.textTertiary,
							unfocusedPlaceholderColor: t.textTertiary,
							focusedSupportingTextColor: t.textSecondary,
							unfocusedSupportingTextColor: t.textSecondary,
						}}
					>
						<TextField.Label><NativeText style={{ fontFamily: font.regular }}>Address</NativeText></TextField.Label>
						<TextField.Placeholder><NativeText style={{ fontFamily: font.regular }}>{openURLPromptCopy.placeholder}</NativeText></TextField.Placeholder>
						<TextField.SupportingText><NativeText style={{ fontFamily: font.regular }}>{openURLPromptCopy.message}</NativeText></TextField.SupportingText>
					</TextField>
				</AlertDialog.Text>
				<AlertDialog.ConfirmButton>
					<TextButton enabled={value.trim().length > 0} onClick={() => submit(value)} colors={buttonColors}>
						<NativeText style={{ typography: "labelLarge", fontFamily: font.medium }}>{openURLPromptCopy.open}</NativeText>
					</TextButton>
				</AlertDialog.ConfirmButton>
				<AlertDialog.DismissButton>
					<TextButton onClick={onDismiss} colors={buttonColors}>
						<NativeText style={{ typography: "labelLarge", fontFamily: font.medium }}>{openURLPromptCopy.cancel}</NativeText>
					</TextButton>
				</AlertDialog.DismissButton>
			</AlertDialog>
		</Host>
	);
}

const styles = StyleSheet.create({
	// The dialog draws in its own window; the host only anchors it.
	host: { position: "absolute" },
});
