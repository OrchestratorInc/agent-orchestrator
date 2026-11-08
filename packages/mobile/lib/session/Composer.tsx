import { ActivityIndicator, Keyboard, Pressable, StyleSheet, TextInput, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { ComposerGlass } from "../chat/composer-glass";
import { COMPOSER_DISC, COMPOSER_FONT_SCALE_CAP, COMPOSER_RADIUS, composerStyleRules, useComposerFieldSize } from "../chat/composerStyles";
import { DictationField } from "../voice/DictationField";
import type { VoiceState } from "../voice/types";
import { usePressFeedback } from "../pressFeedback";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import type { Theme } from "../theme";
import { haptics } from "../haptics";
import type { SendTarget } from "./sendRoute";
import { RouteToggle } from "./RouteToggle";
import { iconSize, radius, space, stateOpacity, touchTarget } from "../tokens";

/** Dictate, filled as on the chat composer's round button. */
const dictateGlyph = { ios: "mic.fill", android: "mic" } as const;

// One field, one round button. The normal route sends a message to the agent; the
// terminal route types into the terminal instead, for prompts the agent cannot
// take (a trust prompt, a password, a picker that wants text). The session screen
// also engages it when the daemon reports a blocked permission prompt (see
// sendRoute.ts). Its toggle sits beside the round button, where the chat
// composer keeps its approval control.
//
// Drawn as the chat composer is when collapsed — one capsule with the glass
// behind it on iOS 26, the elevated fill elsewhere — so the two screens of one
// session do not type into two different-looking fields. The round button
// follows the prompt as the chat's does: it dictates while the field is empty,
// sends once there is something to send, and stops the mic while it is open.
export function Composer({
	value,
	onChangeText,
	onSend,
	sending,
	target,
	onTargetChange,
	targetLocked,
	voice,
	keyboardVisible,
	onDismissKeyboard,
}: {
	value: string;
	onChangeText: (v: string) => void;
	onSend: () => void;
	sending: boolean;
	target: SendTarget;
	onTargetChange: (target: SendTarget) => void;
	/** A plain shell has no agent to message, so there is no route to choose. */
	targetLocked?: boolean;
	voice: { state: VoiceState; partial: string; toggleLatched(): void; subscribeLevel(listener: (level: number) => void): () => void };
	keyboardVisible: boolean;
	onDismissKeyboard: () => void;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const fieldSize = useComposerFieldSize();
	const iconFeedback = usePressFeedback("icon");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	const hasDraft = !!value.trim();
	const canSend = hasDraft && !sending;
	const toTerminal = target === "terminal";
	const listening = voice.state === "starting" || voice.state === "recording";
	const transcribing = voice.state === "transcribing";
	const dictating = listening || transcribing;
	const canDictate = voice.state !== "unavailable" && voice.state !== "denied";
	const primary = listening ? "stopDictation" : transcribing ? "transcribing" : !hasDraft && canDictate ? "dictate" : "send";

	// No haptic of its own on send: the screen confirms the send, or its failure,
	// once it knows which happened.
	const roundButton = primary === "send"
		? <Pressable
			accessibilityRole="button"
			accessibilityLabel="Send"
			accessibilityState={{ disabled: !canSend }}
			disabled={!canSend}
			onPress={onSend}
			android_ripple={sendFeedback.android_ripple}
			style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed, !canSend && styles.disabled]}
		>
			<View style={[styles.roundDisc, styles.roundAccent]}>
				{sending ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.sm} weight="bold" color={t.onAccent} />}
			</View>
		</Pressable>
		: <Pressable
			accessibilityRole="button"
			accessibilityLabel={primary === "stopDictation" ? "Stop dictating" : primary === "transcribing" ? "Transcribing" : "Dictate a message"}
			accessibilityState={{ disabled: transcribing, busy: transcribing }}
			disabled={transcribing}
			android_ripple={sendFeedback.android_ripple}
			onPress={() => { if (primary === "dictate") Keyboard.dismiss(); voice.toggleLatched(); }}
			style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed]}
		>
			<View style={[styles.roundDisc, primary === "stopDictation" ? styles.roundLive : styles.roundAccent]}>
				{transcribing ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name={primary === "stopDictation" ? "stop" : dictateGlyph} size={iconSize.sm} weight="bold" color={primary === "stopDictation" ? t.onDestructive : t.onAccent} />}
			</View>
		</Pressable>;

	return (
		<View style={styles.bar}>
			<View style={styles.composer}>
				<ComposerGlass radius={COMPOSER_RADIUS} />
				<View style={[styles.composerRow, fieldSize.row]}>
					{/* While the mic is open the dictation takes the field's place, as in
					    the chat's composer, rather than a strip over the terminal. */}
					{dictating ? <DictationField state={voice.state} partial={voice.partial} subscribeLevel={voice.subscribeLevel} style={styles.dictation} waveformOnly /> : null}
					<TextInput
						style={[styles.input, fieldSize.field, styles.inputLeading, dictating && styles.hiddenField]}
						accessibilityLabel={toTerminal ? "Text for the terminal" : "Message the agent"}
						value={value}
						onChangeText={onChangeText}
						placeholder={toTerminal ? "Send to terminal…" : "Message the agent…"}
						placeholderTextColor={t.textFaint}
						maxFontSizeMultiplier={COMPOSER_FONT_SCALE_CAP}
						multiline
						keyboardAppearance={scheme}
						{...textSelectionColors(t)}
						// No autoFocus: the bar is always mounted now, so focusing on mount
						// would pop the keyboard over the terminal every time the screen opens.
					/>
					{/* Only offered while there is a keyboard to dismiss, instead of a
					    permanent toggle that claimed to hide a keyboard it did not own. */}
					{keyboardVisible && !dictating ? (
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Hide keyboard"
							onPress={onDismissKeyboard}
							android_ripple={iconFeedback.android_ripple}
							style={({ pressed }) => [styles.control, pressed && iconFeedback.pressed]}
						>
							<AppSymbol name="keyboardDismiss" size={iconSize.lg} color={t.textSecondary} />
						</Pressable>
					) : null}
					{!targetLocked && !dictating ? (
						<RouteToggle
							on={toTerminal}
							onChange={(on) => {
								haptics.select();
								onTargetChange(on ? "terminal" : "agent");
							}}
						/>
					) : null}
					{roundButton}
				</View>
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		...composerStyleRules(t),
		// The chat dock's gutter, so the pill is the same width on both screens.
		bar: { paddingHorizontal: space.sm, paddingVertical: space.xs },
		// The field starts just clear of the capsule's curve.
		inputLeading: { paddingLeft: space.sm },
		control: {
			width: touchTarget,
			height: touchTarget,
			borderRadius: touchTarget / 2,
			overflow: "hidden",
			alignItems: "center",
			justifyContent: "center",
		},
		// The chat composer's round button: a touch target holding a filled disc,
		// the accent to send or dictate, red while the mic is open.
		roundTarget: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
		roundDisc: { width: COMPOSER_DISC, height: COMPOSER_DISC, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center" },
		roundAccent: { backgroundColor: t.accent },
		roundLive: { backgroundColor: t.red },
		dictation: { flex: 1, minHeight: touchTarget, paddingHorizontal: space.xs },
		// Out of the layout while dictating, but still mounted with its draft.
		hiddenField: { display: "none" },
		disabled: { opacity: stateOpacity.disabled },
	});
