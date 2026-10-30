import { AlertDialog, Host, OutlinedTextField, Text, TextButton, useNativeState } from "@expo/ui/jetpack-compose";
import { useState } from "react";
import { StyleSheet } from "react-native";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { useTheme, useThemeState } from "../ThemeProvider";
import { normalizeConversationTitle } from "./conversationMenuModel";
import {
	CONVERSATION_TITLE_MAX_LENGTH,
	RENAME_FAILED,
	RENAME_PROMPT_TITLE,
	type ConversationRenamePromptProps,
	type ConversationRenameRequest,
} from "./ConversationRenamePrompt.types";

export type { ConversationRenamePromptProps, ConversationRenameRequest } from "./ConversationRenamePrompt.types";

/**
 * Renaming as Material 3's dialog: a title, an outlined text field, and Cancel
 * and Save as text buttons. It stays up while the rename runs, and keeps what
 * was typed with the reason when it fails.
 */
export function ConversationRenamePrompt({ request, onClose }: ConversationRenamePromptProps) {
	if (!request) return null;
	// Keyed by request, so each opens with its own title in the field.
	return <RenameDialog key={request.id} request={request} onClose={onClose} />;
}

function RenameDialog({ request, onClose }: { request: ConversationRenameRequest; onClose(): void }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const field = useNativeState(request.initialTitle);
	const [draft, setDraft] = useState(request.initialTitle);
	const [saving, setSaving] = useState(false);
	const [error, setError] = useState<string>();
	const title = normalizeConversationTitle(draft);

	const save = async () => {
		if (!title || saving) return;
		setSaving(true);
		setError(undefined);
		try {
			await request.onRename(title);
			haptics.success();
			onClose();
		} catch (cause) {
			haptics.error();
			setError(userFacingError(cause, RENAME_FAILED));
			setSaving(false);
		}
	};
	// A rename in flight cannot be called back, so the dialog holds until it lands.
	const dismiss = () => {
		if (!saving) onClose();
	};

	return (
		<Host matchContents colorScheme={scheme} style={styles.host}>
			<AlertDialog
				onDismissRequest={dismiss}
				properties={{ dismissOnBackPress: !saving, dismissOnClickOutside: !saving }}
				// The theme's own surfaces rather than Material You's, as every
				// Compose control here takes its colours from the palette.
				colors={{ containerColor: t.bgElevated, titleContentColor: t.textPrimary, textContentColor: t.textSecondary }}
			>
				<AlertDialog.Title>
					<Text>{RENAME_PROMPT_TITLE}</Text>
				</AlertDialog.Title>
				<AlertDialog.Text>
					<OutlinedTextField
						value={field}
						autoFocus
						singleLine
						maxLength={CONVERSATION_TITLE_MAX_LENGTH}
						enabled={!saving}
						isError={Boolean(error)}
						onValueChange={setDraft}
						keyboardOptions={{ capitalization: "sentences", imeAction: "done" }}
						keyboardActions={{ onDone: () => void save() }}
						textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
						colors={{
							focusedTextColor: t.textPrimary,
							unfocusedTextColor: t.textPrimary,
							disabledTextColor: t.textTertiary,
							cursorColor: t.highlight,
							focusedIndicatorColor: t.highlight,
							unfocusedIndicatorColor: t.borderStrong,
							focusedLabelColor: t.highlight,
							unfocusedLabelColor: t.textTertiary,
							errorIndicatorColor: t.red,
							errorLabelColor: t.red,
							errorSupportingTextColor: t.red,
							errorCursorColor: t.red,
						}}
					>
						<OutlinedTextField.Label>
							<Text>Conversation title</Text>
						</OutlinedTextField.Label>
						{error ? (
							<OutlinedTextField.SupportingText>
								<Text>{error}</Text>
							</OutlinedTextField.SupportingText>
						) : null}
					</OutlinedTextField>
				</AlertDialog.Text>
				<AlertDialog.ConfirmButton>
					<TextButton enabled={Boolean(title) && !saving} onClick={() => void save()} colors={{ contentColor: t.accent }}>
						<Text>{saving ? "Saving…" : "Save"}</Text>
					</TextButton>
				</AlertDialog.ConfirmButton>
				<AlertDialog.DismissButton>
					<TextButton enabled={!saving} onClick={dismiss} colors={{ contentColor: t.accent }}>
						<Text>Cancel</Text>
					</TextButton>
				</AlertDialog.DismissButton>
			</AlertDialog>
		</Host>
	);
}

const styles = StyleSheet.create({
	// The dialog is a window of its own; the host only has to be in the tree.
	host: { position: "absolute" },
});
