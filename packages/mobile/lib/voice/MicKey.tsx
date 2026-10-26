import { useEffect } from "react";
import { Pressable, StyleSheet, View, type AccessibilityActionEvent } from "react-native";
import Animated, { cancelAnimation, Easing, interpolate, useAnimatedStyle, useSharedValue, withRepeat, withTiming } from "react-native-reanimated";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { iconSize, radius, stateOpacity } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";
import type { VoiceMode, VoiceState } from "./types";
import { MicGlass } from "./mic-glass";

// The dictation control, with two gestures:
//
//   hold        - push-to-talk. Cheapest audio session, fastest to capture,
//                 built-in mic. For short phrases where warm-up dominates.
//   double-tap  - latch on, hands-free, Bluetooth-capable. Tap once more to
//                 finish. For long prompts where warm-up stops mattering.
//
// A screen reader has no hold to time, so its activation latches and finishes
// hands-free dictation directly.
//
// Sized to the send button and sitting beside it: dictation is a primary way to
// talk to an agent from a phone, and as one more outlined grey pill in the key
// row it was indistinguishable from `zoom-out`.
//
// It is never the only control that commits. In the chat composer it is a bare
// glyph beside a filled send button (`variant="plain"`), so the pair still has a
// clear hierarchy; where it sits in a row of outlined keys it keeps the glass
// disc (`variant="glass"`). Either way the only state that changes its shape is
// recording, which goes solid red — the one moment it should outrank the screen.
//
/** Matches the send button so the two controls are the same size. */
export const MIC_SIZE = 40;
const MIC_RADIUS = radius.md;
/** One breath of the recording ring, out or back. */
const PULSE_MS = 900;

export function MicKey({
	state,
	mode,
	onPressIn,
	onPressOut,
	onToggleLatched,
	circular = false,
	size = MIC_SIZE,
	variant = "glass",
	glyphSize = iconSize.md,
	disabled: disabledByOwner = false,
}: {
	state: VoiceState;
	mode: VoiceMode;
	onPressIn(): void;
	onPressOut(): void;
	/**
	 * Starts or finishes hands-free dictation in one step (`useVoiceInput`'s
	 * `toggleLatched`), for a screen reader's activation. Without it, the key
	 * makes the two taps a sighted user would.
	 */
	onToggleLatched?(): void;
	circular?: boolean;
	size?: number;
	/**
	 * `glass` is the control in a row of outlined keys, where the material is
	 * what separates it from its neighbours. `plain` is the one in the chat's
	 * composer pill: there, the filled send button beside it is the only solid
	 * shape, and a second disc turned the pair into two equally loud buttons.
	 */
	variant?: "glass" | "plain";
	glyphSize?: number;
	/** The field it dictates into cannot take text right now (stopped agent, send in flight). */
	disabled?: boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const feedback = usePressFeedback(variant === "plain" ? "icon" : "button");
	const live = state === "recording" || state === "starting";
	const latched = live && mode === "latched";
	const denied = state === "denied";
	// Rendered even with no recogniser on the device. Returning null here used to
	// remove the control from the row entirely, reflowing everything beside it.
	const unavailable = state === "unavailable";
	// Never while it is listening: a hands-free recording has to stay stoppable.
	const disabled = unavailable || denied || (disabledByOwner && !live);
	const controlShape = { width: size, height: size, borderRadius: circular ? size / 2 : MIC_RADIUS };

	// A ring breathing out from the key while the mic is open. Under Reduce
	// Motion it stays away: the solid red fill already says the mic is live.
	const pulse = useSharedValue(0);
	const breathing = live && !reduceMotion;
	useEffect(() => {
		if (!breathing) {
			cancelAnimation(pulse);
			pulse.value = 0;
			return;
		}
		pulse.value = withRepeat(withTiming(1, { duration: PULSE_MS, easing: Easing.inOut(Easing.ease) }), -1, true);
		return () => cancelAnimation(pulse);
	}, [breathing, pulse]);
	const ringStyle = useAnimatedStyle(() => ({
		opacity: interpolate(pulse.value, [0, 1], [0.45, 0]),
		transform: [{ scale: interpolate(pulse.value, [0, 1], [1, 1.45]) }],
	}));

	// Idle is the only glass state: recording, denied and unavailable each keep a
	// fill that says something the material would soften.
	const glass = variant === "glass" && !live && !denied && !unavailable;
	const fill = live ? t.red : denied ? t.tintRed : unavailable ? t.bgElevated : "transparent";
	const ink = live ? t.onDestructive : denied ? t.red : unavailable ? t.textFaint : t.textPrimary;
	const shapeRadius = circular ? size / 2 : MIC_RADIUS;

	const toggleLatched = () => {
		if (onToggleLatched) {
			onToggleLatched();
			return;
		}
		onPressIn();
		onPressOut();
		if (latched) return;
		onPressIn();
		onPressOut();
	};
	const onAccessibilityAction = (event: AccessibilityActionEvent) => {
		if (event.nativeEvent.actionName === "activate") toggleLatched();
	};

	return (
		<View style={[styles.slot, { width: size, height: size }]}>
			{breathing ? <Animated.View pointerEvents="none" style={[styles.ring, controlShape, ringStyle]} /> : null}
			{glass ? <MicGlass size={size} radius={shapeRadius} /> : null}
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={
					unavailable
						? "Dictation unavailable on this device"
						: latched
							? "Stop dictating"
							: "Hold to dictate, or double-tap for hands-free"
				}
				accessibilityState={{ busy: live, disabled }}
				// TalkBack's double-tap arrives as `activate`; VoiceOver's as an
				// accessibility tap. Either one latches instead of tapping the key.
				accessibilityActions={disabled ? undefined : [{ name: "activate", label: latched ? "Stop dictating" : "Dictate hands-free" }]}
				onAccessibilityAction={disabled ? undefined : onAccessibilityAction}
				onAccessibilityTap={disabled ? undefined : toggleLatched}
				disabled={disabled}
				android_ripple={feedback.android_ripple}
				style={({ pressed }) => [
					styles.mic,
					controlShape,
					{ backgroundColor: fill },
					variant === "glass" && circular && !glass && styles.circular,
					latched && styles.latched,
					unavailable && styles.unavailable,
					// A held key is recording; it keeps its full red rather than dimming.
					pressed && !disabled && !live && feedback.pressed,
				]}
				onPressIn={() => {
					haptics.tap();
					onPressIn();
				}}
				onPressOut={onPressOut}
				// A finger sliding off still ends a held phrase — otherwise the mic
				// would stay open with no obvious way to close it. Latched mode is
				// unaffected: it ignores the finger by design.
				onTouchCancel={onPressOut}
			>
				<AppSymbol name={denied || unavailable ? "micOff" : "mic"} size={glyphSize} color={ink} />
			</Pressable>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		slot: { alignItems: "center", justifyContent: "center" },
		// Clipped so the Android ripple keeps to the key's shape.
		mic: { alignItems: "center", justifyContent: "center", overflow: "hidden" },
		circular: { borderWidth: rowDividerWidth, borderColor: t.borderDefault },
		ring: { position: "absolute", backgroundColor: t.red },
		// Latched holds the mic open with no finger on it, so it gets an outline the
		// held state doesn't — this is the state that must never be missed.
		latched: { borderWidth: 2, borderColor: t.textPrimary },
		unavailable: { borderWidth: rowDividerWidth, borderColor: t.borderSubtle, opacity: stateOpacity.disabled },
	});
