import { Platform } from "react-native";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { useAnimatedStyle } from "react-native-reanimated";
import { KEYBOARD_DOCK_GAP } from "../session/keyboardInset";

/**
 * How a composer's dock rides the keyboard, for every composer that docks to
 * it (the chat's, the reviewer's, New task's), so a fix to the motion is a fix
 * everywhere: New task kept an older copy of it and glitched as the keys came
 * up long after the chat's was smoothed (#8348).
 *
 * The dock gives back its resting inset (`restingInset`, the home indicator's)
 * as the keyboard comes, down to the gap it keeps above the keys. Android's
 * keyboard height spans the navigation bar, so there it gives the inset back
 * over that hidden last stretch alone and rests as soon as the keys are gone;
 * following the keyboard's progress instead lifted the dock ahead of the keys
 * and dropped it after them. `override` pins the rise (0 resting, 1 against
 * the keyboard) while the keyboard's own values are not to be trusted (see
 * useChatKeyboard's dockRiseOverride).
 */
export function useComposerDockRise(restingInset: number, override?: number) {
	const keyboard = useReanimatedKeyboardAnimation();
	return useAnimatedStyle(() => {
		const inset = restingInset - KEYBOARD_DOCK_GAP;
		if (override !== undefined) return { transform: [{ translateY: inset * override }] };
		if (Platform.OS === "android") return { transform: [{ translateY: Math.min(-keyboard.height.value, inset) }] };
		return { transform: [{ translateY: inset * keyboard.progress.value }] };
	});
}
