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

import { Feather } from "./icons";
import {
	boundNotificationSwipeTranslation,
	shouldClearNotificationSwipe,
} from "./notification-swipe-model";
import { useTheme } from "./ThemeProvider";
import { useReducedMotion } from "./useReducedMotion";

const COMMIT_MS = 180;

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

	const clear = useCallback(() => {
		if (!onClear()) {
			committed.value = false;
			translateX.value = withSpring(0, { damping: 22, stiffness: 260, mass: 0.7 });
		}
	}, [onClear, committed, translateX]);
	const onLayout = useCallback((event: LayoutChangeEvent) => {
		rowWidth.value = event.nativeEvent.layout.width;
	}, [rowWidth]);

	const pan = Gesture.Pan()
		.activeOffsetX([-16, 16])
		.failOffsetY([-12, 12])
		.onBegin(() => {
			startX.value = translateX.value;
		})
		.onUpdate((event) => {
			translateX.value = boundNotificationSwipeTranslation(startX.value + event.translationX, rowWidth.value);
		})
		.onEnd(() => {
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
			translateX.value = withSpring(0, {
				damping: reduceMotion ? 100 : 22,
				stiffness: reduceMotion ? 1000 : 260,
				mass: 0.7,
			});
		})
		.onFinalize((_event, success) => {
			if (!success && !committed.value) {
				translateX.value = withSpring(0, { damping: 22, stiffness: 260, mass: 0.7 });
			}
		});

	const animatedStyle = useAnimatedStyle(() => ({
		transform: [{ translateX: translateX.value }],
	}));

	return (
		<View onLayout={onLayout} style={[styles.shell, style]}>
			<View pointerEvents="none" style={[styles.deleteRail, { backgroundColor: t.red }]}>
				<Feather name="trash-2" size={22} color="#ffffff" />
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
		paddingRight: 28,
	},
	foreground: {},
});
