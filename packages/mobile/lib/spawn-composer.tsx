import { useNavigation, type NativeStackNavigationProp } from "expo-router";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, Keyboard, Platform, Pressable, StyleSheet, TextInput, View } from "react-native";
import Animated, { interpolate, useAnimatedStyle, useSharedValue, withSpring, withTiming } from "react-native-reanimated";
import { AppSymbol } from "./AppSymbol";
import { ChatAttachmentMenu } from "./chat/ChatAttachmentMenu";
import { ComposerGlass } from "./chat/composer-glass";
import { COMPOSER_DISC, COMPOSER_FIELD_HEIGHT, COMPOSER_RADIUS, composerStyleRules } from "./chat/composerStyles";
import { usePressFeedback } from "./pressFeedback";
import type { SpawnComposerVoice } from "./spawn-composer-controls.types";
import { DictationField } from "./voice/DictationField";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { textSelectionColors } from "./textSelection";
import { iconSize, radius, space, stateOpacity, touchTarget } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

// The chat composer's numbers, so the two pills open and sit alike.
const CORNER_CONTROL = touchTarget + space.xxs;
const COMPOSER_SPRING = { damping: 22, stiffness: 260, mass: 0.9 } as const;
const dictateGlyph = { ios: "mic.fill", android: "mic" } as const;

export type SpawnComposerProps = {
	prompt: string;
	onChangePrompt: (value: string) => void;
	/** Names who will answer, as a conversation's field does: "Ask Codex". */
	placeholder: string;
	/** Who runs the task and how freely: the controls row's content. */
	settings: ReactNode;
	/** Keeps the pill open while files wait to be sent, as the chat's does. */
	hasAttachments: boolean;
	onChoosePhoto: () => void;
	onChooseFile: () => void;
	voice: SpawnComposerVoice;
	onSpawn: () => void;
	busy: boolean;
	disabled: boolean;
};

/**
 * The new task's composer, drawn as the chat's (`ChatComposer`): a slim pill
 * of attach, the field and the round button that opens, while writing, into a
 * card whose controls row holds who runs the task and how freely. Only what it
 * sends differs.
 */
export function SpawnComposer({ prompt, onChangePrompt, placeholder, settings, hasAttachments, onChoosePhoto, onChooseFile, voice, onSpawn, busy, disabled }: SpawnComposerProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	const [focused, setFocused] = useState(false);
	const field = useRef<TextInput>(null);
	const navigation = useNavigation<NativeStackNavigationProp<Record<string, object | undefined>>>();
	// A new task is for typing into, so the page opens with the keyboard up and
	// the composer open onto who runs the task, as a new chat does. iOS focuses
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
	const promptEmpty = !prompt.trim();
	const expanded = focused || !promptEmpty || hasAttachments || listening || transcribing;
	const reduceMotion = useReducedMotion();
	const openness = useSharedValue(expanded ? 1 : 0);
	useEffect(() => {
		openness.value = reduceMotion ? withTiming(expanded ? 1 : 0, { duration: 0 }) : withSpring(expanded ? 1 : 0, COMPOSER_SPRING);
	}, [expanded, openness, reduceMotion]);
	const fieldInsets = useAnimatedStyle(() => ({
		paddingLeft: interpolate(openness.value, [0, 1], [CORNER_CONTROL, space.sm]),
		paddingRight: interpolate(openness.value, [0, 1], [CORNER_CONTROL, space.sm]),
	}));
	const controlsOpening = useAnimatedStyle(() => ({
		height: interpolate(openness.value, [0, 1], [0, touchTarget], "clamp"),
		opacity: interpolate(openness.value, [0.35, 1], [0, 1], "clamp"),
		transform: [{ translateY: interpolate(openness.value, [0, 1], [space.sm, 0], "clamp") }],
	}));

	// One round button that follows the prompt, as the chat's: it stops the mic
	// while it is open, starts the task once there is something written, and
	// otherwise dictates; a device that cannot dictate keeps Start, which says
	// what is missing when pressed. Dictation is hands-free and drops the
	// keyboard; the card stays open while the mic is.
	const canDictate = voice.state !== "unavailable" && voice.state !== "denied";
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
			accessibilityLabel={primary === "stopDictation" ? "Stop dictating" : primary === "transcribing" ? "Transcribing" : "Dictate the task"}
			accessibilityState={{ disabled: transcribing, busy: transcribing }}
			disabled={transcribing}
			android_ripple={sendFeedback.android_ripple}
			onPress={() => { if (primary === "dictate") Keyboard.dismiss(); voice.onToggleLatched(); }}
			testID="spawn-dictate"
			style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed]}
		>
			<View style={[styles.roundDisc, primary === "stopDictation" ? styles.dictating : styles.roundAccent]}>
				{transcribing ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name={primary === "stopDictation" ? "stop" : dictateGlyph} size={iconSize.sm} weight="bold" color={primary === "stopDictation" ? t.onDestructive : t.onAccent} />}
			</View>
		</Pressable>;

	return (
		<View style={styles.composer}>
			<ComposerGlass radius={COMPOSER_RADIUS} />
			<Animated.View style={[styles.composerRow, fieldInsets]}>
				{/* While the mic is open the dictation takes the field's place, as
				    in the chat's composer; the field waits hidden with the prompt. */}
				{listening || transcribing ? <DictationField state={voice.state} partial={voice.partial} subscribeLevel={voice.subscribeLevel} style={styles.dictation} /> : null}
				<TextInput
					ref={field}
					accessibilityLabel="Task prompt"
					value={prompt}
					onChangeText={onChangePrompt}
					onFocus={() => setFocused(true)}
					onBlur={() => setFocused(false)}
					placeholder={placeholder}
					placeholderTextColor={t.textFaint}
					{...textSelectionColors(t)}
					// Grows on its own between the style's min and max height, as the
					// chat's field does: a height set from the content-size event lagged
					// a line behind and scrolled the top line out of view.
					style={[styles.input, (listening || transcribing) && styles.hiddenField]}
					multiline
					// Android focuses once the push has finished (see above).
					autoFocus={Platform.OS === "ios"}
				/>
			</Animated.View>
			<Animated.View pointerEvents={expanded ? "box-none" : "none"} style={[styles.controlsRow, { paddingLeft: CORNER_CONTROL, paddingRight: CORNER_CONTROL }, controlsOpening]}>
				<View style={styles.settingsSlot}>{settings}</View>
			</Animated.View>
			<View style={[styles.corner, styles.cornerLeading]}>
				<ChatAttachmentMenu disabled={busy} canAttachFile onChoosePhoto={onChoosePhoto} onChooseFile={onChooseFile} />
			</View>
			<View style={[styles.corner, styles.cornerTrailing]}>{roundButton}</View>
		</View>
	);
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
	controlsRow: { width: "100%", flexDirection: "row", alignItems: "center", gap: space.xxs, overflow: "hidden" },
	settingsSlot: { flex: 1, minWidth: 0, minHeight: touchTarget, alignItems: "stretch", justifyContent: "center", overflow: "hidden" },
	inactive: { opacity: stateOpacity.disabled },
	roundTarget: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	roundDisc: { width: COMPOSER_DISC, height: COMPOSER_DISC, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center" },
	roundAccent: { backgroundColor: t.accent },
	dictating: { backgroundColor: t.red },
});
