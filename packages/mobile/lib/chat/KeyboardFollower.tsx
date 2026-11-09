import type { ReactNode } from "react";
import type { LayoutChangeEvent, StyleProp, ViewStyle } from "react-native";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import Animated, { useAnimatedStyle } from "react-native-reanimated";

/**
 * Holds its children against the top of the keyboard by a transform that
 * follows the keyboard's own animation on the UI thread.
 *
 * The chat's composer rode the screen's keyboard padding before, and padding is
 * layout: Android's is set from JS state, and on iOS keyboard-controller's waits
 * on React's commits. In a long conversation, or one that was still loading as
 * it opened, focusing the field re-rendered the whole screen for most of a
 * second, and the keyboard came up over a composer still at the bottom of the
 * screen. A transform needs neither, so the composer arrives with the keys.
 *
 * `lift` is the distance to hold when the keyboard's progress cannot be
 * trusted (see `reduceChatKeyboard`); `undefined` follows the keyboard.
 */
export function KeyboardFollower({ lift, style, onLayout, children }: {
	lift: number | undefined;
	style?: StyleProp<ViewStyle>;
	onLayout?: (event: LayoutChangeEvent) => void;
	children: ReactNode;
}) {
	const keyboard = useReanimatedKeyboardAnimation();
	const follow = useAnimatedStyle(() => ({
		// keyboard-controller's height is negative while the keyboard is up.
		transform: [{ translateY: lift === undefined ? keyboard.height.value : -lift }],
	}), [lift]);
	return <Animated.View pointerEvents="box-none" style={[style, follow]} onLayout={onLayout}>{children}</Animated.View>;
}
