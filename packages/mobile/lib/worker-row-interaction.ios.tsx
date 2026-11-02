import { useEffect, useRef, useState } from "react";
import { View } from "react-native";
import type { GestureType } from "react-native-gesture-handler";
import Swipeable from "react-native-gesture-handler/Swipeable";
import { haptics } from "./haptics";
import { WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";
import { WorkerRowContextMenu } from "./worker-rename-context-menu";
import type { WorkerRowInteractionProps } from "./worker-row-interaction.types";

// Keep native iOS menus/glass controls, but don't build a SwiftUI hosting tree
// behind every resting row. Android owns its separate interaction surface.
export function WorkerRowInteraction({
	sessionId,
	enabled,
	activeSwipeId,
	children,
	rightActions,
	shellStyle,
	foregroundStyle,
	rowStyle,
	pressedStyle,
	accessibilityLabel,
	accessibilityHint,
	onPress,
	actions,
	onAction,
	onSwipeOpen,
	onSwipeClose,
	onReady,
}: WorkerRowInteractionProps) {
	const swipeableRef = useRef<Swipeable>(null);
	const renameGestureRef = useRef<GestureType | undefined>(undefined);
	const [actionsOpen, setActionsOpen] = useState(false);
	const [actionsMounted, setActionsMounted] = useState(false);
	const close = () => swipeableRef.current?.close();

	useEffect(() => {
		onReady?.(close);
	}, [onReady]);
	useEffect(() => {
		if (activeSwipeId && activeSwipeId !== sessionId) close();
	}, [activeSwipeId, sessionId]);

	return (
		<Swipeable
			ref={swipeableRef}
			enabled={enabled}
			containerStyle={shellStyle}
			childrenContainerStyle={foregroundStyle}
			dragOffsetFromRightEdge={16}
			activeOffsetX={actionsOpen ? [-16, 16] : -16}
			failOffsetY={[-12, 12]}
			simultaneousHandlers={renameGestureRef}
			rightThreshold={WORKER_ACTION_REVEAL_WIDTH / 2}
			overshootRight={false}
			onSwipeableOpenStartDrag={() => setActionsMounted(true)}
			onSwipeableWillOpen={() => {
				setActionsMounted(true);
				setActionsOpen(true);
				onSwipeOpen(sessionId, close);
				haptics.select();
			}}
			onSwipeableClose={() => {
				// Also fires when a short/cancelled drag settles back at zero.
				setActionsMounted(false);
				setActionsOpen(false);
				onSwipeClose(sessionId);
			}}
			renderRightActions={() => (
				// Width is present even before controls mount: Swipeable measures it
				// to resolve the very first drag. Keep controls through close motion.
				<View style={{ width: WORKER_ACTION_REVEAL_WIDTH }}>
					{actionsMounted ? rightActions : null}
				</View>
			)}
		>
			{enabled ? (
				<WorkerRowContextMenu
					gestureRef={renameGestureRef}
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
					pressedStyle={pressedStyle}
				>
					{children}
				</WorkerRowContextMenu>
			) : (
				<View style={rowStyle}>{children}</View>
			)}
		</Swipeable>
	);
}
