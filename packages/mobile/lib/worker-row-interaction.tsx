import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { StyleSheet, View, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import ReanimatedSwipeable, { type SwipeableMethods } from "react-native-gesture-handler/ReanimatedSwipeable";
import Animated, { Easing, useAnimatedStyle, useDerivedValue, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";
import { AppSymbol } from "./AppSymbol";
import { haptics } from "./haptics";
import { useTheme } from "./ThemeProvider";
import { iconSize, space } from "./tokens";
import { workerActionSymbol, type WorkerActionId } from "./worker-action-model";
import { isFullSwipe, WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";
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
 * A worker row's touch surface, the same on both platforms: Gesture Handler's
 * Reanimated swipeable, so the drag and its settle run on the UI thread, around
 * the row's Pressable and its native long-press menu (SwiftUI on iOS, Compose on
 * Android).
 *
 * The swipeable has no full swipe of its own. A second pan, recognised with
 * its own, watches the same drag: past the full-swipe point it arms (a haptic,
 * and the uncovered rail turns destructive), and letting go armed archives the
 * row through the same confirmation as the Archive button.
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
	const swipeable = useRef<SwipeableMethods>(null);
	const [open, setOpen] = useState(false);
	// The rail's controls exist only while the row is off its resting position: a
	// resting row hides them behind its foreground, and building native buttons
	// under every mounted row was most of a row's cost.
	const [railMounted, setRailMounted] = useState(false);
	const menuWidth = useMenuRowWidth();
	const rowWidth = useSharedValue(0);
	const railOpen = useSharedValue(false);
	const dragOrigin = useSharedValue(0);
	const armed = useSharedValue(false);
	const close = useCallback(() => swipeable.current?.close(), []);
	const canFullSwipe = enabled && actions.some((action) => action.id === FULL_SWIPE_ACTION);

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
	const fullSwipe = useCallback(() => runAction(FULL_SWIPE_ACTION), [runAction]);
	const arm = useCallback(() => haptics.armed(), []);
	const fullSwipeWatch = useMemo(
		() =>
			Gesture.Pan()
				.enabled(canFullSwipe)
				// A full swipe only ever runs leftward.
				.activeOffsetX([-DRAG_DISTANCE, NEVER])
				.onStart(() => {
					dragOrigin.value = railOpen.value ? -WORKER_ACTION_REVEAL_WIDTH : 0;
				})
				.onUpdate((event) => {
					const next = isFullSwipe(dragOrigin.value + event.translationX, rowWidth.value);
					if (next === armed.value) return;
					armed.value = next;
					if (next) scheduleOnRN(arm);
				})
				// Only a finger lifted while armed archives. A drag the system or
				// another handler cancels ends here too, with success false.
				.onEnd((_event, success) => {
					if (success && armed.value) scheduleOnRN(fullSwipe);
				})
				.onFinalize(() => {
					armed.value = false;
				}),
		[canFullSwipe, dragOrigin, railOpen, rowWidth, armed, arm, fullSwipe],
	);
	const { onLayout: measureMenuWidth } = menuWidth;
	const measure = useCallback((event: LayoutChangeEvent) => {
		rowWidth.value = event.nativeEvent.layout.width;
		measureMenuWidth(event);
	}, [rowWidth, measureMenuWidth]);

	return (
		<GestureDetector gesture={fullSwipeWatch}>
			{/* Its own native view: two detectors cannot share the swipeable's. */}
			<View collapsable={false} onLayout={measure}>
				<ReanimatedSwipeable
					ref={swipeable}
					enabled={enabled}
					containerStyle={shellStyle}
					childrenContainerStyle={foregroundStyle}
					dragOffsetFromRightEdge={DRAG_DISTANCE}
					dragOffsetFromLeftEdge={open ? DRAG_DISTANCE : NEVER}
					rightThreshold={WORKER_ACTION_REVEAL_WIDTH / 2}
					// The row travels past the rail only toward a full swipe.
					overshootRight={canFullSwipe}
					simultaneousWithExternalGesture={fullSwipeWatch}
					onSwipeableOpenStartDrag={() => setRailMounted(true)}
					onSwipeableWillOpen={() => {
						railOpen.value = true;
						setRailMounted(true);
						setOpen(true);
						onSwipeOpen(sessionId, close);
					}}
					onSwipeableWillClose={() => {
						railOpen.value = false;
						setOpen(false);
					}}
					onSwipeableClose={() => {
						// Also fires when a short or cancelled drag settles back at rest.
						railOpen.value = false;
						setRailMounted(false);
						setOpen(false);
						onSwipeClose(sessionId);
					}}
					renderRightActions={(_progress, translation) => (
						<SwipeRail translation={translation} armed={armed}>
							{railMounted ? rightActions : null}
						</SwipeRail>
					)}
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
				</ReanimatedSwipeable>
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
			{/* The rail keeps its width before its controls mount: the swipeable
			    measures it to resolve the very first drag. */}
			<Animated.View style={[styles.rail, controls]}>{children}</Animated.View>
		</>
	);
}

const styles = StyleSheet.create({
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
