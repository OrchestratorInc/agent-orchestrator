import { useEffect, useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { Spinner } from "../Spinner";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { iconSize, space, stateOpacity, type } from "../tokens";
import { ComposerGlass } from "./composer-glass";
import { COMPOSER_RADIUS, composerStyleRules } from "./composerStyles";
import { withEditedBody } from "./messageAttachments";
import { clientMessageId } from "./useConversation";

/**
 * One in-progress edit. The timeline holds it above the virtualized list, so the
 * draft and its delivery handle survive the row scrolling out and back.
 */
export type MessageEditDraft = {
	turnId: string;
	/** The body as first sent, without AO's staged-path block. */
	initialText: string;
	text: string;
	/** AO-staged paths re-appended to the edited body, exactly as first sent. */
	attachments: string[];
	/**
	 * Retrying an uncertain send reuses this, so the daemon replays the first
	 * result instead of branching twice; a definitive refusal replaces it.
	 */
	deliveryId: string;
	/**
	 * A send whose delivery is unresolved: the exact payload (and the body shown
	 * for it) that Retry must resend under the same delivery id. While set, the
	 * text is locked and the edit outlives its prompt.
	 */
	pending?: { body: string; payload: string };
};

const BLOCKED_COPY = "Stop the current turn to edit.";

export function messageEditDraft(input: Omit<MessageEditDraft, "text" | "deliveryId">): MessageEditDraft {
	return { ...input, text: input.initialText, deliveryId: clientMessageId() };
}

/**
 * Edits an earlier human prompt in place of its bubble, as the desktop's inline
 * editor does: the daemon branches just before it and sends the new text with
 * the original attachments and quotes, so only the written body is editable.
 */
export function InlineMessageEditor({
	draft,
	blocked,
	onCancel,
	onEdit,
	onFocus,
}: {
	draft: MessageEditDraft;
	/** The agent is no longer idle, so the daemon would refuse to branch. */
	blocked: boolean;
	onCancel(): void;
	/** Sends the wire `payload`; `body` is what stays on screen if delivery is uncertain. */
	onEdit(payload: string, body: string): Promise<void>;
	onFocus?(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const cancelFeedback = usePressFeedback("button");
	// Send is filled with the accent, so its ripple is drawn in the fill's ink, over it.
	const sendFeedback = usePressFeedback("button", { tone: "onAccent", foreground: true });
	const [text, setText] = useState(draft.text);
	const [sending, setSending] = useState(false);
	const [error, setError] = useState<string>();
	const pending = draft.pending;
	// Retrying an unresolved delivery replays it under the same id, which the
	// daemon answers whether or not the agent has started other work since.
	const isBlocked = blocked && !pending;
	const unchanged = !pending && text.trimEnd() === draft.initialText.trimEnd();
	const shown = pending ? pending.body : text;
	const sendDisabled = sending || isBlocked || !shown.trim() || unchanged;
	// While the agent works, Send stays tappable so it can say why it will not send.
	const sendInert = sending || !shown.trim() || unchanged;

	useEffect(() => {
		if (!blocked) setError((current) => current === BLOCKED_COPY ? undefined : current);
	}, [blocked]);

	const send = async () => {
		if (isBlocked && !sendInert) {
			haptics.error();
			setError(BLOCKED_COPY);
			return;
		}
		if (sendDisabled) return;
		// The outcome answers with its own haptic, so the press itself stays quiet.
		setSending(true);
		setError(undefined);
		try {
			await (pending ? onEdit(pending.payload, pending.body) : onEdit(withEditedBody(text, draft.attachments), text));
			haptics.success();
		} catch (cause) {
			haptics.error();
			setError(cause instanceof Error ? cause.message : "Couldn't send the edited message.");
			setSending(false);
		}
	};

	// The composer's own pill, glass and controls, so an edit reads as the same
	// place you write a message, just moved up to the prompt it replaces.
	return <View style={styles.editor}>
		<ComposerGlass radius={COMPOSER_RADIUS} />
		{error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
		<View style={styles.composerRow}>
			<TextInput
				accessibilityLabel="Edit message"
				value={shown}
				onChangeText={(value) => { draft.text = value; setText(value); }}
				onFocus={onFocus}
				editable={!sending && !pending}
				multiline
				autoFocus
				scrollEnabled
				placeholder="Message the agent"
				placeholderTextColor={t.textFaint}
				{...textSelectionColors(t)}
				style={[styles.input, styles.field]}
			/>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={pending ? "Abandon edit recovery" : "Cancel edit"}
				accessibilityState={{ disabled: sending }}
				disabled={sending}
				onPress={onCancel}
				android_ripple={cancelFeedback.android_ripple}
				style={({ pressed }) => [styles.stop, pressed && cancelFeedback.pressed, sending && { opacity: stateOpacity.disabled }]}
			><AppSymbol name="close" size={iconSize.sm} color={t.textPrimary} /></Pressable>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={pending ? "Retry edit safely" : "Send edited message"}
				accessibilityState={{ disabled: sendDisabled, busy: sending }}
				disabled={sendInert}
				onPress={() => void send()}
				android_ripple={sendFeedback.android_ripple}
				style={({ pressed }) => [styles.send, pressed && sendFeedback.pressed, sendDisabled && !sending && { opacity: stateOpacity.disabled }]}
			>{sending ? <Spinner size={iconSize.md} color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.md} color={t.onAccent} />}</Pressable>
		</View>
	</View>;
}

const makeStyles = (t: Theme) => {
	const composer = composerStyleRules(t);
	return StyleSheet.create({
		editor: { ...composer.composer, alignSelf: "stretch" },
		composerRow: composer.composerRow,
		input: composer.input,
		// No attach control on the left, so the text takes the pill's own inset.
		field: { paddingLeft: space.md },
		// Clipped so an Android ripple keeps to the round control.
		stop: { ...composer.stop, overflow: "hidden" },
		send: { ...composer.send, overflow: "hidden" },
		error: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.md, paddingTop: space.sm },
	});
};
