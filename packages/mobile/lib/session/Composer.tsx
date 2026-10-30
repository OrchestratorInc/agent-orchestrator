import { Pressable, StyleSheet, TextInput, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { ComposerGlass, composerGlassSupported } from "../chat/composer-glass";
import { COMPOSER_RADIUS } from "../chat/composerStyles";
import { MicKey } from "../voice/MicKey";
import type { VoiceMode, VoiceState } from "../voice/types";
import { usePressFeedback } from "../pressFeedback";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import type { Theme } from "../theme";
import { haptics } from "../haptics";
import type { SendTarget } from "./sendRoute";
import { RouteToggle } from "./RouteToggle";
import { font, iconSize, space, stateOpacity, touchTarget, type } from "../tokens";

// One field, one send button. The normal route sends a message to the agent; the
// terminal route is an explicit escape hatch for prompts that need a literal
// character, and the session screen auto-engages it when the daemon reports a
// blocked permission prompt (see sendRoute.ts).
//
// Drawn as the chat composer is — one capsule with the glass behind it on iOS 26,
// the elevated fill elsewhere, and touch-target controls — so the two screens of
// one session do not type into two different-looking fields.
export function Composer({
	value,
	onChangeText,
	onSend,
	sending,
	target,
	onTargetChange,
	voice,
	keyboardVisible,
	onDismissKeyboard,
	targetLocked,
}: {
	value: string;
	onChangeText: (v: string) => void;
	onSend: () => void;
	sending: boolean;
	target: SendTarget;
	onTargetChange: (target: SendTarget) => void;
	voice: { state: VoiceState; mode: VoiceMode; pressIn(): void; pressOut(): void; toggleLatched(): void };
	keyboardVisible: boolean;
	onDismissKeyboard: () => void;
	/** Plain worktree shells have no agent route; hide the misleading toggle. */
	targetLocked?: boolean;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const iconFeedback = usePressFeedback("icon");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	const canSend = !!value.trim() && !sending;
	const toTerminal = target === "terminal";

	return (
		<View style={styles.bar}>
			<View style={styles.pill}>
				<ComposerGlass radius={COMPOSER_RADIUS} />
				<View style={styles.row}>
					{!targetLocked ? (
						<RouteToggle
							on={toTerminal}
							onChange={(on) => {
								haptics.select();
								onTargetChange(on ? "terminal" : "agent");
							}}
						/>
					) : null}
					<TextInput
						style={styles.input}
						accessibilityLabel={toTerminal ? "Text for the terminal" : "Message the agent"}
						value={value}
						onChangeText={onChangeText}
						placeholder={toTerminal ? "Send to terminal…" : "Message the agent…"}
						placeholderTextColor={t.textFaint}
						multiline
						keyboardAppearance={scheme}
						{...textSelectionColors(t)}
						// No autoFocus: the bar is always mounted now, so focusing on mount
						// would pop the keyboard over the terminal every time the screen opens.
					/>
					{/* Only offered while there is a keyboard to dismiss, instead of a
					    permanent toggle that claimed to hide a keyboard it did not own. */}
					{keyboardVisible ? (
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
					<MicKey variant="plain" size={touchTarget} glyphSize={iconSize.lg} state={voice.state} mode={voice.mode} onPressIn={voice.pressIn} onPressOut={voice.pressOut} onToggleLatched={voice.toggleLatched} />
					{/* No haptic of its own: the screen confirms the send, or its failure,
					    once it knows which happened. */}
					<Pressable
						accessibilityRole="button"
						accessibilityLabel="Send"
						accessibilityState={{ disabled: !canSend }}
						disabled={!canSend}
						onPress={onSend}
						android_ripple={sendFeedback.android_ripple}
						style={({ pressed }) => [styles.control, styles.send, !canSend && styles.disabled, pressed && sendFeedback.pressed]}
					>
						<AppSymbol name="send" size={iconSize.lg} color={t.onAccent} weight="semibold" />
					</Pressable>
				</View>
			</View>
		</View>
	);
}

// Single-line text sits centred in a touch-target-tall field, and the field
// grows to about four lines before it scrolls, so a long prompt can't swallow
// the terminal above it.
const LINE_HEIGHT = type.subheadline.lineHeight;
const FIELD_MAX_HEIGHT = 4 * LINE_HEIGHT + (touchTarget - LINE_HEIGHT);

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		bar: {
			paddingHorizontal: space.md,
			paddingTop: space.xs,
			paddingBottom: space.xs,
		},
		// The fill goes transparent where the glass layer draws behind it: an opaque
		// pill under a material is the one arrangement that turns glass grey.
		pill: {
			paddingHorizontal: space.xxs,
			paddingVertical: space.xxs,
			borderRadius: COMPOSER_RADIUS,
			borderCurve: "continuous",
			backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated,
		},
		// Bottom-aligned, so the controls stay put while the field grows above them.
		row: { flexDirection: "row", alignItems: "flex-end", gap: space.xxs },
		input: {
			flex: 1,
			minHeight: touchTarget,
			maxHeight: FIELD_MAX_HEIGHT,
			paddingHorizontal: space.xs,
			paddingVertical: (touchTarget - LINE_HEIGHT) / 2,
			fontFamily: font.regular,
			fontSize: type.subheadline.fontSize,
			lineHeight: LINE_HEIGHT,
			color: t.textPrimary,
			textAlignVertical: "top",
		},
		control: {
			width: touchTarget,
			height: touchTarget,
			borderRadius: touchTarget / 2,
			overflow: "hidden",
			alignItems: "center",
			justifyContent: "center",
		},
		send: { backgroundColor: t.accent },
		disabled: { opacity: stateOpacity.disabled },
	});
