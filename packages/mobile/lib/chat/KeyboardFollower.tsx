import { useEffect, type ReactNode } from "react";
import type { LayoutChangeEvent, StyleProp, ViewStyle } from "react-native";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import Animated, { useAnimatedStyle } from "react-native-reanimated";
import { KeyboardMotionScope, useKeyboardMotion } from "./keyboardMotion";

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
type FollowerProps = {
	lift: number | undefined;
	style?: StyleProp<ViewStyle>;
	onLayout?: (event: LayoutChangeEvent) => void;
	children: ReactNode;
};

export function KeyboardFollower(props: FollowerProps) {
	return <KeyboardMotionScope><Follower {...props} /></KeyboardMotionScope>;
}

function Follower({ lift, style, onLayout, children }: FollowerProps) {
	const keyboard = useReanimatedKeyboardAnimation();
	// A real opening, seen on the UI thread as its first frame starts. A closed
	// keyboard holds the composer down (`lift` 0), and the screen hears of the
	// opening only once JS is free: focusing the field re-renders the screen, so
	// for the first several frames the composer stayed down behind the rising
	// keys and then appeared above them. It follows from the first frame now.
	const { opening } = useKeyboardMotion();
	// Settled closed, by keyboard-controller or by React Native's correction for
	// a close it missed: hold the composer down again whatever the keyboard last
	// reported (see `useChatKeyboard`'s `composerLift`).
	useEffect(() => { if (lift === 0) opening.value = false; }, [lift, opening]);
	const follow = useAnimatedStyle(() => ({
		// keyboard-controller's height is negative while the keyboard is up.
		transform: [{ translateY: lift === undefined || (lift === 0 && opening.value) ? keyboard.height.value : -lift }],
	}), [lift]);
	return <Animated.View pointerEvents="box-none" style={[style, follow]} onLayout={onLayout}>{children}</Animated.View>;
}
