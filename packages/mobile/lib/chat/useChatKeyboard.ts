import { useHeaderHeight } from "expo-router/build/react-navigation/elements";
import { useEffect, useReducer, useRef, useState } from "react";
import { Keyboard, Platform } from "react-native";
import { KeyboardEvents } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import {
	CHAT_KEYBOARD_HIDDEN,
	chatDockRiseOverride,
	dockRestingInset,
	KEYBOARD_DOCK_GAP,
	KEYBOARD_END_GAP_TUCK,
	keyboardVerticalOffset,
	PLATFORM_KEYBOARD_SETTLE_MS,
	reduceChatKeyboard,
	type ChatKeyboardSignal,
	type ChatKeyboardState,
} from "../session/keyboardInset";

const platform = Platform.OS === "ios" ? "ios" : "android";

function initialState(insetsBottom: number): ChatKeyboardState {
	// A screen can mount with the keyboard already up (focus kept across a push).
	// React Native's own record is the one that cannot have gone stale.
	if (!Keyboard.isVisible()) return CHAT_KEYBOARD_HIDDEN;
	const height = Keyboard.metrics()?.height ?? 0;
	return reduceChatKeyboard(platform, CHAT_KEYBOARD_HIDDEN, { kind: "platform-shown", height, insetsBottom });
}

/**
 * Keyboard avoidance shared by the session chat and the reviewer chat, so the
 * two composers sit in the same place and recover the same way. See
 * `reduceChatKeyboard` for why it listens to two sources.
 *
 * iOS keeps keyboard-controller's avoiding view, which interpolates its padding
 * with the keyboard. Android pads the root by hand: edge-to-edge defeats
 * `adjustResize`, so the window no longer shrinks for the keyboard.
 *
 * `underHeader`: the screen is laid out under a clear header, from the
 * window's top (iOS 26's glass bar), so the avoiding view owes it nothing.
 */
export function useChatKeyboard({ underHeader = false, floating = false }: {
	underHeader?: boolean;
	/**
	 * The composer floats over a conversation that moves itself with the
	 * keyboard, so a close starting changes no state: re-rendering the screen
	 * then stalled the keyboard's animation.
	 */
	floating?: boolean;
} = {}) {
	const headerHeight = useHeaderHeight();
	const insets = useSafeAreaInsets();
	const insetsBottom = useRef(insets.bottom);
	insetsBottom.current = insets.bottom;
	const [state, dispatch] = useReducer(
		(current: ChatKeyboardState, signal: ChatKeyboardSignal) => reduceChatKeyboard(platform, current, signal),
		insets.bottom,
		initialState,
	);
	// The keyboard is on its way down. Only the conversation's padding hears it
	// (see rootStyle); the composer's own handling waits for the close as before.
	const [closing, setClosing] = useState(false);
	// keyboard-controller reported this opening, so the list made its own room.
	const [controllerOpen, setControllerOpen] = useState(false);

	useEffect(() => {
		// React Native's reports wait out the keyboard's own animation; see
		// reduceChatKeyboard. Only the latest one is kept, and it is dropped if React
		// Native's own record has moved on by the time it would apply.
		let settle: ReturnType<typeof setTimeout> | undefined;
		const settleThenCorrect = (signal: Extract<ChatKeyboardSignal, { kind: "platform-shown" | "platform-hidden" }>) => {
			if (settle) clearTimeout(settle);
			settle = setTimeout(() => {
				settle = undefined;
				if (Keyboard.isVisible() !== (signal.kind === "platform-shown")) return;
				dispatch(signal);
			}, PLATFORM_KEYBOARD_SETTLE_MS);
		};
		const subscriptions = [
			KeyboardEvents.addListener("keyboardWillShow", (event) => {
				setClosing(false);
				setControllerOpen(true);
				dispatch({ kind: "controller-show", height: event.height });
			}),
			...(floating ? [] : [KeyboardEvents.addListener("keyboardWillHide", () => setClosing(true))]),
			KeyboardEvents.addListener("keyboardDidHide", () => {
				setClosing(false);
				setControllerOpen(false);
				dispatch({ kind: "controller-hidden" });
			}),
			Keyboard.addListener("keyboardDidShow", (event) => settleThenCorrect({ kind: "platform-shown", height: event.endCoordinates.height, insetsBottom: insetsBottom.current })),
			Keyboard.addListener("keyboardDidHide", () => settleThenCorrect({ kind: "platform-hidden" })),
		];
		return () => {
			if (settle) clearTimeout(settle);
			subscriptions.forEach((subscription) => subscription.remove());
		};
	}, [floating]);

	return {
		keyboardVisible: state.visible,
		// Released as the keyboard starts down, not once it has gone: the composer
		// rides the keyboard down frame by frame, and a conversation still padded
		// for the open keyboard left an empty band above it until the close ended.
		// Unpadded, the conversation runs on under the closing keyboard, which
		// uncovers it as it goes.
		rootStyle: platform === "android" ? { paddingBottom: state.visible && !closing ? state.height : 0 } : undefined,
		behavior: platform === "ios" ? "padding" as const : undefined,
		keyboardVerticalOffset: platform === "ios" ? keyboardVerticalOffset(underHeader ? 0 : headerHeight) : 0,
		restingInset: dockRestingInset(insets.bottom),
		// A floating composer's conversation rises with the keyboard by its height
		// less the gap the dock closes, less the end gap it gives up.
		listKeyboardOffset: dockRestingInset(insets.bottom) - KEYBOARD_DOCK_GAP + KEYBOARD_END_GAP_TUCK,
		// Only when keyboard-controller missed the open (see reduceChatKeyboard):
		// the list never heard it either, so the root makes the room instead. A
		// height that merely disagrees with an open it reported is the list's.
		floatingRootStyle: platform === "android" && state.visible && !state.progressTrusted && !controllerOpen ? { paddingBottom: state.height } : undefined,
		dockRiseOverride: platform === "android" ? chatDockRiseOverride(state) : undefined,
		// How far the composer stands above the screen's bottom when the keyboard's
		// own progress cannot be trusted; `undefined` rides it (see KeyboardFollower).
		// A closed keyboard always rests it at the bottom: put away while the app
		// was in the background, the keyboard never ran the animation the follower
		// rides, which stayed at the open height and held the composer up there.
		composerLift: platform === "android" ? (state.visible ? (state.progressTrusted ? undefined : state.height) : 0) : undefined,
	};
}
