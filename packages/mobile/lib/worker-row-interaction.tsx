import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { StyleSheet, View, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, { cancelAnimation, Easing, ReduceMotion, useAnimatedStyle, useDerivedValue, useSharedValue, withSpring, withTiming, type SharedValue } from "react-native-reanimated";
import { scheduleOnRN, scheduleOnUI } from "react-native-worklets";
import { AppSymbol } from "./AppSymbol";
import { haptics } from "./haptics";
import { useTheme } from "./ThemeProvider";
import { iconSize, space } from "./tokens";
import { workerActionSymbol, type WorkerActionId } from "./worker-action-model";
import { isFullSwipe, swipeOffset, swipeReleaseTarget, WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";
import { useMenuRowWidth } from "./useMenuRowWidth";
import { WorkerRowContextMenu } from "./worker-rename-context-menu";
import type { WorkerRowInteractionProps } from "./worker-row-interaction.types";

/** How far a finger travels sideways before the row takes the touch from the list. */
const DRAG_DISTANCE = 16;
/**
 * A resting row has nothing to reveal to its left, so a rightward drag never
 * claims the touch: it stays with the list and with iOS's edge-swipe back.
 * Gesture Handler takes a finite offset, so "never" is one no finger reaches.
 */
const NEVER = 10_000;
/** The action a full swipe runs, as Mail's runs its last one. */
const FULL_SWIPE_ACTION: WorkerActionId = "delete";
/** How long the red takes to sweep across the rail, and back, as a full swipe arms. */
const ARM_DURATION = 120;
/** Quick out of the gate, so the red is there on the first frames, then settling. */
const ARM_EASING = Easing.out(Easing.cubic);
/** How much of the rail a drag uncovers before its controls start to appear. */
const REVEAL_START = WORKER_ACTION_REVEAL_WIDTH * 0.2;
/**
 * How a released row settles: Gesture Handler's swipeable's spring, which the
 * row used before it drew its own swipe. Quick, with no bounce past the rail.
 */
const SETTLE = { mass: 2, damping: 1000, stiffness: 700, overshootClamping: true, reduceMotion: ReduceMotion.System } as const;

type SwipeEvent = "drag" | "open" | "willClose" | "closed";

/** An open row's foreground takes every tap on it, to close. */
const TAKE_TAP = () => true;

/**
 * A worker row's touch surface, the same on both platforms: one pan that drags
 * the row open over its rail and runs on the UI thread, around the row's
 * Pressable and its native long-press menu (SwiftUI on iOS, Compose on
 * Android). Past the full-swipe point the drag arms (a haptic, and the
 * uncovered rail turns destructive), and letting go armed archives the row
 * through the same confirmation as the Archive button. While the rail is open,
 * a tap on the row closes it rather than opening the worker: a plain responder
 * on the foreground, not a gesture, so it never takes a tap from the rail's
 * Pin beside it (an activating gesture cancels the press under it).
 *
 * It was Gesture Handler's swipeable with a second pan watching for the full
 * swipe: three gesture detectors, a dozen shared values and three animated
 * styles under every row, resting or not. Choosing a machine on the board
 * builds a screenful of rows, and that was most of their cost; this draws the
 * same swipe with one pan, a few shared values and one style.
 */
export function WorkerRowInteraction({
	sessionId,
	enabled,
	activeSwipeId,
	children,
	rightActions,
	shellStyle,
	foregroundStyle,
	rowStyle,
	accessibilityLabel,
	accessibilityHint,
	onPress,
	actions,
	onAction,
	onSwipeOpen,
	onSwipeClose,
	onReady,
}: WorkerRowInteractionProps) {
	const [open, setOpen] = useState(false);
	// The rail exists only while the row is off its resting position: a resting
	// row hides it behind its foreground, and building it under every mounted
	// row was most of a row's cost.
	const [railMounted, setRailMounted] = useState(false);
	const menuWidth = useMenuRowWidth();
	// Where the foreground sits: 0 at rest, minus the rail when open, further
	// while a full swipe is dragged.
	const translation = useSharedValue(0);
	// Whether the rail is mounted for this row: from a drag's start, or an
	// opening, until a close has finished. A row without it has nothing to close.
	const railShown = useSharedValue(false);
	const dragOrigin = useSharedValue(0);
	const rowWidth = useSharedValue(0);
	const armed = useSharedValue(false);
	const canFullSwipe = enabled && actions.some((action) => action.id === FULL_SWIPE_ACTION);

	// What the UI thread tells the row, read on the JS thread so a settle that
	// began before a new render reports to the board's current handlers.
	const latest = useRef({ sessionId, onSwipeOpen, onSwipeClose, close: () => {} });
	latest.current.sessionId = sessionId;
	latest.current.onSwipeOpen = onSwipeOpen;
	latest.current.onSwipeClose = onSwipeClose;
	const report = useCallback((event: SwipeEvent) => {
		const current = latest.current;
		if (event === "drag") {
			setRailMounted(true);
		} else if (event === "open") {
			setRailMounted(true);
			setOpen(true);
			current.onSwipeOpen(current.sessionId, current.close);
		} else if (event === "willClose") {
			setOpen(false);
		} else {
			setRailMounted(false);
			setOpen(false);
			current.onSwipeClose(current.sessionId);
		}
	}, []);
	const settle = useCallback((to: number, velocity: number) => {
		"worklet";
		const opening = to !== 0;
		if (!opening && !railShown.value) return;
		railShown.value = true;
		scheduleOnRN(report, opening ? "open" : "willClose");
		translation.value = withSpring(to, { ...SETTLE, velocity }, (finished) => {
			if (!finished || opening) return;
			railShown.value = false;
			scheduleOnRN(report, "closed");
		});
	}, [railShown, report, translation]);
	const close = useCallback(() => scheduleOnUI(settle, 0, 0), [settle]);
	latest.current.close = close;

	useEffect(() => {
		onReady?.(close);
	}, [close, onReady]);
	useEffect(() => {
		if (activeSwipeId && activeSwipeId !== sessionId) close();
	}, [activeSwipeId, close, sessionId]);

	const runAction = useCallback((id: WorkerActionId) => {
		// Close the swipe rail first: leaving it open behind a sheet or an alert
		// strands it there once the action's own UI takes over.
		close();
		onAction(id);
	}, [close, onAction]);
	const archive = useCallback(() => onAction(FULL_SWIPE_ACTION), [onAction]);
	const arm = useCallback(() => haptics.armed(), []);
	const gesture = useMemo(() => {
		const pan = Gesture.Pan()
			.enabled(enabled)
			// Only a leftward drag opens a resting row; an open one closes either way.
			.activeOffsetX([-DRAG_DISTANCE, open ? DRAG_DISTANCE : NEVER])
			.onStart(() => {
				// From where the row is: a finger can catch it while it settles.
				cancelAnimation(translation);
				dragOrigin.value = translation.value;
				if (railShown.value) return;
				railShown.value = true;
				scheduleOnRN(report, "drag");
			})
			.onUpdate((event) => {
				translation.value = swipeOffset(dragOrigin.value, event.translationX, canFullSwipe);
				if (!canFullSwipe) return;
				// A full swipe only ever runs leftward.
				const next = isFullSwipe(dragOrigin.value + event.translationX, rowWidth.value);
				if (next === armed.value) return;
				armed.value = next;
				if (next) scheduleOnRN(arm);
			})
			// Only a finger lifted while armed archives. A drag the system or
			// another handler cancels ends here too, with success false.
			.onEnd((event, success) => {
				if (success && armed.value) {
					settle(0, event.velocityX);
					scheduleOnRN(archive);
					return;
				}
				settle(swipeReleaseTarget(dragOrigin.value + event.translationX, event.velocityX), event.velocityX);
			})
			.onFinalize(() => {
				armed.value = false;
			});
		return pan;
	}, [enabled, open, canFullSwipe, dragOrigin, railShown, report, translation, rowWidth, armed, arm, archive, settle]);
	const slide = useAnimatedStyle(() => ({ transform: [{ translateX: translation.value }] }));
	const { ref: menuSlot, onLayout: measureMenuWidth } = menuWidth;
	const measure = useCallback((event: LayoutChangeEvent) => {
		rowWidth.value = event.nativeEvent.layout.width;
		measureMenuWidth(event);
	}, [rowWidth, measureMenuWidth]);

	return (
		<GestureDetector gesture={gesture}>
			<View ref={menuSlot} collapsable={false} onLayout={measure} style={shellStyle}>
				{railMounted ? (
					<View style={styles.railSlot}>
						<SwipeRail translation={translation} armed={armed}>
							{rightActions}
						</SwipeRail>
					</View>
				) : null}
				{/* Open, the row takes a tap itself (to close) instead of passing it on. */}
				<Animated.View
					testID="worker-row-foreground"
					pointerEvents={open ? "box-only" : "auto"}
					onStartShouldSetResponder={open ? TAKE_TAP : undefined}
					onResponderRelease={open ? close : undefined}
					style={[foregroundStyle, slide]}
				>
					{enabled ? (
						<WorkerRowContextMenu
							onPress={onPress}
							actions={actions}
							onAction={runAction}
							accessibilityLabel={accessibilityLabel}
							accessibilityHint={accessibilityHint}
							style={[rowStyle, menuWidth.style]}
						>
							{children}
						</WorkerRowContextMenu>
					) : (
						<View style={rowStyle}>{children}</View>
					)}
				</Animated.View>
			</View>
		</GestureDetector>
	);
}

/**
 * What the swipe uncovers: the controls in a fixed rail at the trailing edge,
 * over a fill that follows the row past the rail. At rest the fill is the
 * list's own background, so the controls sit in the space the row leaves
 * rather than on a panel, and they grow in as the drag uncovers them. Once
 * letting go would archive, red sweeps across the fill from the trailing edge,
 * the controls give way, and the archive glyph rides its leading edge, as Mail
 * draws a full swipe.
 *
 * The red is its own full-strength layer that sweeps in, not the fill's colour
 * fading from the page's: a fade from near-black passes through dark, greyish
 * reds for most of its frames, which read on Android as a muddy rail and a
 * late red. Every frame of the sweep is either the page or the real red.
 */
function SwipeRail({ translation, armed, children }: {
	translation: SharedValue<number>;
	armed: SharedValue<boolean>;
	children: ReactNode;
}) {
	const t = useTheme();
	// One progress for the whole arming, so its parts stay in step: 0 at rest,
	// 1 once the red covers the rail.
	const arming = useDerivedValue(() => withTiming(armed.value ? 1 : 0, { duration: ARM_DURATION, easing: ARM_EASING }));
	const fill = useAnimatedStyle(() => ({
		width: Math.max(WORKER_ACTION_REVEAL_WIDTH, -translation.value),
	}));
	const red = useAnimatedStyle(() => ({
		transform: [{ scaleX: arming.value }],
	}));
	// The archive glyph sits at the rail's leading edge, which the sweep reaches
	// last: it comes in only as the red arrives under it, never on the dark.
	const armedGlyph = useAnimatedStyle(() => {
		const shown = Math.min(1, Math.max(0, (arming.value - 0.6) / 0.4));
		return { opacity: shown, transform: [{ scale: 0.6 + 0.4 * shown }] };
	});
	// The pin gives way in the sweep's first third: fading over the whole of it,
	// its dark disc sat on the red as a grey blot.
	const controls = useAnimatedStyle(() => {
		const uncovered = (-translation.value - REVEAL_START) / (WORKER_ACTION_REVEAL_WIDTH - REVEAL_START);
		const revealed = Math.min(1, Math.max(0, uncovered));
		return {
			opacity: revealed * (1 - Math.min(1, arming.value * 3)),
			transform: [{ scale: 0.6 + 0.4 * revealed }],
		};
	});
	return (
		<>
			<Animated.View pointerEvents="none" style={[styles.fill, { backgroundColor: t.bgBase }, fill]}>
				<Animated.View style={[StyleSheet.absoluteFill, styles.red, { backgroundColor: t.red }, red]} />
				<Animated.View style={[styles.armedGlyph, armedGlyph]}>
					<AppSymbol name={workerActionSymbol(FULL_SWIPE_ACTION)} size={iconSize.xl} color={t.onDestructive} />
				</Animated.View>
			</Animated.View>
			<Animated.View style={[styles.rail, controls]}>{children}</Animated.View>
		</>
	);
}

const styles = StyleSheet.create({
	// Behind the foreground, the rail at its trailing edge.
	railSlot: {
		...StyleSheet.absoluteFill,
		flexDirection: "row-reverse",
		overflow: "hidden",
	},
	fill: {
		position: "absolute",
		top: 0,
		right: 0,
		bottom: 0,
	},
	// Sweeps in from the trailing edge, where the drag uncovered it first.
	red: { transformOrigin: "right center" },
	armedGlyph: {
		position: "absolute",
		top: 0,
		bottom: 0,
		left: space.xl,
		justifyContent: "center",
	},
	rail: {
		width: WORKER_ACTION_REVEAL_WIDTH,
		// The rail is the row's full height and its controls one fixed block.
		// Centred, they stay level with the row's middle when it grows past the
		// minimum: a rename in progress, a larger text size, a wrapped line.
		justifyContent: "center",
	},
});
