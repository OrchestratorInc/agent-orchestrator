import { useCallback, useEffect, useRef, useState } from "react";
import { StyleSheet, View } from "react-native";
import ReanimatedSwipeable, { type SwipeableMethods } from "react-native-gesture-handler/ReanimatedSwipeable";
import { rowDividerWidth } from "./divider";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";
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

/**
 * A worker row's touch surface, the same on both platforms: Gesture Handler's
 * Reanimated swipeable, so the drag and its settle run on the UI thread, around
 * the row's Pressable and its native long-press menu (SwiftUI on iOS, Compose on
 * Android).
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
	const styles = useThemedStyles(makeStyles);
	const swipeable = useRef<SwipeableMethods>(null);
	const [open, setOpen] = useState(false);
	// The rail's controls exist only while the row is off its resting position: a
	// resting row hides them behind its foreground, and building native buttons
	// under every mounted row was most of a row's cost.
	const [railMounted, setRailMounted] = useState(false);
	const close = useCallback(() => swipeable.current?.close(), []);

	useEffect(() => {
		onReady?.(close);
	}, [close, onReady]);
	useEffect(() => {
		if (activeSwipeId && activeSwipeId !== sessionId) close();
	}, [activeSwipeId, close, sessionId]);

	return (
		<ReanimatedSwipeable
			ref={swipeable}
			enabled={enabled}
			containerStyle={shellStyle}
			childrenContainerStyle={foregroundStyle}
			dragOffsetFromRightEdge={DRAG_DISTANCE}
			dragOffsetFromLeftEdge={open ? DRAG_DISTANCE : NEVER}
			rightThreshold={WORKER_ACTION_REVEAL_WIDTH / 2}
			overshootRight={false}
			onSwipeableOpenStartDrag={() => setRailMounted(true)}
			onSwipeableWillOpen={() => {
				setRailMounted(true);
				setOpen(true);
				onSwipeOpen(sessionId, close);
			}}
			onSwipeableWillClose={() => setOpen(false)}
			onSwipeableClose={() => {
				// Also fires when a short or cancelled drag settles back at rest.
				setRailMounted(false);
				setOpen(false);
				onSwipeClose(sessionId);
			}}
			renderRightActions={() => (
				// The rail keeps its width before its controls mount: the swipeable
				// measures it to resolve the very first drag.
				<View style={styles.rail}>{railMounted ? rightActions : null}</View>
			)}
		>
			{enabled ? (
				<WorkerRowContextMenu
					onPress={onPress}
					actions={actions}
					onAction={(id) => {
						// Close the swipe rail first: leaving it open behind a sheet or an
						// alert strands it there once the action's own UI takes over.
						close();
						onAction(id);
					}}
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
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		rail: {
			width: WORKER_ACTION_REVEAL_WIDTH,
			// The rail is the row's full height and its controls one fixed block.
			// Centred, they stay level with the row's middle when it grows past the
			// minimum: a rename in progress, a larger text size, a wrapped line.
			justifyContent: "center",
			backgroundColor: t.bgElevated,
			borderLeftWidth: rowDividerWidth,
			borderLeftColor: t.borderSubtle,
		},
	});
