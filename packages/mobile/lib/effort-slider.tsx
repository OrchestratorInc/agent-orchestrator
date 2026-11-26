import Zap from "lucide-react-native/icons/zap";
import { useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, useWindowDimensions, View, type DimensionValue, type GestureResponderEvent, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, { Easing, useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import { ComposerOverlay, OverlayHeading, overlayCardStyle, useInOwnWindow } from "./composer-overlay";
import { haptics } from "./haptics";
import { magneticOffset, nearestStop } from "./modelEffort";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { duration, iconSize, space } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";
import { useEffortSlider, type EffortSliderProps } from "./useEffortSlider";

/*
 * The effort slider, the same on both platforms: "Effort › Medium" centred
 * on its own over a card holding a fat track with a dot per level the model
 * takes, lit in the theme's accent up to a white blob that
 * rides inside it. The blob follows the finger but is pulled toward the
 * nearest level, ticks as it lands on each, and glides onto its level when let
 * go. It stays up until a tap lands outside it (see useEffortSliderOpen).
 * Where the model has a fast mode, a bolt at the heading's right flips it,
 * filled while it is on, as desktop marks a fast model.
 */

/** Fast mode as the slider's bolt: on or off, and a flip. */
export type FastToggle = { on: boolean; toggle: () => void };

const TRACK = 40;
const INSET = 4;
const BLOB = TRACK - INSET * 2;
// The blob's centre at either end of the track.
const END = TRACK / 2;
const DOT = 3;
// The track's length: this far between neighbouring levels, so it is only as
// long as the levels need, and never shorter than MIN_TRACK to drag along.
const LEVEL_GAP = 68;
const MIN_TRACK = 240;
// The room kept between the card's track and each side of the screen.
const SCREEN_GUTTER = 28;
// The blob is white in both themes, as a system switch's thumb is.
const BLOB_FILL = "#ffffff";
const BLOB_SHADOW = "#000000";

/** The slider floating over the chat above the composer (see ComposerOverlay). */
export function EffortSliderOverlay({ bottom, overKeyboard, onDismiss, visible = true, ...props }: EffortSliderProps & { bottom?: DimensionValue; overKeyboard?: boolean; onDismiss?: () => void; visible?: boolean; fast?: FastToggle }) {
	return (
		<ComposerOverlay bottom={bottom} overKeyboard={overKeyboard} onDismiss={onDismiss} visible={visible}>
			<EffortSlider {...props} />
		</ComposerOverlay>
	);
}

export function EffortSlider({ choices, onChoose, onRelease, fast }: EffortSliderProps & { fast?: FastToggle }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const [width, setWidth] = useState(0);
	const { index, label, pick } = useEffortSlider(choices, onChoose, haptics.detent);
	const count = choices.length;
	// No wider than the screen leaves it, in from the sides by the card's pad
	// and a gutter: with Ultracode a sixth level ran the track off the screen.
	const { width: screen } = useWindowDimensions();
	const trackWidth = Math.min(screen - SCREEN_GUTTER * 2, Math.max(MIN_TRACK, END * 2 + Math.max(count - 1, 0) * LEVEL_GAP));
	const stepFor = (trackWidth: number) => count > 1 ? (trackWidth - END * 2) / (count - 1) : 0;
	const step = stepFor(width);
	const stopX = (i: number) => END + Math.max(i, 0) * step;

	// The blob's centre along the track.
	const blobX = useSharedValue(0);
	const dragging = useRef(false);
	// The stop the finger last picked. The blob's own position is not read
	// back: mid-glide the JS thread sees a stale value, and a quick tap would
	// land the blob back on the old level.
	const held = useRef(0);
	const glide = (x: number) => {
		blobX.value = reduceMotion ? x : withTiming(x, { duration: duration.base, easing: Easing.out(Easing.cubic) });
	};
	// Settled on the chosen level whenever no finger holds it.
	useEffect(() => { if (!dragging.current) blobX.value = stopX(index); }, [index, width, count]); // eslint-disable-line react-hooks/exhaustive-deps
	const blobStyle = useAnimatedStyle(() => ({ transform: [{ translateX: blobX.value - BLOB / 2 }] }));
	const fillStyle = useAnimatedStyle(() => ({ width: blobX.value + BLOB / 2 + INSET }));

	// `x` is measured from the track's own left edge.
	const follow = (fingerX: number, animate: boolean) => {
		const x = Math.min(width - END, Math.max(END, fingerX));
		const stop = nearestStop(x, END, step, count);
		held.current = stop;
		pick(stop);
		const pulled = stopX(stop) + magneticOffset(x - stopX(stop), step);
		if (animate) glide(pulled);
		else blobX.value = pulled;
	};

	const release = () => {
		if (!dragging.current) return;
		dragging.current = false;
		glide(stopX(held.current));
		onRelease?.();
	};

	// Gesture Handler's pan rather than the responder system: the slider floats
	// over the conversation, outside the composer's bounds, and on Android the
	// list under it took the drag as a scroll before a responder could keep it.
	// With no distance to travel the pan activates on touch down, so the list
	// never starts scrolling, and a tap lands on its level.
	const drag = Gesture.Pan()
		.runOnJS(true)
		.minDistance(0)
		.onBegin((event) => {
			dragging.current = true;
			follow(event.x, true);
		})
		.onUpdate((event) => follow(event.x, false))
		.onFinalize(release);
	// Over the keyboard the slider is in a window of its own, which gesture
	// handler never hears: the same drag through the responder system, claimed
	// as the finger lands and kept to the end.
	const ownWindow = useInOwnWindow();
	const trackLeft = useRef(0);
	const responder = ownWindow ? {
		onStartShouldSetResponder: () => true,
		onMoveShouldSetResponder: () => true,
		onResponderTerminationRequest: () => false,
		onResponderGrant: (event: GestureResponderEvent) => {
			dragging.current = true;
			// Moves are read from the page, against the track's edge as the
			// finger landed: a finger off the track still drives it.
			trackLeft.current = event.nativeEvent.pageX - event.nativeEvent.locationX;
			follow(event.nativeEvent.locationX, true);
		},
		onResponderMove: (event: GestureResponderEvent) => follow(event.nativeEvent.pageX - trackLeft.current, false),
		onResponderRelease: release,
		onResponderTerminate: release,
	} : undefined;

	const track = (
		<View
			{...responder}
			style={[styles.track, { width: trackWidth }]}
			onLayout={(event: LayoutChangeEvent) => {
				const next = event.nativeEvent.layout.width;
				// The blob and fill mount with this width, already on the
				// level: set in an effect after they mount, while the card
				// fades in, the move never reached the fill on Android,
				// and at the top level the track showed no fill at all.
				if (!dragging.current) blobX.value = END + Math.max(index, 0) * stepFor(next);
				setWidth(next);
			}}
			accessible
			accessibilityRole="adjustable"
			accessibilityLabel="Effort"
			accessibilityValue={{ text: label }}
			accessibilityActions={[{ name: "increment" }, { name: "decrement" }]}
			onAccessibilityAction={(event) => pick(Math.max(index, 0) + (event.nativeEvent.actionName === "increment" ? 1 : -1))}
			testID="effort-slider"
		>
			{width && index >= 0 ? <Animated.View pointerEvents="none" style={[styles.fill, fillStyle]} /> : null}
			{width ? choices.map((choice, i) => (
				<View key={choice.value} pointerEvents="none" style={[styles.dot, { left: stopX(i) - DOT, backgroundColor: i <= index ? t.textSecondary : t.textFaint }]} />
			)) : null}
			{width && index >= 0 ? <Animated.View pointerEvents="none" style={[styles.blob, blobStyle]} /> : null}
		</View>
	);
	return (
		<View style={styles.stack}>
			{/* What the track sets, then the level it is on. The track itself is
			    "Effort", adjustable, valued the level, for screen readers. */}
			{/* The heading centred over the card, the bolt at the card's right. */}
			<View style={[styles.header, fast && styles.headerWithBolt, { width: trackWidth + CARD_PAD * 2 }]}>
				<OverlayHeading title="Effort" value={label} valueTestID="effort-slider-value" />
				{fast ? <FastBolt {...fast} /> : null}
			</View>
			<View style={styles.card}>
				{ownWindow ? track : <GestureDetector gesture={drag}>{track}</GestureDetector>}
			</View>
		</View>
	);
}

/** Fast mode's bolt: outlined while off, filled in amber while on. */
function FastBolt({ on, toggle }: FastToggle) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable
			onPress={() => { haptics.select(); toggle(); }}
			hitSlop={BOLT_SLOP}
			accessibilityRole="switch"
			accessibilityLabel="Fast mode"
			accessibilityState={{ checked: on }}
			testID="effort-slider-fast"
			style={({ pressed }) => [styles.bolt, pressed && styles.boltPressed]}
		>
			<Zap size={iconSize.md} strokeWidth={2} color={on ? t.amber : t.textSecondary} fill={on ? t.amber : "none"} />
		</Pressable>
	);
}

// The card's pad around the track.
const CARD_PAD = space.xs;
// The bolt's room, and the reach of a touch past it, to a full touch target.
const BOLT = 32;
const BOLT_SLOP = 6;

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// Each level's preferred room is capped at what the composer leaves:
		// the track's stops are spaced from its measured width, so a narrow
		// screen packs them closer rather than spilling the card off it.
		stack: { gap: space.xs, alignItems: "center", maxWidth: "100%" },
		header: { alignItems: "center", justifyContent: "center" },
		headerWithBolt: { height: BOLT },
		bolt: { position: "absolute", right: 0, top: 0, width: BOLT, height: BOLT, alignItems: "center", justifyContent: "center" },
		boltPressed: { opacity: 0.5 },
		card: {
			maxWidth: "100%",
			padding: CARD_PAD,
			// Concentric with the track's ends, an even pad in from the card's.
			...overlayCardStyle(t, TRACK / 2 + CARD_PAD),
		},
		track: { maxWidth: "100%", height: TRACK, borderRadius: TRACK / 2, borderCurve: "continuous", backgroundColor: t.bgElevatedHover, overflow: "hidden", justifyContent: "center" },
		fill: { position: "absolute", left: 0, top: 0, bottom: 0, borderRadius: TRACK / 2, backgroundColor: t.accentBorder },
		dot: { position: "absolute", width: DOT * 2, height: DOT * 2, borderRadius: DOT },
		blob: {
			position: "absolute",
			left: 0,
			width: BLOB,
			height: BLOB,
			borderRadius: BLOB / 2,
			backgroundColor: BLOB_FILL,
			shadowColor: BLOB_SHADOW,
			shadowOpacity: 0.22,
			shadowRadius: 4,
			shadowOffset: { width: 0, height: 1 },
			elevation: 3,
		},
	});
