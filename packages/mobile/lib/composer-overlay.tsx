import { useNavigation } from "expo-router";
import { useEffect, useLayoutEffect, type ReactNode } from "react";
import { Platform, StyleSheet, Text, View, type DimensionValue, type ViewStyle } from "react-native";
import Animated, { Easing, useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import { AppSymbol } from "./AppSymbol";
import { EffortSliderVeil } from "./effort-slider-veil";
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
const CARD_SHADOW = "#000000";

/**
 * The overlay floating over the screen, a small gap above `bottom` in its
 * container: nothing under it moves when it opens. Android hands it a touch
 * outside its container only while no ancestor clips.
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
export function ComposerOverlay({ bottom, visible = true, veilRise, children }: {
	bottom: DimensionValue;
	visible?: boolean;
	/** How far above the content the veil reaches; a taller overlay needs more for its arch to stay solid behind it. */
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
		transform: [{ translateY: -space.sm + (1 - shown.value) * REVEAL_LIFT }],
	}));
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
			<View style={styles.stack}>{children}</View>
		</Animated.View>
	);
}

/**
 * "Effort › Medium": what the overlay sets, then where it stands, centred
 * above the card rather than inside it. Hidden from screen readers: the card's
 * own control names both.
 */
export function OverlayHeading({ title, value, valueTestID }: { title: string; value: string; valueTestID?: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.heading} accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
			<Text style={styles.title} numberOfLines={1}>{title}</Text>
			<AppSymbol name="chevronForward" size={iconSize.xs} color={t.textTertiary} weight="semibold" />
			<Text style={styles.value} numberOfLines={1} testID={valueTestID}>{value}</Text>
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
		stack: { gap: space.xs, alignItems: "center" },
		heading: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xxs },
		title: { ...type.footnote, color: t.textSecondary },
		value: { ...type.footnote, fontFamily: type.headline.fontFamily, color: t.textPrimary, flexShrink: 1 },
	});
