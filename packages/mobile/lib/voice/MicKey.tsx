import { useEffect } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import Animated, { cancelAnimation, Easing, interpolate, useAnimatedStyle, useSharedValue, withRepeat, withTiming } from "react-native-reanimated";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { iconSize, radius, stateOpacity } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";
import type { VoiceState } from "./types";
import { MicGlass } from "./mic-glass";

// The dictation control: tap to start hands-free dictation, tap again to
// finish. It used to be held to talk, with a double-tap to latch, but a press
// shorter than the mic's warm-up captured nothing and read as broken, and the
// double-tap was undiscoverable. A screen reader's activation is the same tap.
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
	onToggleLatched,
	circular = false,
	size = MIC_SIZE,
	variant = "glass",
	glyphSize = iconSize.md,
	disabled: disabledByOwner = false,
}: {
	state: VoiceState;
	/** Starts or finishes hands-free dictation (`useVoiceInput`'s `toggleLatched`). */
	onToggleLatched(): void;
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

	return (
		<View style={[styles.slot, { width: size, height: size }]}>
			{breathing ? <Animated.View pointerEvents="none" style={[styles.ring, controlShape, ringStyle]} /> : null}
			{glass ? <MicGlass size={size} radius={shapeRadius} /> : null}
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={unavailable ? "Dictation unavailable on this device" : live ? "Stop dictating" : "Dictate"}
				accessibilityState={{ busy: live, disabled }}
				disabled={disabled}
				android_ripple={feedback.android_ripple}
				style={({ pressed }) => [
					styles.mic,
					controlShape,
					{ backgroundColor: fill },
					variant === "glass" && circular && !glass && styles.circular,
					unavailable && styles.unavailable,
					// A recording key keeps its full red rather than dimming.
					pressed && !disabled && !live && feedback.pressed,
				]}
				onPress={() => {
					haptics.tap();
					onToggleLatched();
				}}
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
		unavailable: { borderWidth: rowDividerWidth, borderColor: t.borderSubtle, opacity: stateOpacity.disabled },
	});
