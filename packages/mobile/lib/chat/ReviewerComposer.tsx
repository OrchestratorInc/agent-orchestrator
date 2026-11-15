import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Pressable, Text, TextInput, View } from "react-native";
import Animated, { useAnimatedStyle } from "react-native-reanimated";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { AppSymbol } from "../AppSymbol";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { KEYBOARD_DOCK_GAP } from "../session/keyboardInset";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { iconSize, stateOpacity, touchTarget } from "../tokens";
import { DictationField } from "../voice/DictationField";
import { MicKey } from "../voice/MicKey";
import { useVoiceInput } from "../voice/useVoiceInput";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { ComposerAttachmentTray } from "./ComposerAttachmentTray";
import { ComposerGlass } from "./composer-glass";
import { HardwareReturn } from "./HardwareReturn";
import { useKeyboardSendRefocus } from "./useKeyboardSendRefocus";
import { COMPOSER_FIELD_HEIGHT, COMPOSER_RADIUS, composerStyleRules } from "./composerStyles";
import type { ChatImage, ChatResource } from "./types";
import { composerTrayItems, forgetComposerAttachments, readComposerAttachments, removeComposerAttachment, updateComposerAttachments, useComposerAttachments, type ComposerAttachment as Attachment } from "./composerAttachments";
import { attachComposerImages, MAX_ATTACHMENTS } from "./composerImages";
import { useImagePaste } from "./useImagePaste";

const MAX_FILE_BYTES = 500_000;

/** Reviewer-only composer: send/attachments/interrupt, deliberately no session actions. */
export function ReviewerComposer({ attachmentKey, busy, stopped, attachmentsEnabled = true, onSend, onAcknowledgeSend, completedRetry, onInterrupt, restingInset, dockRiseOverride }: {
	/** Where unsent attachments live, so a pick that outlives this composer still lands (see composerAttachments.ts). */
	attachmentKey: string;
	busy: boolean;
	stopped: boolean;
	attachmentsEnabled?: boolean;
	onSend(text: string, images?: ChatImage[], resources?: ChatResource[]): Promise<string>;
	onAcknowledgeSend(id: string): Promise<void>;
	completedRetry?: { id: string; draftText: string };
	onInterrupt(): Promise<void>;
	/** The dock's inset with the keyboard down; see ChatComposer. */
	restingInset: number;
	/** See ChatComposer's prop of the same name. */
	dockRiseOverride?: number;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const stopFeedback = usePressFeedback("button");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	const [text, setText] = useState("");
	const { items: attachments, notice: attachmentNotice } = useComposerAttachments(attachmentKey);
	const trayItems = useMemo(() => composerTrayItems(attachments), [attachments]);
	const clearAttachments = useCallback(() => updateComposerAttachments(attachmentKey, () => ({ items: [] })), [attachmentKey]);
	const dismissAttachmentNotice = useCallback(() => updateComposerAttachments(attachmentKey, (current) => current.notice ? { items: current.items } : current), [attachmentKey]);
	const [submitting, setSubmitting] = useState(false);
	const inputRef = useRef<TextInput>(null);
	// See ChatComposer: a hardware-keyboard send gives the field its focus back.
	const keyboardSend = useKeyboardSendRefocus(inputRef, submitting);
	const [error, setError] = useState("");
	const keyboard = useReanimatedKeyboardAnimation();
	const voice = useVoiceInput({ onTranscript: useCallback((spoken: string) => setText((old) => old ? `${old} ${spoken}` : spoken), []) });
	const listening = voice.state === "starting" || voice.state === "recording" || voice.state === "transcribing";
	useEffect(() => {
		if (!completedRetry) return;
		if (text.trim() === completedRetry.draftText) { setText(""); clearAttachments(); }
		void onAcknowledgeSend(completedRetry.id).catch((cause) => setError(cause instanceof Error ? cause.message : "Could not clear the sent reply."));
	}, [completedRetry, onAcknowledgeSend, text, clearAttachments]);

	const pasteLayout = useImagePaste(stopped || submitting || !attachmentsEnabled ? null : (assets, overflow) => {
		setError("");
		if (attachComposerImages(attachmentKey, assets, overflow)) haptics.tap();
	});
	const addPhoto = async () => {
		// Captured now: the pick can outlive this composer (see ChatComposer).
		const key = attachmentKey;
		setError("");
		dismissAttachmentNotice();
		try {
			const result = await ImagePicker.launchImageLibraryAsync({ mediaTypes: ["images"], base64: true, quality: 0.82, allowsMultipleSelection: true, selectionLimit: 4 });
			if (result.canceled) return;
			attachComposerImages(key, result.assets);
		} catch (cause) {
			const notice = cause instanceof Error ? cause.message : "Could not attach this photo.";
			updateComposerAttachments(key, (current) => ({ items: current.items, notice }));
		}
	};

	const addFile = async () => {
		const key = attachmentKey;
		setError("");
		dismissAttachmentNotice();
		try {
			const result = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true, type: ["text/*", "application/json", "application/xml", "application/yaml"] });
			if (result.canceled) return;
			const existing = readComposerAttachments(key).items.length;
			const added: Attachment[] = [];
			for (const asset of result.assets) {
				if (existing + added.length >= MAX_ATTACHMENTS) throw new Error(`You can attach up to ${MAX_ATTACHMENTS} items.`);
				if ((asset.size || 0) > MAX_FILE_BYTES) throw new Error(`${asset.name} is larger than 500 KB.`);
				const body = await fetch(asset.uri).then((response) => response.text());
				const bytes = new TextEncoder().encode(body).byteLength;
				if (bytes > MAX_FILE_BYTES) throw new Error(`${asset.name} is larger than 500 KB.`);
				added.push({ id: `${asset.uri}-${Date.now()}`, kind: "resource", name: asset.name, bytes, resource: { uri: `mobile-attachment://${encodeURIComponent(asset.name)}`, name: asset.name, mimeType: asset.mimeType || "text/plain", text: body } });
			}
			updateComposerAttachments(key, (current) => ({ items: [...current.items, ...added], notice: current.notice }));
		} catch (cause) {
			const notice = cause instanceof Error ? cause.message : "Could not attach this file.";
			updateComposerAttachments(key, (current) => ({ items: current.items, notice }));
		}
	};

	const submit = async (from: "button" | "keyboard" = "button") => {
		if (submitting || stopped || (!text.trim() && !attachments.length)) return;
		if (from === "keyboard") keyboardSend.begin();
		// Only what this send submitted; see ChatComposer.
		const sentKey = attachmentKey;
		const sent = new Set(attachments.map((item) => item.id));
		setSubmitting(true); setError(""); dismissAttachmentNotice();
		try {
			const id = await onSend(text.trim(), attachments.flatMap((item) => item.kind === "image" ? [item.image] : []), attachments.flatMap((item) => item.kind === "resource" ? [item.resource] : []));
			setText(""); forgetComposerAttachments(sentKey, sent);
			await onAcknowledgeSend(id);
			haptics.success();
		} catch (cause) { setError(cause instanceof Error ? cause.message : "Could not send this reply."); }
		finally { setSubmitting(false); }
	};

	// Same dock and pill as the session composer (ChatComposer), minus the turn
	// settings and queue rows a reviewer has no use for. The dock holds its resting
	// inset and rides the keyboard's progress to close the difference, exactly as
	// the session composer does, so the two sit in the same place.
	const dockRise = useAnimatedStyle(() => ({
		transform: [{ translateY: (restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value) }],
	}));
	const sendDisabled = stopped || submitting || (!text.trim() && !attachments.length);
	return <Animated.View style={[styles.dock, { paddingBottom: restingInset }, dockRise]}>
		{error || attachmentNotice || voice.error ? <Text accessibilityRole="alert" style={styles.error}>{error || attachmentNotice || voice.error}</Text> : null}
		<View style={[styles.composer, stopped && inactive]}>
			<ComposerGlass radius={COMPOSER_RADIUS} />
			{/* What goes with the reply, inside the pill over the field, as in ChatComposer. */}
			<ComposerAttachmentTray items={trayItems} onRemove={(id) => removeComposerAttachment(attachmentKey, id)} />
			<View style={styles.composerRow}>
				<ChatAttachmentMenu disabled={stopped || submitting || !attachmentsEnabled} canAttachFile onChoosePhoto={() => void addPhoto()} onChooseFile={() => void addFile()} />
				{/* While the mic is open the dictation takes the field's place, as in ChatComposer. */}
				{listening ? <DictationField state={voice.state} partial={voice.partial} subscribeLevel={voice.subscribeLevel} style={dictation} /> : null}
				{/* Return on a hardware keyboard sends; Cmd/Ctrl have no steer to pick here.
				    The wrapper holds the field's place in the row, so it hides with it.
				    While the reviewer works there is no Send, only Stop, so Return is still
				    taken but does nothing, rather than turning into a newline for that turn. */}
				<HardwareReturn style={[styles.inputSlot, listening && hiddenField]} enabled={!stopped && !submitting} onReturn={() => { if (!busy) void submit("keyboard"); }}>
					<TextInput
						ref={inputRef}
						onLayout={pasteLayout}
						accessibilityLabel="Reply to reviewer"
						value={text}
						onChangeText={setText}
						onFocus={keyboardSend.focused}
						placeholder={stopped ? "Reviewer is stopped" : "Reply to reviewer…"}
						placeholderTextColor={t.textFaint}
						{...textSelectionColors(t)}
						multiline
						maxLength={40_000}
						editable={!stopped && !submitting}
						// Grows with its text between the rule's min and max, as ChatComposer's does.
						style={[styles.input, listening && hiddenField]}
					/>
				</HardwareReturn>
				<MicKey variant="plain" size={touchTarget} glyphSize={iconSize.lg} disabled={stopped || submitting} state={voice.state} onToggleLatched={voice.toggleLatched} />
				{/* A view per face: both clip (keeping the ripple round), and Android skips
				    drawing a clipping view's child that resized in the same update as the
				    view's own fill, so a reused stop view left the send arrow blank. */}
				{busy ? <Pressable key="stop" accessibilityRole="button" accessibilityLabel="Stop reviewer" android_ripple={stopFeedback.android_ripple} onPress={() => { haptics.tap(); void onInterrupt().catch((cause) => setError(cause instanceof Error ? cause.message : "Could not stop the reviewer.")); }} style={({ pressed }) => [styles.stop, pressed && stopFeedback.pressed]}><AppSymbol name="stop" size={iconSize.xs} color={t.textPrimary} /></Pressable> : <Pressable key="send" accessibilityRole="button" accessibilityLabel="Send reply" accessibilityState={{ disabled: sendDisabled, busy: submitting }} disabled={sendDisabled} android_ripple={sendFeedback.android_ripple} onPress={() => void submit()} style={({ pressed }) => [styles.send, pressed && sendFeedback.pressed, sendDisabled && inactive]}>{submitting ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.md} color={t.onAccent} />}</Pressable>}
			</View>
		</View>
	</Animated.View>;
}

// The session composer's dock, pill, field and controls; it draws nothing of its own.
const makeStyles = composerStyleRules;
/** The pill, or a control in it, that cannot act right now. */
const inactive = { opacity: stateOpacity.disabled };
/** Out of the layout while dictating, but still mounted with its draft. */
const hiddenField = { display: "none" } as const;
const dictation = { minHeight: COMPOSER_FIELD_HEIGHT };
