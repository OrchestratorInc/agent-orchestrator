import { useCallback } from "react";
import { StyleSheet, View, type LayoutChangeEvent, type StyleProp, type ViewStyle } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
	Easing,
	useAnimatedStyle,
	useSharedValue,
	withSpring,
	withTiming,
} from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";

import { AppSymbol } from "./AppSymbol";
import { haptics } from "./haptics";
import {
	boundNotificationSwipeTranslation,
	shouldClearNotificationSwipe,
} from "./notification-swipe-model";
import { useTheme } from "./ThemeProvider";
import { iconSize, space } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

const COMMIT_MS = 180;
const SETTLE = { damping: 22, stiffness: 260, mass: 0.7 };

export function NotificationSwipeRow({
	children,
	onClear,
	style,
}: {
	children: React.ReactNode;
	onClear: () => boolean;
	style?: StyleProp<ViewStyle>;
}) {
	const t = useTheme();
	const reduceMotion = useReducedMotion();
	const rowWidth = useSharedValue(0);
	const startX = useSharedValue(0);
	const translateX = useSharedValue(0);
	const committed = useSharedValue(false);
	// Whether the row is past the point where letting go clears it.
	const armed = useSharedValue(false);

	// Settles the row back in place; Reduce Motion puts it there at once.
	const restore = useCallback(() => {
		"worklet";
		translateX.value = reduceMotion ? 0 : withSpring(0, SETTLE);
	}, [reduceMotion, translateX]);
	const clear = useCallback(() => {
		if (!onClear()) {
			committed.value = false;
			restore();
		}
	}, [onClear, committed, restore]);
	// Haptics run on the JS thread; the gesture worklet schedules this by name.
	const arm = useCallback(() => haptics.armed(), []);
	const onLayout = useCallback((event: LayoutChangeEvent) => {
		rowWidth.value = event.nativeEvent.layout.width;
	}, [rowWidth]);

	const pan = Gesture.Pan()
		.activeOffsetX([-16, 16])
		.failOffsetY([-12, 12])
		.onBegin(() => {
			startX.value = translateX.value;
			armed.value = false;
		})
		.onUpdate((event) => {
			translateX.value = boundNotificationSwipeTranslation(startX.value + event.translationX, rowWidth.value);
			// One firm bump as the row crosses the point of no return, so the clear
			// is felt before the finger lifts. Backing out re-arms it.
			const nextArmed = shouldClearNotificationSwipe(translateX.value, rowWidth.value);
			if (nextArmed !== armed.value) {
				armed.value = nextArmed;
				if (nextArmed) scheduleOnRN(arm);
			}
		})
		.onEnd(() => {
			armed.value = false;
			if (shouldClearNotificationSwipe(translateX.value, rowWidth.value) && !committed.value) {
				committed.value = true;
				translateX.value = withTiming(
					-rowWidth.value,
					{ duration: reduceMotion ? 0 : COMMIT_MS, easing: Easing.out(Easing.cubic) },
					(finished) => {
						if (finished) scheduleOnRN(clear);
					},
				);
				return;
			}
			restore();
		})
		.onFinalize((_event, success) => {
			if (!success && !committed.value) restore();
		});

	const animatedStyle = useAnimatedStyle(() => ({
		transform: [{ translateX: translateX.value }],
	}));

	return (
		<View onLayout={onLayout} style={[styles.shell, style]}>
			<View pointerEvents="none" style={[styles.deleteRail, { backgroundColor: t.red }]}>
				{/* White on red, as the system's own delete action draws it. */}
				<AppSymbol name="delete" size={iconSize.xl} color={t.onDestructive} />
			</View>
			<GestureDetector gesture={pan}>
				<Animated.View style={[styles.foreground, { backgroundColor: t.bgBase }, animatedStyle]}>
					{children}
				</Animated.View>
			</GestureDetector>
		</View>
	);
}

const styles = StyleSheet.create({
	shell: { overflow: "hidden" },
	deleteRail: {
		position: "absolute",
		top: 0,
		right: 0,
		bottom: 0,
		left: 0,
		alignItems: "flex-end",
		justifyContent: "center",
		paddingRight: space.xxl,
	},
	foreground: {},
});
