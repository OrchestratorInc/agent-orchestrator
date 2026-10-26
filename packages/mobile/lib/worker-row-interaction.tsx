import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { StyleSheet, View, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import ReanimatedSwipeable, { type SwipeableMethods } from "react-native-gesture-handler/ReanimatedSwipeable";
import Animated, { useAnimatedStyle, useSharedValue, type SharedValue } from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";
import { AppSymbol } from "./AppSymbol";
import { rowDividerWidth } from "./divider";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space } from "./tokens";
import { workerActionSymbol, type WorkerActionId } from "./worker-action-model";
import { isFullSwipe, WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";
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
	const measure = useCallback((event: LayoutChangeEvent) => {
		rowWidth.value = event.nativeEvent.layout.width;
	}, [rowWidth]);

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
							style={rowStyle}
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
 * over a fill that follows the row past the rail. Once letting go would
 * archive, the fill takes the destructive tint and the archive glyph rides its
 * leading edge, so the armed state reads by shape as well as colour.
 */
function SwipeRail({ translation, armed, children }: {
	translation: SharedValue<number>;
	armed: SharedValue<boolean>;
	children: ReactNode;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const fill = useAnimatedStyle(() => ({
		width: Math.max(WORKER_ACTION_REVEAL_WIDTH, -translation.value),
		backgroundColor: armed.value ? t.tintRed : t.bgElevated,
	}));
	const armedGlyph = useAnimatedStyle(() => ({ opacity: armed.value ? 1 : 0 }));
	return (
		<>
			<Animated.View pointerEvents="none" style={[styles.fill, fill]}>
				<Animated.View style={[styles.armedGlyph, armedGlyph]}>
					<AppSymbol name={workerActionSymbol(FULL_SWIPE_ACTION)} size={iconSize.xl} color={t.red} />
				</Animated.View>
			</Animated.View>
			{/* The rail keeps its width before its controls mount: the swipeable
			    measures it to resolve the very first drag. */}
			<View style={styles.rail}>{children}</View>
		</>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		fill: {
			position: "absolute",
			top: 0,
			right: 0,
			bottom: 0,
			borderLeftWidth: rowDividerWidth,
			borderLeftColor: t.borderSubtle,
		},
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
