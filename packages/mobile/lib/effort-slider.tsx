import Zap from "lucide-react-native/icons/zap";
import { memo, useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import { StyleSheet, useWindowDimensions, View, type DimensionValue, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector, Pressable } from "react-native-gesture-handler";
import Animated, { Easing, useAnimatedStyle, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";
import { ComposerOverlay, OverlayHeading, overlayCardStyle } from "./composer-overlay";
import { ULTRACODE } from "./chat/ultracode";
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
// While the blob is still gliding from touch down, each move retargets a glide
// this long at the finger: about half the gap closes in a frame, and the glide
// still lands on a finger that stops.
const CATCH_MS = 60;

/** The slider floating over the chat above the composer (see ComposerOverlay). */
export function EffortSliderOverlay({ bottom, overKeyboard, onDismiss, visible = true, ...props }: EffortSliderProps & { bottom?: DimensionValue; overKeyboard?: boolean; onDismiss?: () => void; visible?: boolean; fast?: FastToggle }) {
	return (
		<ComposerOverlay bottom={bottom} overKeyboard={overKeyboard} onDismiss={onDismiss} visible={visible}>
			<EffortSlider {...props} />
		</ComposerOverlay>
	);
}

export function EffortSlider({ choices, onChoose, onRelease, fast }: EffortSliderProps & { fast?: FastToggle }) {
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const [width, setWidth] = useState(0);
	const { index, label, show, choose, pick } = useEffortSlider(choices, onChoose, haptics.detent);
	const count = choices.length;
	// No wider than the screen leaves it, in from the sides by the card's pad
	// and a gutter: with Ultracode a sixth level ran the track off the screen.
	const { width: screen } = useWindowDimensions();
	const trackWidth = Math.min(screen - SCREEN_GUTTER * 2, Math.max(MIN_TRACK, END * 2 + Math.max(count - 1, 0) * LEVEL_GAP));
	const stepFor = (length: number) => (count > 1 ? (length - END * 2) / (count - 1) : 0);
	// Laid out, the track's own width spaces the stops; until then the width it
	// asked for, so the blob and fill are placed from the first frame.
	const laidOut = width || trackWidth;
	const stopX = (i: number) => END + Math.max(i, 0) * stepFor(laidOut);
	// The levels' keys, the same array while they are the same levels, so the
	// track's memo holds through a drag.
	const keyList = choices.map((choice) => choice.value).join("\u0000");
	const keys = useMemo(() => (keyList ? keyList.split("\u0000") : []), [keyList]);

	// The drag runs on the UI thread, so a busy JS thread (a streaming reply)
	// cannot hold the blob back; the blob, the fill and the dots all read
	// `blobX`, so they move in the same frame. JS hears only a new level (the
	// label and a tick) and the release (the choice).
	const blobX = useSharedValue(stopX(index));
	const trackW = useSharedValue(laidOut);
	const step = useSharedValue(stepFor(laidOut));
	const held = useSharedValue(Math.max(index, 0));
	const dragging = useSharedValue(0);
	// The touch-down glide is still running: a move retargets it rather than
	// cutting it short.
	const catching = useSharedValue(false);
	// The level the slider itself last showed, and whether a drag is under way,
	// for JS: a level from outside moves the blob, the slider's own does not.
	const shown = useRef(Math.max(index, 0));
	const midDrag = useRef(false);

	useEffect(() => {
		trackW.value = laidOut;
		step.value = stepFor(laidOut);
		// Fewer levels than before (a model change, even mid-drag): the stop
		// held and the one shown are brought onto the last there is, so the
		// release lands on a real level and chooses one.
		const last = Math.max(count - 1, 0);
		if (held.value > last) held.value = last;
		if (shown.current > last) shown.current = last;
		if (!dragging.value) blobX.value = END + shown.current * step.value;
	}, [laidOut, count]); // eslint-disable-line react-hooks/exhaustive-deps
	// A level from outside (a model change, a save that failed) moves the blob;
	// the slider's own, already there, does not snap it out of its glide.
	useEffect(() => {
		if (index < 0 || index === shown.current || dragging.value) return;
		shown.current = index;
		held.value = index;
		blobX.value = END + index * step.value;
	}, [index]); // eslint-disable-line react-hooks/exhaustive-deps

	const onLevel = (stop: number) => {
		midDrag.current = true;
		shown.current = stop;
		show(stop);
	};
	// Settles a drag once: on release, or on unmount mid-drag, whichever
	// comes first. A release the UI thread sends after the slider is gone
	// finds it settled and does nothing.
	const gone = useRef(false);
	const onEnd = (stop: number) => {
		if (gone.current) return;
		midDrag.current = false;
		shown.current = stop;
		choose(stop);
		onRelease?.();
	};
	// Closed mid-drag (the keyboard, back, a request card taking the host): the
	// level the finger was on is still chosen and released, so the owner saves
	// it whether or not it also hears the close.
	const settleOnUnmount = useRef(() => {});
	settleOnUnmount.current = () => { if (midDrag.current) onEnd(shown.current); };
	useEffect(() => {
		gone.current = false;
		return () => { settleOnUnmount.current(); gone.current = true; };
	}, []);

	// The worklets call JS through these, so they always reach the latest
	// render's callbacks without rebuilding the gesture.
	const onLevelRef = useStableCallback(onLevel);
	const onEndRef = useStableCallback(onEnd);


	const drag = useMemo(() => {
		const glideMs = duration.base;
		const target = (fingerX: number) => {
			"worklet";
			const x = Math.min(trackW.value - END, Math.max(END, fingerX));
			const stop = nearestStop(x, END, step.value, count);
			const at = END + stop * step.value;
			return { stop, pulled: at + magneticOffset(x - at, step.value) };
		};
		const level = (stop: number) => {
			"worklet";
			if (stop === held.value) return;
			held.value = stop;
			scheduleOnRN(onLevelRef, stop);
		};
		// Gesture Handler's pan: with no distance to travel it holds the touch
		// from touch down, so nothing under the slider starts a scroll, and a
		// tap lands on its level.
		return Gesture.Pan()
			.minDistance(0)
			.onBegin((event) => {
				"worklet";
				dragging.value = 1;
				const { stop, pulled } = target(event.x);
				catching.value = !reduceMotion;
				blobX.value = reduceMotion ? pulled : withTiming(pulled, { duration: glideMs, easing: Easing.out(Easing.cubic) });
				level(stop);
			})
			.onUpdate((event) => {
				"worklet";
				const { stop, pulled } = target(event.x);
				// Still gliding from touch down: retarget a short glide at the
				// finger, then hold the finger once caught. Setting a partial
				// position instead cancelled the glide, and the blob stopped short
				// of a finger that stopped moving.
				if (catching.value && Math.abs(pulled - blobX.value) < 1) catching.value = false;
				blobX.value = catching.value ? withTiming(pulled, { duration: CATCH_MS, easing: Easing.out(Easing.quad) }) : pulled;
				level(stop);
			})
			.onFinalize(() => {
				"worklet";
				// A pan cancelled, then finalized, settles once.
				if (!dragging.value) return;
				dragging.value = 0;
				catching.value = false;
				const at = END + held.value * step.value;
				blobX.value = reduceMotion ? at : withTiming(at, { duration: glideMs, easing: Easing.out(Easing.cubic) });
				scheduleOnRN(onEndRef, held.value);
			});
	}, [count, reduceMotion]); // eslint-disable-line react-hooks/exhaustive-deps

	return (
		<View style={styles.stack}>
			{/* What the track sets, then the level it is on. The track itself is
			    "Effort", adjustable, valued the level, for screen readers. */}
			{/* The heading centred over the card, the bolt at the card's right. */}
			<View style={[styles.header, fast && styles.headerWithBolt, { width: trackWidth + CARD_PAD * 2 }]}>
				{/* Claude Code's Ultracode in desktop's gradient, as its effort menu draws it. */}
				<OverlayHeading title="Effort" value={label} valueTestID="effort-slider-value" ultra={choices[index]?.value === ULTRACODE} />
				{fast ? <FastBolt {...fast} /> : null}
			</View>
			{/* The card is "Effort" for screen readers, adjustable, valued the level:
			    the value changes with every level, so it lives here, not on the
			    track, which must not re-render while a finger drags it. */}
			<View
				style={styles.card}
				accessible
				accessibilityRole="adjustable"
				accessibilityLabel="Effort"
				accessibilityValue={{ text: label }}
				accessibilityActions={[{ name: "increment" }, { name: "decrement" }]}
				onAccessibilityAction={(event) => {
					const next = Math.min(count - 1, Math.max(0, Math.max(index, 0) + (event.nativeEvent.actionName === "increment" ? 1 : -1)));
					shown.current = next;
					held.value = next;
					blobX.value = END + next * step.value;
					pick(next);
				}}
				testID="effort-slider"
			>
				<SliderTrack trackWidth={trackWidth} laidOut={laidOut} keys={keys} lit={index >= 0} blobX={blobX} gesture={drag} onWidth={setWidth} />
			</View>
		</View>
	);
}

/**
 * The track, its fill, dots and blob. The blob and the fill move by layout
 * (`left`, `width`), not by transform: Android hands transforms and colours
 * to the screen directly, outside React's tree, and every React commit in the
 * screen (a streaming reply re-renders it ten times a second) briefly put an
 * older position back, so the blob jumped back a step mid-drag. Layout goes
 * through the same tree as React's commits, and the blob and fill land in the
 * same frame. The lit dots ride inside the fill, clipped by it, over the
 * faint ones on the track: a dot is lit exactly where the fill covers it.
 *
 * Memoised on props that hold still through a drag, so a level crossed
 * re-renders the heading, not the track.
 */
const SliderTrack = memo(function SliderTrack({ trackWidth, laidOut, keys, lit, blobX, gesture, onWidth }: {
	trackWidth: number;
	laidOut: number;
	keys: readonly string[];
	/** A level is chosen, so the fill and the blob show. */
	lit: boolean;
	blobX: SharedValue<number>;
	gesture: ComponentProps<typeof GestureDetector>["gesture"];
	onWidth: (width: number) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const blobStyle = useAnimatedStyle(() => ({ left: blobX.value - BLOB / 2 }));
	const fillStyle = useAnimatedStyle(() => ({ width: blobX.value + BLOB / 2 + INSET }));
	const stepPx = keys.length > 1 ? (laidOut - END * 2) / (keys.length - 1) : 0;
	const dots = (color: string) => keys.map((key, i) => <View key={key} pointerEvents="none" style={[styles.dot, { left: END + i * stepPx - DOT, backgroundColor: color }]} />);
	return (
		<GestureDetector gesture={gesture}>
			<View style={[styles.track, { width: trackWidth }]} onLayout={(event: LayoutChangeEvent) => onWidth(event.nativeEvent.layout.width)} testID="effort-slider-track">
				{dots(t.textFaint)}
				{lit ? (
					<Animated.View pointerEvents="none" style={[styles.fill, fillStyle]} testID="effort-slider-fill">
						{dots(t.textSecondary)}
					</Animated.View>
				) : null}
				{lit ? <Animated.View pointerEvents="none" testID="effort-slider-blob" style={[styles.blob, blobStyle]} /> : null}
			</View>
		</GestureDetector>
	);
});

/** A function that keeps its identity while always calling the latest one given. */
function useStableCallback<A extends unknown[]>(fn: (...args: A) => void): (...args: A) => void {
	const latest = useRef(fn);
	latest.current = fn;
	return useRef((...args: A) => latest.current(...args)).current;
}

/** Fast mode's bolt: outlined while off, filled solid in the text colour while on. */
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
			<Zap size={iconSize.md} strokeWidth={2} color={on ? t.textPrimary : t.textSecondary} fill={on ? t.textPrimary : "none"} />
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
		// From the track's start to just past the blob, clipping the lit dots.
		fill: { position: "absolute", left: 0, top: 0, bottom: 0, borderRadius: TRACK / 2, backgroundColor: t.accentBorder, overflow: "hidden", justifyContent: "center" },
		dot: { position: "absolute", width: DOT * 2, height: DOT * 2, borderRadius: DOT },
		blob: {
			position: "absolute",
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
