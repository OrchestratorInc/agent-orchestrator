import { useNavigation } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { DeviceEventEmitter, Platform, Pressable, StyleSheet, Text, View, type DimensionValue, type ViewStyle } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { OverKeyboardView, useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import Animated, { Easing, runOnJS, useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppSymbol } from "./AppSymbol";
import { EffortSliderVeil } from "./effort-slider-veil";
import { GradientText } from "./gradient-text";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, type } from "./tokens";
import { markTouchInside } from "./touchOutside";
import { useReducedMotion } from "./useReducedMotion";

/*
 * What a composer floats over the chat when one of its controls opens: the
 * effort slider, the permission picker. The same on both platforms: a
 * "Title › value" heading centred on its own over a card, on a veil of the
 * page's colour that keeps both readable over a busy conversation.
 */

// How far below its place the content starts as it is revealed, and how
// small: it settles up into place as it shows, and sinks back as it goes.
const REVEAL_LIFT = 6;
const REVEAL_SCALE = 0.98;
/**
 * The share of the reveal the veil takes on its own before the content shows.
 * One value drives both, in two stages: opening, the veil covers the composer
 * first and the content comes in over it; closing, the content goes first and
 * the veil leaves last. Faded together, the content and the composer under it
 * were each half there mid-fade, one over the other, as a double image.
 */
export const VEIL_LEAD = 0.35;
// Easing.inOut(Easing.cubic), written out as a worklet of plain maths, as
// the UI thread runs it: slow at both ends, so the content's stage and the
// veil's after it each read rather than the first being over at once.
function easeInOutCubic(t: number): number {
	"worklet";
	const back = 2 - 2 * t;
	return t < 0.5 ? 4 * t * t * t : 1 - (back * back * back) / 2;
}
// The reveal in, eased to a stop, so it reads as arriving; the fade out a
// touch quicker, eased at both ends so its two stages both show.
const REVEAL_IN = { duration: 180, easing: Easing.out(Easing.cubic) };
const REVEAL_OUT = { duration: 170, easing: easeInOutCubic };
// Over the keyboard: how long the reveal waits for the overlay's first layout
// before starting anyway.
const FIRST_LAYOUT_WAIT_MS = 120;
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
/**
 * How high above the keys the composer's control row (the model pill, the
 * effort gauge, the permission button) sits. An overlay centred on it opens
 * level with the button it opens from, where the thumb already is.
 */
export const COMPOSER_CONTROL_RISE = 37;
// Over the keyboard: the veil's reach above the content.
const KEYS_RISE = 160;
const CARD_SHADOW = "#000000";

/**
 * The veil's and the content's opacity at `shown` (0 hidden, 1 up) of the
 * reveal: the veil over its first VEIL_LEAD, the content over the rest.
 */
export function overlayStages(shown: number): { veil: number; content: number } {
	"worklet";
	const veil = Math.min(Math.max(shown / VEIL_LEAD, 0), 1);
	const content = Math.min(Math.max((shown - VEIL_LEAD) / (1 - VEIL_LEAD), 0), 1);
	return { veil, content };
}

/**
 * The overlay floating over the screen, a small gap above `bottom` in its
 * container, or, with `cover`, laid over the container itself: at least its
 * height, its content at its foot, the veil hiding the composer behind it.
 * Nothing under it moves when it opens. Android hands it a touch outside its
 * container only while no ancestor clips.
 *
 * Kept mounted while hidden by its host, so opening is a reveal on the UI
 * thread rather than a build: the veil fades in over the composer, then the
 * content over it with a small lift, in place at once with Reduce Motion. Hidden, it takes no touches and screen readers
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
export function ComposerOverlay({ bottom, overKeyboard = false, keysOverlap = KEYS_OVERLAP, onDismiss, lift = 0, visible = true, veilRise, fill, children }: {
	/** Where a floating overlay sits in its container; unused over the keyboard. */
	bottom?: DimensionValue;
	/** Opens over the keyboard, in a window of its own (see OverKeyboardOverlay). */
	overKeyboard?: boolean;
	/** Over the keyboard: how far the content reaches down over the keys' top edge; less, or below zero, sits it higher. */
	keysOverlap?: number;
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
	// Over the keyboard the overlay is a window of its own, attached as it
	// opens and taken down as it closes; its content stays built while hidden
	// (keyboard-controller, patched). The window stays up until the fade out
	// has played, so the overlay fades rather than vanishing; and the fade in
	// starts once the window has the overlay laid out, not on the frame that
	// attaches it, which is the slow one: started there, the reveal had played
	// half its course before its first frame showed, and read as a pop. Built
	// already, it is laid out, and the fade starts on the next frame.
	const [windowUp, setWindowUp] = useState(visible);
	const windowUpRef = useRef(windowUp);
	windowUpRef.current = windowUp;
	const awaitingLayout = useRef(false);
	const reveal = useCallback(() => {
		if (!awaitingLayout.current) return;
		awaitingLayout.current = false;
		shown.value = withTiming(1, REVEAL_IN);
	}, [shown]);
	const laidOut = useRef(false);
	const onContentLayout = useCallback(() => {
		laidOut.current = true;
		reveal();
	}, [reveal]);
	useEffect(() => {
		const target = visible ? 1 : 0;
		if (!overKeyboard || reduceMotion) {
			shown.value = reduceMotion ? target : withTiming(target, visible ? REVEAL_IN : REVEAL_OUT);
			if (overKeyboard) setWindowUp(visible);
			return;
		}
		if (!visible) {
			awaitingLayout.current = false;
			shown.value = withTiming(0, REVEAL_OUT, (finished) => { if (finished) runOnJS(setWindowUp)(false); });
			return;
		}
		// Reopened while still fading out: its window is up and laid out.
		if (windowUpRef.current) {
			shown.value = withTiming(1, REVEAL_IN);
			return;
		}
		awaitingLayout.current = true;
		setWindowUp(true);
		if (laidOut.current) {
			const frame = requestAnimationFrame(reveal);
			return () => cancelAnimationFrame(frame);
		}
		const fallback = setTimeout(reveal, FIRST_LAYOUT_WAIT_MS);
		return () => clearTimeout(fallback);
	}, [visible, reduceMotion, shown, overKeyboard, reveal]);
	const veilStyle = useAnimatedStyle(() => ({ opacity: overlayStages(shown.value).veil }));
	const contentStyle = useAnimatedStyle(() => {
		const { content } = overlayStages(shown.value);
		return {
			opacity: content,
			transform: [{ translateY: (1 - content) * REVEAL_LIFT }, { scale: REVEAL_SCALE + (1 - REVEAL_SCALE) * content }],
		};
	});
	if (overKeyboard) {
		return (
			<OverKeyboardOverlay visible={visible} windowUp={windowUp} onFirstLayout={onContentLayout} onDismiss={onDismiss} fill={fill} veilStyle={veilStyle} contentStyle={contentStyle} keysOverlap={keysOverlap}>
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
			style={[styles.overlay, { bottom }]}
		>
			<Animated.View pointerEvents="none" style={[styles.veil, veilStyle]}>
				<EffortSliderVeil rise={veilRise} />
			</Animated.View>
			<Animated.View pointerEvents="box-none" style={[styles.stack, fill && styles.fill, lift ? { paddingBottom: lift } : null, contentStyle]}>
				{children}
			</Animated.View>
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
function OverKeyboardOverlay({ visible, windowUp, onFirstLayout, onDismiss, fill, veilStyle, contentStyle, keysOverlap, children }: {
	visible: boolean;
	/** The window is up: while visible, and through the fade out after. */
	windowUp: boolean;
	/** The overlay is laid out in its window, so its reveal can start. */
	onFirstLayout: () => void;
	keysOverlap: number;
	onDismiss?: () => void;
	fill?: boolean;
	/** The veil's stage of the reveal, and the content's after it (see VEIL_LEAD). */
	veilStyle: ReturnType<typeof useAnimatedStyle>;
	contentStyle: ReturnType<typeof useAnimatedStyle>;
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
		return { bottom: keys > insets.bottom ? keys - keysOverlap : insets.bottom + FOOT_GAP };
	}, [insets.bottom, keysOverlap]);
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
		<OverKeyboardView visible={windowUp}>
			<View style={styles.window} pointerEvents={visible ? "box-none" : "none"}>
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
						style={styles.aboveKeys}
					>
						{/* The fade over the keys' top rows. On Android the patched window
						    reaches down to its foot; a tap on it closes the overlay, and the
						    keys below it stay the keyboard's to tap. */}
						<Animated.View pointerEvents="none" style={[styles.veil, veilStyle]}>
							<EffortSliderVeil rise={KEYS_RISE} fall={KEYS_FALL} fallNativeID={Platform.OS === "android" ? FADE_ID : undefined} />
						</Animated.View>
						<Animated.View pointerEvents="box-none" style={[styles.content, fill && styles.fill, contentStyle]}>
							{/* Its touches are its own. The window is native, but a touch in it
							    still bubbles through React's tree to the app root, which would
							    otherwise take it for a tap outside and close it as the finger
							    lands. */}
							<View style={[styles.stack, fill && styles.fill]} onTouchStart={markTouchInside} onLayout={onFirstLayout}>
								{/* Gesture handler's root for this window: the window is outside the
								    app's native tree, so without one no gesture in it (the slider's
								    drag) is heard, as a React Native Modal needs its own. */}
								<GestureHandlerRootView style={[styles.gestureRoot, fill && styles.fill]}>{children}</GestureHandlerRootView>
							</View>
						</Animated.View>
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
			// The gap above the composer comes from a translateY: a margin does
			// not move an absolutely placed view off its `bottom`, and the card
			// sat on the composer's edge.
			transform: [{ translateY: -FLOAT_GAP }],
			alignItems: "center",
		},
		// The veil's own layer, over the overlay's whole box, so it fades on a
		// stage of its own; its parts reach out past the box from there.
		veil: { position: "absolute", top: 0, bottom: 0, left: 0, right: 0 },
		// Over the keyboard: the layer the content fades and settles in on, as wide as the stack in it.
		content: { maxWidth: "100%" },
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
