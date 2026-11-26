import { useNavigation } from "expo-router";
import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";
import { DeviceEventEmitter, Platform, Pressable, StyleSheet, Text, View, type DimensionValue, type ViewStyle } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { OverKeyboardView, useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import Animated, { Easing, useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppSymbol } from "./AppSymbol";
import { EffortSliderVeil } from "./effort-slider-veil";
import { GradientText } from "./gradient-text";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { duration, iconSize, space, type } from "./tokens";
import { markTouchInside } from "./touchOutside";
import { useReducedMotion } from "./useReducedMotion";

/*
 * What a composer floats over the chat when one of its controls opens: the
 * effort slider, the permission picker. The same on both platforms: a
 * "Title › value" heading centred on its own over a card, on a veil of the
 * page's colour that keeps both readable over a busy conversation.
 */

// How far below its place the overlay starts as it is revealed.
const REVEAL_LIFT = 6;
// The gap a floating overlay keeps above its `bottom` once revealed.
const FLOAT_GAP = space.sm;
// Over the keyboard: how far the content reaches down over the keys' top edge,
// across the suggestion strip, as ChatGPT's slider does.
const KEYS_OVERLAP = 32;
// How far the veil fades down past the content, over the keys below it.
const KEYS_FALL = 56;
// With no keyboard up: the gap between the content and the screen's foot.
const FOOT_GAP = space.sm;
// The patched keyboard-controller window (patches/react-native-keyboard-controller+1.21.9.patch):
// the view it ends at, the event it sends for a touch outside it, and the
// Android version from which it can leave the header's touches to the app.
const TOUCH_REGION_ID = "RNKC_OverKeyboardTouchRegion";
/** The fade over the keys, which the patched Android window reaches down to draw. */
const FADE_ID = "RNKC_OverKeyboardFade";
const TOUCH_OUTSIDE_EVENT = "KeyboardController::overKeyboardTouchOutside";
const ANDROID_TOUCHABLE_REGION = 33;
// The header's room below the status bar: taps there go through to it on iOS.
const HEADER_ROOM = 64;
// Over the keyboard: the veil's reach above the content.
const KEYS_RISE = 160;
const CARD_SHADOW = "#000000";

/**
 * The overlay floating over the screen, a small gap above `bottom` in its
 * container, or, with `cover`, laid over the container itself: at least its
 * height, its content at its foot, the veil hiding the composer behind it.
 * Nothing under it moves when it opens. Android hands it a touch outside its
 * container only while no ancestor clips.
 *
 * Kept mounted while hidden by its host, so opening is a reveal on the UI
 * thread rather than a build: a quick fade with a small lift, in place at
 * once with Reduce Motion. Hidden, it takes no touches and screen readers
 * skip it.
 *
 * While it is up the screen's back swipe is off: on iOS a left-to-right swipe
 * anywhere on the page is the back gesture, and it took a drag toward the
 * higher levels from the effort slider and pulled the page back instead. The
 * header's back button still leaves, and a tap outside closes the overlay.
 * The option is iOS's alone, and setting it re-renders the screen, so
 * Android, which has no such swipe, skips it rather than hold up the first
 * frame.
 */
export function ComposerOverlay({ bottom, overKeyboard = false, onDismiss, lift = 0, visible = true, veilRise, fill, children }: {
	/** Where a floating overlay sits in its container; unused over the keyboard. */
	bottom?: DimensionValue;
	/** Opens over the keyboard, in a window of its own (see OverKeyboardOverlay). */
	overKeyboard?: boolean;
	/** Over the keyboard: a tap anywhere but on the content, which only puts the overlay away. */
	onDismiss?: () => void;
	/** Room left under the content, inside the veil, so the veil still reaches the composer's bottom edge below it. */
	lift?: number;
	/** Spans the composer's width, as the permission picker laid over the pill does, rather than centring a card of its own width. */
	fill?: boolean;
	visible?: boolean;
	/** How tall the veil's fade above the overlay is. */
	veilRise?: number;
	children: ReactNode;
}) {
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	const reduceMotion = useReducedMotion();
	// A layout effect, so its hand-back runs before any the screen makes in
	// the same commit (the chat's request card turns the swipe off as well).
	useLayoutEffect(() => {
		if (!visible || Platform.OS !== "ios") return;
		navigation.setOptions({ gestureEnabled: false });
		return () => navigation.setOptions({ gestureEnabled: true });
	}, [navigation, visible]);
	const shown = useSharedValue(visible ? 1 : 0);
	useEffect(() => {
		const target = visible ? 1 : 0;
		shown.value = reduceMotion ? target : withTiming(target, { duration: duration.fast, easing: Easing.out(Easing.cubic) });
	}, [visible, reduceMotion, shown]);
	const reveal = useAnimatedStyle(() => ({
		opacity: shown.value,
		transform: [{ translateY: (overKeyboard ? 0 : -FLOAT_GAP) + (1 - shown.value) * REVEAL_LIFT }],
	}));
	if (overKeyboard) {
		return (
			<OverKeyboardOverlay visible={visible} onDismiss={onDismiss} fill={fill} reveal={reveal}>
				{children}
			</OverKeyboardOverlay>
		);
	}
	// box-none: the overlay spans the composer's width to centre its card, and
	// a touch beside the card must still count as one outside it.
	return (
		<Animated.View
			pointerEvents={visible ? "box-none" : "none"}
			onTouchStart={visible ? markTouchInside : undefined}
			accessibilityElementsHidden={!visible}
			importantForAccessibility={visible ? "auto" : "no-hide-descendants"}
			style={[styles.overlay, { bottom }, reveal]}
		>
			<EffortSliderVeil rise={veilRise} />
			<View pointerEvents="box-none" style={[styles.stack, fill && styles.fill, lift ? { paddingBottom: lift } : null]}>
				{children}
			</View>
		</Animated.View>
	);
}

/**
 * The overlay over the keyboard, as ChatGPT's effort slider is: in a window of
 * its own above every other, the keyboard's included (keyboard-controller's
 * OverKeyboardView), so the field keeps its focus and the keys stay up under
 * it. The content sits on the keys' top edge, across the suggestion strip,
 * over the composer, on a veil that hides the composer, fades up into the
 * chat and down over the keys' top rows; with no keyboard up it sits over
 * the composer at the screen's foot.
 *
 * Touches: one on the content is the content's. One on the chat between the
 * header and the content only puts the overlay away (`onDismiss`), nothing
 * under it pressed or scrolled. One on the header or the keys puts it away
 * and goes on to them, so the key types and the header's button works. On
 * iOS that is the window's own: a point no view of it claims falls through
 * to the window below. On Android the window is keyboard-controller's,
 * patched to end at the touch region view below it, so the keys under it
 * get their own touches, and to report a touch outside it, which puts the
 * overlay away; on Android 13+ it leaves the header's touches to the app
 * too. Older Android's header taps only put it away. The window carries its own
 * gesture handler root, so gestures (the slider's drag) run there on the UI
 * thread as anywhere else.
 */
function OverKeyboardOverlay({ visible, onDismiss, fill, reveal, children }: {
	visible: boolean;
	onDismiss?: () => void;
	fill?: boolean;
	reveal: ReturnType<typeof useAnimatedStyle>;
	children: ReactNode;
}) {
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const keyboard = useReanimatedKeyboardAnimation();
	// Across the keys' top edge while they are up, over the composer at the
	// foot of the screen while they are not; it follows them as they move. The
	// band is the chat from under the header down to the content's foot: the
	// part of the screen a tap only closes it in.
	const band = useAnimatedStyle(() => {
		const keys = -keyboard.height.value;
		return { bottom: keys > insets.bottom ? keys - KEYS_OVERLAP : insets.bottom + FOOT_GAP };
	}, [insets.bottom]);
	// The header's taps reach the app where the window can leave them out: on
	// iOS, and on Android 13+, whose window is untouchable above the band (see
	// the keyboard-controller patch). Older Android only closes the overlay.
	const passThrough = Platform.OS === "ios" || (Platform.OS === "android" && Platform.Version >= ANDROID_TOUCHABLE_REGION);
	// A key tapped below the window, or the header above it, is reported by the
	// patched window as a touch outside it: the key still types, and the
	// overlay goes, even for a key that types nothing (shift, ?123).
	const dismiss = useRef(onDismiss);
	dismiss.current = onDismiss;
	useEffect(() => {
		if (Platform.OS !== "android" || !visible) return;
		const outside = DeviceEventEmitter.addListener(TOUCH_OUTSIDE_EVENT, () => dismiss.current?.());
		return () => outside.remove();
	}, [visible]);
	return (
		<OverKeyboardView visible={visible}>
			<View style={styles.window} pointerEvents="box-none">
				{passThrough ? null : <Pressable style={StyleSheet.absoluteFill} onPressIn={onDismiss} accessible={false} />}
				<Animated.View pointerEvents="box-none" style={[styles.band, { top: passThrough ? insets.top + HEADER_ROOM : 0 }, band]}>
					{passThrough ? <Pressable style={StyleSheet.absoluteFill} onPressIn={onDismiss} accessible={false} /> : null}
					{/* Android: the part of the screen the overlay's window keeps. The
					    patched window ends at this view's foot, so the keys below it
					    are the keyboard's own, and on Android 13+ it is untouchable
					    above it. Never flattened away, so the window can find it. */}
					{Platform.OS === "android" ? <View nativeID={TOUCH_REGION_ID} collapsable={false} pointerEvents="none" style={styles.touchRegion} /> : null}
					{/* The touch areas that put it away are no screen reader elements,
					    so VoiceOver's escape (the two-finger scrub) does instead;
					    TalkBack's back does through the host's back handler. */}
					<Animated.View
						pointerEvents="box-none"
						accessibilityElementsHidden={!visible}
						importantForAccessibility={visible ? "auto" : "no-hide-descendants"}
						onAccessibilityEscape={visible ? onDismiss : undefined}
						style={[styles.aboveKeys, reveal]}
					>
						{/* The fade over the keys' top rows. On Android the patched window
						    reaches down to its foot; a tap on it closes the overlay, and the
						    keys below it stay the keyboard's to tap. */}
						<EffortSliderVeil rise={KEYS_RISE} fall={KEYS_FALL} fallNativeID={Platform.OS === "android" ? FADE_ID : undefined} />
						{/* Its touches are its own. The window is native, but a touch in it
						    still bubbles through React's tree to the app root, which would
						    otherwise take it for a tap outside and close it as the finger
						    lands. */}
						<View style={[styles.stack, fill && styles.fill]} onTouchStart={markTouchInside}>
							{/* Gesture handler's root for this window: the window is outside the
							    app's native tree, so without one no gesture in it (the slider's
							    drag) is heard, as a React Native Modal needs its own. */}
							<GestureHandlerRootView style={[styles.gestureRoot, fill && styles.fill]}>{children}</GestureHandlerRootView>
						</View>
					</Animated.View>
				</Animated.View>
			</View>
		</OverKeyboardView>
	);
}

/**
 * "Effort › Medium": what the overlay sets, then where it stands, centred
 * above the card rather than inside it. Hidden from screen readers: the card's
 * own control names both.
 */
export function OverlayHeading({ title, value, valueTestID, ultra = false }: {
	title: string;
	value: string;
	valueTestID?: string;
	/** The value in desktop's Ultracode gradient, as desktop marks that level. */
	ultra?: boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.heading} accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
			<Text style={styles.title} numberOfLines={1}>{title}</Text>
			<AppSymbol name="chevronForward" size={iconSize.xs} color={t.textTertiary} weight="semibold" />
			{ultra ? <GradientText style={styles.value} testID={valueTestID}>{value}</GradientText> : <Text style={styles.value} numberOfLines={1} testID={valueTestID}>{value}</Text>}
		</View>
	);
}

/** The card an overlay's control sits in, its corners `radius` round. */
export function overlayCardStyle(t: Theme, radius: number): ViewStyle {
	return {
		borderRadius: radius,
		borderCurve: "continuous",
		backgroundColor: t.bgElevated,
		borderWidth: StyleSheet.hairlineWidth,
		borderColor: t.borderDefault,
		shadowColor: CARD_SHADOW,
		shadowOpacity: 0.25,
		shadowRadius: 12,
		shadowOffset: { width: 0, height: 4 },
		elevation: 8,
	};
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		overlay: {
			position: "absolute",
			left: 0,
			right: 0,
			zIndex: 1,
			// The gap above the composer comes from the reveal's translateY: a
			// margin does not move an absolutely placed view off its `bottom`,
			// and the card sat on the composer's edge.
			alignItems: "center",
		},
		window: { flex: 1 },
		// Over the keyboard: the chat from under the header to the content's foot.
		band: { position: "absolute", left: 0, right: 0 },
		// The band, and the veil's solid foot just below it.
		touchRegion: { position: "absolute", left: 0, right: 0, top: 0, bottom: -space.sm },
		// The content at the band's foot, a composer's inset in from the screen's sides.
		aboveKeys: { position: "absolute", left: space.sm, right: space.sm, bottom: 0, alignItems: "center" },
		// No wider than the composer, so a card inside can be capped to it.
		stack: { gap: space.xs, alignItems: "center", maxWidth: "100%" },
		fill: { alignSelf: "stretch" },
		// Sized by its content like the stack, never stretched (its default is flex: 1).
		gestureRoot: { alignItems: "center", maxWidth: "100%" },
		heading: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xxs },
		title: { ...type.footnote, color: t.textSecondary },
		value: { ...type.footnote, fontFamily: type.headline.fontFamily, color: t.textPrimary, flexShrink: 1 },
	});
