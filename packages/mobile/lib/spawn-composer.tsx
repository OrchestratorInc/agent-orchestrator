import { useNavigation, type NativeStackNavigationProp } from "expo-router";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, Keyboard, Platform, Pressable, StyleSheet, TextInput, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { ChatAttachmentMenu } from "./chat/ChatAttachmentMenu";
import { ComposerGlass } from "./chat/composer-glass";
import { ComposerSuggestionPanel } from "./chat/ComposerSuggestionPanel";
import { findComposerSuggestion, rankComposerSkills, replaceComposerSuggestion } from "./chat/composerSuggestions";
import { createRequestGate } from "./chat/requestGate";
import type { ChatSkill } from "./chat/types";
import type { ComposerImageAsset, PasteOverflow } from "./chat/composerImages";
import { COMPOSER_DISC, COMPOSER_FIELD_HEIGHT, COMPOSER_FONT_SCALE_CAP, COMPOSER_RADIUS, composerStyleRules, useComposerFieldSize } from "./chat/composerStyles";
import { usePressFeedback } from "./pressFeedback";
import type { SpawnComposerVoice } from "./spawn-composer-controls.types";
import { useImagePaste } from "./chat/useImagePaste";
import { DictationField } from "./voice/DictationField";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { textSelectionColors } from "./textSelection";
import { iconSize, radius, space, stateOpacity, touchTarget } from "./tokens";

// The chat composer's numbers, so the two pills sit alike.
const CORNER_CONTROL = touchTarget + space.xxs;
const dictateGlyph = { ios: "mic.fill", android: "mic" } as const;

export type SpawnComposerProps = {
	prompt: string;
	onChangePrompt: (value: string) => void;
	/** Names who will answer, as a conversation's field does: "Ask Codex". */
	placeholder: string;
	/** What goes with the task, drawn inside the pill over the field. */
	attachments?: ReactNode;
	/** Who runs the task and how freely: the controls row's content. */
	settings: ReactNode;
	onChoosePhoto: () => void;
	onChooseFile: () => void;
	/** Takes images pasted into the field; null while they can't be attached. */
	onPasteImages?: ((assets: ComposerImageAsset[], overflow?: PasteOverflow) => void) | null;
	voice: SpawnComposerVoice;
	/**
	 * The agent's skills for a `/` at the start of the prompt, read when the list
	 * opens; absent where the task's agent has none to offer (Cloud).
	 */
	onLoadSkills?: () => Promise<ChatSkill[]>;
	onSpawn: () => void;
	busy: boolean;
	disabled: boolean;
};

/** The `/` list, and how tall the composer it stands over is. */
export type SpawnSuggestionOverlay = { panel: ReactNode; composerHeight: number };

/**
 * Where the page draws the composer's `/` list: over the page above the
 * composer, so the machine and project rows stay where they are. Without a
 * host the list sits in the composer's own column, over the pill.
 */
export const SpawnSuggestionHost = createContext<((overlay: SpawnSuggestionOverlay | null) => void) | null>(null);

/**
 * The new task's composer, drawn as the chat's open card (`ChatComposer`):
 * picked attachments over the field, the field over a controls row of who runs the task and how freely, between
 * attach and the round button. Unlike the chat's it never folds to a pill: the
 * page is for choosing who runs the task, so the model, effort and approval
 * stay in sight while the field is empty and unfocused.
 */
export function SpawnComposer({ prompt, onChangePrompt, placeholder, attachments, settings, onChoosePhoto, onChooseFile, onPasteImages, voice, onLoadSkills, onSpawn, busy, disabled }: SpawnComposerProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	const field = useRef<TextInput>(null);
	const pasteLayout = useImagePaste(onPasteImages ?? null);
	// The field's heights at the reader's text size, as the chat's: in points
	// alone, the largest sizes cut the placeholder off top and bottom.
	const fieldSize = useComposerFieldSize();
	const navigation = useNavigation<NativeStackNavigationProp<Record<string, object | undefined>>>();
	// A new task is for typing into, so the page opens with the keyboard up, as
	// a new chat does. iOS focuses
	// the field with the page. Android's focus lands mid-transition there and is
	// lost, so it waits for the push to finish, once: coming back from a sheet
	// opened over the page must not raise the keyboard again.
	useEffect(() => {
		if (Platform.OS !== "android") return;
		let focusedOnce = false;
		return navigation.addListener("transitionEnd", (event) => {
			if (focusedOnce || event.data.closing) return;
			focusedOnce = true;
			field.current?.focus();
		});
	}, [navigation]);
	const listening = voice.state === "recording" || voice.state === "starting";
	const transcribing = voice.state === "transcribing";
	// While the mic is open the pill holds only the waveform and the stop
	// button: no attach, no settings row, so the trace gets the whole width.
	const dictating = listening || transcribing;
	const promptEmpty = !prompt.trim();
	const skills = useSkillSuggestions({ prompt, onChangePrompt, onLoadSkills, enabled: !dictating && !busy });
	const overlayHost = useContext(SpawnSuggestionHost);
	const [composerHeight, setComposerHeight] = useState(0);
	useEffect(() => { overlayHost?.(skills.panel ? { panel: skills.panel, composerHeight } : null); }, [composerHeight, overlayHost, skills.panel]);
	useEffect(() => () => overlayHost?.(null), [overlayHost]);

	// One round button that follows the prompt, as the chat's: it stops the mic
	// while it is open, starts the task once there is something written, and
	// otherwise dictates; a device with no recogniser keeps Start, which says
	// what is missing when pressed. Dictation is hands-free and drops the
	// keyboard.
	// A denied mic still dictates from here: its tap opens Settings, where the
	// microphone can be turned back on.
	const canDictate = voice.state !== "unavailable";
	const micDenied = voice.state === "denied";
	const primary = busy ? "send" : listening ? "stopDictation" : transcribing ? "transcribing" : promptEmpty && canDictate ? "dictate" : "send";
	const roundButton = primary === "send"
		? <Pressable
			accessibilityRole="button"
			accessibilityLabel={busy ? "Starting task" : "Start task"}
			accessibilityState={{ disabled, busy }}
			disabled={disabled}
			android_ripple={sendFeedback.android_ripple}
			onPress={onSpawn}
			testID="spawn-submit"
			style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed, disabled && !busy && styles.inactive]}
		>
			<View style={[styles.roundDisc, styles.roundAccent]}>
				{busy ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.sm} weight="bold" color={t.onAccent} />}
			</View>
		</Pressable>
		: <Pressable
			accessibilityRole="button"
			accessibilityLabel={primary === "stopDictation" ? "Stop dictating" : primary === "transcribing" ? "Transcribing" : micDenied ? "Turn on microphone access in Settings" : "Dictate the task"}
			accessibilityState={{ disabled: transcribing, busy: transcribing }}
			disabled={transcribing}
			android_ripple={sendFeedback.android_ripple}
			onPress={() => { if (primary === "dictate") Keyboard.dismiss(); voice.onToggleLatched(); }}
			testID="spawn-dictate"
			style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed]}
		>
			<View style={[styles.roundDisc, primary === "stopDictation" ? styles.dictating : styles.roundAccent]}>
				{transcribing ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name={primary === "stopDictation" ? "stop" : micDenied ? "micOff" : dictateGlyph} size={iconSize.sm} weight="bold" color={primary === "stopDictation" ? t.onDestructive : t.onAccent} />}
			</View>
		</Pressable>;

	return (
		<>
		{overlayHost ? null : skills.panel}
		<View style={styles.composer} onLayout={(event) => setComposerHeight(event.nativeEvent.layout.height)}>
			<ComposerGlass radius={COMPOSER_RADIUS} />
			{attachments}
			<View style={[styles.composerRow, dictating ? styles.dictationInsets : styles.fieldInsets]}>
				{/* While the mic is open the dictation takes the field's place, as
				    in the chat's composer; the field waits hidden with the prompt. */}
				{dictating ? <DictationField state={voice.state} partial={voice.partial} subscribeLevel={voice.subscribeLevel} style={styles.dictation} waveformOnly /> : null}
				<TextInput
					ref={field}
					onLayout={pasteLayout}
					accessibilityLabel="Task prompt"
					value={prompt}
					onChangeText={onChangePrompt}
					onSelectionChange={skills.onSelectionChange}
					onFocus={skills.onFocus}
					onBlur={skills.onBlur}
					placeholder={placeholder}
					placeholderTextColor={t.textFaint}
					maxFontSizeMultiplier={COMPOSER_FONT_SCALE_CAP}
					{...textSelectionColors(t)}
					// Grows on its own between the style's min and max height, as the
					// chat's field does: a height set from the content-size event lagged
					// a line behind and scrolled the top line out of view.
					style={[styles.input, fieldSize.field, dictating && styles.hiddenField]}
					multiline
					// Android focuses once the push has finished (see above).
					autoFocus={Platform.OS === "ios"}
				/>
			</View>
			{dictating ? null : <>
				<View pointerEvents="box-none" style={styles.controlsRow}>
					<View style={styles.settingsSlot}>{settings}</View>
				</View>
				<View style={[styles.corner, styles.cornerLeading]}>
					<ChatAttachmentMenu disabled={busy} canAttachFile onChoosePhoto={onChoosePhoto} onChooseFile={onChooseFile} />
				</View>
			</>}
			<View style={[styles.corner, styles.cornerTrailing]}>{roundButton}</View>
		</View>
		</>
	);
}

/**
 * The chat composer's `/` list (see `ChatComposer`) for a task's first prompt:
 * a `/` at its start, while the field has focus, lists the agent's skills over
 * the composer and what follows filters them; a choice replaces the token.
 * Only skills: an `@` file needs the worktree the task has not made yet.
 */
function useSkillSuggestions({ prompt, onChangePrompt, onLoadSkills, enabled }: {
	prompt: string;
	onChangePrompt: (value: string) => void;
	onLoadSkills?: () => Promise<ChatSkill[]>;
	enabled: boolean;
}) {
	const [focused, setFocused] = useState(false);
	const [cursor, setCursor] = useState<number>();
	const latest = useRef({ prompt, cursor });
	latest.current = { prompt, cursor };
	const found = focused && enabled && onLoadSkills ? findComposerSuggestion(prompt, cursor) : undefined;
	const suggestion = found?.kind === "skills" ? found : undefined;
	const open = Boolean(suggestion);
	// Kept with the loader that read it: another agent's or project's list is
	// never shown for this one, while this one's stays up as it is read again.
	const [loaded, setLoaded] = useState<{ from: () => Promise<ChatSkill[]>; skills: ChatSkill[] }>();
	const gate = useRef(createRequestGate()).current;
	useEffect(() => () => gate.invalidate(), [gate]);
	// Read each time the list opens, and again for another agent or project
	// (a new loader); the loader's own cache spares the machine the repeats.
	useEffect(() => {
		if (!open || !onLoadSkills) { gate.invalidate(); return; }
		const request = gate.begin();
		onLoadSkills()
			.then((skills) => { if (gate.isCurrent(request)) setLoaded({ from: onLoadSkills, skills }); })
			.catch(() => { if (gate.isCurrent(request)) setLoaded({ from: onLoadSkills, skills: [] }); });
	}, [gate, onLoadSkills, open]);
	const query = suggestion?.query;
	const catalog = loaded && loaded.from === onLoadSkills ? loaded.skills : undefined;
	const items = useMemo(() => query !== undefined && catalog ? rankComposerSkills(catalog, query) : undefined, [catalog, query]);
	// Text that only looks like a command (`/usr/bin`, an unknown name) gets no
	// list rather than an empty one in the way.
	const show = Boolean(suggestion && !(items?.length === 0 && suggestion.query.trim()));
	const select = useCallback((value: string) => {
		const { prompt: text, cursor: caret } = latest.current;
		const trigger = findComposerSuggestion(text, caret);
		if (trigger?.kind !== "skills") return;
		const next = replaceComposerSuggestion(text, trigger, value);
		// After the separator, so the caret no longer reads as the trigger.
		const tokenEnd = next.length - (text.length - trigger.end);
		const nextCaret = /\s/.test(next[tokenEnd] ?? "") ? tokenEnd + 1 : tokenEnd;
		latest.current = { prompt: next, cursor: nextCaret };
		setCursor(nextCaret);
		onChangePrompt(next);
	}, [onChangePrompt]);
	// The same element while nothing in it changes: the page holds it in its own
	// state (see SpawnSuggestionHost), so a new one each render would loop.
	const panel = useMemo(() => show ? <ComposerSuggestionPanel kind="skills" items={items} onSelect={select} /> : null, [items, select, show]);
	return {
		panel,
		onSelectionChange: (event: { nativeEvent: { selection: { start: number } } }) => setCursor(event.nativeEvent.selection.start),
		onFocus: () => setFocused(true),
		onBlur: () => setFocused(false),
	};
}

// The chat composer's own rules for its corners, controls row and round
// button (see `ChatComposer`), on the pill they share.
const makeStyles = (t: Theme) => StyleSheet.create({
	...composerStyleRules(t),
	corner: { position: "absolute", bottom: space.xs, height: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xxs },
	cornerLeading: { left: space.xs },
	cornerTrailing: { right: space.xs },
	dictation: { minHeight: COMPOSER_FIELD_HEIGHT },
	// Out of the layout while dictating, but still mounted with the prompt.
	hiddenField: { display: "none" },
	fieldInsets: { paddingHorizontal: space.sm },
	// The stop button sits in the trailing corner on the field's own row.
	dictationInsets: { paddingLeft: space.md, paddingRight: CORNER_CONTROL },
	controlsRow: { width: "100%", height: touchTarget, paddingHorizontal: CORNER_CONTROL, flexDirection: "row", alignItems: "center", gap: space.xxs, overflow: "hidden" },
	settingsSlot: { flex: 1, minWidth: 0, minHeight: touchTarget, alignItems: "stretch", justifyContent: "center", overflow: "hidden" },
	inactive: { opacity: stateOpacity.disabled },
	roundTarget: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	roundDisc: { width: COMPOSER_DISC, height: COMPOSER_DISC, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center" },
	roundAccent: { backgroundColor: t.accent },
	dictating: { backgroundColor: t.red },
});
